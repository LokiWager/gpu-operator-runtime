package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/loki/gpu-operator-runtime/pkg/contract"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

// CreateGPUUnit consumes one ready stock unit and creates an active GPUUnit object.
func (s *Service) CreateGPUUnit(ctx context.Context, req CreateGPUUnitRequest) (domain.GPUUnitRuntime, bool, error) {
	if s.operator == nil {
		return domain.GPUUnitRuntime{}, false, &UnavailableError{Message: "operator client is not available"}
	}

	req, err := contract.NormalizeCreateGPUUnitRequest(req)
	if err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}

	requestHash, err := hashGPUUnitCreateRequest(req)
	if err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}
	instanceNamespace := runtimev1alpha1.DefaultInstanceNamespace
	if err := s.ensureGPUStoragesExist(ctx, instanceNamespace, req.StorageMounts); err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}

	s.unitMu.Lock()
	defer s.unitMu.Unlock()

	if runtimeView, ok, err := s.findExistingGPUUnitOperation(ctx, req.OperationID, instanceNamespace, req.Name, requestHash); err != nil {
		return domain.GPUUnitRuntime{}, false, err
	} else if ok {
		return runtimeView, false, nil
	}

	if err := s.ensureGPUStoragesExclusivelyMountable(ctx, instanceNamespace, "", req.StorageMounts); err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}

	stock, err := s.claimReadyStockUnit(ctx, req.SpecName, req.OperationID)
	if err != nil {
		return domain.GPUUnitRuntime{}, false, err
	}

	originalStock := stock.DeepCopy()
	active := buildActiveUnitFromStock(*stock, req, requestHash)

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
			if getErr := s.operator.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: instanceNamespace}, &existing); getErr == nil {
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

	ns, err := resolveRuntimeInstanceNamespace(namespace)
	if err != nil {
		return nil, err
	}

	var list runtimev1alpha1.GPUUnitList
	opts := []ctrlclient.ListOption{ctrlclient.InNamespace(ns)}
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

	instance, err := s.getActiveGPUUnit(ctx, namespace, name)
	if err != nil {
		return domain.GPUUnitRuntime{}, err
	}
	req, err = contract.NormalizeUpdateGPUUnitRequest(instance.Name, instance.Namespace, req)
	if err != nil {
		return domain.GPUUnitRuntime{}, err
	}

	next := instance.Spec
	if req.Image != "" {
		next.Image = req.Image
	}
	if !contract.IsZeroGPUUnitTemplate(req.Template) {
		next.Template = req.Template
	}

	accessInput := next.Access
	if !contract.IsZeroGPUUnitAccess(req.Access) {
		accessInput = req.Access
	}
	access, err := contract.NormalizeGPUUnitAccess(accessInput, next.Template.Ports)
	if err != nil {
		if contract.IsZeroGPUUnitAccess(req.Access) {
			access, err = contract.NormalizeGPUUnitAccess(runtimev1alpha1.GPUUnitAccess{}, next.Template.Ports)
		}
		if err != nil {
			return domain.GPUUnitRuntime{}, err
		}
	}
	next.Access = access

	if req.SSH != nil {
		next.SSH = *req.SSH
	}
	if req.Serverless != nil {
		next.Serverless = *req.Serverless
	}
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

// hashGPUUnitCreateRequest creates the stable request hash used by create idempotency.
func hashGPUUnitCreateRequest(req CreateGPUUnitRequest) (string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal create gpu unit request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
