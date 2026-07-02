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

func TestRuntimePackageCatalogNormalizedExpandsHAMiVirtualGPU(t *testing.T) {
	catalog, err := RuntimePackageCatalog{{
		ID:       "gpu-hami-10g-50c",
		SpecName: "gpu.hami.10g.50c.4c.16g",
		CPU:      "4",
		Memory:   "16Gi",
		GPU:      1,
		VirtualGPU: RuntimePackageVirtualGPUSpec{
			Provider: " HAMi ",
			Memory:   " 10Gi ",
			Cores:    50,
		},
		Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
			ClaimRequestName: "gpu",
		},
	}}.Normalized()
	if err != nil {
		t.Fatalf("normalize catalog: %v", err)
	}

	pkg := catalog[0]
	if pkg.VirtualGPU.Provider != RuntimePackageVirtualGPUProviderHAMi {
		t.Fatalf("expected hami provider, got %+v", pkg.VirtualGPU)
	}
	if pkg.VirtualGPU.Memory != "10Gi" || pkg.VirtualGPU.Cores != 50 {
		t.Fatalf("expected normalized virtual gpu, got %+v", pkg.VirtualGPU)
	}
	if pkg.Allocation.DeviceClassName != HAMiDRADeviceClassName {
		t.Fatalf("expected hami device class, got %+v", pkg.Allocation)
	}
	if pkg.Allocation.Count != 1 {
		t.Fatalf("expected default hami count 1, got %+v", pkg.Allocation)
	}
	if pkg.Allocation.Capacity[HAMiDRACapacityMemoryKey] != "10Gi" {
		t.Fatalf("expected hami memory capacity, got %+v", pkg.Allocation.Capacity)
	}
	if pkg.Allocation.Capacity[HAMiDRACapacityCoresKey] != "50" {
		t.Fatalf("expected hami cores capacity, got %+v", pkg.Allocation.Capacity)
	}
}

func TestRuntimePackageCatalogNormalizedRejectsInvalidHAMiVirtualGPU(t *testing.T) {
	tests := []struct {
		name       string
		virtualGPU RuntimePackageVirtualGPUSpec
		allocation runtimev1alpha1.GPUUnitAllocationSpec
	}{
		{
			name: "missing provider",
			virtualGPU: RuntimePackageVirtualGPUSpec{
				Memory: "10Gi",
			},
		},
		{
			name: "unsupported provider",
			virtualGPU: RuntimePackageVirtualGPUSpec{
				Provider: "other",
				Memory:   "10Gi",
			},
		},
		{
			name: "missing memory",
			virtualGPU: RuntimePackageVirtualGPUSpec{
				Provider: "hami",
				Cores:    50,
			},
		},
		{
			name: "invalid cores",
			virtualGPU: RuntimePackageVirtualGPUSpec{
				Provider: "hami",
				Memory:   "10Gi",
				Cores:    101,
			},
		},
		{
			name: "conflicting capacity",
			virtualGPU: RuntimePackageVirtualGPUSpec{
				Provider: "hami",
				Memory:   "10Gi",
			},
			allocation: runtimev1alpha1.GPUUnitAllocationSpec{
				Capacity: map[string]string{
					HAMiDRACapacityMemoryKey: "20Gi",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RuntimePackageCatalog{{
				ID:         "gpu-hami-bad",
				SpecName:   "gpu.hami.bad",
				CPU:        "4",
				Memory:     "16Gi",
				GPU:        1,
				VirtualGPU: tt.virtualGPU,
				Allocation: tt.allocation,
			}}.Normalized()
			if err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}
