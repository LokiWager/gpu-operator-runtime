package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/loki/gpu-operator-runtime/pkg/contract"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

// Service owns the control-plane business logic behind the HTTP API.
type Service struct {
	kube                  kubernetes.Interface
	operator              ctrlclient.Client
	logger                *slog.Logger
	startedAt             time.Time
	httpClient            *http.Client
	nvidiaMetricsEndpoint string
	serverlessPublisher   serverless.InvocationPublisher
	packageCatalog        contract.RuntimePackageCatalog

	packageMu sync.RWMutex
	unitMu    sync.Mutex
}

// New builds a Service with optional Kubernetes and operator clients.
func New(kubeClient kubernetes.Interface, operatorClient ctrlclient.Client, logger *slog.Logger) *Service {
	return &Service{
		kube:       kubeClient,
		operator:   operatorClient,
		logger:     logger,
		startedAt:  time.Now().UTC(),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// ConfigureRuntimePackages replaces the ops-managed package catalog used by create request normalization.
func (s *Service) ConfigureRuntimePackages(catalog contract.RuntimePackageCatalog) error {
	normalized, err := catalog.Normalized()
	if err != nil {
		return err
	}
	s.packageMu.Lock()
	defer s.packageMu.Unlock()
	s.packageCatalog = normalized
	return nil
}

// ConfigureNvidiaMetrics wires an optional DCGM exporter endpoint into runtime health and metrics collection.
func (s *Service) ConfigureNvidiaMetrics(endpoint string, httpClient *http.Client) {
	s.nvidiaMetricsEndpoint = strings.TrimSpace(endpoint)
	if httpClient != nil {
		s.httpClient = httpClient
	}
}

// ConfigureServerlessPublisher wires an optional NATS-backed queue publisher into the serverless ingress surface.
func (s *Service) ConfigureServerlessPublisher(publisher serverless.InvocationPublisher) {
	s.serverlessPublisher = publisher
}

func (s *Service) runtimePackageCatalog() contract.RuntimePackageCatalog {
	s.packageMu.RLock()
	defer s.packageMu.RUnlock()
	return s.packageCatalog.Clone()
}

// Health reports process uptime, cluster reachability, and unit counts.
func (s *Service) Health(ctx context.Context) (domain.HealthStatus, error) {
	health := domain.HealthStatus{
		StartedAt:              s.startedAt,
		UptimeSeconds:          int64(time.Since(s.startedAt).Seconds()),
		KubernetesConnected:    false,
		NodeCount:              0,
		ReadyNodeCount:         0,
		ActiveUnitCount:        0,
		TotalGPUCapacity:       0,
		TotalGPUAllocatable:    0,
		NvidiaMetricsConnected: false,
	}

	kubeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	nodeInventory, err := s.collectNodeInventory(kubeCtx)
	if err != nil {
		health.KubeError = err.Error()
	}
	health.KubernetesConnected = nodeInventory.Connected
	health.NodeCount = nodeInventory.NodeCount
	health.ReadyNodeCount = nodeInventory.ReadyNodeCount
	health.TotalGPUCapacity = nodeInventory.TotalGPUCapacity
	health.TotalGPUAllocatable = nodeInventory.TotalGPUAllocatable
	health.GPUProducts = append([]domain.GPUProductHealth(nil), nodeInventory.GPUProducts...)

	nvidiaTelemetry, err := s.collectNvidiaTelemetry(kubeCtx)
	if err != nil {
		health.NvidiaMetricsError = err.Error()
	}
	health.NvidiaMetricsConnected = nvidiaTelemetry.Connected
	health.GPUDeviceCount = nvidiaTelemetry.DeviceCount
	health.TotalGPUMemoryMiB = nvidiaTelemetry.TotalMemoryMiB
	health.UsedGPUMemoryMiB = nvidiaTelemetry.UsedMemoryMiB
	health.FreeGPUMemoryMiB = nvidiaTelemetry.FreeMemoryMiB
	health.AverageGPUUtilizationPercent = nvidiaTelemetry.AverageGPUUtilizationPercent
	if health.NvidiaMetricsError == "" {
		health.NvidiaMetricsError = nvidiaTelemetry.Error
	}

	_, activeUnits, err := s.collectRuntimeUnitPhaseCounts(kubeCtx)
	if err == nil {
		health.ActiveUnitCount = activeUnits
	}
	return health, nil
}
