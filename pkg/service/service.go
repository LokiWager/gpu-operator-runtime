package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/loki/gpu-operator-runtime/pkg/domain"
	"github.com/loki/gpu-operator-runtime/pkg/store"
)

type CreateStockRequest struct {
	Number   int    `json:"number"`
	SpecName string `json:"specName"`
	CPU      string `json:"cpu,omitempty"`
	Memory   string `json:"memory,omitempty"`
	GPUType  string `json:"gpuType,omitempty"`
	GPUNum   int    `json:"gpuNum,omitempty"`
}

type DeleteStockRequest struct {
	Number   int    `json:"number"`
	SpecName string `json:"specName"`
}

type CreateVMRequest struct {
	VMID       string `json:"vmID,omitempty"`
	TenantID   string `json:"tenantID,omitempty"`
	TenantName string `json:"tenantName,omitempty"`
	Image      string `json:"image,omitempty"`
	SpecName   string `json:"specName,omitempty"`
	StockID    string `json:"stockID,omitempty"`
}

type Service struct {
	store     *store.MemoryStore
	kube      kubernetes.Interface
	logger    *slog.Logger
	startedAt time.Time
}

func New(store *store.MemoryStore, kubeClient kubernetes.Interface, logger *slog.Logger) *Service {
	return &Service{
		store:     store,
		kube:      kubeClient,
		logger:    logger,
		startedAt: time.Now().UTC(),
	}
}

func (s *Service) CreateStocks(ctx context.Context, req CreateStockRequest) ([]domain.Stock, error) {
	_ = ctx
	if req.Number <= 0 {
		return nil, fmt.Errorf("number should be > 0")
	}
	if strings.TrimSpace(req.SpecName) == "" {
		return nil, fmt.Errorf("specName is required")
	}

	spec := domain.StockSpec{
		Name:    strings.TrimSpace(req.SpecName),
		CPU:     strings.TrimSpace(req.CPU),
		Memory:  strings.TrimSpace(req.Memory),
		GPUType: strings.TrimSpace(req.GPUType),
		GPUNum:  req.GPUNum,
	}

	stocks, err := s.store.CreateStocks(req.Number, spec)
	if err != nil {
		return nil, err
	}
	return stocks, nil
}

func (s *Service) DeleteStocks(ctx context.Context, req DeleteStockRequest) ([]string, error) {
	_ = ctx
	return s.store.DeleteStocks(req.Number, strings.TrimSpace(req.SpecName))
}

func (s *Service) ListStocks(ctx context.Context) ([]domain.Stock, error) {
	_ = ctx
	return s.store.ListStocks(), nil
}

func (s *Service) CreateVM(ctx context.Context, req CreateVMRequest) (domain.VM, error) {
	_ = ctx

	vmID := strings.TrimSpace(req.VMID)
	if vmID == "" {
		vmID = fmt.Sprintf("vm-%d", time.Now().UTC().UnixNano())
	}

	var (
		stock domain.Stock
		err   error
	)

	if strings.TrimSpace(req.StockID) != "" {
		stock, err = s.store.ReserveStockByID(strings.TrimSpace(req.StockID))
	} else {
		specName := strings.TrimSpace(req.SpecName)
		if specName == "" {
			return domain.VM{}, fmt.Errorf("specName is required when stockID is not provided")
		}
		stock, err = s.store.ReserveStock(specName)
	}
	if err != nil {
		return domain.VM{}, err
	}

	now := time.Now().UTC()
	vm := domain.VM{
		ID:         vmID,
		TenantID:   strings.TrimSpace(req.TenantID),
		TenantName: strings.TrimSpace(req.TenantName),
		Image:      strings.TrimSpace(req.Image),
		SpecName:   stock.Spec.Name,
		StockID:    stock.ID,
		Status:     domain.VMStatusRunning,
		CreatedAt:  now,
		StartedAt:  now,
	}

	if err := s.store.CreateVM(vm); err != nil {
		_ = s.store.ReleaseStock(stock.ID)
		return domain.VM{}, err
	}
	return vm, nil
}

func (s *Service) DeleteVM(ctx context.Context, vmID string) error {
	_ = ctx
	vm, err := s.store.DeleteVM(strings.TrimSpace(vmID))
	if err != nil {
		return err
	}
	if vm.StockID != "" {
		if err := s.store.ReleaseStock(vm.StockID); err != nil {
			s.logger.Warn("release stock failed", "stockID", vm.StockID, "err", err)
		}
	}
	return nil
}

func (s *Service) GetVM(ctx context.Context, vmID string) (domain.VM, error) {
	_ = ctx
	vm, ok := s.store.GetVM(strings.TrimSpace(vmID))
	if !ok {
		return domain.VM{}, fmt.Errorf("vm %s not found", vmID)
	}
	return vm, nil
}

func (s *Service) ListVMs(ctx context.Context) ([]domain.VM, error) {
	_ = ctx
	return s.store.ListVMs(), nil
}

func (s *Service) Health(ctx context.Context) (domain.HealthStatus, error) {
	summary := s.store.Summary()
	health := domain.HealthStatus{
		StartedAt:           s.startedAt,
		UptimeSeconds:       int64(time.Since(s.startedAt).Seconds()),
		KubernetesConnected: false,
		NodeCount:           0,
		Summary:             summary,
	}

	if s.kube == nil {
		return health, nil
	}

	kubeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	nodes, err := s.kube.CoreV1().Nodes().List(kubeCtx, metav1.ListOptions{})
	if err != nil {
		health.KubeError = err.Error()
		return health, nil
	}
	health.KubernetesConnected = true
	health.NodeCount = len(nodes.Items)
	return health, nil
}

func (s *Service) Summary() domain.RuntimeSummary {
	return s.store.Summary()
}
