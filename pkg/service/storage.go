package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

// CreateGPUStorageRequest asks the service to persist a new RBD-backed workspace storage resource.
type CreateGPUStorageRequest struct {
	Name             string                                 `json:"name"`
	Namespace        string                                 `json:"namespace,omitempty"`
	Size             string                                 `json:"size"`
	StorageClassName string                                 `json:"storageClassName,omitempty"`
	Prepare          runtimev1alpha1.GPUStoragePrepareSpec  `json:"prepare,omitempty"`
	Accessor         runtimev1alpha1.GPUStorageAccessorSpec `json:"accessor,omitempty"`
}

// UpdateGPUStorageRequest captures the mutable storage fields.
type UpdateGPUStorageRequest struct {
	Size     string                                  `json:"size"`
	Accessor *runtimev1alpha1.GPUStorageAccessorSpec `json:"accessor,omitempty"`
}

// RecoverGPUStorageRequest records one recovery action against a failed storage prepare workflow.
type RecoverGPUStorageRequest struct{}

// CreateGPUStorage persists a new GPUStorage object and lets the controller bind its PVC.
func (s *Service) CreateGPUStorage(ctx context.Context, req CreateGPUStorageRequest) (domain.GPUStorageRuntime, error) {
	if s.operator == nil {
		return domain.GPUStorageRuntime{}, &UnavailableError{Message: "operator client is not available"}
	}

	req, err := normalizeCreateGPUStorageRequest(req)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}
	if err := s.ensureGPUStoragePrepareSourcesExist(ctx, req.Namespace, req.Name, req.Prepare); err != nil {
		return domain.GPUStorageRuntime{}, err
	}

	var existing runtimev1alpha1.GPUStorage
	err = s.operator.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, &existing)
	if err == nil {
		return domain.GPUStorageRuntime{}, &ConflictError{
			Message: fmt.Sprintf("gpu storage %s/%s already exists", req.Namespace, req.Name),
		}
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return domain.GPUStorageRuntime{}, err
	}

	storage := &runtimev1alpha1.GPUStorage{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GPUStorage",
			APIVersion: runtimev1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelAppNameKey:   runtimev1alpha1.LabelAppNameValue,
				runtimev1alpha1.LabelManagedByKey: runtimev1alpha1.LabelManagedByValue,
				runtimev1alpha1.LabelStorageKey:   req.Name,
			},
		},
		Spec: runtimev1alpha1.GPUStorageSpec{
			Size:             req.Size,
			StorageClassName: req.StorageClassName,
			Prepare:          req.Prepare,
			Accessor:         req.Accessor,
		},
	}
	if err := s.operator.Create(ctx, storage); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.GPUStorageRuntime{}, &ConflictError{
				Message: fmt.Sprintf("gpu storage %s/%s already exists", req.Namespace, req.Name),
			}
		}
		return domain.GPUStorageRuntime{}, err
	}

	return s.storageRuntimeFromObject(ctx, storage, nil)
}

// ListGPUStorages returns persistent storage resources, optionally filtered by namespace.
func (s *Service) ListGPUStorages(ctx context.Context, namespace string) ([]domain.GPUStorageRuntime, error) {
	if s.operator == nil {
		return nil, &UnavailableError{Message: "operator client is not available"}
	}

	ns, err := normalizeNamespace(namespace)
	if err != nil {
		return nil, err
	}

	var storages runtimev1alpha1.GPUStorageList
	opts := []ctrlclient.ListOption{}
	if ns != metav1.NamespaceAll {
		opts = append(opts, ctrlclient.InNamespace(ns))
	}
	if err := s.operator.List(ctx, &storages, opts...); err != nil {
		return nil, err
	}

	mountedBy, err := s.storageMountIndex(ctx, ns)
	if err != nil {
		return nil, err
	}

	out := make([]domain.GPUStorageRuntime, 0, len(storages.Items))
	for i := range storages.Items {
		key := storageIndexKey(storages.Items[i].Namespace, storages.Items[i].Name)
		out = append(out, gpuStorageRuntimeFromObject(&storages.Items[i], mountedBy[key]))
	}
	return out, nil
}

// GetGPUStorage returns one persistent storage resource by namespace and name.
func (s *Service) GetGPUStorage(ctx context.Context, namespace, name string) (domain.GPUStorageRuntime, error) {
	if s.operator == nil {
		return domain.GPUStorageRuntime{}, &UnavailableError{Message: "operator client is not available"}
	}

	storage, err := s.getGPUStorageObject(ctx, namespace, name)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}

	mountedBy, err := s.storageMountIndex(ctx, storage.Namespace)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}
	return s.storageRuntimeFromObject(ctx, storage, mountedBy)
}

// UpdateGPUStorage updates the requested storage size on a GPUStorage resource.
func (s *Service) UpdateGPUStorage(ctx context.Context, namespace, name string, req UpdateGPUStorageRequest) (domain.GPUStorageRuntime, error) {
	if s.operator == nil {
		return domain.GPUStorageRuntime{}, &UnavailableError{Message: "operator client is not available"}
	}

	req, nextQty, err := normalizeUpdateGPUStorageRequest(req)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}

	storage, err := s.getGPUStorageObject(ctx, namespace, name)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}

	currentQty, err := resource.ParseQuantity(storage.Spec.Size)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}
	accessorChanged := applyGPUStorageAccessorSpec(storage, req.Accessor)
	if nextQty.Cmp(currentQty) < 0 {
		return domain.GPUStorageRuntime{}, &ValidationError{
			Message: fmt.Sprintf("size %q cannot be smaller than current size %q", req.Size, storage.Spec.Size),
		}
	}
	if nextQty.Cmp(currentQty) == 0 {
		if accessorChanged {
			if err := s.operator.Update(ctx, storage); err != nil {
				return domain.GPUStorageRuntime{}, err
			}
		}
		mountedBy, indexErr := s.storageMountIndex(ctx, storage.Namespace)
		if indexErr != nil {
			return domain.GPUStorageRuntime{}, indexErr
		}
		return s.storageRuntimeFromObject(ctx, storage, mountedBy)
	}

	storage.Spec.Size = nextQty.String()
	if err := s.operator.Update(ctx, storage); err != nil {
		return domain.GPUStorageRuntime{}, err
	}

	mountedBy, err := s.storageMountIndex(ctx, storage.Namespace)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}
	return s.storageRuntimeFromObject(ctx, storage, mountedBy)
}

// RecoverGPUStorage requests a new prepare attempt for one storage object without mutating the prepare contract itself.
func (s *Service) RecoverGPUStorage(ctx context.Context, namespace, name string) (domain.GPUStorageRuntime, error) {
	if s.operator == nil {
		return domain.GPUStorageRuntime{}, &UnavailableError{Message: "operator client is not available"}
	}

	storage, err := s.getGPUStorageObject(ctx, namespace, name)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}
	if isZeroGPUStoragePrepare(storage.Spec.Prepare) {
		return domain.GPUStorageRuntime{}, &ValidationError{
			Message: fmt.Sprintf("gpu storage %s/%s has no prepare workflow to recover", storage.Namespace, storage.Name),
		}
	}

	if storage.Annotations == nil {
		storage.Annotations = map[string]string{}
	}
	storage.Annotations[runtimev1alpha1.AnnotationStorageRecoveryNonce] = metav1.Now().UTC().Format(time.RFC3339Nano)
	if err := s.operator.Update(ctx, storage); err != nil {
		return domain.GPUStorageRuntime{}, err
	}

	mountedBy, err := s.storageMountIndex(ctx, storage.Namespace)
	if err != nil {
		return domain.GPUStorageRuntime{}, err
	}
	return s.storageRuntimeFromObject(ctx, storage, mountedBy)
}

// DeleteGPUStorage deletes one storage resource after ensuring no active unit still mounts it.
func (s *Service) DeleteGPUStorage(ctx context.Context, namespace, name string) error {
	if s.operator == nil {
		return &UnavailableError{Message: "operator client is not available"}
	}

	storage, err := s.getGPUStorageObject(ctx, namespace, name)
	if err != nil {
		return err
	}

	mountedBy, err := s.mountedGPUUnitsForStorage(ctx, storage.Namespace, storage.Name)
	if err != nil {
		return err
	}
	if len(mountedBy) > 0 {
		return &ConflictError{
			Message: fmt.Sprintf("gpu storage %s/%s is still mounted by gpu units: %s", storage.Namespace, storage.Name, strings.Join(mountedBy, ", ")),
		}
	}

	return s.operator.Delete(ctx, storage)
}

func normalizeCreateGPUStorageRequest(req CreateGPUStorageRequest) (CreateGPUStorageRequest, error) {
	name, err := normalizeGPUStorageName(req.Name)
	if err != nil {
		return CreateGPUStorageRequest{}, err
	}
	req.Name = name

	namespace, err := normalizeGPUStorageNamespace(req.Namespace)
	if err != nil {
		return CreateGPUStorageRequest{}, err
	}
	req.Namespace = namespace

	size, _, err := normalizeGPUStorageSize(req.Size)
	if err != nil {
		return CreateGPUStorageRequest{}, err
	}
	req.Size = size

	storageClassName, err := normalizeGPUStorageClassName(req.StorageClassName)
	if err != nil {
		return CreateGPUStorageRequest{}, err
	}
	req.StorageClassName = storageClassName

	prepare, err := normalizeGPUStoragePrepare(req.Prepare)
	if err != nil {
		return CreateGPUStorageRequest{}, err
	}
	req.Prepare = prepare
	req.Accessor = normalizeGPUStorageAccessor(req.Accessor)

	return req, nil
}

func normalizeUpdateGPUStorageRequest(req UpdateGPUStorageRequest) (UpdateGPUStorageRequest, resource.Quantity, error) {
	size, qty, err := normalizeGPUStorageSize(req.Size)
	if err != nil {
		return UpdateGPUStorageRequest{}, resource.Quantity{}, err
	}
	req.Size = size
	if req.Accessor != nil {
		normalized := normalizeGPUStorageAccessor(*req.Accessor)
		req.Accessor = &normalized
	}
	return req, qty, nil
}

func (s *Service) getGPUStorageObject(ctx context.Context, namespace, name string) (*runtimev1alpha1.GPUStorage, error) {
	ns, trimmedName, err := normalizeNamespacedObject(namespace, name)
	if err != nil {
		return nil, err
	}

	var storage runtimev1alpha1.GPUStorage
	if err := s.operator.Get(ctx, types.NamespacedName{Namespace: ns, Name: trimmedName}, &storage); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, &NotFoundError{Message: fmt.Sprintf("gpu storage %s/%s not found", ns, trimmedName)}
		}
		return nil, err
	}
	return &storage, nil
}

func (s *Service) storageRuntimeFromObject(ctx context.Context, storage *runtimev1alpha1.GPUStorage, mountedBy map[string][]string) (domain.GPUStorageRuntime, error) {
	if mountedBy == nil {
		index, err := s.storageMountIndex(ctx, storage.Namespace)
		if err != nil {
			return domain.GPUStorageRuntime{}, err
		}
		mountedBy = index
	}
	return gpuStorageRuntimeFromObject(storage, mountedBy[storageIndexKey(storage.Namespace, storage.Name)]), nil
}

func gpuStorageRuntimeFromObject(storage *runtimev1alpha1.GPUStorage, mountedBy []string) domain.GPUStorageRuntime {
	var reason string
	var message string
	if cond := apimeta.FindStatusCondition(storage.Status.Conditions, runtimev1alpha1.ConditionReady); cond != nil {
		reason = cond.Reason
		message = cond.Message
	}

	out := domain.GPUStorageRuntime{
		Name:               storage.Name,
		Namespace:          storage.Namespace,
		Size:               storage.Spec.Size,
		StorageClassName:   effectiveGPUStorageClassName(storage.Spec.StorageClassName),
		Prepare:            storage.Spec.Prepare,
		Accessor:           storage.Spec.Accessor,
		ClaimName:          storage.Status.ClaimName,
		Capacity:           storage.Status.Capacity,
		MountedBy:          append([]string(nil), mountedBy...),
		Phase:              storage.Status.Phase,
		PrepareStatus:      storage.Status.Prepare,
		AccessorStatus:     storage.Status.Accessor,
		ObservedGeneration: storage.Status.ObservedGeneration,
		LastSyncTime:       storage.Status.LastSyncTime.Time,
		Reason:             reason,
		Message:            message,
	}
	sort.Strings(out.MountedBy)
	return out
}

// effectiveGPUStorageClassName exposes the effective default even when older objects omit the field.
func effectiveGPUStorageClassName(raw string) string {
	if raw == "" {
		return runtimev1alpha1.DefaultGPUStorageClassName
	}
	return raw
}

func applyGPUStorageAccessorSpec(storage *runtimev1alpha1.GPUStorage, accessor *runtimev1alpha1.GPUStorageAccessorSpec) bool {
	if accessor == nil || storage.Spec.Accessor == *accessor {
		return false
	}
	storage.Spec.Accessor = *accessor
	return true
}

func (s *Service) storageMountIndex(ctx context.Context, namespace string) (map[string][]string, error) {
	var units runtimev1alpha1.GPUUnitList
	var opts []ctrlclient.ListOption
	if namespace != metav1.NamespaceAll {
		opts = append(opts, ctrlclient.InNamespace(namespace))
	}
	if err := s.operator.List(ctx, &units, opts...); err != nil {
		return nil, err
	}

	index := map[string][]string{}
	for i := range units.Items {
		unit := &units.Items[i]
		if !isActiveGPUUnit(unit) {
			continue
		}
		for _, mount := range unit.Spec.StorageMounts {
			key := storageIndexKey(unit.Namespace, mount.Name)
			index[key] = append(index[key], unit.Name)
		}
	}

	for key := range index {
		sort.Strings(index[key])
	}
	return index, nil
}

func (s *Service) mountedGPUUnitsForStorage(ctx context.Context, namespace, storageName string) ([]string, error) {
	index, err := s.storageMountIndex(ctx, namespace)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), index[storageIndexKey(namespace, storageName)]...), nil
}

func (s *Service) ensureGPUStoragesExist(ctx context.Context, namespace string, mounts []runtimev1alpha1.GPUUnitStorageMount) error {
	for _, mount := range mounts {
		var storage runtimev1alpha1.GPUStorage
		err := s.operator.Get(ctx, types.NamespacedName{Namespace: namespace, Name: mount.Name}, &storage)
		if apierrors.IsNotFound(err) {
			return &NotFoundError{Message: fmt.Sprintf("gpu storage %s/%s not found", namespace, mount.Name)}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ensureGPUStoragePrepareSourcesExist(ctx context.Context, namespace, storageName string, prepare runtimev1alpha1.GPUStoragePrepareSpec) error {
	if prepare.FromStorageName == "" {
		return nil
	}
	if prepare.FromStorageName == storageName {
		return &ValidationError{Message: "prepare.fromStorageName cannot point to the same storage object"}
	}

	var source runtimev1alpha1.GPUStorage
	err := s.operator.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prepare.FromStorageName}, &source)
	if apierrors.IsNotFound(err) {
		return &NotFoundError{Message: fmt.Sprintf("gpu storage %s/%s not found", namespace, prepare.FromStorageName)}
	}
	if err != nil {
		return err
	}
	return nil
}

// ensureGPUStoragesExclusivelyMountable rejects mounts that would reuse one RBD-backed workspace across active units.
func (s *Service) ensureGPUStoragesExclusivelyMountable(ctx context.Context, namespace, unitName string, mounts []runtimev1alpha1.GPUUnitStorageMount) error {
	for _, mount := range mounts {
		mountedBy, err := s.mountedGPUUnitsForStorage(ctx, namespace, mount.Name)
		if err != nil {
			return err
		}
		for _, mountedUnitName := range mountedBy {
			if mountedUnitName == unitName {
				continue
			}
			return &ConflictError{
				Message: fmt.Sprintf(
					"gpu storage %s/%s is already mounted by gpu unit %s; the default RBD-backed storage path is exclusive per active runtime",
					namespace,
					mount.Name,
					mountedUnitName,
				),
			}
		}
	}
	return nil
}

func storageIndexKey(namespace, name string) string {
	return namespace + "/" + name
}
