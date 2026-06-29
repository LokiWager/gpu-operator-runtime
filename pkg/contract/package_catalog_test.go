package contract

import (
	"testing"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestRuntimePackageCatalogNormalizedRejectsDuplicateIDs(t *testing.T) {
	_, err := RuntimePackageCatalog{
		{
			ID:       "gpu-a",
			SpecName: "gpu.a",
			CPU:      "1",
			Memory:   "1Gi",
			GPU:      1,
			Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
				DeviceClassName: "gpu-a",
				Count:           1,
			},
		},
		{
			ID:       "GPU-A",
			SpecName: "gpu.a",
			CPU:      "1",
			Memory:   "1Gi",
			GPU:      1,
			Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
				DeviceClassName: "gpu-a",
				Count:           1,
			},
		},
	}.Normalized()
	if err == nil {
		t.Fatalf("expected duplicate package validation error")
	}
}

func TestRuntimePackageCatalogNormalizedRejectsClaimName(t *testing.T) {
	_, err := RuntimePackageCatalog{{
		ID:       "gpu-a",
		SpecName: "gpu.a",
		CPU:      "1",
		Memory:   "1Gi",
		GPU:      1,
		Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
			DeviceClassName: "gpu-a",
			ClaimName:       "shared-claim",
			Count:           1,
		},
	}}.Normalized()
	if err == nil {
		t.Fatalf("expected claimName validation error")
	}
}
