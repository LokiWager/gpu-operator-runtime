package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func unitDRAClaimName(instance runtimev1alpha1.GPUUnit) string {
	claimName := strings.TrimSpace(instance.Spec.Allocation.ClaimName)
	if claimName != "" {
		return claimName
	}
	return runtimev1alpha1.GPUUnitNamePrefix + instance.Name + "-gpu"
}

func unitDRAClaimRequestName(instance runtimev1alpha1.GPUUnit) string {
	requestName := strings.TrimSpace(instance.Spec.Allocation.ClaimRequestName)
	if requestName != "" {
		return requestName
	}
	return runtimev1alpha1.UnitDRAClaimRequestName
}

func desiredUnitDRAResourceClaim(instance runtimev1alpha1.GPUUnit) (*resourcev1.ResourceClaim, error) {
	allocation := instance.Spec.Allocation
	deviceClassName := strings.TrimSpace(allocation.DeviceClassName)
	if deviceClassName == "" {
		return nil, fmt.Errorf("dra allocation requires deviceClassName")
	}
	count := allocation.Count
	if count <= 0 {
		count = int64(instance.Spec.GPU)
	}
	if count <= 0 {
		return nil, fmt.Errorf("dra allocation requires count > 0")
	}

	exact := resourcev1.ExactDeviceRequest{
		DeviceClassName: deviceClassName,
		AllocationMode:  resourcev1.DeviceAllocationModeExactCount,
		Count:           count,
		Selectors:       desiredDRASelectors(allocation.Selectors),
	}
	capacity, err := desiredDRACapacity(allocation.Capacity)
	if err != nil {
		return nil, err
	}
	if len(capacity) > 0 {
		exact.Capacity = &resourcev1.CapacityRequirements{Requests: capacity}
	}

	request := resourcev1.DeviceRequest{
		Name:    unitDRAClaimRequestName(instance),
		Exactly: &exact,
	}

	return &resourcev1.ResourceClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ResourceClaim",
			APIVersion: resourcev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      unitDRAClaimName(instance),
			Namespace: instance.Namespace,
			Labels:    unitObjectLabels(instance),
		},
		Spec: resourcev1.ResourceClaimSpec{
			Devices: resourcev1.DeviceClaim{
				Requests: []resourcev1.DeviceRequest{request},
			},
		},
	}, nil
}

func desiredDRACapacity(values map[string]string) (map[resourcev1.QualifiedName]resource.Quantity, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[resourcev1.QualifiedName]resource.Quantity, len(values))
	for key, value := range values {
		name := strings.TrimSpace(key)
		if name == "" {
			return nil, fmt.Errorf("dra capacity key is required")
		}
		qty, err := resource.ParseQuantity(value)
		if err != nil {
			return nil, fmt.Errorf("parse dra capacity %q: %w", name, err)
		}
		out[resourcev1.QualifiedName(name)] = qty
	}
	return out, nil
}

func desiredDRASelectors(values []string) []resourcev1.DeviceSelector {
	selectors := make([]resourcev1.DeviceSelector, 0, len(values))
	for _, value := range values {
		expression := strings.TrimSpace(value)
		if expression == "" {
			continue
		}
		selectors = append(selectors, resourcev1.DeviceSelector{
			CEL: &resourcev1.CELDeviceSelector{Expression: expression},
		})
	}
	return selectors
}

func (r *GPUUnitReconciler) reconcileGPUUnitDRAClaim(ctx context.Context, instance *runtimev1alpha1.GPUUnit) (runtimev1alpha1.GPUUnitDRAStatus, bool, error) {
	desired, err := desiredUnitDRAResourceClaim(*instance)
	if err != nil {
		return runtimev1alpha1.GPUUnitDRAStatus{}, false, err
	}
	if err := controllerutil.SetControllerReference(instance, desired, r.Scheme); err != nil {
		return runtimev1alpha1.GPUUnitDRAStatus{}, false, err
	}

	var claim resourcev1.ResourceClaim
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.Get(ctx, key, &claim); err != nil {
		if !apierrors.IsNotFound(err) {
			return runtimev1alpha1.GPUUnitDRAStatus{}, false, err
		}
		if err := r.Create(ctx, desired); err != nil {
			return runtimev1alpha1.GPUUnitDRAStatus{}, false, err
		}
		return runtimev1alpha1.GPUUnitDRAStatus{ClaimName: desired.Name}, true, nil
	}

	if !reflect.DeepEqual(claim.Spec, desired.Spec) {
		return gpuUnitDRAStatusFromClaim(&claim), false, fmt.Errorf("dra ResourceClaim %s/%s spec differs from GPUUnit allocation; recreate the GPUUnit to change package allocation", claim.Namespace, claim.Name)
	}
	return gpuUnitDRAStatusFromClaim(&claim), false, nil
}

func gpuUnitDRAStatusFromClaim(claim *resourcev1.ResourceClaim) runtimev1alpha1.GPUUnitDRAStatus {
	status := runtimev1alpha1.GPUUnitDRAStatus{ClaimName: claim.Name}
	if claim.Status.Allocation == nil {
		return status
	}
	status.Allocated = true
	results := append([]resourcev1.DeviceRequestAllocationResult(nil), claim.Status.Allocation.Devices.Results...)
	sort.Slice(results, func(i, j int) bool {
		left := results[i]
		right := results[j]
		if left.Request != right.Request {
			return left.Request < right.Request
		}
		if left.Driver != right.Driver {
			return left.Driver < right.Driver
		}
		if left.Pool != right.Pool {
			return left.Pool < right.Pool
		}
		return left.Device < right.Device
	})
	status.Devices = make([]runtimev1alpha1.GPUUnitDRADeviceStatus, 0, len(results))
	for _, result := range results {
		device := runtimev1alpha1.GPUUnitDRADeviceStatus{
			Request:          result.Request,
			Driver:           result.Driver,
			Pool:             result.Pool,
			Device:           result.Device,
			ConsumedCapacity: draCapacityMapToStrings(result.ConsumedCapacity),
		}
		if result.ShareID != nil {
			device.ShareID = string(*result.ShareID)
		}
		status.Devices = append(status.Devices, device)
	}
	return status
}

func draCapacityMapToStrings(values map[resourcev1.QualifiedName]resource.Quantity) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for name, qty := range values {
		out[string(name)] = qty.String()
	}
	return out
}

func desiredDRAResourceClaims(instance runtimev1alpha1.GPUUnit) []corev1.PodResourceClaim {
	claimName := unitDRAClaimName(instance)
	return []corev1.PodResourceClaim{{
		Name:              runtimev1alpha1.UnitDRAClaimName,
		ResourceClaimName: &claimName,
	}}
}
