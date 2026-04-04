/*
Copyright 2026.
*/

package controller

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
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
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile moves the observed PVC, prepare job, and accessor state toward the desired GPUStorage spec.
func (r *GPUStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var storage runtimev1alpha1.GPUStorage
	if err := r.Get(ctx, req.NamespacedName, &storage); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	pvc, pvcChanged, err := r.reconcileGPUStoragePVC(ctx, &storage)
	if err != nil {
		if updateErr := r.markStorageFailed(ctx, &storage, runtimev1alpha1.ReasonStoragePVCSyncFailed, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	prepareProgress, err := r.reconcileGPUStoragePrepare(ctx, &storage, pvc)
	if err != nil {
		if updateErr := r.markStorageFailed(ctx, &storage, runtimev1alpha1.ReasonStorageInvalidSpec, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, nil
	}

	accessorProgress, err := r.reconcileGPUStorageAccessor(ctx, &storage, pvc, prepareProgress)
	if err != nil {
		if updateErr := r.markStorageFailed(ctx, &storage, runtimev1alpha1.ReasonStorageAccessorFailed, err.Error()); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	next := buildGPUStorageStatus(storage, pvc, prepareProgress, accessorProgress)
	if err := r.updateGPUStorageStatus(ctx, &storage, next); err != nil {
		return ctrl.Result{}, err
	}

	if pvcChanged || prepareProgress.Changed || accessorProgress.Changed || next.Phase != runtimev1alpha1.StoragePhaseReady {
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
		Owns(&batchv1.Job{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named(gpuStorageControllerName).
		Complete(r)
}

// reconcileGPUStoragePVC creates or updates the PVC owned by one storage object.
func (r *GPUStorageReconciler) reconcileGPUStoragePVC(ctx context.Context, storage *runtimev1alpha1.GPUStorage) (*corev1.PersistentVolumeClaim, bool, error) {
	desiredPVC, err := desiredGPUStoragePVC(*storage)
	if err != nil {
		return nil, false, err
	}

	var pvc corev1.PersistentVolumeClaim
	key := types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}
	if err := r.Get(ctx, key, &pvc); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}

		if err := controllerutil.SetControllerReference(storage, desiredPVC, r.Scheme); err != nil {
			return nil, false, err
		}
		if err := r.Create(ctx, desiredPVC); err != nil {
			return nil, false, err
		}
		return desiredPVC, true, nil
	}

	if syncGPUStoragePVC(&pvc, desiredPVC) {
		if err := r.Update(ctx, &pvc); err != nil {
			return nil, false, err
		}
		return &pvc, true, nil
	}

	return &pvc, false, nil
}

func (r *GPUStorageReconciler) reconcileGPUStoragePrepare(
	ctx context.Context,
	storage *runtimev1alpha1.GPUStorage,
	pvc *corev1.PersistentVolumeClaim,
) (storagePrepareProgress, error) {
	if err := validateGPUStoragePrepareSpec(storage.Name, storage.Spec.Prepare); err != nil {
		return storagePrepareProgress{}, err
	}

	if isZeroControllerGPUStoragePrepare(storage.Spec.Prepare) {
		return storagePrepareNotRequestedProgress(), nil
	}

	if !isGPUStorageClaimBound(pvc) {
		return storagePreparePVCWaitingProgress(), nil
	}

	progress, err := newStoragePrepareJobProgress(*storage)
	if err != nil {
		return storagePrepareProgress{}, err
	}

	sourceClaimName, waitProgress, waiting, err := r.resolveGPUStoragePrepareSource(ctx, storage, progress)
	if err != nil {
		return storagePrepareProgress{}, err
	}
	if waiting {
		return waitProgress, nil
	}

	return r.reconcileGPUStoragePrepareJobState(ctx, storage, pvc, sourceClaimName, progress)
}

func (r *GPUStorageReconciler) reconcileGPUStorageAccessor(
	ctx context.Context,
	storage *runtimev1alpha1.GPUStorage,
	pvc *corev1.PersistentVolumeClaim,
	prepare storagePrepareProgress,
) (storageAccessorProgress, error) {
	if !storage.Spec.Accessor.Enabled {
		return r.reconcileAbsentGPUStorageAccessor(ctx, storage, storageAccessorDisabledProgress)
	}

	if !isGPUStorageClaimBound(pvc) || !prepare.Ready {
		return r.reconcileAbsentGPUStorageAccessor(ctx, storage, storageAccessorPendingProgress)
	}

	service := desiredGPUStorageAccessorService(*storage)
	serviceChanged, err := r.reconcileGPUStorageAccessorService(ctx, storage, service)
	if err != nil {
		return storageAccessorProgress{}, err
	}

	deployment := desiredGPUStorageAccessorDeployment(*storage, firstNonEmpty(pvc.Name, storage.Name))
	currentDeployment, deploymentChanged, err := r.reconcileGPUStorageAccessorDeployment(ctx, storage, deployment)
	if err != nil {
		return storageAccessorProgress{}, err
	}

	return storageAccessorProgressFromDeployment(
		storage.Namespace,
		service.Name,
		currentDeployment.Status.AvailableReplicas,
		serviceChanged || deploymentChanged,
	), nil
}

func (r *GPUStorageReconciler) resolveGPUStoragePrepareSource(
	ctx context.Context,
	storage *runtimev1alpha1.GPUStorage,
	progress storagePrepareProgress,
) (string, storagePrepareProgress, bool, error) {
	if storage.Spec.Prepare.FromStorageName == "" {
		return "", progress, false, nil
	}

	var source runtimev1alpha1.GPUStorage
	err := r.Get(ctx, types.NamespacedName{Namespace: storage.Namespace, Name: storage.Spec.Prepare.FromStorageName}, &source)
	if apierrors.IsNotFound(err) {
		progress.Message = fmt.Sprintf("Waiting for source storage %s to exist.", storage.Spec.Prepare.FromStorageName)
		return "", progress, true, nil
	}
	if err != nil {
		return "", storagePrepareProgress{}, false, err
	}
	if source.Status.Phase != runtimev1alpha1.StoragePhaseReady {
		progress.Message = fmt.Sprintf("Waiting for source storage %s to become ready.", source.Name)
		return "", progress, true, nil
	}

	return firstNonEmpty(source.Status.ClaimName, source.Name), progress, false, nil
}

func (r *GPUStorageReconciler) reconcileGPUStoragePrepareJobState(
	ctx context.Context,
	storage *runtimev1alpha1.GPUStorage,
	pvc *corev1.PersistentVolumeClaim,
	sourceClaimName string,
	progress storagePrepareProgress,
) (storagePrepareProgress, error) {
	var job batchv1.Job
	err := r.Get(ctx, types.NamespacedName{Namespace: storage.Namespace, Name: progress.JobName}, &job)
	if apierrors.IsNotFound(err) {
		if storage.Status.Prepare.ObservedDigest == progress.Digest &&
			storage.Status.Prepare.Phase == runtimev1alpha1.StoragePreparePhaseSucceeded {
			return storagePrepareSucceededProgress(storage, progress), nil
		}

		newJob := desiredGPUStoragePrepareJob(*storage, progress.JobName, firstNonEmpty(pvc.Name, storage.Name), sourceClaimName)
		if err := controllerutil.SetControllerReference(storage, newJob, r.Scheme); err != nil {
			return storagePrepareProgress{}, err
		}
		if err := r.Create(ctx, newJob); err != nil {
			return storagePrepareProgress{}, err
		}
		progress.Changed = true
		progress.RecoveryPhase = recoveryPhaseForPrepare(storage, progress.Phase)
		return progress, nil
	}
	if err != nil {
		return storagePrepareProgress{}, err
	}

	return storagePrepareProgressFromJob(storage, progress, job), nil
}

func (r *GPUStorageReconciler) reconcileAbsentGPUStorageAccessor(
	ctx context.Context,
	storage *runtimev1alpha1.GPUStorage,
	build func(bool) storageAccessorProgress,
) (storageAccessorProgress, error) {
	changed, err := r.ensureGPUStorageAccessorAbsent(ctx, storage)
	if err != nil {
		return storageAccessorProgress{}, err
	}
	return build(changed), nil
}

func (r *GPUStorageReconciler) reconcileGPUStorageAccessorService(
	ctx context.Context,
	storage *runtimev1alpha1.GPUStorage,
	desired *corev1.Service,
) (bool, error) {
	var service corev1.Service
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.Get(ctx, key, &service); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
		if err := controllerutil.SetControllerReference(storage, desired, r.Scheme); err != nil {
			return false, err
		}
		if err := r.Create(ctx, desired); err != nil {
			return false, err
		}
		return true, nil
	}

	if !syncGPUStorageAccessorService(&service, desired) {
		return false, nil
	}
	if err := r.Update(ctx, &service); err != nil {
		return false, err
	}
	return true, nil
}

func (r *GPUStorageReconciler) reconcileGPUStorageAccessorDeployment(
	ctx context.Context,
	storage *runtimev1alpha1.GPUStorage,
	desired *appsv1.Deployment,
) (*appsv1.Deployment, bool, error) {
	var deployment appsv1.Deployment
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.Get(ctx, key, &deployment); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}
		if err := controllerutil.SetControllerReference(storage, desired, r.Scheme); err != nil {
			return nil, false, err
		}
		if err := r.Create(ctx, desired); err != nil {
			return nil, false, err
		}
		return desired, true, nil
	}

	if !syncGPUStorageAccessorDeployment(&deployment, desired) {
		return &deployment, false, nil
	}
	if err := r.Update(ctx, &deployment); err != nil {
		return nil, false, err
	}
	return &deployment, true, nil
}

func (r *GPUStorageReconciler) ensureGPUStorageAccessorAbsent(ctx context.Context, storage *runtimev1alpha1.GPUStorage) (bool, error) {
	changed := false

	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: storage.Namespace, Name: storageAccessorDeploymentName(storage.Name)}, &dep); err == nil {
		if err := r.Delete(ctx, &dep); err != nil {
			return changed, err
		}
		changed = true
	} else if !apierrors.IsNotFound(err) {
		return changed, err
	}

	var svc corev1.Service
	if err := r.Get(ctx, types.NamespacedName{Namespace: storage.Namespace, Name: storageAccessorServiceName(storage.Name)}, &svc); err == nil {
		if err := r.Delete(ctx, &svc); err != nil {
			return changed, err
		}
		changed = true
	} else if !apierrors.IsNotFound(err) {
		return changed, err
	}

	return changed, nil
}

func validateGPUStoragePrepareSpec(storageName string, prepare runtimev1alpha1.GPUStoragePrepareSpec) error {
	fromImage := stringsTrim(prepare.FromImage)
	fromStorageName := stringsLowerTrim(prepare.FromStorageName)
	commandCount := countNonEmpty(prepare.Command)
	argsCount := countNonEmpty(prepare.Args)

	if fromImage != "" && fromStorageName != "" {
		return fmt.Errorf("prepare.fromImage and prepare.fromStorageName are mutually exclusive")
	}
	if fromStorageName != "" {
		if errs := validation.IsDNS1123Subdomain(fromStorageName); len(errs) > 0 {
			return fmt.Errorf("prepare.fromStorageName %q is invalid: %s", fromStorageName, stringsJoin(errs, ", "))
		}
		if fromStorageName == storageName {
			return fmt.Errorf("prepare.fromStorageName cannot point to the same storage object")
		}
		if commandCount > 0 || argsCount > 0 {
			return fmt.Errorf("prepare.command and prepare.args are not supported with prepare.fromStorageName")
		}
	}
	if fromImage == "" && (commandCount > 0 || argsCount > 0) {
		return fmt.Errorf("prepare.command and prepare.args require prepare.fromImage")
	}
	if fromImage != "" && commandCount == 0 && argsCount == 0 {
		return fmt.Errorf("prepare.command or prepare.args is required when prepare.fromImage is set")
	}
	return nil
}

func isZeroControllerGPUStoragePrepare(prepare runtimev1alpha1.GPUStoragePrepareSpec) bool {
	return stringsTrim(prepare.FromImage) == "" &&
		stringsLowerTrim(prepare.FromStorageName) == "" &&
		countNonEmpty(prepare.Command) == 0 &&
		countNonEmpty(prepare.Args) == 0
}

func recoveryPhaseForPrepare(storage *runtimev1alpha1.GPUStorage, phase string) string {
	if isZeroControllerGPUStoragePrepare(storage.Spec.Prepare) {
		return runtimev1alpha1.StorageRecoveryPhaseNone
	}
	requested := stringsTrim(storage.GetAnnotations()[runtimev1alpha1.AnnotationStorageRecoveryNonce]) != ""
	switch phase {
	case runtimev1alpha1.StoragePreparePhaseFailed:
		return runtimev1alpha1.StorageRecoveryPhaseRequired
	case runtimev1alpha1.StoragePreparePhasePending, runtimev1alpha1.StoragePreparePhaseRunning:
		if requested {
			return runtimev1alpha1.StorageRecoveryPhaseRunning
		}
		return runtimev1alpha1.StorageRecoveryPhaseNone
	case runtimev1alpha1.StoragePreparePhaseSucceeded:
		if requested {
			return runtimev1alpha1.StorageRecoveryPhaseSucceeded
		}
		return runtimev1alpha1.StorageRecoveryPhaseNone
	default:
		return runtimev1alpha1.StorageRecoveryPhaseNone
	}
}

// markStorageFailed writes a failed status snapshot for one storage object.
func (r *GPUStorageReconciler) markStorageFailed(ctx context.Context, storage *runtimev1alpha1.GPUStorage, reason, message string) error {
	next := runtimev1alpha1.GPUStorageStatus{
		ClaimName:          storage.Name,
		Phase:              runtimev1alpha1.StoragePhaseFailed,
		ObservedGeneration: storage.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
		Prepare: runtimev1alpha1.GPUStoragePrepareStatus{
			Phase:          storage.Status.Prepare.Phase,
			JobName:        storage.Status.Prepare.JobName,
			ObservedDigest: storage.Status.Prepare.ObservedDigest,
			RecoveryPhase:  storage.Status.Prepare.RecoveryPhase,
		},
		Accessor: storage.Status.Accessor,
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
			Labels:    storageOwnedLabels(storage.Name),
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

// buildGPUStorageStatus derives the storage status snapshot from PVC, prepare, and accessor state.
func buildGPUStorageStatus(
	storage runtimev1alpha1.GPUStorage,
	pvc *corev1.PersistentVolumeClaim,
	prepare storagePrepareProgress,
	accessor storageAccessorProgress,
) runtimev1alpha1.GPUStorageStatus {
	next := runtimev1alpha1.GPUStorageStatus{
		ClaimName:          firstNonEmpty(storage.Name),
		ObservedGeneration: storage.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
		Prepare: runtimev1alpha1.GPUStoragePrepareStatus{
			Phase:          prepare.Phase,
			JobName:        prepare.JobName,
			ObservedDigest: prepare.Digest,
			RecoveryPhase:  prepare.RecoveryPhase,
		},
		Accessor: runtimev1alpha1.GPUStorageAccessorStatus{
			Phase:       accessor.Phase,
			ServiceName: accessor.ServiceName,
			AccessURL:   accessor.AccessURL,
		},
	}
	if pvc != nil {
		next.ClaimName = firstNonEmpty(pvc.Name, storage.Name)
		if capacity, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
			next.Capacity = capacity.String()
		}
	}

	readyCondition := metav1.Condition{
		Type:               runtimev1alpha1.ConditionReady,
		ObservedGeneration: storage.Generation,
	}
	preparedCondition := metav1.Condition{
		Type:               runtimev1alpha1.ConditionPrepared,
		ObservedGeneration: storage.Generation,
	}
	accessorCondition := metav1.Condition{
		Type:               runtimev1alpha1.ConditionAccessorReady,
		ObservedGeneration: storage.Generation,
	}

	switch prepare.Phase {
	case runtimev1alpha1.StoragePreparePhaseSucceeded, runtimev1alpha1.StoragePreparePhaseNotRequested:
		preparedCondition.Status = metav1.ConditionTrue
		preparedCondition.Reason = runtimev1alpha1.ReasonStoragePrepareReady
		preparedCondition.Message = runtimev1alpha1.StatusMessageStoragePrepared
	case runtimev1alpha1.StoragePreparePhaseFailed:
		preparedCondition.Status = metav1.ConditionFalse
		preparedCondition.Reason = runtimev1alpha1.ReasonStoragePrepareFailed
		preparedCondition.Message = firstNonEmpty(prepare.Message, runtimev1alpha1.StatusMessageStoragePrepareFailed)
	case runtimev1alpha1.StoragePreparePhaseRunning:
		preparedCondition.Status = metav1.ConditionFalse
		preparedCondition.Reason = runtimev1alpha1.ReasonStoragePrepareRunning
		preparedCondition.Message = runtimev1alpha1.StatusMessageStoragePrepareRunning
	default:
		preparedCondition.Status = metav1.ConditionFalse
		preparedCondition.Reason = runtimev1alpha1.ReasonStoragePreparePending
		preparedCondition.Message = firstNonEmpty(prepare.Message, runtimev1alpha1.StatusMessageStoragePreparePending)
	}

	switch accessor.Phase {
	case runtimev1alpha1.StorageAccessorPhaseReady, runtimev1alpha1.StorageAccessorPhaseDisabled:
		accessorCondition.Status = metav1.ConditionTrue
		accessorCondition.Reason = runtimev1alpha1.ReasonStorageAccessorReady
		accessorCondition.Message = firstNonEmpty(accessor.Message, runtimev1alpha1.StatusMessageStorageAccessorReady)
	case runtimev1alpha1.StorageAccessorPhaseFailed:
		accessorCondition.Status = metav1.ConditionFalse
		accessorCondition.Reason = runtimev1alpha1.ReasonStorageAccessorFailed
		accessorCondition.Message = accessor.Message
	default:
		accessorCondition.Status = metav1.ConditionFalse
		accessorCondition.Reason = runtimev1alpha1.ReasonStorageAccessorPending
		accessorCondition.Message = firstNonEmpty(accessor.Message, runtimev1alpha1.StatusMessageStorageAccessorPending)
	}

	switch {
	case pvc == nil || pvc.Status.Phase != corev1.ClaimBound:
		next.Phase = runtimev1alpha1.StoragePhasePending
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = runtimev1alpha1.ReasonStoragePending
		readyCondition.Message = runtimev1alpha1.StatusMessageStoragePending
	case prepare.Phase == runtimev1alpha1.StoragePreparePhaseFailed:
		next.Phase = runtimev1alpha1.StoragePhaseFailed
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = runtimev1alpha1.ReasonStoragePrepareFailed
		readyCondition.Message = firstNonEmpty(prepare.Message, runtimev1alpha1.StatusMessageStoragePrepareFailed)
	case storage.Spec.Accessor.Enabled && accessor.Phase == runtimev1alpha1.StorageAccessorPhaseFailed:
		next.Phase = runtimev1alpha1.StoragePhaseFailed
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = runtimev1alpha1.ReasonStorageAccessorFailed
		readyCondition.Message = accessor.Message
	case !prepare.Ready:
		next.Phase = runtimev1alpha1.StoragePhasePending
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = prepare.Reason
		readyCondition.Message = prepare.Message
	case storage.Spec.Accessor.Enabled && !accessor.Ready:
		next.Phase = runtimev1alpha1.StoragePhasePending
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = accessor.Reason
		readyCondition.Message = accessor.Message
	default:
		next.Phase = runtimev1alpha1.StoragePhaseReady
		readyCondition.Status = metav1.ConditionTrue
		readyCondition.Reason = runtimev1alpha1.ReasonStorageReady
		readyCondition.Message = runtimev1alpha1.StatusMessageStorageReady
	}

	apimeta.SetStatusCondition(&next.Conditions, readyCondition)
	apimeta.SetStatusCondition(&next.Conditions, preparedCondition)
	apimeta.SetStatusCondition(&next.Conditions, accessorCondition)
	return next
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

func stringsLowerTrim(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func countNonEmpty(values []string) int {
	total := 0
	for _, value := range values {
		if stringsTrim(value) != "" {
			total++
		}
	}
	return total
}

func stringsJoin(values []string, sep string) string {
	return strings.Join(values, sep)
}
