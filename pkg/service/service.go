package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

type CreateStockPoolRequest struct {
	OperationID string                            `json:"operationID"`
	Name        string                            `json:"name,omitempty"`
	Namespace   string                            `json:"namespace,omitempty"`
	SpecName    string                            `json:"specName"`
	Image       string                            `json:"image,omitempty"`
	Memory      string                            `json:"memory,omitempty"`
	GPU         int32                             `json:"gpu,omitempty"`
	Replicas    int32                             `json:"replicas"`
	Template    runtimev1alpha1.StockPoolTemplate `json:"template,omitempty"`
}

type createStockPoolJob struct {
	operationID string
	requestHash string
	req         CreateStockPoolRequest
}

type Service struct {
	kube      kubernetes.Interface
	operator  ctrlclient.Client
	logger    *slog.Logger
	startedAt time.Time

	jobMu         sync.RWMutex
	jobs          map[string]domain.OperatorJob
	requestHashes map[string]string
	jobQueue      chan createStockPoolJob
}

func New(kubeClient kubernetes.Interface, operatorClient ctrlclient.Client, logger *slog.Logger) *Service {
	return &Service{
		kube:          kubeClient,
		operator:      operatorClient,
		logger:        logger,
		startedAt:     time.Now().UTC(),
		jobs:          map[string]domain.OperatorJob{},
		requestHashes: map[string]string{},
		jobQueue:      make(chan createStockPoolJob, 128),
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
			s.setJobRunning(job.operationID)
			if err := s.createStockPoolObject(ctx, job.operationID, job.requestHash, job.req); err != nil {
				s.setJobFailed(job.operationID, err)
				continue
			}
			s.setJobSucceeded(job.operationID, job.req)
		}
	}
}

func (s *Service) CreateStockPoolAsync(ctx context.Context, req CreateStockPoolRequest) (domain.OperatorJob, bool, error) {
	if s.operator == nil {
		return domain.OperatorJob{}, false, &UnavailableError{Message: "operator client is not available"}
	}

	req, requestHash, err := s.normalizeCreateStockPoolRequest(req)
	if err != nil {
		return domain.OperatorJob{}, false, err
	}

	if job, ok, err := s.findExistingOperation(ctx, req.OperationID, req.Namespace, req.Name, requestHash); err != nil {
		return domain.OperatorJob{}, false, err
	} else if ok {
		return job, false, nil
	}

	now := time.Now().UTC()
	job := domain.OperatorJob{
		ID:              req.OperationID,
		OperationID:     req.OperationID,
		Type:            "create_stockpool",
		Status:          domain.OperatorJobPending,
		ObjectName:      req.Name,
		ObjectNamespace: req.Namespace,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	s.jobMu.Lock()
	s.jobs[req.OperationID] = job
	s.requestHashes[req.OperationID] = requestHash
	s.jobMu.Unlock()

	s.jobQueue <- createStockPoolJob{
		operationID: req.OperationID,
		requestHash: requestHash,
		req:         req,
	}

	return job, true, nil
}

func (s *Service) GetOperatorJob(ctx context.Context, operationID string) (domain.OperatorJob, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return domain.OperatorJob{}, &ValidationError{Message: "operationID is required"}
	}

	s.jobMu.RLock()
	job, ok := s.jobs[operationID]
	s.jobMu.RUnlock()
	if ok {
		return job, nil
	}

	if s.operator == nil {
		return domain.OperatorJob{}, &NotFoundError{Message: fmt.Sprintf("operation %s not found", operationID)}
	}

	pool, err := s.findStockPoolByOperationID(ctx, operationID)
	if err != nil {
		return domain.OperatorJob{}, err
	}
	if pool == nil {
		return domain.OperatorJob{}, &NotFoundError{Message: fmt.Sprintf("operation %s not found", operationID)}
	}

	job = recoveredJobFromPool(operationID, pool)

	s.jobMu.Lock()
	s.jobs[operationID] = job
	s.requestHashes[operationID] = pool.GetAnnotations()[runtimev1alpha1.AnnotationRequestHash]
	s.jobMu.Unlock()

	return job, nil
}

func (s *Service) ListStockPools(ctx context.Context, namespace string) ([]domain.StockPoolRuntime, error) {
	if s.operator == nil {
		return nil, &UnavailableError{Message: "operator client is not available"}
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
		var reason string
		var message string
		if cond := apimeta.FindStatusCondition(item.Status.Conditions, runtimev1alpha1.ConditionReady); cond != nil {
			reason = cond.Reason
			message = cond.Message
		}

		out = append(out, domain.StockPoolRuntime{
			Name:               item.Name,
			Namespace:          item.Namespace,
			SpecName:           item.Spec.SpecName,
			Image:              item.Spec.Image,
			Memory:             item.Spec.Memory,
			GPU:                item.Spec.GPU,
			DesiredReplicas:    item.Spec.Replicas,
			AvailableReplicas:  item.Status.Available,
			AllocatedReplicas:  item.Status.Allocated,
			Phase:              item.Status.Phase,
			ObservedGeneration: item.Status.ObservedGeneration,
			LastSyncTime:       item.Status.LastSyncTime.Time,
			ServiceName:        item.Status.ServiceName,
			Reason:             reason,
			Message:            message,
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

func (s *Service) normalizeCreateStockPoolRequest(req CreateStockPoolRequest) (CreateStockPoolRequest, string, error) {
	req.OperationID = strings.TrimSpace(req.OperationID)
	if req.OperationID == "" {
		return CreateStockPoolRequest{}, "", &ValidationError{Message: "operationID is required"}
	}

	req.SpecName = strings.TrimSpace(req.SpecName)
	if req.SpecName == "" {
		return CreateStockPoolRequest{}, "", &ValidationError{Message: "specName is required"}
	}

	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if errs := validation.IsDNS1123Label(req.Namespace); len(errs) > 0 {
		return CreateStockPoolRequest{}, "", &ValidationError{
			Message: fmt.Sprintf("namespace %q is invalid: %s", req.Namespace, strings.Join(errs, ", ")),
		}
	}

	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if req.Name == "" {
		req.Name = generatedStockPoolName(req.SpecName, req.OperationID)
	}
	if errs := validation.IsDNS1123Subdomain(req.Name); len(errs) > 0 {
		return CreateStockPoolRequest{}, "", &ValidationError{
			Message: fmt.Sprintf("name %q is invalid: %s", req.Name, strings.Join(errs, ", ")),
		}
	}

	req.Image = strings.TrimSpace(req.Image)
	req.Memory = strings.TrimSpace(req.Memory)
	if req.Memory != "" {
		if _, err := resource.ParseQuantity(req.Memory); err != nil {
			return CreateStockPoolRequest{}, "", &ValidationError{Message: fmt.Sprintf("memory %q is invalid: %v", req.Memory, err)}
		}
	}
	if req.Replicas < 0 {
		return CreateStockPoolRequest{}, "", &ValidationError{Message: "replicas should be >= 0"}
	}
	if req.GPU < 0 {
		return CreateStockPoolRequest{}, "", &ValidationError{Message: "gpu should be >= 0"}
	}

	template, err := normalizeTemplate(req.Template)
	if err != nil {
		return CreateStockPoolRequest{}, "", err
	}
	req.Template = template

	requestHash, err := hashCreateRequest(req)
	if err != nil {
		return CreateStockPoolRequest{}, "", err
	}
	return req, requestHash, nil
}

func normalizeTemplate(t runtimev1alpha1.StockPoolTemplate) (runtimev1alpha1.StockPoolTemplate, error) {
	seenEnvNames := map[string]struct{}{}
	for i := range t.Envs {
		t.Envs[i].Name = strings.TrimSpace(t.Envs[i].Name)
		if t.Envs[i].Name == "" {
			return runtimev1alpha1.StockPoolTemplate{}, &ValidationError{Message: "template env name is required"}
		}
		if errs := validation.IsEnvVarName(t.Envs[i].Name); len(errs) > 0 {
			return runtimev1alpha1.StockPoolTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template env %q is invalid: %s", t.Envs[i].Name, strings.Join(errs, ", ")),
			}
		}
		if _, exists := seenEnvNames[t.Envs[i].Name]; exists {
			return runtimev1alpha1.StockPoolTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template env %q is duplicated", t.Envs[i].Name),
			}
		}
		seenEnvNames[t.Envs[i].Name] = struct{}{}
	}

	seenPortNames := map[string]struct{}{}
	seenPortNumbers := map[int32]struct{}{}
	for i := range t.Ports {
		t.Ports[i].Name = strings.TrimSpace(t.Ports[i].Name)
		if t.Ports[i].Name == "" {
			return runtimev1alpha1.StockPoolTemplate{}, &ValidationError{Message: "template port name is required"}
		}
		if errs := validation.IsValidPortName(t.Ports[i].Name); len(errs) > 0 {
			return runtimev1alpha1.StockPoolTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template port name %q is invalid: %s", t.Ports[i].Name, strings.Join(errs, ", ")),
			}
		}
		if t.Ports[i].Port <= 0 || t.Ports[i].Port > 65535 {
			return runtimev1alpha1.StockPoolTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template port %d is out of range", t.Ports[i].Port),
			}
		}
		if t.Ports[i].Protocol == "" {
			t.Ports[i].Protocol = "TCP"
		}
		if _, exists := seenPortNames[t.Ports[i].Name]; exists {
			return runtimev1alpha1.StockPoolTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template port name %q is duplicated", t.Ports[i].Name),
			}
		}
		if _, exists := seenPortNumbers[t.Ports[i].Port]; exists {
			return runtimev1alpha1.StockPoolTemplate{}, &ValidationError{
				Message: fmt.Sprintf("template port %d is duplicated", t.Ports[i].Port),
			}
		}
		seenPortNames[t.Ports[i].Name] = struct{}{}
		seenPortNumbers[t.Ports[i].Port] = struct{}{}
	}

	return t, nil
}

func hashCreateRequest(req CreateStockPoolRequest) (string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal create request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) findExistingOperation(ctx context.Context, operationID, namespace, objectName, requestHash string) (domain.OperatorJob, bool, error) {
	s.jobMu.RLock()
	job, jobExists := s.jobs[operationID]
	existingHash := s.requestHashes[operationID]
	s.jobMu.RUnlock()
	if jobExists {
		if existingHash != requestHash {
			return domain.OperatorJob{}, false, &ConflictError{
				Message: fmt.Sprintf("operation %s already exists with a different request payload", operationID),
			}
		}
		return job, true, nil
	}

	pool, err := s.findStockPoolByOperationID(ctx, operationID)
	if err != nil {
		return domain.OperatorJob{}, false, err
	}
	if pool != nil {
		if pool.GetAnnotations()[runtimev1alpha1.AnnotationRequestHash] != requestHash {
			return domain.OperatorJob{}, false, &ConflictError{
				Message: fmt.Sprintf("operation %s already exists with a different request payload", operationID),
			}
		}
		job = recoveredJobFromPool(operationID, pool)
		s.jobMu.Lock()
		s.jobs[operationID] = job
		s.requestHashes[operationID] = requestHash
		s.jobMu.Unlock()
		return job, true, nil
	}

	pool, err = s.findStockPoolByName(ctx, namespace, objectName)
	if err != nil {
		return domain.OperatorJob{}, false, err
	}
	if pool == nil {
		return domain.OperatorJob{}, false, nil
	}

	existingOperationID := pool.GetAnnotations()[runtimev1alpha1.AnnotationOperationID]
	if existingOperationID == operationID && pool.GetAnnotations()[runtimev1alpha1.AnnotationRequestHash] == requestHash {
		job = recoveredJobFromPool(operationID, pool)
		s.jobMu.Lock()
		s.jobs[operationID] = job
		s.requestHashes[operationID] = requestHash
		s.jobMu.Unlock()
		return job, true, nil
	}

	return domain.OperatorJob{}, false, &ConflictError{
		Message: fmt.Sprintf("stockpool name %s/%s is already in use", pool.Namespace, pool.Name),
	}
}

func (s *Service) findStockPoolByOperationID(ctx context.Context, operationID string) (*runtimev1alpha1.StockPool, error) {
	var list runtimev1alpha1.StockPoolList
	if err := s.operator.List(ctx, &list); err != nil {
		return nil, err
	}

	var found *runtimev1alpha1.StockPool
	for i := range list.Items {
		item := &list.Items[i]
		if item.GetAnnotations()[runtimev1alpha1.AnnotationOperationID] != operationID {
			continue
		}
		if found != nil {
			return nil, &ConflictError{
				Message: fmt.Sprintf("multiple stockpools exist for operation %s", operationID),
			}
		}
		found = item.DeepCopy()
	}
	return found, nil
}

func (s *Service) findStockPoolByName(ctx context.Context, namespace, name string) (*runtimev1alpha1.StockPool, error) {
	var list runtimev1alpha1.StockPoolList
	if err := s.operator.List(ctx, &list); err != nil {
		return nil, err
	}

	for i := range list.Items {
		if list.Items[i].Namespace == namespace && list.Items[i].Name == name {
			return list.Items[i].DeepCopy(), nil
		}
	}
	return nil, nil
}

func recoveredJobFromPool(operationID string, pool *runtimev1alpha1.StockPool) domain.OperatorJob {
	createdAt := pool.CreationTimestamp.Time
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := pool.Status.LastSyncTime.Time
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	return domain.OperatorJob{
		ID:              operationID,
		OperationID:     operationID,
		Type:            "create_stockpool",
		Status:          domain.OperatorJobSucceeded,
		ObjectName:      pool.Name,
		ObjectNamespace: pool.Namespace,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
}

func (s *Service) createStockPoolObject(ctx context.Context, operationID, requestHash string, req CreateStockPoolRequest) error {
	obj := &runtimev1alpha1.StockPool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StockPool",
			APIVersion: runtimev1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Annotations: map[string]string{
				runtimev1alpha1.AnnotationOperationID: operationID,
				runtimev1alpha1.AnnotationRequestHash: requestHash,
			},
		},
		Spec: runtimev1alpha1.StockPoolSpec{
			SpecName: req.SpecName,
			Image:    req.Image,
			Memory:   req.Memory,
			GPU:      req.GPU,
			Replicas: req.Replicas,
			Template: req.Template,
		},
	}

	if err := s.operator.Create(ctx, obj); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}

		var existing runtimev1alpha1.StockPool
		if getErr := s.operator.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, &existing); getErr != nil {
			return getErr
		}
		if existing.GetAnnotations()[runtimev1alpha1.AnnotationOperationID] != operationID {
			return &ConflictError{
				Message: fmt.Sprintf("stockpool name %s/%s is already in use", existing.Namespace, existing.Name),
			}
		}
		if existing.GetAnnotations()[runtimev1alpha1.AnnotationRequestHash] != requestHash {
			return &ConflictError{
				Message: fmt.Sprintf("operation %s already exists with a different request payload", operationID),
			}
		}
	}

	return nil
}

func (s *Service) setJobRunning(operationID string) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	job := s.jobs[operationID]
	job.Status = domain.OperatorJobRunning
	job.UpdatedAt = time.Now().UTC()
	s.jobs[operationID] = job
}

func (s *Service) setJobFailed(operationID string, err error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	job := s.jobs[operationID]
	job.Status = domain.OperatorJobFailed
	job.Error = err.Error()
	job.UpdatedAt = time.Now().UTC()
	s.jobs[operationID] = job
}

func (s *Service) setJobSucceeded(operationID string, req CreateStockPoolRequest) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	job := s.jobs[operationID]
	job.Status = domain.OperatorJobSucceeded
	job.ObjectName = req.Name
	job.ObjectNamespace = req.Namespace
	job.UpdatedAt = time.Now().UTC()
	s.jobs[operationID] = job
}

func generatedStockPoolName(specName, operationID string) string {
	name := fmt.Sprintf("pool-%s-%s", normalizeName(specName), shortHash(operationID))
	if len(name) <= 63 {
		return name
	}
	name = name[:63]
	return strings.TrimRight(name, "-")
}

func shortHash(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:10]
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
