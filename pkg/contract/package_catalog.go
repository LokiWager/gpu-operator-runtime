package contract

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

const (
	// RuntimePackageVirtualGPUProviderHAMi enables HAMi's native DRA provider path.
	RuntimePackageVirtualGPUProviderHAMi = "hami"

	// HAMiDRADeviceClassName is the DeviceClass exposed by HAMi DRA native mode.
	HAMiDRADeviceClassName = "hami-core-gpu.project-hami.io"

	// HAMiDRACapacityMemoryKey and HAMiDRACapacityCoresKey are the capacity keys HAMi consumes through DRA.
	HAMiDRACapacityMemoryKey = "memory"
	HAMiDRACapacityCoresKey  = "cores"
)

// RuntimePackageCatalog is the ops-managed list of runtime packages loaded from YAML.
type RuntimePackageCatalog []RuntimePackageSpec

// RuntimePackageSpec expands a stable package ID into a concrete DRA-backed runtime contract.
type RuntimePackageSpec struct {
	ID         string                                `json:"id" yaml:"id"`
	SpecName   string                                `json:"specName" yaml:"specName"`
	CPU        string                                `json:"cpu" yaml:"cpu"`
	Memory     string                                `json:"memory" yaml:"memory"`
	GPU        int32                                 `json:"gpu" yaml:"gpu"`
	VirtualGPU RuntimePackageVirtualGPUSpec          `json:"virtualGPU,omitempty" yaml:"virtualGPU,omitempty"`
	Allocation runtimev1alpha1.GPUUnitAllocationSpec `json:"allocation" yaml:"allocation"`
}

// RuntimePackageVirtualGPUSpec describes provider-specific virtual GPU intent in the ops catalog.
type RuntimePackageVirtualGPUSpec struct {
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Memory   string `json:"memory,omitempty" yaml:"memory,omitempty"`
	Cores    int32  `json:"cores,omitempty" yaml:"cores,omitempty"`
}

// Normalized validates and canonicalizes the package catalog loaded from configuration.
func (c RuntimePackageCatalog) Normalized() (RuntimePackageCatalog, error) {
	out := make(RuntimePackageCatalog, 0, len(c))
	seen := map[string]struct{}{}
	for _, item := range c {
		normalized, err := item.Normalized()
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized.ID]; ok {
			return nil, &ValidationError{Message: fmt.Sprintf("packageID %q is configured more than once", normalized.ID)}
		}
		seen[normalized.ID] = struct{}{}
		out = append(out, normalized)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Normalized validates and canonicalizes a single runtime package.
func (p RuntimePackageSpec) Normalized() (RuntimePackageSpec, error) {
	p.ID = strings.ToLower(strings.TrimSpace(p.ID))
	if p.ID == "" {
		return RuntimePackageSpec{}, &ValidationError{Message: "package id is required"}
	}
	if errs := validation.IsDNS1123Label(p.ID); len(errs) > 0 {
		return RuntimePackageSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q is invalid: %s", p.ID, strings.Join(errs, "; "))}
	}

	p.SpecName = strings.TrimSpace(p.SpecName)
	if p.SpecName == "" {
		return RuntimePackageSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q specName is required", p.ID)}
	}
	p.CPU = strings.TrimSpace(p.CPU)
	if p.CPU == "" {
		return RuntimePackageSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q cpu is required", p.ID)}
	}
	if _, err := resource.ParseQuantity(p.CPU); err != nil {
		return RuntimePackageSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q cpu %q is invalid: %v", p.ID, p.CPU, err)}
	}
	p.Memory = strings.TrimSpace(p.Memory)
	if p.Memory == "" {
		return RuntimePackageSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q memory is required", p.ID)}
	}
	if _, err := resource.ParseQuantity(p.Memory); err != nil {
		return RuntimePackageSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q memory %q is invalid: %v", p.ID, p.Memory, err)}
	}
	if p.GPU <= 0 {
		return RuntimePackageSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q gpu should be > 0", p.ID)}
	}

	virtualGPU, allocation, err := normalizeRuntimePackageVirtualGPU(p.ID, p.VirtualGPU, p.Allocation)
	if err != nil {
		return RuntimePackageSpec{}, err
	}
	p.VirtualGPU = virtualGPU
	p.Allocation = allocation

	allocation, err = normalizePackageDRAAllocation(p.ID, p.Allocation)
	if err != nil {
		return RuntimePackageSpec{}, err
	}
	p.Allocation = allocation
	return p, nil
}

// Lookup returns one normalized package by ID.
func (c RuntimePackageCatalog) Lookup(packageID string) (RuntimePackageSpec, bool) {
	normalizedID := strings.ToLower(strings.TrimSpace(packageID))
	for _, item := range c {
		if item.ID == normalizedID {
			return item, true
		}
	}
	return RuntimePackageSpec{}, false
}

// Clone returns a deep copy of the catalog so request paths do not share mutable maps or slices.
func (c RuntimePackageCatalog) Clone() RuntimePackageCatalog {
	out := make(RuntimePackageCatalog, 0, len(c))
	for _, item := range c {
		out = append(out, cloneRuntimePackageSpec(item))
	}
	return out
}

// ExpandRuntimePackage applies a control-plane managed package to the request.
func ExpandRuntimePackage(req CreateGPUUnitRequest, catalog RuntimePackageCatalog) (CreateGPUUnitRequest, error) {
	packageID := strings.ToLower(strings.TrimSpace(req.PackageID))
	pkg, ok := catalog.Lookup(packageID)
	if !ok {
		return CreateGPUUnitRequest{}, &ValidationError{Message: fmt.Sprintf("packageID %q is not configured", req.PackageID)}
	}

	if err := rejectPackageStringOverride("specName", req.SpecName, pkg.SpecName); err != nil {
		return CreateGPUUnitRequest{}, err
	}
	if err := rejectPackageStringOverride("cpu", req.CPU, pkg.CPU); err != nil {
		return CreateGPUUnitRequest{}, err
	}
	if err := rejectPackageStringOverride("memory", req.Memory, pkg.Memory); err != nil {
		return CreateGPUUnitRequest{}, err
	}
	if req.GPU != 0 && req.GPU != pkg.GPU {
		return CreateGPUUnitRequest{}, &ValidationError{
			Message: fmt.Sprintf("gpu %d conflicts with packageID %q gpu %d", req.GPU, pkg.ID, pkg.GPU),
		}
	}

	req.PackageID = pkg.ID
	req.SpecName = pkg.SpecName
	req.CPU = pkg.CPU
	req.Memory = pkg.Memory
	req.GPU = pkg.GPU
	req.Allocation = cloneAllocation(pkg.Allocation)
	return req, nil
}

func normalizeRuntimePackageVirtualGPU(packageID string, virtualGPU RuntimePackageVirtualGPUSpec, allocation runtimev1alpha1.GPUUnitAllocationSpec) (RuntimePackageVirtualGPUSpec, runtimev1alpha1.GPUUnitAllocationSpec, error) {
	virtualGPU.Provider = strings.ToLower(strings.TrimSpace(virtualGPU.Provider))
	virtualGPU.Memory = strings.TrimSpace(virtualGPU.Memory)

	if virtualGPU.Provider == "" {
		if virtualGPU.Memory != "" || virtualGPU.Cores != 0 {
			return RuntimePackageVirtualGPUSpec{}, runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q virtualGPU provider is required", packageID)}
		}
		return virtualGPU, allocation, nil
	}
	if virtualGPU.Provider != RuntimePackageVirtualGPUProviderHAMi {
		return RuntimePackageVirtualGPUSpec{}, runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q virtualGPU provider %q is not supported", packageID, virtualGPU.Provider)}
	}
	if virtualGPU.Memory == "" {
		return RuntimePackageVirtualGPUSpec{}, runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q hami virtualGPU memory is required", packageID)}
	}
	memoryQuantity, err := resource.ParseQuantity(virtualGPU.Memory)
	if err != nil {
		return RuntimePackageVirtualGPUSpec{}, runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q hami virtualGPU memory %q is invalid: %v", packageID, virtualGPU.Memory, err)}
	}
	if memoryQuantity.Sign() <= 0 {
		return RuntimePackageVirtualGPUSpec{}, runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q hami virtualGPU memory should be > 0", packageID)}
	}
	if virtualGPU.Cores < 0 || virtualGPU.Cores > 100 {
		return RuntimePackageVirtualGPUSpec{}, runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q hami virtualGPU cores should be between 1 and 100 when configured", packageID)}
	}

	allocation.DeviceClassName = strings.TrimSpace(allocation.DeviceClassName)
	if allocation.DeviceClassName == "" {
		allocation.DeviceClassName = HAMiDRADeviceClassName
	}
	if allocation.Count == 0 {
		allocation.Count = 1
	}
	if err := mergePackageDRACapacity(packageID, &allocation, HAMiDRACapacityMemoryKey, virtualGPU.Memory, "virtualGPU.memory"); err != nil {
		return RuntimePackageVirtualGPUSpec{}, runtimev1alpha1.GPUUnitAllocationSpec{}, err
	}
	if virtualGPU.Cores > 0 {
		cores := strconv.FormatInt(int64(virtualGPU.Cores), 10)
		if err := mergePackageDRACapacity(packageID, &allocation, HAMiDRACapacityCoresKey, cores, "virtualGPU.cores"); err != nil {
			return RuntimePackageVirtualGPUSpec{}, runtimev1alpha1.GPUUnitAllocationSpec{}, err
		}
	}
	return virtualGPU, allocation, nil
}

func mergePackageDRACapacity(packageID string, allocation *runtimev1alpha1.GPUUnitAllocationSpec, key, value, sourceField string) error {
	if allocation.Capacity == nil {
		allocation.Capacity = map[string]string{}
	}
	for existingKey, existingValue := range allocation.Capacity {
		if strings.TrimSpace(existingKey) != key {
			continue
		}
		normalizedValue := strings.TrimSpace(existingValue)
		if normalizedValue != value {
			return &ValidationError{Message: fmt.Sprintf("packageID %q dra capacity %q=%q conflicts with %s %q", packageID, key, normalizedValue, sourceField, value)}
		}
		if existingKey != key {
			delete(allocation.Capacity, existingKey)
		}
	}
	allocation.Capacity[key] = value
	return nil
}

func normalizePackageDRAAllocation(packageID string, allocation runtimev1alpha1.GPUUnitAllocationSpec) (runtimev1alpha1.GPUUnitAllocationSpec, error) {
	allocation.DeviceClassName = strings.TrimSpace(allocation.DeviceClassName)
	if allocation.DeviceClassName == "" {
		return runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q dra deviceClassName is required", packageID)}
	}
	allocation.ClaimName = strings.TrimSpace(allocation.ClaimName)
	if allocation.ClaimName != "" {
		return runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q must not configure dra claimName", packageID)}
	}
	allocation.ClaimRequestName = strings.TrimSpace(allocation.ClaimRequestName)
	if allocation.ClaimRequestName == "" {
		allocation.ClaimRequestName = runtimev1alpha1.UnitDRAClaimRequestName
	}
	if allocation.Count <= 0 {
		return runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q dra count should be > 0", packageID)}
	}

	capacity := make(map[string]string, len(allocation.Capacity))
	for key, value := range allocation.Capacity {
		name := strings.TrimSpace(key)
		if name == "" {
			return runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q dra capacity key is required", packageID)}
		}
		normalizedValue := strings.TrimSpace(value)
		if _, err := resource.ParseQuantity(normalizedValue); err != nil {
			return runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q dra capacity %q is invalid: %v", packageID, name, err)}
		}
		capacity[name] = normalizedValue
	}
	if len(capacity) > 0 {
		allocation.Capacity = capacity
	}

	selectors := make([]string, 0, len(allocation.Selectors))
	for _, selector := range allocation.Selectors {
		normalized := strings.TrimSpace(selector)
		if normalized == "" {
			return runtimev1alpha1.GPUUnitAllocationSpec{}, &ValidationError{Message: fmt.Sprintf("packageID %q dra selector expression is required", packageID)}
		}
		selectors = append(selectors, normalized)
	}
	allocation.Selectors = selectors
	return allocation, nil
}

func cloneRuntimePackageSpec(in RuntimePackageSpec) RuntimePackageSpec {
	in.Allocation = cloneAllocation(in.Allocation)
	return in
}

func cloneAllocation(in runtimev1alpha1.GPUUnitAllocationSpec) runtimev1alpha1.GPUUnitAllocationSpec {
	out := in
	if in.Capacity != nil {
		out.Capacity = make(map[string]string, len(in.Capacity))
		for key, value := range in.Capacity {
			out.Capacity[key] = value
		}
	}
	out.Selectors = append([]string(nil), in.Selectors...)
	return out
}

func rejectPackageStringOverride(field, got, want string) error {
	got = strings.TrimSpace(got)
	if got == "" || got == want {
		return nil
	}
	return &ValidationError{Message: fmt.Sprintf("%s %q conflicts with package value %q", field, got, want)}
}
