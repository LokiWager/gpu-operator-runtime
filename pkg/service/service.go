package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

type CreateStockPoolRequest struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	SpecName  string `json:"specName"`
	Replicas  int32  `json:"replicas"`
}

type createStockPoolJob struct {
	jobID string
	req   CreateStockPoolRequest
}

type Service struct {
	kube      kubernetes.Interface
	operator  ctrlclient.Client
	logger    *slog.Logger
	startedAt time.Time

	jobMu    sync.RWMutex
	jobs     map[string]domain.OperatorJob
	jobQueue chan createStockPoolJob
}

func New(kubeClient kubernetes.Interface, operatorClient ctrlclient.Client, logger *slog.Logger) *Service {
	return &Service{
		kube:      kubeClient,
		operator:  operatorClient,
		logger:    logger,
		startedAt: time.Now().UTC(),
		jobs:      map[string]domain.OperatorJob{},
		jobQueue:  make(chan createStockPoolJob, 128),
	}
}

func (s *Service) StartOperatorJobWorker(ctx context.Context) {
	if s.operator == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.jobQueue:
			s.setJobRunning(job.jobID)
			if err := s.createStockPoolObject(ctx, job.req); err != nil {
				s.setJobFailed(job.jobID, err)
				continue
			}
			s.setJobSucceeded(job.jobID, job.req)
		}
	}
}

func (s *Service) CreateStockPoolAsync(ctx context.Context, req CreateStockPoolRequest) (domain.OperatorJob, error) {
	_ = ctx
	if s.operator == nil {
		return domain.OperatorJob{}, fmt.Errorf("operator client is not available")
	}
	if strings.TrimSpace(req.SpecName) == "" {
		return domain.OperatorJob{}, fmt.Errorf("specName is required")
	}
	if req.Replicas < 0 {
		return domain.OperatorJob{}, fmt.Errorf("replicas should be >= 0")
	}
	if strings.TrimSpace(req.Namespace) == "" {
		req.Namespace = "default"
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = fmt.Sprintf("pool-%s-%d", normalizeName(req.SpecName), time.Now().UTC().UnixNano())
	}

	jobID := fmt.Sprintf("job-%d", time.Now().UTC().UnixNano())
	now := time.Now().UTC()
	job := domain.OperatorJob{
		ID:              jobID,
		Type:            "create_stockpool",
		Status:          domain.OperatorJobPending,
		ObjectName:      req.Name,
		ObjectNamespace: req.Namespace,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	s.jobMu.Lock()
	s.jobs[jobID] = job
	s.jobMu.Unlock()

	s.jobQueue <- createStockPoolJob{jobID: jobID, req: req}
	return job, nil
}

func (s *Service) GetOperatorJob(ctx context.Context, jobID string) (domain.OperatorJob, error) {
	_ = ctx

	s.jobMu.RLock()
	defer s.jobMu.RUnlock()

	job, ok := s.jobs[strings.TrimSpace(jobID)]
	if !ok {
		return domain.OperatorJob{}, fmt.Errorf("job %s not found", jobID)
	}
	return job, nil
}

func (s *Service) ListStockPools(ctx context.Context, namespace string) ([]domain.StockPoolRuntime, error) {
	if s.operator == nil {
		return nil, fmt.Errorf("operator client is not available")
	}

	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = metav1.NamespaceAll
	}

	var list runtimev1alpha1.StockPoolList
	opts := []ctrlclient.ListOption{}
	if ns != metav1.NamespaceAll {
		opts = append(opts, ctrlclient.InNamespace(ns))
	}
	if err := s.operator.List(ctx, &list, opts...); err != nil {
		return nil, err
	}

	out := make([]domain.StockPoolRuntime, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, domain.StockPoolRuntime{
			Name:               item.Name,
			Namespace:          item.Namespace,
			SpecName:           item.Spec.SpecName,
			DesiredReplicas:    item.Spec.Replicas,
			AvailableReplicas:  item.Status.Available,
			AllocatedReplicas:  item.Status.Allocated,
			Phase:              item.Status.Phase,
			ObservedGeneration: item.Status.ObservedGeneration,
			LastSyncTime:       item.Status.LastSyncTime.Time,
		})
	}
	return out, nil
}

func (s *Service) Health(ctx context.Context) (domain.HealthStatus, error) {
	health := domain.HealthStatus{
		StartedAt:           s.startedAt,
		UptimeSeconds:       int64(time.Since(s.startedAt).Seconds()),
		KubernetesConnected: false,
		NodeCount:           0,
		StockPoolCount:      0,
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

	if s.operator != nil {
		var pools runtimev1alpha1.StockPoolList
		if err := s.operator.List(kubeCtx, &pools); err == nil {
			health.StockPoolCount = len(pools.Items)
		}
	}
	return health, nil
}

func (s *Service) createStockPoolObject(ctx context.Context, req CreateStockPoolRequest) error {
	obj := &runtimev1alpha1.StockPool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StockPool",
			APIVersion: runtimev1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
		Spec: runtimev1alpha1.StockPoolSpec{
			SpecName: req.SpecName,
			Replicas: req.Replicas,
		},
	}
	return s.operator.Create(ctx, obj)
}

func (s *Service) setJobRunning(jobID string) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	job := s.jobs[jobID]
	job.Status = domain.OperatorJobRunning
	job.UpdatedAt = time.Now().UTC()
	s.jobs[jobID] = job
}

func (s *Service) setJobFailed(jobID string, err error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	job := s.jobs[jobID]
	job.Status = domain.OperatorJobFailed
	job.Error = err.Error()
	job.UpdatedAt = time.Now().UTC()
	s.jobs[jobID] = job
}

func (s *Service) setJobSucceeded(jobID string, req CreateStockPoolRequest) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	job := s.jobs[jobID]
	job.Status = domain.OperatorJobSucceeded
	job.ObjectName = req.Name
	job.ObjectNamespace = req.Namespace
	job.UpdatedAt = time.Now().UTC()
	s.jobs[jobID] = job
}

func normalizeName(specName string) string {
	name := strings.ToLower(strings.TrimSpace(specName))
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, "_", "-")
	if name == "" {
		return "default"
	}
	return name
}
