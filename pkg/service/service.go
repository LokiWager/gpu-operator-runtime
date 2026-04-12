package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

// CreateStockUnitsRequest describes one idempotent request to seed stock units.
//
// Stock units reserve capacity with the built-in stock image. The caller only chooses the stock class and resource envelope.
type CreateStockUnitsRequest struct {
	OperationID string `json:"operationID"`
	SpecName    string `json:"specName"`
	Memory      string `json:"memory,omitempty"`
	GPU         int32  `json:"gpu,omitempty"`
	Replicas    int32  `json:"replicas"`
}

// createStockUnitsJob is the in-memory work item consumed by the async worker.
type createStockUnitsJob struct {
	operationID string
	requestHash string
	req         CreateStockUnitsRequest
}

// Service owns the control-plane business logic behind the HTTP API.
type Service struct {
	kube      kubernetes.Interface
	operator  ctrlclient.Client
	logger    *slog.Logger
	startedAt time.Time

	unitMu        sync.Mutex
	jobMu         sync.RWMutex
	jobs          map[string]domain.OperatorJob
	requestHashes map[string]string
	jobQueue      chan createStockUnitsJob
}

// New builds a Service with optional Kubernetes and operator clients.
func New(kubeClient kubernetes.Interface, operatorClient ctrlclient.Client, logger *slog.Logger) *Service {
	return &Service{
		kube:          kubeClient,
		operator:      operatorClient,
		logger:        logger,
		startedAt:     time.Now().UTC(),
		jobs:          map[string]domain.OperatorJob{},
		requestHashes: map[string]string{},
		jobQueue:      make(chan createStockUnitsJob, 128),
	}
}

// StartOperatorJobWorker drains async stock seeding jobs until the context is cancelled.
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
			if err := s.createStockUnits(ctx, job.operationID, job.requestHash, job.req); err != nil {
				s.setJobFailed(job.operationID, err)
				continue
			}
			s.setJobSucceeded(job.operationID, job.req)
		}
	}
}

// CreateStockUnitsAsync validates a stock seeding request and enqueues it once per operationID.
func (s *Service) CreateStockUnitsAsync(ctx context.Context, req CreateStockUnitsRequest) (domain.OperatorJob, bool, error) {
	if s.operator == nil {
		return domain.OperatorJob{}, false, &UnavailableError{Message: "operator client is not available"}
	}

	req, requestHash, err := s.normalizeCreateStockUnitsRequest(req)
	if err != nil {
		return domain.OperatorJob{}, false, err
	}

	if job, ok, err := s.findExistingStockOperation(ctx, req.OperationID, requestHash); err != nil {
		return domain.OperatorJob{}, false, err
	} else if ok {
		return job, false, nil
	}

	now := time.Now().UTC()
	job := domain.OperatorJob{
		ID:              req.OperationID,
		OperationID:     req.OperationID,
		Type:            "seed_stock_units",
		Status:          domain.OperatorJobPending,
		ObjectName:      stockUnitBatchName(req.SpecName, req.OperationID),
		ObjectNamespace: runtimev1alpha1.DefaultStockNamespace,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	s.jobMu.Lock()
	s.jobs[req.OperationID] = job
	s.requestHashes[req.OperationID] = requestHash
	s.jobMu.Unlock()

	s.jobQueue <- createStockUnitsJob{
		operationID: req.OperationID,
		requestHash: requestHash,
		req:         req,
	}

	return job, true, nil
}

// GetOperatorJob returns in-memory or reconstructed status for one stock seeding operation.
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

	units, err := s.findStockUnitsByOperationID(ctx, operationID)
	if err != nil {
		return domain.OperatorJob{}, err
	}
	if len(units) == 0 {
		return domain.OperatorJob{}, &NotFoundError{Message: fmt.Sprintf("operation %s not found", operationID)}
	}

	job = recoveredJobFromStockUnits(operationID, units)

	s.jobMu.Lock()
	s.jobs[operationID] = job
	s.requestHashes[operationID] = units[0].GetAnnotations()[runtimev1alpha1.AnnotationRequestHash]
	s.jobMu.Unlock()

	return job, nil
}

// Health reports process uptime, cluster reachability, and unit counts.
func (s *Service) Health(ctx context.Context) (domain.HealthStatus, error) {
	health := domain.HealthStatus{
		StartedAt:           s.startedAt,
		UptimeSeconds:       int64(time.Since(s.startedAt).Seconds()),
		KubernetesConnected: false,
		NodeCount:           0,
		StockUnitCount:      0,
		ActiveUnitCount:     0,
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
		var units runtimev1alpha1.GPUUnitList
		if err := s.operator.List(kubeCtx, &units); err == nil {
			for i := range units.Items {
				if isStockGPUUnit(&units.Items[i]) {
					health.StockUnitCount++
				} else {
					health.ActiveUnitCount++
				}
			}
		}
	}
	return health, nil
}

// normalizeCreateStockUnitsRequest trims, validates, and hashes a stock seeding request.
func (s *Service) normalizeCreateStockUnitsRequest(req CreateStockUnitsRequest) (CreateStockUnitsRequest, string, error) {
	req.OperationID = strings.TrimSpace(req.OperationID)
	if req.OperationID == "" {
		return CreateStockUnitsRequest{}, "", &ValidationError{Message: "operationID is required"}
	}

	req.SpecName = strings.TrimSpace(req.SpecName)
	if req.SpecName == "" {
		return CreateStockUnitsRequest{}, "", &ValidationError{Message: "specName is required"}
	}

	req.Memory = strings.TrimSpace(req.Memory)
	if req.Memory != "" {
		if _, err := resource.ParseQuantity(req.Memory); err != nil {
			return CreateStockUnitsRequest{}, "", &ValidationError{Message: fmt.Sprintf("memory %q is invalid: %v", req.Memory, err)}
		}
	}
	if req.Replicas <= 0 {
		return CreateStockUnitsRequest{}, "", &ValidationError{Message: "replicas should be > 0"}
	}
	if req.GPU < 0 {
		return CreateStockUnitsRequest{}, "", &ValidationError{Message: "gpu should be >= 0"}
	}

	requestHash, err := hashCreateStockUnitsRequest(req)
	if err != nil {
		return CreateStockUnitsRequest{}, "", err
	}
	return req, requestHash, nil
}

// hashCreateStockUnitsRequest creates the stable payload hash used for idempotency checks.
func hashCreateStockUnitsRequest(req CreateStockUnitsRequest) (string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal stock create request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// findExistingStockOperation resolves idempotent replays and detects conflicting payload reuse.
func (s *Service) findExistingStockOperation(ctx context.Context, operationID, requestHash string) (domain.OperatorJob, bool, error) {
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

	units, err := s.findStockUnitsByOperationID(ctx, operationID)
	if err != nil {
		return domain.OperatorJob{}, false, err
	}
	if len(units) == 0 {
		return domain.OperatorJob{}, false, nil
	}
	if units[0].GetAnnotations()[runtimev1alpha1.AnnotationRequestHash] != requestHash {
		return domain.OperatorJob{}, false, &ConflictError{
			Message: fmt.Sprintf("operation %s already exists with a different request payload", operationID),
		}
	}

	job = recoveredJobFromStockUnits(operationID, units)
	s.jobMu.Lock()
	s.jobs[operationID] = job
	s.requestHashes[operationID] = requestHash
	s.jobMu.Unlock()
	return job, true, nil
}

// findStockUnitsByOperationID loads stock units created by one operator request.
func (s *Service) findStockUnitsByOperationID(ctx context.Context, operationID string) ([]runtimev1alpha1.GPUUnit, error) {
	var list runtimev1alpha1.GPUUnitList
	if err := s.operator.List(ctx, &list, ctrlclient.InNamespace(runtimev1alpha1.DefaultStockNamespace)); err != nil {
		return nil, err
	}

	units := make([]runtimev1alpha1.GPUUnit, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].GetAnnotations()[runtimev1alpha1.AnnotationOperationID] != operationID {
			continue
		}
		units = append(units, list.Items[i])
	}

	sort.Slice(units, func(i, j int) bool {
		if units[i].Name != units[j].Name {
			return units[i].Name < units[j].Name
		}
		return units[i].CreationTimestamp.Time.Before(units[j].CreationTimestamp.Time)
	})

	return units, nil
}

// recoveredJobFromStockUnits rebuilds operator job status from persisted stock units.
func recoveredJobFromStockUnits(operationID string, units []runtimev1alpha1.GPUUnit) domain.OperatorJob {
	createdAt := time.Now().UTC()
	updatedAt := createdAt
	if len(units) > 0 {
		createdAt = units[0].CreationTimestamp.Time
		updatedAt = createdAt
	}
	for i := range units {
		if ts := units[i].CreationTimestamp.Time; !ts.IsZero() && ts.Before(createdAt) {
			createdAt = ts
		}
		if ts := units[i].CreationTimestamp.Time; ts.After(updatedAt) {
			updatedAt = ts
		}
	}

	expectedReplicas := int32(len(units))
	if raw := strings.TrimSpace(units[0].GetAnnotations()[runtimev1alpha1.AnnotationStockReplicas]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			expectedReplicas = int32(parsed)
		}
	}

	status := domain.OperatorJobSucceeded
	if int32(len(units)) < expectedReplicas {
		status = domain.OperatorJobRunning
	}

	return domain.OperatorJob{
		ID:              operationID,
		OperationID:     operationID,
		Type:            "seed_stock_units",
		Status:          status,
		ObjectName:      stockUnitBatchName(units[0].Spec.SpecName, operationID),
		ObjectNamespace: runtimev1alpha1.DefaultStockNamespace,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
}

// createStockUnits upserts the desired stock unit set for one operation.
func (s *Service) createStockUnits(ctx context.Context, operationID, requestHash string, req CreateStockUnitsRequest) error {
	for ordinal := int32(0); ordinal < req.Replicas; ordinal++ {
		name := generatedStockUnitName(req.SpecName, operationID, ordinal)
		desired := desiredStockUnit(req, operationID, requestHash, name, ordinal)

		var existing runtimev1alpha1.GPUUnit
		err := s.operator.Get(ctx, types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}, &existing)
		if apierrors.IsNotFound(err) {
			if err := s.operator.Create(ctx, desired); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if existing.GetAnnotations()[runtimev1alpha1.AnnotationOperationID] != operationID {
			return &ConflictError{Message: fmt.Sprintf("stock unit name %s/%s is already in use", existing.Namespace, existing.Name)}
		}
		if existing.GetAnnotations()[runtimev1alpha1.AnnotationRequestHash] != requestHash {
			return &ConflictError{Message: fmt.Sprintf("operation %s already exists with a different request payload", operationID)}
		}

		needsUpdate := false
		if !reflect.DeepEqual(existing.Spec, desired.Spec) {
			existing.Spec = desired.Spec
			needsUpdate = true
		}

		labels := mergeManagedMap(existing.Labels, desired.Labels)
		if !reflect.DeepEqual(existing.Labels, labels) {
			existing.Labels = labels
			needsUpdate = true
		}

		annotations := mergeManagedMap(existing.Annotations, desired.Annotations)
		if !reflect.DeepEqual(existing.Annotations, annotations) {
			existing.Annotations = annotations
			needsUpdate = true
		}

		if needsUpdate {
			if err := s.operator.Update(ctx, &existing); err != nil {
				return err
			}
		}
	}

	return nil
}

// desiredStockUnit materializes the GPUUnit object stored in the stock namespace.
func desiredStockUnit(req CreateStockUnitsRequest, operationID, requestHash, name string, ordinal int32) *runtimev1alpha1.GPUUnit {
	return &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GPUUnit",
			APIVersion: runtimev1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: runtimev1alpha1.DefaultStockNamespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelAppNameKey:   runtimev1alpha1.LabelAppNameValue,
				runtimev1alpha1.LabelManagedByKey: runtimev1alpha1.LabelManagedByValue,
				runtimev1alpha1.LabelUnitKey:      name,
			},
			Annotations: map[string]string{
				runtimev1alpha1.AnnotationOperationID:   operationID,
				runtimev1alpha1.AnnotationRequestHash:   requestHash,
				runtimev1alpha1.AnnotationStockReplicas: strconv.Itoa(int(req.Replicas)),
				runtimev1alpha1.AnnotationStockOrdinal:  strconv.Itoa(int(ordinal + 1)),
			},
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: req.SpecName,
			Image:    runtimev1alpha1.StockReservationImage,
			Memory:   req.Memory,
			GPU:      req.GPU,
		},
	}
}

// mergeManagedMap overlays operator-managed keys while preserving unrelated user metadata.
func mergeManagedMap(current, managed map[string]string) map[string]string {
	if len(current) == 0 {
		out := make(map[string]string, len(managed))
		for key, value := range managed {
			out[key] = value
		}
		return out
	}

	out := make(map[string]string, len(current)+len(managed))
	for key, value := range current {
		out[key] = value
	}
	for key, value := range managed {
		out[key] = value
	}
	return out
}

// setJobRunning marks one in-memory job as running.
func (s *Service) setJobRunning(operationID string) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	job := s.jobs[operationID]
	job.Status = domain.OperatorJobRunning
	job.UpdatedAt = time.Now().UTC()
	s.jobs[operationID] = job
}

// setJobFailed marks one in-memory job as failed.
func (s *Service) setJobFailed(operationID string, err error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	job := s.jobs[operationID]
	job.Status = domain.OperatorJobFailed
	job.Error = err.Error()
	job.UpdatedAt = time.Now().UTC()
	s.jobs[operationID] = job
}

// setJobSucceeded marks one in-memory job as succeeded.
func (s *Service) setJobSucceeded(operationID string, req CreateStockUnitsRequest) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	job := s.jobs[operationID]
	job.Status = domain.OperatorJobSucceeded
	job.ObjectName = stockUnitBatchName(req.SpecName, req.OperationID)
	job.ObjectNamespace = runtimev1alpha1.DefaultStockNamespace
	job.UpdatedAt = time.Now().UTC()
	s.jobs[operationID] = job
}

// stockUnitBatchName creates the shared name prefix for all units seeded by one operation.
func stockUnitBatchName(specName, operationID string) string {
	name := fmt.Sprintf("stock-%s-%s", normalizeName(specName), shortHash(operationID))
	if len(name) <= 59 {
		return name
	}
	return strings.TrimRight(name[:59], "-")
}

// generatedStockUnitName creates a deterministic unit name within one stock batch.
func generatedStockUnitName(specName, operationID string, ordinal int32) string {
	suffix := fmt.Sprintf("-%03d", ordinal+1)
	prefix := stockUnitBatchName(specName, operationID)
	if len(prefix)+len(suffix) > 63 {
		prefix = strings.TrimRight(prefix[:63-len(suffix)], "-")
	}
	return prefix + suffix
}

// shortHash returns a short stable digest used in object names.
func shortHash(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:10]
}

// normalizeName turns a spec name into a DNS-safe object-name fragment.
func normalizeName(specName string) string {
	name := strings.ToLower(strings.TrimSpace(specName))
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, "_", "-")
	if name == "" {
		return "default"
	}
	return name
}
