package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

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
func (s *Service) claimReadyStockUnit(ctx context.Context, specName, operationID string) (*runtimev1alpha1.GPUUnit, error) {
	stockNamespace := runtimev1alpha1.DefaultStockNamespace
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

// buildActiveUnitFromStock copies the reserved stock envelope into a new active unit object.
func buildActiveUnitFromStock(stock runtimev1alpha1.GPUUnit, req CreateGPUUnitRequest, requestHash string) *runtimev1alpha1.GPUUnit {
	return &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GPUUnit",
			APIVersion: runtimev1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
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
			SSH:           req.SSH,
			StorageMounts: append([]runtimev1alpha1.GPUUnitStorageMount(nil), req.StorageMounts...),
		},
	}
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
	ns, err := resolveRuntimeInstanceNamespace(namespace)
	if err != nil {
		return nil, err
	}
	trimmedName, err := normalizeRuntimeResourceName(name)
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
