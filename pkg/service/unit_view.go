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
		Name:                 instance.Name,
		Namespace:            instance.Namespace,
		Lifecycle:            lifecycleForUnit(instance),
		SpecName:             instance.Spec.SpecName,
		SourceStockName:      sourceStockNameForUnit(instance),
		SourceStockNamespace: sourceStockNamespaceForUnit(instance),
		Image:                instance.Spec.Image,
		Memory:               instance.Spec.Memory,
		GPU:                  instance.Spec.GPU,
		Template:             instance.Spec.Template,
		Access:               instance.Spec.Access,
		SSH:                  instance.Spec.SSH,
		StorageMounts:        append([]runtimev1alpha1.GPUUnitStorageMount(nil), instance.Spec.StorageMounts...),
		Phase:                instance.Status.Phase,
		ReadyReplicas:        instance.Status.ReadyReplicas,
		ObservedGeneration:   instance.Status.ObservedGeneration,
		LastSyncTime:         instance.Status.LastSyncTime.Time,
		ServiceName:          instance.Status.ServiceName,
		AccessURL:            instance.Status.AccessURL,
		SSHStatus:            instance.Status.SSH,
		Reason:               reason,
		Message:              message,
	}
}

// lifecycleForUnit derives the API lifecycle label from namespace placement.
func lifecycleForUnit(instance *runtimev1alpha1.GPUUnit) string {
	if isStockGPUUnit(instance) {
		return runtimev1alpha1.LifecycleStock
	}
	return runtimev1alpha1.LifecycleInstance
}

// isStockGPUUnit reports whether the object belongs to the stock namespace.
func isStockGPUUnit(instance *runtimev1alpha1.GPUUnit) bool {
	return instance != nil && instance.Namespace == runtimev1alpha1.DefaultStockNamespace
}

// isActiveGPUUnit reports whether the object belongs to an active runtime namespace.
func isActiveGPUUnit(instance *runtimev1alpha1.GPUUnit) bool {
	return !isStockGPUUnit(instance)
}

// sourceStockNameForUnit returns the provenance annotation recorded during handoff.
func sourceStockNameForUnit(instance *runtimev1alpha1.GPUUnit) string {
	if instance == nil {
		return ""
	}
	return strings.TrimSpace(instance.GetAnnotations()[runtimev1alpha1.AnnotationSourceStockName])
}

// sourceStockNamespaceForUnit returns the stock namespace recorded during handoff.
func sourceStockNamespaceForUnit(instance *runtimev1alpha1.GPUUnit) string {
	if instance == nil {
		return ""
	}
	return strings.TrimSpace(instance.GetAnnotations()[runtimev1alpha1.AnnotationSourceStockNamespace])
}
