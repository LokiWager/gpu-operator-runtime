package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

// CreateGPUUnitRequest asks the service to consume one ready stock unit into active runtime.
//
// The request supplies the user-visible runtime image and pod slice, while the reserved memory and GPU shape still come from stock.
type CreateGPUUnitRequest struct {
	OperationID    string                                `json:"operationID"`
	Name           string                                `json:"name"`
	Namespace      string                                `json:"namespace,omitempty"`
	SpecName       string                                `json:"specName"`
	StockNamespace string                                `json:"stockNamespace,omitempty"`
	Image          string                                `json:"image"`
	Template       runtimev1alpha1.GPUUnitTemplate       `json:"template,omitempty"`
	Access         runtimev1alpha1.GPUUnitAccess         `json:"access,omitempty"`
	StorageMounts  []runtimev1alpha1.GPUUnitStorageMount `json:"storageMounts,omitempty"`
}

// UpdateGPUUnitRequest captures the mutable runtime fields for an active unit.
type UpdateGPUUnitRequest struct {
	Image         string                                 `json:"image,omitempty"`
	Template      runtimev1alpha1.GPUUnitTemplate        `json:"template,omitempty"`
	Access        runtimev1alpha1.GPUUnitAccess          `json:"access,omitempty"`
	StorageMounts *[]runtimev1alpha1.GPUUnitStorageMount `json:"storageMounts,omitempty"`
}

// CreateGPUUnit consumes one ready stock unit and creates an active GPUUnit object.
func (s *Service) CreateGPUUnit(ctx context.Context, req CreateGPUUnitRequest) (domain.GPUUnitRuntime, bool, error) {
	if s.operator == nil {
		return domain.GPUUnitRuntime{}, false, &UnavailableError{Message: "operator client is not available"}
	}

	req, requestHash, err := normalizeCreateGPUUnitRequest(req)
	if err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}
	if err := s.ensureGPUStoragesExist(ctx, req.Namespace, req.StorageMounts); err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}

	s.unitMu.Lock()
	defer s.unitMu.Unlock()

	if runtimeView, ok, err := s.findExistingGPUUnitOperation(ctx, req.OperationID, req.Namespace, req.Name, requestHash); err != nil {
		return domain.GPUUnitRuntime{}, false, err
	} else if ok {
		return runtimeView, false, nil
	}

	if err := s.ensureGPUStoragesExclusivelyMountable(ctx, req.Namespace, "", req.StorageMounts); err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}

	stock, err := s.claimReadyStockUnit(ctx, req.StockNamespace, req.SpecName, req.OperationID)
	if err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}

	originalStock := stock.DeepCopy()

	active, err := buildActiveUnitFromStock(*stock, req, requestHash)
	if err != nil {
		_ = s.releaseStockClaim(ctx, originalStock)
		return domain.GPUUnitRuntime{}, false, err
	}

	if err := s.operator.Delete(ctx, stock); err != nil {
		_ = s.releaseStockClaim(ctx, originalStock)
		return domain.GPUUnitRuntime{}, false, err
	}

	if err := s.operator.Create(ctx, active); err != nil {
		restore := originalStock.DeepCopy()
		clearStockClaimAnnotations(restore)
		_ = s.operator.Create(ctx, restore)

		if apierrors.IsAlreadyExists(err) {
			var existing runtimev1alpha1.GPUUnit
			if getErr := s.operator.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, &existing); getErr == nil {
				if existing.GetAnnotations()[runtimev1alpha1.AnnotationOperationID] == req.OperationID &&
					existing.GetAnnotations()[runtimev1alpha1.AnnotationRequestHash] == requestHash &&
					isActiveGPUUnit(&existing) {
					return gpuUnitRuntimeFromObject(&existing), false, nil
				}
			}
		}
		return domain.GPUUnitRuntime{}, false, err
	}

	return gpuUnitRuntimeFromObject(active), true, nil
}

// ListGPUUnits returns active runtime units, optionally filtered by namespace.
func (s *Service) ListGPUUnits(ctx context.Context, namespace string) ([]domain.GPUUnitRuntime, error) {
	if s.operator == nil {
		return nil, &UnavailableError{Message: "operator client is not available"}
	}

	ns, err := normalizeNamespace(namespace)
	if err != nil {
		return nil, err
	}

	var list runtimev1alpha1.GPUUnitList
	opts := []ctrlclient.ListOption{}
	if ns != metav1.NamespaceAll {
		opts = append(opts, ctrlclient.InNamespace(ns))
	}
	if err := s.operator.List(ctx, &list, opts...); err != nil {
		return nil, err
	}

	out := make([]domain.GPUUnitRuntime, 0, len(list.Items))
	for i := range list.Items {
		if !isActiveGPUUnit(&list.Items[i]) {
			continue
		}
		out = append(out, gpuUnitRuntimeFromObject(&list.Items[i]))
	}
	return out, nil
}

// GetGPUUnit returns one active runtime unit by namespace and name.
func (s *Service) GetGPUUnit(ctx context.Context, namespace, name string) (domain.GPUUnitRuntime, error) {
	if s.operator == nil {
		return domain.GPUUnitRuntime{}, &UnavailableError{Message: "operator client is not available"}
	}

	instance, err := s.getActiveGPUUnit(ctx, namespace, name)
	if err != nil {
		return domain.GPUUnitRuntime{}, err
	}
	return gpuUnitRuntimeFromObject(instance), nil
}

// UpdateGPUUnit mutates the allowed fields on an active runtime unit.
func (s *Service) UpdateGPUUnit(ctx context.Context, namespace, name string, req UpdateGPUUnitRequest) (domain.GPUUnitRuntime, error) {
	if s.operator == nil {
		return domain.GPUUnitRuntime{}, &UnavailableError{Message: "operator client is not available"}
	}

	s.unitMu.Lock()
	defer s.unitMu.Unlock()

	req, err := normalizeUpdateGPUUnitRequest(req)
	if err != nil {
		return domain.GPUUnitRuntime{}, err
	}

	instance, err := s.getActiveGPUUnit(ctx, namespace, name)
	if err != nil {
		return domain.GPUUnitRuntime{}, err
	}

	next := instance.Spec
	if req.Image != "" {
		next.Image = req.Image
	}
	if !isZeroGPUUnitTemplate(req.Template) {
		next.Template = req.Template
	}

	accessInput := next.Access
	if !isZeroGPUUnitAccess(req.Access) {
		accessInput = req.Access
	}
	access, err := normalizeGPUUnitAccess(accessInput, next.Template.Ports)
	if err != nil {
		if isZeroGPUUnitAccess(req.Access) {
			access, err = normalizeGPUUnitAccess(runtimev1alpha1.GPUUnitAccess{}, next.Template.Ports)
		}
		if err != nil {
			return domain.GPUUnitRuntime{}, err
		}
	}
	next.Access = access
	if req.StorageMounts != nil {
		if err := s.ensureGPUStoragesExist(ctx, instance.Namespace, *req.StorageMounts); err != nil {
			return domain.GPUUnitRuntime{}, err
		}
		if err := s.ensureGPUStoragesExclusivelyMountable(ctx, instance.Namespace, instance.Name, *req.StorageMounts); err != nil {
			return domain.GPUUnitRuntime{}, err
		}
		next.StorageMounts = append([]runtimev1alpha1.GPUUnitStorageMount(nil), (*req.StorageMounts)...)
	}

	if reflect.DeepEqual(instance.Spec, next) {
		return gpuUnitRuntimeFromObject(instance), nil
	}

	instance.Spec = next
	if err := s.operator.Update(ctx, instance); err != nil {
		return domain.GPUUnitRuntime{}, err
	}

	return gpuUnitRuntimeFromObject(instance), nil
}

// DeleteGPUUnit deletes one active runtime unit.
func (s *Service) DeleteGPUUnit(ctx context.Context, namespace, name string) error {
	if s.operator == nil {
		return &UnavailableError{Message: "operator client is not available"}
	}

	instance, err := s.getActiveGPUUnit(ctx, namespace, name)
	if err != nil {
		return err
	}
	return s.operator.Delete(ctx, instance)
}

// normalizeCreateGPUUnitRequest validates and hashes a create request for idempotency checks.
func normalizeCreateGPUUnitRequest(req CreateGPUUnitRequest) (CreateGPUUnitRequest, string, error) {
	req.OperationID = strings.TrimSpace(req.OperationID)
	if req.OperationID == "" {
		return CreateGPUUnitRequest{}, "", &ValidationError{Message: "operationID is required"}
	}

	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if req.Name == "" {
		return CreateGPUUnitRequest{}, "", &ValidationError{Message: "name is required"}
	}
	if errs := validation.IsDNS1123Subdomain(req.Name); len(errs) > 0 {
		return CreateGPUUnitRequest{}, "", &ValidationError{
			Message: fmt.Sprintf("name %q is invalid: %s", req.Name, strings.Join(errs, ", ")),
		}
	}

	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		req.Namespace = runtimev1alpha1.DefaultInstanceNamespace
	}
	if errs := validation.IsDNS1123Label(req.Namespace); len(errs) > 0 {
		return CreateGPUUnitRequest{}, "", &ValidationError{
			Message: fmt.Sprintf("namespace %q is invalid: %s", req.Namespace, strings.Join(errs, ", ")),
		}
	}

	req.SpecName = strings.TrimSpace(req.SpecName)
	if req.SpecName == "" {
		return CreateGPUUnitRequest{}, "", &ValidationError{Message: "specName is required"}
	}

	req.Image = strings.TrimSpace(req.Image)
	if req.Image == "" {
		return CreateGPUUnitRequest{}, "", &ValidationError{Message: "image is required"}
	}

	req.StockNamespace = strings.TrimSpace(req.StockNamespace)
	if req.StockNamespace == "" {
		req.StockNamespace = runtimev1alpha1.DefaultStockNamespace
	}
	if errs := validation.IsDNS1123Label(req.StockNamespace); len(errs) > 0 {
		return CreateGPUUnitRequest{}, "", &ValidationError{
			Message: fmt.Sprintf("stockNamespace %q is invalid: %s", req.StockNamespace, strings.Join(errs, ", ")),
		}
	}
	if req.Namespace == req.StockNamespace {
		return CreateGPUUnitRequest{}, "", &ValidationError{
			Message: fmt.Sprintf("target namespace %q must differ from stockNamespace", req.Namespace),
		}
	}

	template, err := normalizeTemplate(req.Template)
	if err != nil {
		return CreateGPUUnitRequest{}, "", err
	}
	req.Template = template

	access, err := normalizeGPUUnitAccess(req.Access, req.Template.Ports)
	if err != nil {
		return CreateGPUUnitRequest{}, "", err
	}
	req.Access = access

	mounts, err := normalizeGPUUnitStorageMounts(req.StorageMounts)
	if err != nil {
		return CreateGPUUnitRequest{}, "", err
	}
	req.StorageMounts = mounts

	requestHash, err := hashGPUUnitCreateRequest(req)
	if err != nil {
		return CreateGPUUnitRequest{}, "", err
	}
	return req, requestHash, nil
}

// normalizeUpdateGPUUnitRequest validates the mutable runtime fields for an update.
func normalizeUpdateGPUUnitRequest(req UpdateGPUUnitRequest) (UpdateGPUUnitRequest, error) {
	req.Image = strings.TrimSpace(req.Image)

	template, err := normalizeTemplate(req.Template)
	if err != nil {
		return UpdateGPUUnitRequest{}, err
	}
	req.Template = template

	req.Access.PrimaryPort = strings.TrimSpace(req.Access.PrimaryPort)
	req.Access.Scheme = strings.ToLower(strings.TrimSpace(req.Access.Scheme))
	if req.StorageMounts != nil {
		mounts, err := normalizeGPUUnitStorageMounts(*req.StorageMounts)
		if err != nil {
			return UpdateGPUUnitRequest{}, err
		}
		req.StorageMounts = &mounts
	}
	return req, nil
}

// normalizeGPUUnitAccess validates the named access port against the runtime template.
func normalizeGPUUnitAccess(access runtimev1alpha1.GPUUnitAccess, ports []runtimev1alpha1.GPUUnitPortSpec) (runtimev1alpha1.GPUUnitAccess, error) {
	access.PrimaryPort = strings.TrimSpace(access.PrimaryPort)
	access.Scheme = strings.ToLower(strings.TrimSpace(access.Scheme))
	if access.Scheme == "" {
		access.Scheme = runtimev1alpha1.DefaultAccessScheme
	}

	if len(ports) == 0 {
		if access.PrimaryPort != "" {
			return runtimev1alpha1.GPUUnitAccess{}, &ValidationError{
				Message: fmt.Sprintf("access.primaryPort %q requires at least one runtime port", access.PrimaryPort),
			}
		}
		return access, nil
	}

	if access.PrimaryPort == "" {
		access.PrimaryPort = ports[0].Name
	}

	for _, port := range ports {
		if port.Name == access.PrimaryPort {
			return access, nil
		}
	}

	return runtimev1alpha1.GPUUnitAccess{}, &ValidationError{
		Message: fmt.Sprintf("access.primaryPort %q does not exist in template.ports", access.PrimaryPort),
	}
}

// hashGPUUnitCreateRequest creates the stable request hash used by create idempotency.
func hashGPUUnitCreateRequest(req CreateGPUUnitRequest) (string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal create gpu unit request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// findExistingGPUUnitOperation resolves idempotent create replays and name conflicts.
func (s *Service) findExistingGPUUnitOperation(ctx context.Context, operationID, namespace, objectName, requestHash string) (domain.GPUUnitRuntime, bool, error) {
	var list runtimev1alpha1.GPUUnitList
	if err := s.operator.List(ctx, &list); err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}

	for i := range list.Items {
		item := &list.Items[i]
		if !isActiveGPUUnit(item) {
			continue
		}
		if item.GetAnnotations()[runtimev1alpha1.AnnotationOperationID] == operationID {
			if item.GetAnnotations()[runtimev1alpha1.AnnotationRequestHash] != requestHash {
				return domain.GPUUnitRuntime{}, false, &ConflictError{
					Message: fmt.Sprintf("operation %s already exists with a different request payload", operationID),
				}
			}
			return gpuUnitRuntimeFromObject(item), true, nil
		}
	}

	for i := range list.Items {
		item := &list.Items[i]
		if !isActiveGPUUnit(item) {
			continue
		}
		if item.Namespace == namespace && item.Name == objectName {
			if item.GetAnnotations()[runtimev1alpha1.AnnotationOperationID] == operationID &&
				item.GetAnnotations()[runtimev1alpha1.AnnotationRequestHash] == requestHash {
				return gpuUnitRuntimeFromObject(item), true, nil
			}
			return domain.GPUUnitRuntime{}, false, &ConflictError{
				Message: fmt.Sprintf("gpu unit name %s/%s is already in use", namespace, objectName),
			}
		}
	}

	return domain.GPUUnitRuntime{}, false, nil
}

// claimReadyStockUnit selects and claims one ready stock unit for the requested spec.
func (s *Service) claimReadyStockUnit(ctx context.Context, stockNamespace, specName, operationID string) (*runtimev1alpha1.GPUUnit, error) {
	var list runtimev1alpha1.GPUUnitList
	if err := s.operator.List(ctx, &list, ctrlclient.InNamespace(stockNamespace)); err != nil {
		return nil, err
	}

	candidates := make([]runtimev1alpha1.GPUUnit, 0, len(list.Items))
	for i := range list.Items {
		item := list.Items[i]
		if !isStockGPUUnit(&item) {
			continue
		}
		if item.Spec.SpecName != specName {
			continue
		}
		if strings.TrimSpace(item.GetAnnotations()[runtimev1alpha1.AnnotationStockClaimID]) != "" {
			continue
		}
		if item.Status.Phase != runtimev1alpha1.PhaseReady {
			continue
		}
		candidates = append(candidates, item)
	}

	if len(candidates) == 0 {
		return nil, &CapacityError{
			Message: fmt.Sprintf("no ready stock unit is available for spec %s in namespace %s", specName, stockNamespace),
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].CreationTimestamp.Time
		right := candidates[j].CreationTimestamp.Time
		if !left.Equal(right) {
			return left.Before(right)
		}
		return candidates[i].Name < candidates[j].Name
	})

	selected := candidates[0].DeepCopy()
	if selected.Annotations == nil {
		selected.Annotations = map[string]string{}
	}
	selected.Annotations[runtimev1alpha1.AnnotationStockClaimID] = operationID
	if err := s.operator.Update(ctx, selected); err != nil {
		return nil, err
	}
	return selected, nil
}

// buildActiveUnitFromStock copies the reserved stock envelope into a new active unit object
// and overlays the caller's runtime image, template, and access settings.
func buildActiveUnitFromStock(stock runtimev1alpha1.GPUUnit, req CreateGPUUnitRequest, requestHash string) (*runtimev1alpha1.GPUUnit, error) {
	active := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GPUUnit",
			APIVersion: runtimev1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelAppNameKey:   runtimev1alpha1.LabelAppNameValue,
				runtimev1alpha1.LabelManagedByKey: runtimev1alpha1.LabelManagedByValue,
				runtimev1alpha1.LabelUnitKey:      req.Name,
			},
			Annotations: map[string]string{
				runtimev1alpha1.AnnotationOperationID:          req.OperationID,
				runtimev1alpha1.AnnotationRequestHash:          requestHash,
				runtimev1alpha1.AnnotationSourceStockName:      stock.Name,
				runtimev1alpha1.AnnotationSourceStockNamespace: stock.Namespace,
			},
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName:      stock.Spec.SpecName,
			Image:         req.Image,
			Memory:        stock.Spec.Memory,
			GPU:           stock.Spec.GPU,
			Template:      req.Template,
			Access:        req.Access,
			StorageMounts: append([]runtimev1alpha1.GPUUnitStorageMount(nil), req.StorageMounts...),
		},
	}
	return active, nil
}

// releaseStockClaim clears an optimistic stock claim when handoff fails before deletion.
func (s *Service) releaseStockClaim(ctx context.Context, stock *runtimev1alpha1.GPUUnit) error {
	if stock == nil {
		return nil
	}

	current := &runtimev1alpha1.GPUUnit{}
	err := s.operator.Get(ctx, types.NamespacedName{Name: stock.Name, Namespace: stock.Namespace}, current)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	clearStockClaimAnnotations(current)
	return s.operator.Update(ctx, current)
}

// clearStockClaimAnnotations removes transient handoff markers from a stock unit.
func clearStockClaimAnnotations(stock *runtimev1alpha1.GPUUnit) {
	if stock.Annotations == nil {
		return
	}
	delete(stock.Annotations, runtimev1alpha1.AnnotationStockClaimID)
	if len(stock.Annotations) == 0 {
		stock.Annotations = nil
	}
}

// getActiveGPUUnit loads one active unit and rejects stock objects with the same name.
func (s *Service) getActiveGPUUnit(ctx context.Context, namespace, name string) (*runtimev1alpha1.GPUUnit, error) {
	ns, trimmedName, err := normalizeNamespacedObject(namespace, name)
	if err != nil {
		return nil, err
	}

	var instance runtimev1alpha1.GPUUnit
	if err := s.operator.Get(ctx, types.NamespacedName{Namespace: ns, Name: trimmedName}, &instance); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, &NotFoundError{Message: fmt.Sprintf("gpu unit %s/%s not found", ns, trimmedName)}
		}
		return nil, err
	}
	if !isActiveGPUUnit(&instance) {
		return nil, &NotFoundError{Message: fmt.Sprintf("gpu unit %s/%s not found", ns, trimmedName)}
	}
	return &instance, nil
}

// gpuUnitRuntimeFromObject converts a Kubernetes object into the API runtime view.
func gpuUnitRuntimeFromObject(instance *runtimev1alpha1.GPUUnit) domain.GPUUnitRuntime {
	var reason string
	var message string
	if cond := apimeta.FindStatusCondition(instance.Status.Conditions, runtimev1alpha1.ConditionReady); cond != nil {
		reason = cond.Reason
		message = cond.Message
	}

	return domain.GPUUnitRuntime{
		Name:                 instance.Name,
		Namespace:            instance.Namespace,
		Lifecycle:            lifecycleForUnit(instance),
		SpecName:             instance.Spec.SpecName,
		SourceStockName:      sourceStockNameForUnit(instance),
		SourceStockNamespace: sourceStockNamespaceForUnit(instance),
		Image:                instance.Spec.Image,
		Memory:               instance.Spec.Memory,
		GPU:                  instance.Spec.GPU,
		Template:             instance.Spec.Template,
		Access:               instance.Spec.Access,
		StorageMounts:        append([]runtimev1alpha1.GPUUnitStorageMount(nil), instance.Spec.StorageMounts...),
		Phase:                instance.Status.Phase,
		ReadyReplicas:        instance.Status.ReadyReplicas,
		ObservedGeneration:   instance.Status.ObservedGeneration,
		LastSyncTime:         instance.Status.LastSyncTime.Time,
		ServiceName:          instance.Status.ServiceName,
		AccessURL:            instance.Status.AccessURL,
		Reason:               reason,
		Message:              message,
	}
}

// normalizeNamespace validates an optional namespace filter.
func normalizeNamespace(namespace string) (string, error) {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return metav1.NamespaceAll, nil
	}
	if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
		return "", &ValidationError{
			Message: fmt.Sprintf("namespace %q is invalid: %s", ns, strings.Join(errs, ", ")),
		}
	}
	return ns, nil
}

// normalizeNamespacedObject validates an object lookup key and applies default namespace rules.
func normalizeNamespacedObject(namespace, name string) (string, string, error) {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = runtimev1alpha1.DefaultInstanceNamespace
	}
	if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
		return "", "", &ValidationError{
			Message: fmt.Sprintf("namespace %q is invalid: %s", ns, strings.Join(errs, ", ")),
		}
	}

	trimmedName := strings.ToLower(strings.TrimSpace(name))
	if trimmedName == "" {
		return "", "", &ValidationError{Message: "name is required"}
	}
	if errs := validation.IsDNS1123Subdomain(trimmedName); len(errs) > 0 {
		return "", "", &ValidationError{
			Message: fmt.Sprintf("name %q is invalid: %s", trimmedName, strings.Join(errs, ", ")),
		}
	}

	return ns, trimmedName, nil
}

// isZeroGPUUnitTemplate reports whether an update omitted template changes.
func isZeroGPUUnitTemplate(t runtimev1alpha1.GPUUnitTemplate) bool {
	return len(t.Command) == 0 && len(t.Args) == 0 && len(t.Envs) == 0 && len(t.Ports) == 0
}

// isZeroGPUUnitAccess reports whether an update omitted access changes.
func isZeroGPUUnitAccess(access runtimev1alpha1.GPUUnitAccess) bool {
	return strings.TrimSpace(access.PrimaryPort) == "" && strings.TrimSpace(access.Scheme) == ""
}

// lifecycleForUnit derives the API lifecycle label from namespace placement.
func lifecycleForUnit(instance *runtimev1alpha1.GPUUnit) string {
	if isStockGPUUnit(instance) {
		return runtimev1alpha1.LifecycleStock
	}
	return runtimev1alpha1.LifecycleInstance
}

// isStockGPUUnit reports whether the object belongs to the stock namespace.
func isStockGPUUnit(instance *runtimev1alpha1.GPUUnit) bool {
	return instance != nil && instance.Namespace == runtimev1alpha1.DefaultStockNamespace
}

// isActiveGPUUnit reports whether the object belongs to an active runtime namespace.
func isActiveGPUUnit(instance *runtimev1alpha1.GPUUnit) bool {
	return !isStockGPUUnit(instance)
}

// sourceStockNameForUnit returns the provenance annotation recorded during handoff.
func sourceStockNameForUnit(instance *runtimev1alpha1.GPUUnit) string {
	if instance == nil {
		return ""
	}
	return strings.TrimSpace(instance.GetAnnotations()[runtimev1alpha1.AnnotationSourceStockName])
}

// sourceStockNamespaceForUnit returns the stock namespace recorded during handoff.
func sourceStockNamespaceForUnit(instance *runtimev1alpha1.GPUUnit) string {
	if instance == nil {
		return ""
	}
	return strings.TrimSpace(instance.GetAnnotations()[runtimev1alpha1.AnnotationSourceStockNamespace])
}
