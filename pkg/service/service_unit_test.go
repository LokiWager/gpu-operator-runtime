package service

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestService_CreateGPUUnitConsumesReadyStockUnit(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-001",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
		unitMemory:   "16Gi",
		unitGPU:      1,
	})

	unit, created, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-1",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "pytorch:2.6",
		Template: runtimev1alpha1.GPUUnitTemplate{
			Ports: []runtimev1alpha1.GPUUnitPortSpec{{
				Name: "http",
				Port: 8080,
			}},
		},
		Access: runtimev1alpha1.GPUUnitAccess{
			PrimaryPort: "http",
			Scheme:      "http",
		},
	})
	if err != nil {
		t.Fatalf("create gpu unit error: %v", err)
	}
	if !created {
		t.Fatalf("expected create to persist a new gpu unit")
	}
	if unit.Namespace != runtimev1alpha1.DefaultInstanceNamespace {
		t.Fatalf("expected default instance namespace, got %s", unit.Namespace)
	}
	if unit.Lifecycle != runtimev1alpha1.LifecycleInstance {
		t.Fatalf("expected lifecycle=%s, got %s", runtimev1alpha1.LifecycleInstance, unit.Lifecycle)
	}
	if unit.SourceStockName != "stock-g1-001" {
		t.Fatalf("expected source stock stock-g1-001, got %s", unit.SourceStockName)
	}
	if unit.Image != "pytorch:2.6" || unit.Memory != "16Gi" || unit.GPU != 1 {
		t.Fatalf("expected stock unit resource envelope to be copied, got image=%s memory=%s gpu=%d", unit.Image, unit.Memory, unit.GPU)
	}
	if unit.Access.PrimaryPort != "http" {
		t.Fatalf("expected default primary port http, got %s", unit.Access.PrimaryPort)
	}

	var stored runtimev1alpha1.GPUUnit
	if err := svc.operator.Get(ctx, types.NamespacedName{Name: "demo-instance", Namespace: runtimev1alpha1.DefaultInstanceNamespace}, &stored); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	if isStockGPUUnit(&stored) {
		t.Fatalf("expected stored object to be active")
	}
	if got := stored.GetAnnotations()[runtimev1alpha1.AnnotationSourceStockName]; got != "stock-g1-001" {
		t.Fatalf("expected source stock annotation, got %q", got)
	}

	var deleted runtimev1alpha1.GPUUnit
	err = svc.operator.Get(ctx, types.NamespacedName{Name: "stock-g1-001", Namespace: runtimev1alpha1.DefaultStockNamespace}, &deleted)
	if err == nil {
		t.Fatalf("expected consumed stock unit to be deleted")
	}
}

func TestService_CreateGPUUnit_IsIdempotent(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-001",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})

	req := CreateGPUUnitRequest{
		OperationID: "gpu-create-2",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "pytorch:2.6",
		Template: runtimev1alpha1.GPUUnitTemplate{
			Ports: []runtimev1alpha1.GPUUnitPortSpec{{Name: "http", Port: 8080}},
		},
	}

	first, created, err := svc.CreateGPUUnit(ctx, req)
	if err != nil {
		t.Fatalf("first create error: %v", err)
	}
	if !created {
		t.Fatalf("expected first create to persist a new object")
	}

	second, created, err := svc.CreateGPUUnit(ctx, req)
	if err != nil {
		t.Fatalf("second create error: %v", err)
	}
	if created {
		t.Fatalf("expected second create to reuse existing object")
	}
	if first.Name != second.Name || first.Namespace != second.Namespace {
		t.Fatalf("expected idempotent replay to return same runtime object")
	}
}

func TestService_CreateGPUUnit_RejectsOperationConflict(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-001",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})

	req := CreateGPUUnitRequest{
		OperationID: "gpu-create-3",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "pytorch:2.6",
	}
	if _, _, err := svc.CreateGPUUnit(ctx, req); err != nil {
		t.Fatalf("first create error: %v", err)
	}

	_, _, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-3",
		Name:        "other-instance",
		SpecName:    "g1.1",
		Image:       "pytorch:2.7",
	})
	if err == nil {
		t.Fatalf("expected conflict error")
	}

	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected conflict error, got %T", err)
	}
}

func TestService_CreateGPUUnit_RequiresReadyStockCapacity(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName: "stock-g1-001",
		specName: "g1.1",
		phase:    runtimev1alpha1.PhaseProgressing,
	})

	_, _, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-4",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "pytorch:2.6",
	})
	if err == nil {
		t.Fatalf("expected capacity error")
	}

	var capacityErr *CapacityError
	if !errors.As(err, &capacityErr) {
		t.Fatalf("expected capacity error, got %T", err)
	}
}

func TestService_CreateGPUUnit_RequiresImage(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	_, _, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-missing-image",
		Name:        "demo-instance",
		SpecName:    "g1.1",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %T", err)
	}
}

func TestService_CreateGPUUnit_RequiresReferencedStorageToExist(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-001",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})

	_, _, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-storage-missing",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "python:3.12",
		StorageMounts: []runtimev1alpha1.GPUUnitStorageMount{{
			Name:      "missing-storage",
			MountPath: "/data",
		}},
	})
	if err == nil {
		t.Fatalf("expected missing storage error")
	}

	var notFoundErr *NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected not found error, got %T", err)
	}
}

func TestService_UpdateGPUUnit_StorageMounts(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-001",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})
	seedGPUStorage(t, ctx, svc, gpuStorageSeedOptions{
		name:      "model-cache",
		namespace: runtimev1alpha1.DefaultInstanceNamespace,
		size:      "20Gi",
		phase:     runtimev1alpha1.StoragePhaseReady,
	})

	_, _, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-with-storage",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "python:3.12",
		StorageMounts: []runtimev1alpha1.GPUUnitStorageMount{{
			Name:      "model-cache",
			MountPath: "/data",
		}},
	})
	if err != nil {
		t.Fatalf("create gpu unit error: %v", err)
	}

	updated, err := svc.UpdateGPUUnit(ctx, runtimev1alpha1.DefaultInstanceNamespace, "demo-instance", UpdateGPUUnitRequest{
		StorageMounts: &[]runtimev1alpha1.GPUUnitStorageMount{{
			Name:      "model-cache",
			MountPath: "/workspace/cache",
		}},
	})
	if err != nil {
		t.Fatalf("update gpu unit error: %v", err)
	}
	if len(updated.StorageMounts) != 1 || updated.StorageMounts[0].MountPath != "/workspace/cache" {
		t.Fatalf("expected updated storage mount path, got %+v", updated.StorageMounts)
	}
}

func TestService_CreateGPUUnit_RejectsStorageAlreadyMountedByAnotherActiveUnit(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-001",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})
	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-002",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})
	seedGPUStorage(t, ctx, svc, gpuStorageSeedOptions{
		name:      "model-cache",
		namespace: runtimev1alpha1.DefaultInstanceNamespace,
		size:      "20Gi",
		phase:     runtimev1alpha1.StoragePhaseReady,
	})

	_, _, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-storage-exclusive-1",
		Name:        "demo-instance-a",
		SpecName:    "g1.1",
		Image:       "python:3.12",
		StorageMounts: []runtimev1alpha1.GPUUnitStorageMount{{
			Name:      "model-cache",
			MountPath: "/data",
		}},
	})
	if err != nil {
		t.Fatalf("create first gpu unit error: %v", err)
	}

	_, _, err = svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-storage-exclusive-2",
		Name:        "demo-instance-b",
		SpecName:    "g1.1",
		Image:       "python:3.12",
		StorageMounts: []runtimev1alpha1.GPUUnitStorageMount{{
			Name:      "model-cache",
			MountPath: "/data",
		}},
	})
	if err == nil {
		t.Fatalf("expected create conflict when storage is already mounted")
	}

	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected conflict error, got %T", err)
	}
}

func TestService_UpdateGPUUnit_RejectsStorageAlreadyMountedByAnotherActiveUnit(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-001",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})
	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-002",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})
	seedGPUStorage(t, ctx, svc, gpuStorageSeedOptions{
		name:      "model-cache",
		namespace: runtimev1alpha1.DefaultInstanceNamespace,
		size:      "20Gi",
		phase:     runtimev1alpha1.StoragePhaseReady,
	})

	_, _, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-storage-exclusive-update-1",
		Name:        "demo-instance-a",
		SpecName:    "g1.1",
		Image:       "python:3.12",
		StorageMounts: []runtimev1alpha1.GPUUnitStorageMount{{
			Name:      "model-cache",
			MountPath: "/data",
		}},
	})
	if err != nil {
		t.Fatalf("create first gpu unit error: %v", err)
	}

	_, _, err = svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-storage-exclusive-update-2",
		Name:        "demo-instance-b",
		SpecName:    "g1.1",
		Image:       "python:3.12",
	})
	if err != nil {
		t.Fatalf("create second gpu unit error: %v", err)
	}

	_, err = svc.UpdateGPUUnit(ctx, runtimev1alpha1.DefaultInstanceNamespace, "demo-instance-b", UpdateGPUUnitRequest{
		StorageMounts: &[]runtimev1alpha1.GPUUnitStorageMount{{
			Name:      "model-cache",
			MountPath: "/workspace/cache",
		}},
	})
	if err == nil {
		t.Fatalf("expected update conflict when storage is already mounted")
	}

	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected conflict error, got %T", err)
	}
}

func TestService_UpdateListAndDeleteGPUUnit(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-001",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})
	seedStockUnit(t, ctx, svc, stockSeedOptions{
		unitName:     "stock-g1-002",
		specName:     "g1.1",
		phase:        runtimev1alpha1.PhaseReady,
		readyMessage: runtimev1alpha1.StatusMessageStockReady,
	})

	_, created, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-5",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "python:3.12",
		Template: runtimev1alpha1.GPUUnitTemplate{
			Ports: []runtimev1alpha1.GPUUnitPortSpec{{Name: "http", Port: 8080}},
		},
	})
	if err != nil {
		t.Fatalf("create error: %v", err)
	}
	if !created {
		t.Fatalf("expected create")
	}

	updated, err := svc.UpdateGPUUnit(ctx, runtimev1alpha1.DefaultInstanceNamespace, "demo-instance", UpdateGPUUnitRequest{
		Image: "pytorch:2.6",
		Template: runtimev1alpha1.GPUUnitTemplate{
			Command: []string{"python"},
			Args:    []string{"-m", "http.server", "7860"},
			Ports: []runtimev1alpha1.GPUUnitPortSpec{{
				Name: "web",
				Port: 7860,
			}},
		},
		Access: runtimev1alpha1.GPUUnitAccess{
			PrimaryPort: "web",
			Scheme:      "http",
		},
	})
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	if updated.Image != "pytorch:2.6" {
		t.Fatalf("expected updated image, got %s", updated.Image)
	}
	if updated.Access.PrimaryPort != "web" {
		t.Fatalf("expected updated access port, got %s", updated.Access.PrimaryPort)
	}

	items, err := svc.ListGPUUnits(ctx, runtimev1alpha1.DefaultInstanceNamespace)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one active gpu unit, got %d", len(items))
	}
	if items[0].Lifecycle != runtimev1alpha1.LifecycleInstance {
		t.Fatalf("expected active lifecycle, got %s", items[0].Lifecycle)
	}

	if err := svc.DeleteGPUUnit(ctx, runtimev1alpha1.DefaultInstanceNamespace, "demo-instance"); err != nil {
		t.Fatalf("delete error: %v", err)
	}
	if _, err := svc.GetGPUUnit(ctx, runtimev1alpha1.DefaultInstanceNamespace, "demo-instance"); err == nil {
		t.Fatalf("expected get after delete to fail")
	}

	items, err = svc.ListGPUUnits(ctx, "")
	if err != nil {
		t.Fatalf("list all error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected stock units to stay hidden from the runtime list, got %d items", len(items))
	}
}

type stockSeedOptions struct {
	unitName     string
	specName     string
	phase        string
	readyMessage string
	unitMemory   string
	unitGPU      int32
}

func seedStockUnit(t *testing.T, ctx context.Context, svc *Service, opts stockSeedOptions) {
	t.Helper()

	if opts.unitMemory == "" {
		opts.unitMemory = "16Gi"
	}

	unit := &runtimev1alpha1.GPUUnit{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.unitName,
			Namespace: runtimev1alpha1.DefaultStockNamespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelUnitKey: opts.unitName,
			},
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: opts.specName,
			Image:    runtimev1alpha1.StockReservationImage,
			Memory:   opts.unitMemory,
			GPU:      opts.unitGPU,
		},
		Status: runtimev1alpha1.GPUUnitStatus{
			Phase: opts.phase,
			Conditions: []metav1.Condition{{
				Type:    runtimev1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  runtimev1alpha1.ReasonStockNotReady,
				Message: runtimev1alpha1.StatusMessageStockWait,
			}},
		},
	}
	if opts.phase == runtimev1alpha1.PhaseReady {
		unit.Status.ReadyReplicas = 1
		unit.Status.Conditions[0].Status = metav1.ConditionTrue
		unit.Status.Conditions[0].Reason = runtimev1alpha1.ReasonStockReady
		if opts.readyMessage != "" {
			unit.Status.Conditions[0].Message = opts.readyMessage
		} else {
			unit.Status.Conditions[0].Message = runtimev1alpha1.StatusMessageStockReady
		}
	}

	if err := svc.operator.Create(ctx, unit); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create stock unit error: %v", err)
	}
	if err := svc.operator.Status().Update(ctx, unit); err != nil {
		t.Fatalf("update stock unit status error: %v", err)
	}
}
