/*
Copyright 2026.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

// StockPoolReconciler reconciles a StockPool object.
type StockPoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	stockPoolControllerName = "stockpool"
	statusOnlyMessage       = "status already updated"
	parseMemoryErrorFormat  = "parse memory %q: %w"
	requeueAfterUpdate      = 2 * time.Second
)

var errStatusOnly = errors.New(statusOnlyMessage)

// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=stockpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=stockpools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=stockpools/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile moves the observed cluster state toward StockPool spec.
func (r *StockPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pool runtimev1alpha1.StockPool
	if err := r.Get(ctx, req.NamespacedName, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	desired := pool.Spec.Replicas
	if desired < 0 {
		desired = 0
	}

	if _, err := desiredPodTemplate(pool); err != nil {
		if updateErr := r.markPoolFailed(ctx, &pool, desired, "", runtimev1alpha1.ReasonInvalidSpec, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, nil
	}

	serviceName, serviceChanged, err := r.reconcileService(ctx, &pool)
	if err != nil {
		if updateErr := r.markPoolFailed(ctx, &pool, desired, serviceName, runtimev1alpha1.ReasonServiceSyncFailed, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	dep, deploymentChanged, err := r.reconcileDeployment(ctx, &pool, desired)
	if err != nil {
		if errors.Is(err, errStatusOnly) {
			return ctrl.Result{}, nil
		}
		reason := runtimev1alpha1.ReasonDeploymentSyncFailed
		if err := r.markPoolFailed(ctx, &pool, desired, serviceName, reason, err.Error()); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	podFailureMessage, err := r.findPodFailureMessage(ctx, &pool)
	if err != nil {
		if updateErr := r.markPoolFailed(ctx, &pool, desired, serviceName, runtimev1alpha1.ReasonPodStatusSyncFailed, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	next := buildPoolStatus(pool, desired, dep.Status.AvailableReplicas, serviceName, podFailureMessage)
	if err := r.updatePoolStatus(ctx, &pool, next); err != nil {
		return ctrl.Result{}, err
	}

	if serviceChanged || deploymentChanged {
		return ctrl.Result{RequeueAfter: requeueAfterUpdate}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StockPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.StockPool{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named(stockPoolControllerName).
		Complete(r)
}

func (r *StockPoolReconciler) reconcileDeployment(ctx context.Context, pool *runtimev1alpha1.StockPool, replicas int32) (*appsv1.Deployment, bool, error) {
	depName := deploymentNameForPool(pool.Name)

	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: pool.Namespace, Name: depName}, &dep); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}

		newDep, err := desiredDeployment(*pool, replicas)
		if err != nil {
			if markErr := r.markPoolFailed(ctx, pool, replicas, pool.Status.ServiceName, runtimev1alpha1.ReasonInvalidSpec, err.Error()); markErr != nil {
				return nil, false, markErr
			}
			return nil, false, errStatusOnly
		}
		if err := controllerutil.SetControllerReference(pool, newDep, r.Scheme); err != nil {
			return nil, false, err
		}
		if err := r.Create(ctx, newDep); err != nil {
			return nil, false, err
		}
		return newDep, true, nil
	}

	expectedTemplate, err := desiredPodTemplate(*pool)
	if err != nil {
		if markErr := r.markPoolFailed(ctx, pool, replicas, pool.Status.ServiceName, runtimev1alpha1.ReasonInvalidSpec, err.Error()); markErr != nil {
			return nil, false, markErr
		}
		return nil, false, errStatusOnly
	}

	needsUpdate := dep.Spec.Replicas == nil || *dep.Spec.Replicas != replicas
	if needsUpdate {
		dep.Spec.Replicas = ptr.To(replicas)
	}
	if !reflect.DeepEqual(dep.Spec.Template.Spec, expectedTemplate.Spec) ||
		!reflect.DeepEqual(dep.Spec.Template.Labels, expectedTemplate.Labels) {
		dep.Spec.Template = expectedTemplate
		needsUpdate = true
	}

	if needsUpdate {
		if err := r.Update(ctx, &dep); err != nil {
			return nil, false, err
		}
	}

	return &dep, needsUpdate, nil
}

func (r *StockPoolReconciler) reconcileService(ctx context.Context, pool *runtimev1alpha1.StockPool) (string, bool, error) {
	serviceName := serviceNameForPool(pool.Name)
	ports := desiredServicePorts(pool.Spec.Template.Ports)
	if len(ports) == 0 {
		var svc corev1.Service
		err := r.Get(ctx, types.NamespacedName{Namespace: pool.Namespace, Name: serviceName}, &svc)
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if err := r.Delete(ctx, &svc); err != nil {
			return "", false, err
		}
		return "", true, nil
	}

	var svc corev1.Service
	if err := r.Get(ctx, types.NamespacedName{Namespace: pool.Namespace, Name: serviceName}, &svc); err != nil {
		if !apierrors.IsNotFound(err) {
			return "", false, err
		}

		newSvc := desiredService(*pool, ports)
		if err := controllerutil.SetControllerReference(pool, newSvc, r.Scheme); err != nil {
			return "", false, err
		}
		if err := r.Create(ctx, newSvc); err != nil {
			return "", false, err
		}
		return serviceName, true, nil
	}

	expectedPorts := desiredServicePorts(pool.Spec.Template.Ports)
	needsUpdate := !reflect.DeepEqual(svc.Spec.Ports, expectedPorts) ||
		!reflect.DeepEqual(svc.Spec.Selector, map[string]string{runtimev1alpha1.LabelPoolKey: pool.Name})
	if needsUpdate {
		svc.Spec.Selector = map[string]string{runtimev1alpha1.LabelPoolKey: pool.Name}
		svc.Spec.Ports = expectedPorts
		if err := r.Update(ctx, &svc); err != nil {
			return "", false, err
		}
	}

	return serviceName, needsUpdate, nil
}

func (r *StockPoolReconciler) markPoolFailed(ctx context.Context, pool *runtimev1alpha1.StockPool, desired int32, serviceName, reason, message string) error {
	next := runtimev1alpha1.StockPoolStatus{
		Allocated:          desired,
		Phase:              runtimev1alpha1.PhaseFailed,
		ObservedGeneration: pool.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
		ServiceName:        serviceName,
	}
	apimeta.SetStatusCondition(&next.Conditions, metav1.Condition{
		Type:               runtimev1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: pool.Generation,
		Reason:             reason,
		Message:            message,
	})
	return r.updatePoolStatus(ctx, pool, next)
}

func (r *StockPoolReconciler) updatePoolStatus(ctx context.Context, pool *runtimev1alpha1.StockPool, next runtimev1alpha1.StockPoolStatus) error {
	if statusEqual(pool.Status, next) {
		return nil
	}

	pool.Status = next
	return r.Status().Update(ctx, pool)
}

func statusEqual(a, b runtimev1alpha1.StockPoolStatus) bool {
	aa := normalizeStatusForCompare(a)
	bb := normalizeStatusForCompare(b)
	return reflect.DeepEqual(aa, bb)
}

func normalizeStatusForCompare(status runtimev1alpha1.StockPoolStatus) runtimev1alpha1.StockPoolStatus {
	status.LastSyncTime = metav1.Time{}
	for i := range status.Conditions {
		status.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	return status
}

func (r *StockPoolReconciler) findPodFailureMessage(ctx context.Context, pool *runtimev1alpha1.StockPool) (string, error) {
	var pods corev1.PodList
	if err := r.List(
		ctx,
		&pods,
		client.InNamespace(pool.Namespace),
		client.MatchingLabels{runtimev1alpha1.LabelPoolKey: pool.Name},
	); err != nil {
		return "", err
	}

	for i := range pods.Items {
		if message, ok := podFailureMessage(pods.Items[i]); ok {
			return message, nil
		}
	}

	return "", nil
}

func buildPoolStatus(pool runtimev1alpha1.StockPool, desired, available int32, serviceName, podFailureMessage string) runtimev1alpha1.StockPoolStatus {
	next := runtimev1alpha1.StockPoolStatus{
		Available:          available,
		Allocated:          maxInt32(desired-available, 0),
		ObservedGeneration: pool.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
		ServiceName:        serviceName,
	}

	condition := metav1.Condition{
		Type:               runtimev1alpha1.ConditionReady,
		ObservedGeneration: pool.Generation,
	}
	switch {
	case desired == 0:
		next.Phase = runtimev1alpha1.PhaseEmpty
		condition.Status = metav1.ConditionFalse
		condition.Reason = runtimev1alpha1.ReasonScaledToZero
		condition.Message = runtimev1alpha1.StatusMessageScaledToZero
	case available >= desired:
		next.Phase = runtimev1alpha1.PhaseReady
		condition.Status = metav1.ConditionTrue
		condition.Reason = runtimev1alpha1.ReasonDeploymentReady
		condition.Message = runtimev1alpha1.StatusMessageDeploymentReady
	case podFailureMessage != "":
		next.Phase = runtimev1alpha1.PhaseFailed
		condition.Status = metav1.ConditionFalse
		condition.Reason = runtimev1alpha1.ReasonPodStartupFailed
		condition.Message = podFailureMessage
	default:
		next.Phase = runtimev1alpha1.PhaseProgressing
		condition.Status = metav1.ConditionFalse
		condition.Reason = runtimev1alpha1.ReasonDeploymentProgressing
		condition.Message = runtimev1alpha1.StatusMessageDeploymentProgressing
	}
	apimeta.SetStatusCondition(&next.Conditions, condition)
	return next
}

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

func isIgnorableWaitingReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "", "ContainerCreating", "PodInitializing":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func desiredDeployment(pool runtimev1alpha1.StockPool, replicas int32) (*appsv1.Deployment, error) {
	name := deploymentNameForPool(pool.Name)
	labels := map[string]string{
		runtimev1alpha1.LabelAppNameKey:   runtimev1alpha1.LabelAppNameValue,
		runtimev1alpha1.LabelManagedByKey: runtimev1alpha1.LabelManagedByValue,
		runtimev1alpha1.LabelPoolKey:      pool.Name,
	}
	template, err := desiredPodTemplate(pool)
	if err != nil {
		return nil, err
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pool.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{runtimev1alpha1.LabelPoolKey: pool.Name}},
			Template: template,
		},
	}, nil
}

func desiredPodTemplate(pool runtimev1alpha1.StockPool) (corev1.PodTemplateSpec, error) {
	labels := map[string]string{runtimev1alpha1.LabelPoolKey: pool.Name}
	image := pool.Spec.Image
	if image == "" {
		image = runtimev1alpha1.DefaultRuntimeImage
	}

	resources := corev1.ResourceRequirements{}
	if pool.Spec.Memory != "" {
		qty, err := resource.ParseQuantity(pool.Spec.Memory)
		if err != nil {
			return corev1.PodTemplateSpec{}, fmt.Errorf(parseMemoryErrorFormat, pool.Spec.Memory, err)
		}
		resources.Requests = corev1.ResourceList{corev1.ResourceMemory: qty}
		resources.Limits = corev1.ResourceList{corev1.ResourceMemory: qty}
	}
	if pool.Spec.GPU > 0 {
		if resources.Requests == nil {
			resources.Requests = corev1.ResourceList{}
		}
		if resources.Limits == nil {
			resources.Limits = corev1.ResourceList{}
		}
		gpuQty := *resource.NewQuantity(int64(pool.Spec.GPU), resource.DecimalSI)
		resources.Requests[corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName)] = gpuQty
		resources.Limits[corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName)] = gpuQty
	}

	container := corev1.Container{
		Name:            runtimev1alpha1.RuntimeWorkerContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             defaultRuntimeEnv(pool),
		Ports:           desiredContainerPorts(pool.Spec.Template.Ports),
		Resources:       resources,
	}
	if len(pool.Spec.Template.Command) > 0 {
		container.Command = append([]string(nil), pool.Spec.Template.Command...)
	}
	if len(pool.Spec.Template.Args) > 0 {
		container.Args = append([]string(nil), pool.Spec.Template.Args...)
	}
	if len(pool.Spec.Template.Command) == 0 && len(pool.Spec.Template.Args) == 0 {
		container.Command = []string{
			runtimev1alpha1.RuntimeCommandShell,
			runtimev1alpha1.RuntimeCommandShellFlag,
			runtimev1alpha1.RuntimeCommandSleep,
		}
	}

	for _, env := range pool.Spec.Template.Envs {
		container.Env = append(container.Env, corev1.EnvVar{Name: env.Name, Value: env.Value})
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{container}},
	}, nil
}

func defaultRuntimeEnv(pool runtimev1alpha1.StockPool) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: runtimev1alpha1.EnvSpecName, Value: pool.Spec.SpecName},
		{Name: runtimev1alpha1.EnvPoolName, Value: pool.Name},
		{Name: runtimev1alpha1.EnvGPUCount, Value: fmt.Sprintf("%d", pool.Spec.GPU)},
		{Name: runtimev1alpha1.EnvMemoryLimit, Value: pool.Spec.Memory},
	}
}

func desiredContainerPorts(ports []runtimev1alpha1.StockPoolPortSpec) []corev1.ContainerPort {
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

func desiredService(pool runtimev1alpha1.StockPool, ports []corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceNameForPool(pool.Name),
			Namespace: pool.Namespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelAppNameKey:   runtimev1alpha1.LabelAppNameValue,
				runtimev1alpha1.LabelManagedByKey: runtimev1alpha1.LabelManagedByValue,
				runtimev1alpha1.LabelPoolKey:      pool.Name,
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{runtimev1alpha1.LabelPoolKey: pool.Name},
			Ports:    ports,
		},
	}
}

func desiredServicePorts(ports []runtimev1alpha1.StockPoolPortSpec) []corev1.ServicePort {
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

func deploymentNameForPool(poolName string) string {
	return runtimev1alpha1.StockPoolResourceNamePrefix + poolName
}

func serviceNameForPool(poolName string) string {
	return runtimev1alpha1.StockPoolResourceNamePrefix + poolName
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
