package service

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestService_GPUStorageCRUD(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	created, err := svc.CreateGPUStorage(ctx, CreateGPUStorageRequest{
		Name: "model-cache",
		Size: "20Gi",
	})
	if err != nil {
		t.Fatalf("create gpu storage error: %v", err)
	}
	if created.Namespace != runtimev1alpha1.DefaultInstanceNamespace {
		t.Fatalf("expected default namespace %s, got %s", runtimev1alpha1.DefaultInstanceNamespace, created.Namespace)
	}
	if created.Size != "20Gi" {
		t.Fatalf("expected storage size 20Gi, got %s", created.Size)
	}
	if created.StorageClassName != runtimev1alpha1.DefaultGPUStorageClassName {
		t.Fatalf("expected default storage class %s, got %s", runtimev1alpha1.DefaultGPUStorageClassName, created.StorageClassName)
	}

	updated, err := svc.UpdateGPUStorage(ctx, created.Namespace, created.Name, UpdateGPUStorageRequest{Size: "40Gi"})
	if err != nil {
		t.Fatalf("update gpu storage error: %v", err)
	}
	if updated.Size != "40Gi" {
		t.Fatalf("expected updated size 40Gi, got %s", updated.Size)
	}

	list, err := svc.ListGPUStorages(ctx, created.Namespace)
	if err != nil {
		t.Fatalf("list gpu storages error: %v", err)
	}
	if len(list) != 1 || list[0].Name != created.Name {
		t.Fatalf("expected one storage named %s, got %+v", created.Name, list)
	}

	if err := svc.DeleteGPUStorage(ctx, created.Namespace, created.Name); err != nil {
		t.Fatalf("delete gpu storage error: %v", err)
	}

	var gone runtimev1alpha1.GPUStorage
	if err := svc.operator.Get(ctx, types.NamespacedName{Namespace: created.Namespace, Name: created.Name}, &gone); err == nil {
		t.Fatalf("expected storage to be deleted")
	}
}

func TestService_DeleteGPUStorage_RejectsMountedStorage(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedGPUStorage(t, ctx, svc, gpuStorageSeedOptions{
		name:      "model-cache",
		namespace: runtimev1alpha1.DefaultInstanceNamespace,
		size:      "20Gi",
		phase:     runtimev1alpha1.StoragePhaseReady,
	})

	seedActiveGPUUnit(t, ctx, svc, activeUnitSeedOptions{
		name:      "demo-instance",
		namespace: runtimev1alpha1.DefaultInstanceNamespace,
		storageMounts: []runtimev1alpha1.GPUUnitStorageMount{{
			Name:      "model-cache",
			MountPath: "/data",
		}},
	})

	err := svc.DeleteGPUStorage(ctx, runtimev1alpha1.DefaultInstanceNamespace, "model-cache")
	if err == nil {
		t.Fatalf("expected delete conflict")
	}

	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected conflict error, got %T", err)
	}
}

type gpuStorageSeedOptions struct {
	name             string
	namespace        string
	size             string
	storageClassName string
	phase            string
	capacity         string
}

func seedGPUStorage(t *testing.T, ctx context.Context, svc *Service, opts gpuStorageSeedOptions) {
	t.Helper()

	namespace := opts.namespace
	if namespace == "" {
		namespace = runtimev1alpha1.DefaultInstanceNamespace
	}
	size := opts.size
	if size == "" {
		size = "20Gi"
	}
	phase := opts.phase
	if phase == "" {
		phase = runtimev1alpha1.StoragePhasePending
	}

	storage := &runtimev1alpha1.GPUStorage{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUStorage",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.name,
			Namespace: namespace,
		},
		Spec: runtimev1alpha1.GPUStorageSpec{
			Size:             size,
			StorageClassName: opts.storageClassName,
		},
		Status: runtimev1alpha1.GPUStorageStatus{
			ClaimName: opts.name,
			Phase:     phase,
			Conditions: []metav1.Condition{{
				Type:    runtimev1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  runtimev1alpha1.ReasonStoragePending,
				Message: runtimev1alpha1.StatusMessageStoragePending,
			}},
		},
	}
	if phase == runtimev1alpha1.StoragePhaseReady {
		storage.Status.Conditions[0].Status = metav1.ConditionTrue
		storage.Status.Conditions[0].Reason = runtimev1alpha1.ReasonStorageReady
		storage.Status.Conditions[0].Message = runtimev1alpha1.StatusMessageStorageReady
		if opts.capacity != "" {
			storage.Status.Capacity = opts.capacity
		} else {
			storage.Status.Capacity = size
		}
	}

	if err := svc.operator.Create(ctx, storage); err != nil {
		t.Fatalf("create gpu storage error: %v", err)
	}
	if err := svc.operator.Status().Update(ctx, storage); err != nil {
		t.Fatalf("update gpu storage status error: %v", err)
	}
}

type activeUnitSeedOptions struct {
	name          string
	namespace     string
	storageMounts []runtimev1alpha1.GPUUnitStorageMount
}

func seedActiveGPUUnit(t *testing.T, ctx context.Context, svc *Service, opts activeUnitSeedOptions) {
	t.Helper()

	namespace := opts.namespace
	if namespace == "" {
		namespace = runtimev1alpha1.DefaultInstanceNamespace
	}

	unit := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.name,
			Namespace: namespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName:      "g1.1",
			Image:         "python:3.12",
			Memory:        "16Gi",
			StorageMounts: append([]runtimev1alpha1.GPUUnitStorageMount(nil), opts.storageMounts...),
		},
		Status: runtimev1alpha1.GPUUnitStatus{
			Phase: runtimev1alpha1.PhaseProgressing,
		},
	}
	if err := svc.operator.Create(ctx, unit); err != nil {
		t.Fatalf("create active gpu unit error: %v", err)
	}
}
