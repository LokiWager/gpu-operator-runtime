package controller

import (
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

const (
	statusOnlyMessage      = "status already updated"
	parseMemoryErrorFormat = "parse memory %q: %w"
	requeueAfterUpdate     = 2 * time.Second
)

// desiredUnitDeployment builds the single-replica workload owned by one GPUUnit.
func desiredUnitDeployment(instance runtimev1alpha1.GPUUnit) (*appsv1.Deployment, error) {
	name := deploymentNameForUnit(instance.Name)
	labels := unitObjectLabels(instance)
	template, err := desiredUnitPodTemplate(instance)
	if err != nil {
		return nil, err
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{runtimev1alpha1.LabelUnitKey: instance.Name}},
			Template: template,
		},
	}, nil
}

// desiredUnitPodTemplate converts the unit spec into the pod template owned by the Deployment.
func desiredUnitPodTemplate(instance runtimev1alpha1.GPUUnit) (corev1.PodTemplateSpec, error) {
	labels := unitPodLabels(instance)
	image := instance.Spec.Image
	if image == "" {
		image = runtimev1alpha1.DefaultRuntimeImage
	}

	resources := corev1.ResourceRequirements{}
	if instance.Spec.Memory != "" {
		qty, err := resource.ParseQuantity(instance.Spec.Memory)
		if err != nil {
			return corev1.PodTemplateSpec{}, fmt.Errorf(parseMemoryErrorFormat, instance.Spec.Memory, err)
		}
		resources.Requests = corev1.ResourceList{corev1.ResourceMemory: qty}
		resources.Limits = corev1.ResourceList{corev1.ResourceMemory: qty}
	}
	if instance.Spec.GPU > 0 {
		if resources.Requests == nil {
			resources.Requests = corev1.ResourceList{}
		}
		if resources.Limits == nil {
			resources.Limits = corev1.ResourceList{}
		}
		gpuQty := *resource.NewQuantity(int64(instance.Spec.GPU), resource.DecimalSI)
		resources.Requests[corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName)] = gpuQty
		resources.Limits[corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName)] = gpuQty
	}

	container := corev1.Container{
		Name:            runtimev1alpha1.RuntimeWorkerContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             defaultGPUUnitEnv(instance),
		Ports:           desiredContainerPorts(instance.Spec.Template.Ports),
		Resources:       resources,
	}
	if len(instance.Spec.Template.Command) > 0 {
		container.Command = append([]string(nil), instance.Spec.Template.Command...)
	}
	if len(instance.Spec.Template.Args) > 0 {
		container.Args = append([]string(nil), instance.Spec.Template.Args...)
	}
	if len(instance.Spec.Template.Command) == 0 && len(instance.Spec.Template.Args) == 0 {
		container.Command = []string{
			runtimev1alpha1.RuntimeCommandShell,
			runtimev1alpha1.RuntimeCommandShellFlag,
			runtimev1alpha1.RuntimeCommandSleep,
		}
	}
	for _, env := range instance.Spec.Template.Envs {
		container.Env = append(container.Env, corev1.EnvVar{Name: env.Name, Value: env.Value})
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{container}},
	}, nil
}

// defaultGPUUnitEnv injects runtime metadata that every managed container should see.
func defaultGPUUnitEnv(instance runtimev1alpha1.GPUUnit) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: runtimev1alpha1.EnvSpecName, Value: instance.Spec.SpecName},
		{Name: runtimev1alpha1.EnvUnitName, Value: instance.Name},
		{Name: runtimev1alpha1.EnvGPUCount, Value: fmt.Sprintf("%d", instance.Spec.GPU)},
		{Name: runtimev1alpha1.EnvMemoryLimit, Value: instance.Spec.Memory},
	}
}

// desiredContainerPorts maps API port declarations into container port objects.
func desiredContainerPorts(ports []runtimev1alpha1.GPUUnitPortSpec) []corev1.ContainerPort {
	out := make([]corev1.ContainerPort, 0, len(ports))
	for _, port := range ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		out = append(out, corev1.ContainerPort{
			Name:          port.Name,
			ContainerPort: port.Port,
			Protocol:      protocol,
		})
	}
	return out
}

// desiredServicePorts maps API port declarations into Service port objects.
func desiredServicePorts(ports []runtimev1alpha1.GPUUnitPortSpec) []corev1.ServicePort {
	out := make([]corev1.ServicePort, 0, len(ports))
	for _, port := range ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		out = append(out, corev1.ServicePort{
			Name:       port.Name,
			Port:       port.Port,
			TargetPort: intstr.FromInt32(port.Port),
			Protocol:   protocol,
		})
	}
	return out
}

// desiredGPUUnitService builds the stable ClusterIP Service for an active runtime unit.
func desiredGPUUnitService(instance runtimev1alpha1.GPUUnit, ports []corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceNameForUnit(instance.Name),
			Namespace: instance.Namespace,
			Labels:    unitObjectLabels(instance),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{runtimev1alpha1.LabelUnitKey: instance.Name},
			Ports:    ports,
		},
	}
}

// normalizeControllerGPUUnitAccess validates controller-side service exposure settings.
func normalizeControllerGPUUnitAccess(access runtimev1alpha1.GPUUnitAccess, ports []runtimev1alpha1.GPUUnitPortSpec) (runtimev1alpha1.GPUUnitAccess, error) {
	access.PrimaryPort = strings.TrimSpace(access.PrimaryPort)
	access.Scheme = strings.ToLower(strings.TrimSpace(access.Scheme))
	if access.Scheme == "" {
		access.Scheme = runtimev1alpha1.DefaultAccessScheme
	}

	if len(ports) == 0 {
		if access.PrimaryPort != "" {
			return runtimev1alpha1.GPUUnitAccess{}, fmt.Errorf("access.primaryPort %q requires at least one runtime port", access.PrimaryPort)
		}
		return access, nil
	}

	if access.PrimaryPort == "" {
		access.PrimaryPort = ports[0].Name
	}
	for _, port := range ports {
		if port.Name == access.PrimaryPort {
			return access, nil
		}
	}
	return runtimev1alpha1.GPUUnitAccess{}, fmt.Errorf("access.primaryPort %q does not exist in template.ports", access.PrimaryPort)
}

// buildUnitAccessURL renders the in-cluster URL published in unit status.
func buildUnitAccessURL(namespace, serviceName string, access runtimev1alpha1.GPUUnitAccess, ports []runtimev1alpha1.GPUUnitPortSpec) (string, error) {
	normalizedAccess, err := normalizeControllerGPUUnitAccess(access, ports)
	if err != nil {
		return "", err
	}
	if len(ports) == 0 {
		return "", nil
	}

	for _, port := range ports {
		if port.Name == normalizedAccess.PrimaryPort {
			return fmt.Sprintf("%s://%s.%s.svc.cluster.local:%d", normalizedAccess.Scheme, serviceName, namespace, port.Port), nil
		}
	}
	return "", fmt.Errorf("access.primaryPort %q does not exist in template.ports", normalizedAccess.PrimaryPort)
}

// podFailureMessage extracts the most useful startup failure from a pod status.
func podFailureMessage(pod corev1.Pod) (string, bool) {
	for _, status := range pod.Status.InitContainerStatuses {
		if message, ok := containerFailureMessage(status); ok {
			return fmt.Sprintf("Pod %s init container %s: %s", pod.Name, status.Name, message), true
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if message, ok := containerFailureMessage(status); ok {
			return fmt.Sprintf("Pod %s container %s: %s", pod.Name, status.Name, message), true
		}
	}
	if pod.Status.Phase == corev1.PodFailed {
		if message := firstNonEmpty(pod.Status.Message, pod.Status.Reason); message != "" {
			return fmt.Sprintf("Pod %s: %s", pod.Name, message), true
		}
	}
	return "", false
}

// containerFailureMessage extracts a meaningful failure from one container status entry.
func containerFailureMessage(status corev1.ContainerStatus) (string, bool) {
	if waiting := status.State.Waiting; waiting != nil {
		if isIgnorableWaitingReason(waiting.Reason) {
			return "", false
		}
		if waiting.Reason == "CrashLoopBackOff" {
			if message := terminatedFailureMessage(status.LastTerminationState.Terminated); message != "" {
				return message, true
			}
		}
		if message := firstNonEmpty(waiting.Message, waiting.Reason); message != "" {
			return message, true
		}
	}

	if message := terminatedFailureMessage(status.State.Terminated); message != "" {
		return message, true
	}
	return "", false
}

// terminatedFailureMessage normalizes terminated container state into one readable message.
func terminatedFailureMessage(terminated *corev1.ContainerStateTerminated) string {
	if terminated == nil {
		return ""
	}
	if terminated.ExitCode == 0 && terminated.Signal == 0 {
		return ""
	}
	if message := firstNonEmpty(terminated.Message); message != "" {
		return message
	}
	if reason := firstNonEmpty(terminated.Reason); reason != "" {
		if terminated.ExitCode != 0 {
			return fmt.Sprintf("%s (exit code %d)", reason, terminated.ExitCode)
		}
		return reason
	}
	if terminated.ExitCode != 0 {
		return fmt.Sprintf("container exited with code %d", terminated.ExitCode)
	}
	return fmt.Sprintf("container terminated by signal %d", terminated.Signal)
}

// isIgnorableWaitingReason filters transient startup states that should not mark failure.
func isIgnorableWaitingReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "", "ContainerCreating", "PodInitializing":
		return true
	default:
		return false
	}
}

// firstNonEmpty returns the first trimmed non-empty value from the candidates.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// buildGPUUnitStatus derives the status snapshot written back after each reconcile.
func buildGPUUnitStatus(instance runtimev1alpha1.GPUUnit, available int32, serviceName, accessURL, failureMessage string) runtimev1alpha1.GPUUnitStatus {
	next := runtimev1alpha1.GPUUnitStatus{
		ReadyReplicas:      available,
		ObservedGeneration: instance.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
		ServiceName:        serviceName,
		AccessURL:          accessURL,
	}

	condition := metav1.Condition{
		Type:               runtimev1alpha1.ConditionReady,
		ObservedGeneration: instance.Generation,
	}

	if lifecycleForUnit(instance) == runtimev1alpha1.LifecycleStock {
		next.ServiceName = ""
		next.AccessURL = ""
		switch {
		case available >= 1:
			next.Phase = runtimev1alpha1.PhaseReady
			condition.Status = metav1.ConditionTrue
			condition.Reason = runtimev1alpha1.ReasonStockReady
			condition.Message = runtimev1alpha1.StatusMessageStockReady
		case failureMessage != "":
			next.Phase = runtimev1alpha1.PhaseFailed
			condition.Status = metav1.ConditionFalse
			condition.Reason = runtimev1alpha1.ReasonPodStartupFailed
			condition.Message = failureMessage
		default:
			next.Phase = runtimev1alpha1.PhaseProgressing
			condition.Status = metav1.ConditionFalse
			condition.Reason = runtimev1alpha1.ReasonStockNotReady
			condition.Message = runtimev1alpha1.StatusMessageStockWait
		}
	} else {
		switch {
		case available >= 1:
			next.Phase = runtimev1alpha1.PhaseReady
			condition.Status = metav1.ConditionTrue
			condition.Reason = runtimev1alpha1.ReasonUnitReady
			condition.Message = runtimev1alpha1.StatusMessageUnitReady
		case failureMessage != "":
			next.Phase = runtimev1alpha1.PhaseFailed
			condition.Status = metav1.ConditionFalse
			condition.Reason = runtimev1alpha1.ReasonPodStartupFailed
			condition.Message = failureMessage
		default:
			next.Phase = runtimev1alpha1.PhaseProgressing
			condition.Status = metav1.ConditionFalse
			condition.Reason = runtimev1alpha1.ReasonUnitProgressing
			condition.Message = runtimev1alpha1.StatusMessageUnitWait
		}
	}

	apimeta.SetStatusCondition(&next.Conditions, condition)
	return next
}

// lifecycleForUnit derives whether the controller should treat this unit as stock or active.
func lifecycleForUnit(instance runtimev1alpha1.GPUUnit) string {
	if isStockUnit(instance) {
		return runtimev1alpha1.LifecycleStock
	}
	return runtimev1alpha1.LifecycleInstance
}

// unitObjectLabels returns the shared label set applied to owned objects.
func unitObjectLabels(instance runtimev1alpha1.GPUUnit) map[string]string {
	return map[string]string{
		runtimev1alpha1.LabelAppNameKey:   runtimev1alpha1.LabelAppNameValue,
		runtimev1alpha1.LabelManagedByKey: runtimev1alpha1.LabelManagedByValue,
		runtimev1alpha1.LabelUnitKey:      instance.Name,
	}
}

// unitPodLabels returns the labels applied to pod templates.
func unitPodLabels(instance runtimev1alpha1.GPUUnit) map[string]string {
	return unitObjectLabels(instance)
}

// deploymentNameForUnit returns the managed Deployment name for a unit.
func deploymentNameForUnit(instanceName string) string {
	return prefixedRuntimeName(runtimev1alpha1.GPUUnitNamePrefix, instanceName)
}

// serviceNameForUnit returns the managed Service name for a unit.
func serviceNameForUnit(instanceName string) string {
	return prefixedRuntimeName(runtimev1alpha1.GPUUnitNamePrefix, instanceName)
}

// prefixedRuntimeName builds a DNS-safe object name under the Kubernetes length limit.
func prefixedRuntimeName(prefix, name string) string {
	out := prefix + name
	if len(out) <= 63 {
		return out
	}
	return strings.TrimRight(out[:63], "-")
}

// isStockUnit reports whether the controller should treat the unit as stock inventory.
func isStockUnit(instance runtimev1alpha1.GPUUnit) bool {
	return instance.Namespace == runtimev1alpha1.DefaultStockNamespace
}
