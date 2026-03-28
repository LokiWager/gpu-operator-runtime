/*
Copyright 2026.
*/

package controller

import (
	"context"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

// GPUStorageReconciler reconciles a GPUStorage object.
type GPUStorageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const gpuStorageControllerName = "gpustorage"

// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=gpustorages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=gpustorages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=gpustorages/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete

// Reconcile moves the observed PVC state toward the desired GPUStorage spec.
func (r *GPUStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var storage runtimev1alpha1.GPUStorage
	if err := r.Get(ctx, req.NamespacedName, &storage); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	pvc, changed, err := r.reconcileGPUStoragePVC(ctx, &storage)
	if err != nil {
		if updateErr := r.markStorageFailed(ctx, &storage, runtimev1alpha1.ReasonStoragePVCSyncFailed, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	next, err := buildGPUStorageStatus(storage, pvc)
	if err != nil {
		if updateErr := r.markStorageFailed(ctx, &storage, runtimev1alpha1.ReasonStorageInvalidSpec, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, nil
	}

	if err := r.updateGPUStorageStatus(ctx, &storage, next); err != nil {
		return ctrl.Result{}, err
	}

	if changed || next.Phase != runtimev1alpha1.StoragePhaseReady {
		return ctrl.Result{RequeueAfter: requeueAfterUpdate}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GPUStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.GPUStorage{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Named(gpuStorageControllerName).
		Complete(r)
}

// reconcileGPUStoragePVC creates or updates the PVC owned by one storage object.
func (r *GPUStorageReconciler) reconcileGPUStoragePVC(ctx context.Context, storage *runtimev1alpha1.GPUStorage) (*corev1.PersistentVolumeClaim, bool, error) {
	var pvc corev1.PersistentVolumeClaim
	key := types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}
	if err := r.Get(ctx, key, &pvc); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}

		newPVC, err := desiredGPUStoragePVC(*storage)
		if err != nil {
			return nil, false, err
		}
		if err := controllerutil.SetControllerReference(storage, newPVC, r.Scheme); err != nil {
			return nil, false, err
		}
		if err := r.Create(ctx, newPVC); err != nil {
			return nil, false, err
		}
		return newPVC, true, nil
	}

	desiredQty, err := resource.ParseQuantity(storage.Spec.Size)
	if err != nil {
		return nil, false, err
	}

	needsUpdate := false
	if pvc.Labels == nil {
		pvc.Labels = map[string]string{}
	}
	if pvc.Labels[runtimev1alpha1.LabelStorageKey] != storage.Name {
		pvc.Labels[runtimev1alpha1.LabelStorageKey] = storage.Name
		needsUpdate = true
	}
	if pvc.Labels[runtimev1alpha1.LabelAppNameKey] != runtimev1alpha1.LabelAppNameValue {
		pvc.Labels[runtimev1alpha1.LabelAppNameKey] = runtimev1alpha1.LabelAppNameValue
		needsUpdate = true
	}
	if pvc.Labels[runtimev1alpha1.LabelManagedByKey] != runtimev1alpha1.LabelManagedByValue {
		pvc.Labels[runtimev1alpha1.LabelManagedByKey] = runtimev1alpha1.LabelManagedByValue
		needsUpdate = true
	}

	currentQty := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if currentQty.Cmp(desiredQty) != 0 {
		if pvc.Spec.Resources.Requests == nil {
			pvc.Spec.Resources.Requests = corev1.ResourceList{}
		}
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = desiredQty
		needsUpdate = true
	}

	desiredStorageClassName := resolvedGPUStorageClassName(storage.Spec.StorageClassName)
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != desiredStorageClassName {
		pvc.Spec.StorageClassName = &desiredStorageClassName
		needsUpdate = true
	}

	if needsUpdate {
		if err := r.Update(ctx, &pvc); err != nil {
			return nil, false, err
		}
	}

	return &pvc, needsUpdate, nil
}

// markStorageFailed writes a failed status snapshot for one storage object.
func (r *GPUStorageReconciler) markStorageFailed(ctx context.Context, storage *runtimev1alpha1.GPUStorage, reason, message string) error {
	next := runtimev1alpha1.GPUStorageStatus{
		ClaimName:          storage.Name,
		Phase:              runtimev1alpha1.StoragePhaseFailed,
		ObservedGeneration: storage.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
	}
	apimeta.SetStatusCondition(&next.Conditions, metav1.Condition{
		Type:               runtimev1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: storage.Generation,
		Reason:             reason,
		Message:            message,
	})
	return r.updateGPUStorageStatus(ctx, storage, next)
}

// updateGPUStorageStatus performs a status write only when the effective snapshot changed.
func (r *GPUStorageReconciler) updateGPUStorageStatus(ctx context.Context, storage *runtimev1alpha1.GPUStorage, next runtimev1alpha1.GPUStorageStatus) error {
	if gpuStorageStatusEqual(storage.Status, next) {
		return nil
	}
	storage.Status = next
	return r.Status().Update(ctx, storage)
}

func gpuStorageStatusEqual(a, b runtimev1alpha1.GPUStorageStatus) bool {
	aa := normalizeGPUStorageStatusForCompare(a)
	bb := normalizeGPUStorageStatusForCompare(b)
	return reflect.DeepEqual(aa, bb)
}

func normalizeGPUStorageStatusForCompare(status runtimev1alpha1.GPUStorageStatus) runtimev1alpha1.GPUStorageStatus {
	status.LastSyncTime = metav1.Time{}
	for i := range status.Conditions {
		status.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	return status
}

// desiredGPUStoragePVC builds the PVC owned by one GPUStorage object.
func desiredGPUStoragePVC(storage runtimev1alpha1.GPUStorage) (*corev1.PersistentVolumeClaim, error) {
	qty, err := resource.ParseQuantity(storage.Spec.Size)
	if err != nil {
		return nil, err
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      storage.Name,
			Namespace: storage.Namespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelAppNameKey:   runtimev1alpha1.LabelAppNameValue,
				runtimev1alpha1.LabelManagedByKey: runtimev1alpha1.LabelManagedByValue,
				runtimev1alpha1.LabelStorageKey:   storage.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: qty,
				},
			},
		},
	}
	storageClassName := resolvedGPUStorageClassName(storage.Spec.StorageClassName)
	pvc.Spec.StorageClassName = &storageClassName
	return pvc, nil
}

// resolvedGPUStorageClassName keeps direct manifest usage aligned with the API default.
func resolvedGPUStorageClassName(raw string) string {
	if raw == "" {
		return runtimev1alpha1.DefaultGPUStorageClassName
	}
	return raw
}

// buildGPUStorageStatus derives the storage status snapshot from the observed PVC state.
func buildGPUStorageStatus(storage runtimev1alpha1.GPUStorage, pvc *corev1.PersistentVolumeClaim) (runtimev1alpha1.GPUStorageStatus, error) {
	if pvc == nil {
		return runtimev1alpha1.GPUStorageStatus{}, nil
	}

	next := runtimev1alpha1.GPUStorageStatus{
		ClaimName:          pvc.Name,
		ObservedGeneration: storage.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
	}
	if capacity, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
		next.Capacity = capacity.String()
	}

	condition := metav1.Condition{
		Type:               runtimev1alpha1.ConditionReady,
		ObservedGeneration: storage.Generation,
	}

	switch pvc.Status.Phase {
	case corev1.ClaimBound:
		next.Phase = runtimev1alpha1.StoragePhaseReady
		condition.Status = metav1.ConditionTrue
		condition.Reason = runtimev1alpha1.ReasonStorageReady
		condition.Message = runtimev1alpha1.StatusMessageStorageReady
	case corev1.ClaimLost:
		next.Phase = runtimev1alpha1.StoragePhaseFailed
		condition.Status = metav1.ConditionFalse
		condition.Reason = runtimev1alpha1.ReasonStorageClaimLost
		condition.Message = string(pvc.Status.Phase)
	default:
		next.Phase = runtimev1alpha1.StoragePhasePending
		condition.Status = metav1.ConditionFalse
		condition.Reason = runtimev1alpha1.ReasonStoragePending
		condition.Message = runtimev1alpha1.StatusMessageStoragePending
	}

	apimeta.SetStatusCondition(&next.Conditions, condition)
	return next, nil
}
