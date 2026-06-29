package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/contract"
)

const testPackageRTX3080Pair = "gpu-rtx3080-2x-cpu10-mem40g"

func testRuntimePackageCatalog() contract.RuntimePackageCatalog {
	return contract.RuntimePackageCatalog{{
		ID:       testPackageRTX3080Pair,
		SpecName: "gpu.rtx3080.2x.10c.40g",
		CPU:      "10",
		Memory:   "40Gi",
		GPU:      2,
		Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
			DeviceClassName:  "nvidia-rtx-3080",
			ClaimRequestName: runtimev1alpha1.UnitDRAClaimRequestName,
			Count:            2,
		},
	}}
}

func newOperatorService(t *testing.T) (*Service, context.Context, context.CancelFunc) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}
	if err := resourcev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add resource scheme error: %v", err)
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithStatusSubresource(&runtimev1alpha1.GPUStorage{}).
		WithStatusSubresource(&resourcev1.ResourceClaim{}).
		Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(nil, cl, logger)
	if err := svc.ConfigureRuntimePackages(testRuntimePackageCatalog()); err != nil {
		t.Fatalf("configure runtime packages: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return svc, ctx, cancel
}
