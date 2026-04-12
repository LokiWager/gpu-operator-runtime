/*
Copyright 2026.
*/

package controller

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

// GPUUnitReconciler reconciles a GPUUnit object.
type GPUUnitReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const gpuUnitControllerName = "gpuunit"

var errStatusOnly = errors.New(statusOnlyMessage)
var errUnitSSHSpecIncomplete = errors.New("ssh access is enabled but spec.ssh is incomplete")

// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=gpuunits,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=gpuunits/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=gpuunits/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile moves the observed cluster state toward GPUUnit spec.
func (r *GPUUnitReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var instance runtimev1alpha1.GPUUnit
	if err := r.Get(ctx, req.NamespacedName, &instance); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	storageMounts, storageReady, storageWaitMessage, err := r.resolveGPUUnitStorageMounts(ctx, &instance)
	if err != nil {
		if updateErr := r.markUnitFailed(ctx, &instance, "", "", runtimev1alpha1.ReasonStorageMountInvalid, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, nil
	}

	if _, err := desiredUnitPodTemplate(instance, storageMounts); err != nil {
		reason := runtimev1alpha1.ReasonInvalidSpec
		if errors.Is(err, errUnitSSHSpecIncomplete) {
			reason = runtimev1alpha1.ReasonSSHConfigInvalid
		}
		if updateErr := r.markUnitFailed(ctx, &instance, "", "", reason, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, nil
	}

	lifecycle := lifecycleForUnit(instance)
	serviceName := ""
	accessURL := ""
	serviceChanged := false

	if lifecycle == runtimev1alpha1.LifecycleInstance {
		access, err := normalizeControllerGPUUnitAccess(instance.Spec.Access, instance.Spec.Template.Ports)
		if err != nil {
			if updateErr := r.markUnitFailed(ctx, &instance, "", "", runtimev1alpha1.ReasonAccessConfigInvalid, err.Error()); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{}, nil
		}
		serviceName, accessURL, serviceChanged, err = r.reconcileGPUUnitService(ctx, &instance, access)
		if err != nil {
			if updateErr := r.markUnitFailed(ctx, &instance, serviceName, accessURL, runtimev1alpha1.ReasonUnitServiceSyncFailed, err.Error()); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{}, err
		}
	} else {
		changed, err := r.ensureGPUUnitServiceAbsent(ctx, &instance)
		if err != nil {
			if updateErr := r.markUnitFailed(ctx, &instance, "", "", runtimev1alpha1.ReasonUnitServiceSyncFailed, err.Error()); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{}, err
		}
		serviceChanged = changed
	}

	sshConfigChanged, err := r.reconcileGPUUnitSSHAuthorizedKeysConfig(ctx, &instance)
	if err != nil {
		if updateErr := r.markUnitFailed(ctx, &instance, serviceName, accessURL, runtimev1alpha1.ReasonUnitDeploymentSyncFailed, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	dep, deploymentChanged, err := r.reconcileGPUUnitDeployment(ctx, &instance, storageMounts)
	if err != nil {
		if errors.Is(err, errStatusOnly) {
			return ctrl.Result{}, nil
		}
		if updateErr := r.markUnitFailed(ctx, &instance, serviceName, accessURL, runtimev1alpha1.ReasonUnitDeploymentSyncFailed, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	podFailureMessage, sshFailureMessage, err := r.inspectGPUUnitPods(ctx, &instance)
	if err != nil {
		if updateErr := r.markUnitFailed(ctx, &instance, serviceName, accessURL, runtimev1alpha1.ReasonPodStatusSyncFailed, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	sshProgress := buildUnitSSHProgress(instance, dep.Status.AvailableReplicas, sshFailureMessage)
	next := buildGPUUnitStatus(
		instance,
		dep.Status.AvailableReplicas,
		serviceName,
		accessURL,
		podFailureMessage,
		storageReady,
		storageWaitMessage,
		sshProgress,
	)
	if err := r.updateGPUUnitStatus(ctx, &instance, next); err != nil {
		return ctrl.Result{}, err
	}

	if serviceChanged || sshConfigChanged || deploymentChanged || !storageReady {
		return ctrl.Result{RequeueAfter: requeueAfterUpdate}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GPUUnitReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.GPUUnit{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Named(gpuUnitControllerName).
		Complete(r)
}

// reconcileGPUUnitDeployment creates or updates the Deployment owned by a unit.
func (r *GPUUnitReconciler) reconcileGPUUnitDeployment(ctx context.Context, instance *runtimev1alpha1.GPUUnit, storageMounts []resolvedGPUUnitStorageMount) (*appsv1.Deployment, bool, error) {
	depName := deploymentNameForUnit(instance.Name)

	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: depName}, &dep); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}

		newDep, err := desiredUnitDeployment(*instance, storageMounts)
		if err != nil {
			reason := runtimev1alpha1.ReasonInvalidSpec
			if errors.Is(err, errUnitSSHSpecIncomplete) {
				reason = runtimev1alpha1.ReasonSSHConfigInvalid
			}
			if markErr := r.markUnitFailed(ctx, instance, instance.Status.ServiceName, instance.Status.AccessURL, reason, err.Error()); markErr != nil {
				return nil, false, markErr
			}
			return nil, false, errStatusOnly
		}
		if err := controllerutil.SetControllerReference(instance, newDep, r.Scheme); err != nil {
			return nil, false, err
		}
		if err := r.Create(ctx, newDep); err != nil {
			return nil, false, err
		}
		return newDep, true, nil
	}

	expectedTemplate, err := desiredUnitPodTemplate(*instance, storageMounts)
	if err != nil {
		reason := runtimev1alpha1.ReasonInvalidSpec
		if errors.Is(err, errUnitSSHSpecIncomplete) {
			reason = runtimev1alpha1.ReasonSSHConfigInvalid
		}
		if markErr := r.markUnitFailed(ctx, instance, instance.Status.ServiceName, instance.Status.AccessURL, reason, err.Error()); markErr != nil {
			return nil, false, markErr
		}
		return nil, false, errStatusOnly
	}

	needsUpdate := dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1
	if needsUpdate {
		dep.Spec.Replicas = ptr.To(int32(1))
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

// reconcileGPUUnitService creates or updates the Service published for an active unit.
func (r *GPUUnitReconciler) reconcileGPUUnitService(ctx context.Context, instance *runtimev1alpha1.GPUUnit, access runtimev1alpha1.GPUUnitAccess) (string, string, bool, error) {
	serviceName := serviceNameForUnit(instance.Name)
	ports := desiredServicePorts(instance.Spec.Template.Ports)
	if len(ports) == 0 {
		changed, err := r.ensureGPUUnitServiceAbsent(ctx, instance)
		return "", "", changed, err
	}

	accessURL, err := buildUnitAccessURL(instance.Namespace, serviceName, access, instance.Spec.Template.Ports)
	if err != nil {
		return "", "", false, err
	}

	var svc corev1.Service
	if err := r.Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: serviceName}, &svc); err != nil {
		if !apierrors.IsNotFound(err) {
			return "", "", false, err
		}

		newSvc := desiredGPUUnitService(*instance, ports)
		if err := controllerutil.SetControllerReference(instance, newSvc, r.Scheme); err != nil {
			return "", "", false, err
		}
		if err := r.Create(ctx, newSvc); err != nil {
			return "", "", false, err
		}
		return serviceName, accessURL, true, nil
	}

	expectedSelector := map[string]string{runtimev1alpha1.LabelUnitKey: instance.Name}
	needsUpdate := !reflect.DeepEqual(svc.Spec.Ports, ports) || !reflect.DeepEqual(svc.Spec.Selector, expectedSelector)
	if needsUpdate {
		svc.Spec.Ports = ports
		svc.Spec.Selector = expectedSelector
		if err := r.Update(ctx, &svc); err != nil {
			return "", "", false, err
		}
	}
	return serviceName, accessURL, needsUpdate, nil
}

// ensureGPUUnitServiceAbsent removes any stale Service when the unit should stay private.
func (r *GPUUnitReconciler) ensureGPUUnitServiceAbsent(ctx context.Context, instance *runtimev1alpha1.GPUUnit) (bool, error) {
	serviceName := serviceNameForUnit(instance.Name)
	var svc corev1.Service
	err := r.Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: serviceName}, &svc)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := r.Delete(ctx, &svc); err != nil {
		return false, err
	}
	return true, nil
}

// markUnitFailed writes a failed status snapshot and preserves exposure details when relevant.
func (r *GPUUnitReconciler) markUnitFailed(ctx context.Context, instance *runtimev1alpha1.GPUUnit, serviceName, accessURL, reason, message string) error {
	next := runtimev1alpha1.GPUUnitStatus{
		Phase:              runtimev1alpha1.PhaseFailed,
		ObservedGeneration: instance.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
		ServiceName:        serviceName,
		AccessURL:          accessURL,
	}
	if lifecycleForUnit(*instance) == runtimev1alpha1.LifecycleStock {
		next.ServiceName = ""
		next.AccessURL = ""
	} else if instance.Spec.SSH.Enabled {
		sshProgress := buildUnitSSHProgress(*instance, 0, "")
		sshProgress.Phase = runtimev1alpha1.UnitSSHPhaseFailed
		sshProgress.Reason = firstNonEmpty(reason, runtimev1alpha1.ReasonUnitSSHFailed)
		sshProgress.Message = firstNonEmpty(message, sshProgress.AccessCommand, runtimev1alpha1.StatusMessageUnitSSHPending)
		next.SSH = gpuUnitSSHStatusFromProgress(sshProgress)
		apimeta.SetStatusCondition(&next.Conditions, buildUnitSSHCondition(instance.Generation, sshProgress))
	}
	apimeta.SetStatusCondition(&next.Conditions, statusConditionFromDecision(runtimev1alpha1.ConditionReady, instance.Generation, conditionDecision{
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}))
	return r.updateGPUUnitStatus(ctx, instance, next)
}

// updateGPUUnitStatus performs a status write only when the effective snapshot changed.
func (r *GPUUnitReconciler) updateGPUUnitStatus(ctx context.Context, instance *runtimev1alpha1.GPUUnit, next runtimev1alpha1.GPUUnitStatus) error {
	if gpuUnitStatusEqual(instance.Status, next) {
		return nil
	}
	instance.Status = next
	return r.Status().Update(ctx, instance)
}

// gpuUnitStatusEqual compares status snapshots while ignoring volatile timestamps.
func gpuUnitStatusEqual(a, b runtimev1alpha1.GPUUnitStatus) bool {
	aa := normalizeGPUUnitStatusForCompare(a)
	bb := normalizeGPUUnitStatusForCompare(b)
	return reflect.DeepEqual(aa, bb)
}

// normalizeGPUUnitStatusForCompare strips timestamp noise before status equality checks.
func normalizeGPUUnitStatusForCompare(status runtimev1alpha1.GPUUnitStatus) runtimev1alpha1.GPUUnitStatus {
	status.LastSyncTime = metav1.Time{}
	for i := range status.Conditions {
		status.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	return status
}

// inspectGPUUnitPods scans owned pods for the first meaningful workload and SSH failures.
func (r *GPUUnitReconciler) inspectGPUUnitPods(ctx context.Context, instance *runtimev1alpha1.GPUUnit) (string, string, error) {
	var pods corev1.PodList
	if err := r.List(
		ctx,
		&pods,
		client.InNamespace(instance.Namespace),
		client.MatchingLabels{runtimev1alpha1.LabelUnitKey: instance.Name},
	); err != nil {
		return "", "", err
	}
	for i := range pods.Items {
		if message, ok := podFailureMessage(pods.Items[i]); ok {
			sshMessage, _ := namedContainerFailureMessage(
				pods.Items[i],
				runtimev1alpha1.UnitSSHContainerName,
				runtimev1alpha1.UnitSSHFRPContainerName,
			)
			return message, sshMessage, nil
		}
		if sshMessage, ok := namedContainerFailureMessage(
			pods.Items[i],
			runtimev1alpha1.UnitSSHContainerName,
			runtimev1alpha1.UnitSSHFRPContainerName,
		); ok {
			return "", sshMessage, nil
		}
	}
	return "", "", nil
}

// resolveGPUUnitStorageMounts loads referenced storage objects and reports whether they are ready.
func (r *GPUUnitReconciler) resolveGPUUnitStorageMounts(ctx context.Context, instance *runtimev1alpha1.GPUUnit) ([]resolvedGPUUnitStorageMount, bool, string, error) {
	if isStockUnit(*instance) {
		if len(instance.Spec.StorageMounts) > 0 {
			return nil, false, "", errors.New("stock units cannot declare storageMounts")
		}
		return nil, true, "", nil
	}

	if len(instance.Spec.StorageMounts) == 0 {
		return nil, true, "", nil
	}

	attachments := make([]resolvedGPUUnitStorageMount, 0, len(instance.Spec.StorageMounts))
	pending := make([]string, 0)
	allReady := true

	for _, mount := range instance.Spec.StorageMounts {
		var storage runtimev1alpha1.GPUStorage
		if err := r.Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: mount.Name}, &storage); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, false, "", errors.New("referenced gpu storage " + instance.Namespace + "/" + mount.Name + " does not exist")
			}
			return nil, false, "", err
		}

		ready := storage.Status.Phase == runtimev1alpha1.StoragePhaseReady
		if !ready {
			allReady = false
			pending = append(pending, storage.Name)
		}

		attachments = append(attachments, resolvedGPUUnitStorageMount{
			Mount:     mount,
			ClaimName: storage.Name,
			Ready:     ready,
		})
	}

	if allReady {
		return attachments, true, "", nil
	}

	return attachments, false, "Waiting for attached storage to become ready: " + strings.Join(pending, ", "), nil
}
