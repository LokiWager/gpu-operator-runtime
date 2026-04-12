package controller

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

type storageReadyStatusInput struct {
	ClaimBound      bool
	Prepare         storagePrepareProgress
	Accessor        storageAccessorProgress
	AccessorEnabled bool
}

type storageReadyStatusDecision struct {
	Phase string
	Ready conditionDecision
}

var storagePrepareConditionByPhase = map[string]conditionDecision{
	runtimev1alpha1.StoragePreparePhaseNotRequested: {
		Status:  metav1.ConditionTrue,
		Reason:  runtimev1alpha1.ReasonStoragePrepareReady,
		Message: runtimev1alpha1.StatusMessageStoragePrepared,
	},
	runtimev1alpha1.StoragePreparePhasePending: {
		Status:  metav1.ConditionFalse,
		Reason:  runtimev1alpha1.ReasonStoragePreparePending,
		Message: runtimev1alpha1.StatusMessageStoragePreparePending,
	},
	runtimev1alpha1.StoragePreparePhaseRunning: {
		Status:  metav1.ConditionFalse,
		Reason:  runtimev1alpha1.ReasonStoragePrepareRunning,
		Message: runtimev1alpha1.StatusMessageStoragePrepareRunning,
	},
	runtimev1alpha1.StoragePreparePhaseSucceeded: {
		Status:  metav1.ConditionTrue,
		Reason:  runtimev1alpha1.ReasonStoragePrepareReady,
		Message: runtimev1alpha1.StatusMessageStoragePrepared,
	},
	runtimev1alpha1.StoragePreparePhaseFailed: {
		Status:  metav1.ConditionFalse,
		Reason:  runtimev1alpha1.ReasonStoragePrepareFailed,
		Message: runtimev1alpha1.StatusMessageStoragePrepareFailed,
	},
}

var storageAccessorConditionByPhase = map[string]conditionDecision{
	runtimev1alpha1.StorageAccessorPhaseDisabled: {
		Status:  metav1.ConditionTrue,
		Reason:  runtimev1alpha1.ReasonStorageAccessorReady,
		Message: runtimev1alpha1.StatusMessageStorageAccessorDisabled,
	},
	runtimev1alpha1.StorageAccessorPhasePending: {
		Status:  metav1.ConditionFalse,
		Reason:  runtimev1alpha1.ReasonStorageAccessorPending,
		Message: runtimev1alpha1.StatusMessageStorageAccessorPending,
	},
	runtimev1alpha1.StorageAccessorPhaseReady: {
		Status:  metav1.ConditionTrue,
		Reason:  runtimev1alpha1.ReasonStorageAccessorReady,
		Message: runtimev1alpha1.StatusMessageStorageAccessorReady,
	},
	runtimev1alpha1.StorageAccessorPhaseFailed: {
		Status: metav1.ConditionFalse,
		Reason: runtimev1alpha1.ReasonStorageAccessorFailed,
	},
}

var storageReadyStatusRules = []statusRule[storageReadyStatusInput, storageReadyStatusDecision]{
	{
		Match: func(input storageReadyStatusInput) bool {
			return !input.ClaimBound
		},
		Build: func(input storageReadyStatusInput) storageReadyStatusDecision {
			return storageReadyStatusDecision{
				Phase: runtimev1alpha1.StoragePhasePending,
				Ready: conditionDecision{
					Status:  metav1.ConditionFalse,
					Reason:  runtimev1alpha1.ReasonStoragePending,
					Message: runtimev1alpha1.StatusMessageStoragePending,
				},
			}
		},
	},
	{
		Match: func(input storageReadyStatusInput) bool {
			return input.Prepare.Phase == runtimev1alpha1.StoragePreparePhaseFailed
		},
		Build: func(input storageReadyStatusInput) storageReadyStatusDecision {
			return storageReadyStatusDecision{
				Phase: runtimev1alpha1.StoragePhaseFailed,
				Ready: conditionDecision{
					Status:  metav1.ConditionFalse,
					Reason:  firstNonEmpty(input.Prepare.Reason, runtimev1alpha1.ReasonStoragePrepareFailed),
					Message: firstNonEmpty(input.Prepare.Message, runtimev1alpha1.StatusMessageStoragePrepareFailed),
				},
			}
		},
	},
	{
		Match: func(input storageReadyStatusInput) bool {
			return input.AccessorEnabled && input.Accessor.Phase == runtimev1alpha1.StorageAccessorPhaseFailed
		},
		Build: func(input storageReadyStatusInput) storageReadyStatusDecision {
			return storageReadyStatusDecision{
				Phase: runtimev1alpha1.StoragePhaseFailed,
				Ready: conditionDecision{
					Status:  metav1.ConditionFalse,
					Reason:  firstNonEmpty(input.Accessor.Reason, runtimev1alpha1.ReasonStorageAccessorFailed),
					Message: input.Accessor.Message,
				},
			}
		},
	},
	{
		Match: func(input storageReadyStatusInput) bool {
			return !input.Prepare.Ready
		},
		Build: func(input storageReadyStatusInput) storageReadyStatusDecision {
			return storageReadyStatusDecision{
				Phase: runtimev1alpha1.StoragePhasePending,
				Ready: conditionDecision{
					Status:  metav1.ConditionFalse,
					Reason:  input.Prepare.Reason,
					Message: input.Prepare.Message,
				},
			}
		},
	},
	{
		Match: func(input storageReadyStatusInput) bool {
			return input.AccessorEnabled && !input.Accessor.Ready
		},
		Build: func(input storageReadyStatusInput) storageReadyStatusDecision {
			return storageReadyStatusDecision{
				Phase: runtimev1alpha1.StoragePhasePending,
				Ready: conditionDecision{
					Status:  metav1.ConditionFalse,
					Reason:  input.Accessor.Reason,
					Message: input.Accessor.Message,
				},
			}
		},
	},
}

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

	readyDecision := resolveStatusRule(storageReadyStatusInput{
		ClaimBound:      isGPUStorageClaimBound(pvc),
		Prepare:         prepare,
		Accessor:        accessor,
		AccessorEnabled: storage.Spec.Accessor.Enabled,
	}, storageReadyStatusRules, defaultStorageReadyStatusDecision)

	next.Phase = readyDecision.Phase
	apimeta.SetStatusCondition(&next.Conditions, statusConditionFromDecision(runtimev1alpha1.ConditionReady, storage.Generation, readyDecision.Ready))
	apimeta.SetStatusCondition(&next.Conditions, buildStoragePrepareCondition(storage.Generation, prepare))
	apimeta.SetStatusCondition(&next.Conditions, buildStorageAccessorCondition(storage.Generation, accessor))
	return next
}

func defaultStorageReadyStatusDecision(storageReadyStatusInput) storageReadyStatusDecision {
	return storageReadyStatusDecision{
		Phase: runtimev1alpha1.StoragePhaseReady,
		Ready: conditionDecision{
			Status:  metav1.ConditionTrue,
			Reason:  runtimev1alpha1.ReasonStorageReady,
			Message: runtimev1alpha1.StatusMessageStorageReady,
		},
	}
}

func buildStoragePrepareCondition(generation int64, progress storagePrepareProgress) metav1.Condition {
	decision, ok := storagePrepareConditionByPhase[progress.Phase]
	if !ok {
		decision = storagePrepareConditionByPhase[runtimev1alpha1.StoragePreparePhasePending]
	}
	decision.Reason = firstNonEmpty(progress.Reason, decision.Reason)
	decision.Message = firstNonEmpty(progress.Message, decision.Message)
	return statusConditionFromDecision(runtimev1alpha1.ConditionPrepared, generation, decision)
}

func buildStorageAccessorCondition(generation int64, progress storageAccessorProgress) metav1.Condition {
	decision, ok := storageAccessorConditionByPhase[progress.Phase]
	if !ok {
		decision = storageAccessorConditionByPhase[runtimev1alpha1.StorageAccessorPhasePending]
	}
	decision.Reason = firstNonEmpty(progress.Reason, decision.Reason)
	decision.Message = firstNonEmpty(progress.Message, decision.Message)
	return statusConditionFromDecision(runtimev1alpha1.ConditionAccessorReady, generation, decision)
}
