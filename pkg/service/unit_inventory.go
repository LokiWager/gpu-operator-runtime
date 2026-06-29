package service

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

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

// buildActiveUnitFromRequest materializes one active runtime unit from the validated API request.
func buildActiveUnitFromRequest(req CreateGPUUnitRequest, requestHash string) *runtimev1alpha1.GPUUnit {
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
				runtimev1alpha1.AnnotationOperationID: req.OperationID,
				runtimev1alpha1.AnnotationRequestHash: requestHash,
			},
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			PackageID:     req.PackageID,
			SpecName:      req.SpecName,
			Image:         req.Image,
			CPU:           req.CPU,
			Memory:        req.Memory,
			GPU:           req.GPU,
			Allocation:    req.Allocation,
			Template:      req.Template,
			Access:        req.Access,
			SSH:           req.SSH,
			Serverless:    req.Serverless,
			StorageMounts: append([]runtimev1alpha1.GPUUnitStorageMount(nil), req.StorageMounts...),
		},
	}
}

// getActiveGPUUnit loads one runtime unit from the fixed instance namespace.
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
	return &instance, nil
}
