package service

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestService_CreateGPUUnit_DRAPackageAllocation(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	unit, created, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-dra-1",
		Name:        "demo-dra",
		PackageID:   "gpu-rtx3080-2x-cpu10-mem40g",
		Image:       "pytorch:2.6",
	})
	if err != nil {
		t.Fatalf("create gpu unit error: %v", err)
	}
	if !created {
		t.Fatalf("expected create to persist a new gpu unit")
	}
	if unit.PackageID != "gpu-rtx3080-2x-cpu10-mem40g" {
		t.Fatalf("expected package id, got %s", unit.PackageID)
	}
	if unit.CPU != "10" || unit.Memory != "40Gi" || unit.GPU != 2 {
		t.Fatalf("expected package resources, got cpu=%s memory=%s gpu=%d", unit.CPU, unit.Memory, unit.GPU)
	}
	if unit.Allocation.DeviceClassName != "nvidia-rtx-3080" || unit.Allocation.ClaimName != "unit-demo-dra-gpu" {
		t.Fatalf("expected package DRA allocation, got %+v", unit.Allocation)
	}

	var stored runtimev1alpha1.GPUUnit
	if err := svc.operator.Get(ctx, types.NamespacedName{Name: "demo-dra", Namespace: runtimev1alpha1.DefaultInstanceNamespace}, &stored); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	if stored.Spec.Allocation.DeviceClassName != "nvidia-rtx-3080" || stored.Spec.Allocation.Count != 2 {
		t.Fatalf("expected stored DRA allocation, got %+v", stored.Spec.Allocation)
	}
}

func TestService_CreateGPUUnit_DRAPackageAllocationRejectsClaimQuotaExhaustion(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()
	svc.kube = k8sfake.NewSimpleClientset(runtimeDRAResourceQuota("runtime-dra-quota", "1", "1"))

	_, _, err := svc.CreateGPUUnit(ctx, CreateGPUUnitRequest{
		OperationID: "gpu-create-dra-quota",
		Name:        "demo-dra",
		PackageID:   "gpu-rtx3080-2x-cpu10-mem40g",
		Image:       "pytorch:2.6",
	})
	if err == nil {
		t.Fatalf("expected quota capacity error")
	}
	var capacityErr *CapacityError
	if !errors.As(err, &capacityErr) {
		t.Fatalf("expected capacity error, got %T", err)
	}
}

func TestService_CreateGPUUnit_IsIdempotent(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := baseCreateGPUUnitRequest("gpu-create-2", "demo-instance")
	req.Template = runtimev1alpha1.GPUUnitTemplate{
		Ports: []runtimev1alpha1.GPUUnitPortSpec{{Name: "http", Port: 8080}},
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

	if _, _, err := svc.CreateGPUUnit(ctx, baseCreateGPUUnitRequest("gpu-create-3", "demo-instance")); err != nil {
		t.Fatalf("first create error: %v", err)
	}

	_, _, err := svc.CreateGPUUnit(ctx, baseCreateGPUUnitRequest("gpu-create-3", "other-instance"))
	if err == nil {
		t.Fatalf("expected conflict error")
	}
	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected conflict error, got %T", err)
	}
}

func TestService_CreateGPUUnit_RequiresImage(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := baseCreateGPUUnitRequest("gpu-create-missing-image", "demo-instance")
	req.Image = ""
	_, _, err := svc.CreateGPUUnit(ctx, req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %T", err)
	}
}

func TestService_CreateGPUUnit_RequiresPackageOrDRAAllocation(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := CreateGPUUnitRequest{
		OperationID: "gpu-create-missing-resources",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "pytorch:2.6",
		CPU:         "10",
		Memory:      "40Gi",
		GPU:         2,
	}
	_, _, err := svc.CreateGPUUnit(ctx, req)
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

	req := baseCreateGPUUnitRequest("gpu-create-storage-missing", "demo-instance")
	req.StorageMounts = []runtimev1alpha1.GPUUnitStorageMount{{
		Name:      "missing-storage",
		MountPath: "/data",
	}}
	_, _, err := svc.CreateGPUUnit(ctx, req)
	if err == nil {
		t.Fatalf("expected missing storage error")
	}
	var notFoundErr *NotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected not found error, got %T", err)
	}
}

func TestService_CreateGPUUnit_NormalizesSSHSpec(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := baseCreateGPUUnitRequest("gpu-create-ssh-1", "demo-ssh")
	req.SSH = runtimev1alpha1.GPUUnitSSHSpec{
		Enabled:      true,
		Username:     "Runtime",
		ServerAddr:   "frps.internal",
		DomainSuffix: "ssh.example.com",
		AuthorizedKeys: []string{
			"  ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== demo@example  ",
			"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== demo@example",
		},
	}
	unit, created, err := svc.CreateGPUUnit(ctx, req)
	if err != nil {
		t.Fatalf("create gpu unit with ssh error: %v", err)
	}
	if !created {
		t.Fatalf("expected create to persist a new gpu unit")
	}
	if !unit.SSH.Enabled {
		t.Fatalf("expected ssh to be enabled")
	}
	if unit.SSH.Username != "runtime" {
		t.Fatalf("expected ssh username runtime, got %s", unit.SSH.Username)
	}
	if len(unit.SSH.AuthorizedKeys) != 1 {
		t.Fatalf("expected deduped authorized keys, got %+v", unit.SSH.AuthorizedKeys)
	}
	if unit.SSH.ServerPort != runtimev1alpha1.DefaultUnitSSHFRPPort {
		t.Fatalf("expected default frp server port %d, got %d", runtimev1alpha1.DefaultUnitSSHFRPPort, unit.SSH.ServerPort)
	}
	if unit.SSH.ConnectHost != "frps.internal" {
		t.Fatalf("expected connect host frps.internal, got %s", unit.SSH.ConnectHost)
	}
	if unit.SSH.ConnectPort != runtimev1alpha1.DefaultUnitSSHProxyPort {
		t.Fatalf("expected default proxy port %d, got %d", runtimev1alpha1.DefaultUnitSSHProxyPort, unit.SSH.ConnectPort)
	}
	if unit.SSH.ClientDomain != "demo-ssh.runtime-instance.ssh.example.com" {
		t.Fatalf("expected generated client domain, got %s", unit.SSH.ClientDomain)
	}
}

func TestService_CreateGPUUnit_RejectsSSHWithoutKeys(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := baseCreateGPUUnitRequest("gpu-create-ssh-2", "demo-ssh")
	req.SSH = runtimev1alpha1.GPUUnitSSHSpec{
		Enabled:      true,
		ServerAddr:   "frps.internal",
		DomainSuffix: "ssh.example.com",
	}
	_, _, err := svc.CreateGPUUnit(ctx, req)
	if err == nil {
		t.Fatalf("expected ssh validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %T", err)
	}
}

func TestService_UpdateGPUUnit_StorageMounts(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()
	seedGPUStorage(t, ctx, svc, gpuStorageSeedOptions{
		name:      "model-cache",
		namespace: runtimev1alpha1.DefaultInstanceNamespace,
		size:      "20Gi",
		phase:     runtimev1alpha1.StoragePhaseReady,
	})

	req := baseCreateGPUUnitRequest("gpu-create-with-storage", "demo-instance")
	req.StorageMounts = []runtimev1alpha1.GPUUnitStorageMount{{
		Name:      "model-cache",
		MountPath: "/data",
	}}
	if _, _, err := svc.CreateGPUUnit(ctx, req); err != nil {
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
	seedGPUStorage(t, ctx, svc, gpuStorageSeedOptions{
		name:      "model-cache",
		namespace: runtimev1alpha1.DefaultInstanceNamespace,
		size:      "20Gi",
		phase:     runtimev1alpha1.StoragePhaseReady,
	})

	req := baseCreateGPUUnitRequest("gpu-create-storage-exclusive-1", "demo-instance-a")
	req.StorageMounts = []runtimev1alpha1.GPUUnitStorageMount{{
		Name:      "model-cache",
		MountPath: "/data",
	}}
	if _, _, err := svc.CreateGPUUnit(ctx, req); err != nil {
		t.Fatalf("create first gpu unit error: %v", err)
	}

	req = baseCreateGPUUnitRequest("gpu-create-storage-exclusive-2", "demo-instance-b")
	req.StorageMounts = []runtimev1alpha1.GPUUnitStorageMount{{
		Name:      "model-cache",
		MountPath: "/data",
	}}
	_, _, err := svc.CreateGPUUnit(ctx, req)
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
	seedGPUStorage(t, ctx, svc, gpuStorageSeedOptions{
		name:      "model-cache",
		namespace: runtimev1alpha1.DefaultInstanceNamespace,
		size:      "20Gi",
		phase:     runtimev1alpha1.StoragePhaseReady,
	})

	req := baseCreateGPUUnitRequest("gpu-create-storage-exclusive-update-1", "demo-instance-a")
	req.StorageMounts = []runtimev1alpha1.GPUUnitStorageMount{{
		Name:      "model-cache",
		MountPath: "/data",
	}}
	if _, _, err := svc.CreateGPUUnit(ctx, req); err != nil {
		t.Fatalf("create first gpu unit error: %v", err)
	}

	if _, _, err := svc.CreateGPUUnit(ctx, baseCreateGPUUnitRequest("gpu-create-storage-exclusive-update-2", "demo-instance-b")); err != nil {
		t.Fatalf("create second gpu unit error: %v", err)
	}

	_, err := svc.UpdateGPUUnit(ctx, runtimev1alpha1.DefaultInstanceNamespace, "demo-instance-b", UpdateGPUUnitRequest{
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

	req := baseCreateGPUUnitRequest("gpu-create-5", "demo-instance")
	req.Template = runtimev1alpha1.GPUUnitTemplate{
		Ports: []runtimev1alpha1.GPUUnitPortSpec{{Name: "http", Port: 8080}},
	}
	_, created, err := svc.CreateGPUUnit(ctx, req)
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
		t.Fatalf("expected no gpu units after delete, got %d items", len(items))
	}
}

func baseCreateGPUUnitRequest(operationID, name string) CreateGPUUnitRequest {
	return CreateGPUUnitRequest{
		OperationID: operationID,
		Name:        name,
		PackageID:   testPackageRTX3080Pair,
		Image:       "pytorch:2.6",
	}
}

func runtimeDRAResourceQuota(name, hardClaims, usedClaims string) *corev1.ResourceQuota {
	claimQuotaName := corev1.ResourceName("count/resourceclaims.resource.k8s.io")
	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Status: corev1.ResourceQuotaStatus{
			Hard: corev1.ResourceList{
				claimQuotaName:                resource.MustParse(hardClaims),
				corev1.ResourceRequestsCPU:    resource.MustParse("160"),
				corev1.ResourceRequestsMemory: resource.MustParse("256Gi"),
			},
			Used: corev1.ResourceList{
				claimQuotaName:                resource.MustParse(usedClaims),
				corev1.ResourceRequestsCPU:    resource.MustParse("0"),
				corev1.ResourceRequestsMemory: resource.MustParse("0"),
			},
		},
	}
}
