package service

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

// ensureDRAAllocationAvailable rejects requests that clearly exceed namespace policy.
func (s *Service) ensureDRAAllocationAvailable(ctx context.Context, req CreateGPUUnitRequest) error {
	if s.kube == nil {
		return nil
	}

	return s.ensureResourceQuotaAllows(ctx, runtimev1alpha1.DefaultInstanceNamespace, req)
}

func (s *Service) ensureResourceQuotaAllows(ctx context.Context, namespace string, req CreateGPUUnitRequest) error {
	quotas, err := s.kube.CoreV1().ResourceQuotas(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	draClaimQuotaName := corev1.ResourceName("count/resourceclaims.resource.k8s.io")
	for i := range quotas.Items {
		quota := &quotas.Items[i]
		if req.CPU != "" {
			cpu, err := resource.ParseQuantity(req.CPU)
			if err != nil {
				return err
			}
			if err := ensureQuotaResourceAllows(quota, corev1.ResourceRequestsCPU, cpu); err != nil {
				return err
			}
		}
		if err := ensureQuotaResourceAllows(quota, draClaimQuotaName, *resource.NewQuantity(1, resource.DecimalSI)); err != nil {
			return err
		}
		if req.Memory != "" {
			memory, err := resource.ParseQuantity(req.Memory)
			if err != nil {
				return err
			}
			if err := ensureQuotaResourceAllows(quota, corev1.ResourceRequestsMemory, memory); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureQuotaResourceAllows(quota *corev1.ResourceQuota, name corev1.ResourceName, requested resource.Quantity) error {
	hard, ok := quota.Status.Hard[name]
	if !ok {
		return nil
	}

	used := quota.Status.Used[name]
	projected := used.DeepCopy()
	projected.Add(requested)
	if projected.Cmp(hard) <= 0 {
		return nil
	}
	return &CapacityError{
		Message: fmt.Sprintf("resource quota %s/%s would be exceeded for %s: used=%s requested=%s hard=%s", quota.Namespace, quota.Name, name, used.String(), requested.String(), hard.String()),
	}
}

// RuntimeInventory returns a Kubernetes-derived inventory and quota view.
func (s *Service) RuntimeInventory(ctx context.Context) (domain.RuntimeInventoryStatus, error) {
	nodeInventory, err := s.collectNodeInventory(ctx)
	if err != nil {
		return domain.RuntimeInventoryStatus{}, err
	}

	draInventory, err := s.collectDRAInventory(ctx)
	if err != nil {
		return domain.RuntimeInventoryStatus{}, err
	}

	out := domain.RuntimeInventoryStatus{
		KubernetesConnected: nodeInventory.Connected,
		NodeCount:           nodeInventory.NodeCount,
		ReadyNodeCount:      nodeInventory.ReadyNodeCount,
		TotalGPUCapacity:    nodeInventory.TotalGPUCapacity,
		TotalGPUAllocatable: nodeInventory.TotalGPUAllocatable,
		DRAClaimCount:       draInventory.ClaimCount,
		DRAAllocatedClaims:  draInventory.AllocatedClaims,
		DRAAllocatedDevices: draInventory.AllocatedDevices,
		DRAResourceSlices:   draInventory.ResourceSlices,
		DRADevices:          append([]domain.RuntimeDRADeviceStatus(nil), draInventory.Devices...),
		GPUProducts:         append([]domain.GPUProductHealth(nil), nodeInventory.GPUProducts...),
	}
	if s.kube == nil {
		return out, nil
	}

	quotas, err := s.kube.CoreV1().ResourceQuotas(runtimev1alpha1.DefaultInstanceNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return domain.RuntimeInventoryStatus{}, err
	}
	out.ResourceQuotas = make([]domain.RuntimeQuotaStatus, 0, len(quotas.Items))
	for i := range quotas.Items {
		quota := &quotas.Items[i]
		out.ResourceQuotas = append(out.ResourceQuotas, domain.RuntimeQuotaStatus{
			Name:      quota.Name,
			Namespace: quota.Namespace,
			Hard:      quantityMapToStrings(quota.Status.Hard),
			Used:      quantityMapToStrings(quota.Status.Used),
		})
	}
	sort.Slice(out.ResourceQuotas, func(i, j int) bool {
		if out.ResourceQuotas[i].Namespace != out.ResourceQuotas[j].Namespace {
			return out.ResourceQuotas[i].Namespace < out.ResourceQuotas[j].Namespace
		}
		return out.ResourceQuotas[i].Name < out.ResourceQuotas[j].Name
	})
	return out, nil
}

type draInventorySnapshot struct {
	ClaimCount       int
	AllocatedClaims  int
	AllocatedDevices int
	ResourceSlices   int
	Devices          []domain.RuntimeDRADeviceStatus
}

func (s *Service) collectDRAInventory(ctx context.Context) (draInventorySnapshot, error) {
	if s.operator == nil {
		return draInventorySnapshot{}, nil
	}

	var claims resourcev1.ResourceClaimList
	if err := s.operator.List(ctx, &claims, ctrlclient.InNamespace(runtimev1alpha1.DefaultInstanceNamespace)); err != nil {
		return draInventorySnapshot{}, err
	}

	out := draInventorySnapshot{ClaimCount: len(claims.Items)}
	for i := range claims.Items {
		claim := &claims.Items[i]
		if claim.Status.Allocation == nil {
			continue
		}
		out.AllocatedClaims++
		results := claim.Status.Allocation.Devices.Results
		out.AllocatedDevices += len(results)
		for _, result := range results {
			device := domain.RuntimeDRADeviceStatus{
				ClaimName:        claim.Name,
				Namespace:        claim.Namespace,
				Request:          result.Request,
				Driver:           result.Driver,
				Pool:             result.Pool,
				Device:           result.Device,
				ConsumedCapacity: draQuantityMapToStrings(result.ConsumedCapacity),
			}
			if result.ShareID != nil {
				device.ShareID = string(*result.ShareID)
			}
			out.Devices = append(out.Devices, device)
		}
	}

	var slices resourcev1.ResourceSliceList
	if err := s.operator.List(ctx, &slices); err != nil {
		return draInventorySnapshot{}, err
	}
	out.ResourceSlices = len(slices.Items)
	sort.Slice(out.Devices, func(i, j int) bool {
		if out.Devices[i].Namespace != out.Devices[j].Namespace {
			return out.Devices[i].Namespace < out.Devices[j].Namespace
		}
		if out.Devices[i].ClaimName != out.Devices[j].ClaimName {
			return out.Devices[i].ClaimName < out.Devices[j].ClaimName
		}
		if out.Devices[i].Driver != out.Devices[j].Driver {
			return out.Devices[i].Driver < out.Devices[j].Driver
		}
		if out.Devices[i].Pool != out.Devices[j].Pool {
			return out.Devices[i].Pool < out.Devices[j].Pool
		}
		return out.Devices[i].Device < out.Devices[j].Device
	})
	return out, nil
}

func quantityMapToStrings(values corev1.ResourceList) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for name, qty := range values {
		out[string(name)] = qty.String()
	}
	return out
}

func draQuantityMapToStrings(values map[resourcev1.QualifiedName]resource.Quantity) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for name, qty := range values {
		out[string(name)] = qty.String()
	}
	return out
}
