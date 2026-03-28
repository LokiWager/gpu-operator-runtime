package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

// CreateGPUStorageRequest asks the service to persist a new RBD-backed workspace storage resource.
type CreateGPUStorageRequest struct {
	Name             string `json:"name"`
	Namespace        string `json:"namespace,omitempty"`
	Size             string `json:"size"`
	StorageClassName string `json:"storageClassName,omitempty"`
}

// UpdateGPUStorageRequest captures the mutable storage fields.
type UpdateGPUStorageRequest struct {
	Size string `json:"size"`
}

// CreateGPUStorage persists a new GPUStorage object and lets the controller bind its PVC.
func (s *Service) CreateGPUStorage(ctx context.Context, req CreateGPUStorageRequest) (domain.GPUStorageRuntime, error) {
	if s.operator == nil {
		return domain.GPUStorageRuntime{}, &UnavailableError{Message: "operator client is not available"}
	}

	req, err := normalizeCreateGPUStorageRequest(req)
	if err != nil {
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
	if nextQty.Cmp(currentQty) < 0 {
		return domain.GPUStorageRuntime{}, &ValidationError{
			Message: fmt.Sprintf("size %q cannot be smaller than current size %q", req.Size, storage.Spec.Size),
		}
	}
	if nextQty.Cmp(currentQty) == 0 {
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
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if req.Name == "" {
		return CreateGPUStorageRequest{}, &ValidationError{Message: "name is required"}
	}
	if errs := validation.IsDNS1123Subdomain(req.Name); len(errs) > 0 {
		return CreateGPUStorageRequest{}, &ValidationError{
			Message: fmt.Sprintf("name %q is invalid: %s", req.Name, strings.Join(errs, ", ")),
		}
	}

	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		req.Namespace = runtimev1alpha1.DefaultInstanceNamespace
	}
	if errs := validation.IsDNS1123Label(req.Namespace); len(errs) > 0 {
		return CreateGPUStorageRequest{}, &ValidationError{
			Message: fmt.Sprintf("namespace %q is invalid: %s", req.Namespace, strings.Join(errs, ", ")),
		}
	}
	if req.Namespace == runtimev1alpha1.DefaultStockNamespace {
		return CreateGPUStorageRequest{}, &ValidationError{
			Message: fmt.Sprintf("namespace %q cannot be used for persistent storage", req.Namespace),
		}
	}

	req.Size = strings.TrimSpace(req.Size)
	if req.Size == "" {
		return CreateGPUStorageRequest{}, &ValidationError{Message: "size is required"}
	}
	qty, err := resource.ParseQuantity(req.Size)
	if err != nil {
		return CreateGPUStorageRequest{}, &ValidationError{Message: fmt.Sprintf("size %q is invalid: %v", req.Size, err)}
	}
	req.Size = qty.String()

	req.StorageClassName = strings.TrimSpace(req.StorageClassName)
	if req.StorageClassName == "" {
		req.StorageClassName = runtimev1alpha1.DefaultGPUStorageClassName
	}
	if errs := validation.IsDNS1123Subdomain(req.StorageClassName); len(errs) > 0 {
		return CreateGPUStorageRequest{}, &ValidationError{
			Message: fmt.Sprintf("storageClassName %q is invalid: %s", req.StorageClassName, strings.Join(errs, ", ")),
		}
	}

	return req, nil
}

func normalizeUpdateGPUStorageRequest(req UpdateGPUStorageRequest) (UpdateGPUStorageRequest, resource.Quantity, error) {
	req.Size = strings.TrimSpace(req.Size)
	if req.Size == "" {
		return UpdateGPUStorageRequest{}, resource.Quantity{}, &ValidationError{Message: "size is required"}
	}
	qty, err := resource.ParseQuantity(req.Size)
	if err != nil {
		return UpdateGPUStorageRequest{}, resource.Quantity{}, &ValidationError{Message: fmt.Sprintf("size %q is invalid: %v", req.Size, err)}
	}
	req.Size = qty.String()
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
		ClaimName:          storage.Status.ClaimName,
		Capacity:           storage.Status.Capacity,
		MountedBy:          append([]string(nil), mountedBy...),
		Phase:              storage.Status.Phase,
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

func (s *Service) storageMountIndex(ctx context.Context, namespace string) (map[string][]string, error) {
	var units runtimev1alpha1.GPUUnitList
	opts := []ctrlclient.ListOption{}
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
