package service

import (
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

// gpuUnitRuntimeFromObject converts a Kubernetes object into the API runtime view.
func gpuUnitRuntimeFromObject(instance *runtimev1alpha1.GPUUnit) domain.GPUUnitRuntime {
	var reason string
	var message string
	if cond := apimeta.FindStatusCondition(instance.Status.Conditions, runtimev1alpha1.ConditionReady); cond != nil {
		reason = cond.Reason
		message = cond.Message
	}

	return domain.GPUUnitRuntime{
		Name:               instance.Name,
		Namespace:          instance.Namespace,
		Lifecycle:          lifecycleForUnit(instance),
		PackageID:          instance.Spec.PackageID,
		SpecName:           instance.Spec.SpecName,
		Image:              instance.Spec.Image,
		CPU:                instance.Spec.CPU,
		Memory:             instance.Spec.Memory,
		GPU:                instance.Spec.GPU,
		Allocation:         normalizedUnitAllocation(instance.Spec.Allocation),
		Template:           instance.Spec.Template,
		Access:             instance.Spec.Access,
		SSH:                instance.Spec.SSH,
		Serverless:         instance.Spec.Serverless,
		StorageMounts:      append([]runtimev1alpha1.GPUUnitStorageMount(nil), instance.Spec.StorageMounts...),
		Phase:              instance.Status.Phase,
		ReadyReplicas:      instance.Status.ReadyReplicas,
		ObservedGeneration: instance.Status.ObservedGeneration,
		LastSyncTime:       instance.Status.LastSyncTime.Time,
		ServiceName:        instance.Status.ServiceName,
		AccessURL:          instance.Status.AccessURL,
		SSHStatus:          instance.Status.SSH,
		ServerlessStatus:   instance.Status.Serverless,
		DRAStatus:          instance.Status.DRA,
		Reason:             reason,
		Message:            message,
	}
}

func normalizedUnitAllocation(allocation runtimev1alpha1.GPUUnitAllocationSpec) runtimev1alpha1.GPUUnitAllocationSpec {
	allocation.DeviceClassName = strings.TrimSpace(allocation.DeviceClassName)
	allocation.ClaimName = strings.TrimSpace(allocation.ClaimName)
	allocation.ClaimRequestName = strings.TrimSpace(allocation.ClaimRequestName)
	if allocation.ClaimRequestName == "" {
		allocation.ClaimRequestName = runtimev1alpha1.UnitDRAClaimRequestName
	}
	return allocation
}

// lifecycleForUnit derives the API lifecycle label.
func lifecycleForUnit(instance *runtimev1alpha1.GPUUnit) string {
	return runtimev1alpha1.LifecycleInstance
}
