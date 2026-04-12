package controller

import (
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

type unitSSHProgress struct {
	Phase         string
	Username      string
	TargetHost    string
	ConnectHost   string
	ConnectPort   int32
	AccessCommand string
	Reason        string
	Message       string
	Ready         bool
}

type unitReadyStatusInput struct {
	Available          int32
	FailureMessage     string
	StorageReady       bool
	StorageWaitMessage string
}

type unitReadyStatusDecision struct {
	Phase string
	Ready conditionDecision
}

var stockUnitStatusRules = []statusRule[unitReadyStatusInput, unitReadyStatusDecision]{
	{
		Match: func(input unitReadyStatusInput) bool {
			return input.Available >= 1
		},
		Build: func(input unitReadyStatusInput) unitReadyStatusDecision {
			return unitReadyStatusDecision{
				Phase: runtimev1alpha1.PhaseReady,
				Ready: conditionDecision{
					Status:  metav1.ConditionTrue,
					Reason:  runtimev1alpha1.ReasonStockReady,
					Message: runtimev1alpha1.StatusMessageStockReady,
				},
			}
		},
	},
	{
		Match: func(input unitReadyStatusInput) bool {
			return input.FailureMessage != ""
		},
		Build: func(input unitReadyStatusInput) unitReadyStatusDecision {
			return unitReadyStatusDecision{
				Phase: runtimev1alpha1.PhaseFailed,
				Ready: conditionDecision{
					Status:  metav1.ConditionFalse,
					Reason:  runtimev1alpha1.ReasonPodStartupFailed,
					Message: input.FailureMessage,
				},
			}
		},
	},
}

var instanceUnitStatusRules = []statusRule[unitReadyStatusInput, unitReadyStatusDecision]{
	{
		Match: func(input unitReadyStatusInput) bool {
			return input.Available >= 1
		},
		Build: func(input unitReadyStatusInput) unitReadyStatusDecision {
			return unitReadyStatusDecision{
				Phase: runtimev1alpha1.PhaseReady,
				Ready: conditionDecision{
					Status:  metav1.ConditionTrue,
					Reason:  runtimev1alpha1.ReasonUnitReady,
					Message: runtimev1alpha1.StatusMessageUnitReady,
				},
			}
		},
	},
	{
		Match: func(input unitReadyStatusInput) bool {
			return input.FailureMessage != ""
		},
		Build: func(input unitReadyStatusInput) unitReadyStatusDecision {
			return unitReadyStatusDecision{
				Phase: runtimev1alpha1.PhaseFailed,
				Ready: conditionDecision{
					Status:  metav1.ConditionFalse,
					Reason:  runtimev1alpha1.ReasonPodStartupFailed,
					Message: input.FailureMessage,
				},
			}
		},
	},
	{
		Match: func(input unitReadyStatusInput) bool {
			return !input.StorageReady
		},
		Build: func(input unitReadyStatusInput) unitReadyStatusDecision {
			return unitReadyStatusDecision{
				Phase: runtimev1alpha1.PhaseProgressing,
				Ready: conditionDecision{
					Status:  metav1.ConditionFalse,
					Reason:  runtimev1alpha1.ReasonStorageNotReady,
					Message: firstNonEmpty(input.StorageWaitMessage, runtimev1alpha1.StatusMessageUnitStorage),
				},
			}
		},
	},
}

var unitSSHConditionByPhase = map[string]conditionDecision{
	runtimev1alpha1.UnitSSHPhaseDisabled: {
		Status:  metav1.ConditionTrue,
		Reason:  runtimev1alpha1.ReasonUnitSSHReady,
		Message: runtimev1alpha1.StatusMessageUnitSSHDisabled,
	},
	runtimev1alpha1.UnitSSHPhasePending: {
		Status:  metav1.ConditionFalse,
		Reason:  runtimev1alpha1.ReasonUnitSSHPending,
		Message: runtimev1alpha1.StatusMessageUnitSSHPending,
	},
	runtimev1alpha1.UnitSSHPhaseReady: {
		Status:  metav1.ConditionTrue,
		Reason:  runtimev1alpha1.ReasonUnitSSHReady,
		Message: runtimev1alpha1.StatusMessageUnitSSHReady,
	},
	runtimev1alpha1.UnitSSHPhaseFailed: {
		Status: metav1.ConditionFalse,
		Reason: runtimev1alpha1.ReasonUnitSSHFailed,
	},
}

func buildUnitSSHProgress(
	instance runtimev1alpha1.GPUUnit,
	available int32,
	sshFailure string,
) unitSSHProgress {
	if !instance.Spec.SSH.Enabled {
		return unitSSHProgress{
			Phase:   runtimev1alpha1.UnitSSHPhaseDisabled,
			Reason:  runtimev1alpha1.ReasonUnitSSHReady,
			Message: runtimev1alpha1.StatusMessageUnitSSHDisabled,
			Ready:   true,
		}
	}

	sshSpec, err := resolveUnitSSHSpec(instance)
	if err != nil {
		return unitSSHProgress{
			Phase:   runtimev1alpha1.UnitSSHPhaseFailed,
			Reason:  runtimev1alpha1.ReasonSSHConfigInvalid,
			Message: err.Error(),
		}
	}

	progress := unitSSHProgress{
		Phase:         runtimev1alpha1.UnitSSHPhasePending,
		Username:      sshSpec.Username,
		TargetHost:    sshTargetHostForUnit(instance, sshSpec),
		ConnectHost:   sshSpec.ConnectHost,
		ConnectPort:   sshSpec.ConnectPort,
		AccessCommand: buildUnitSSHAccessCommand(instance, sshSpec),
		Reason:        runtimev1alpha1.ReasonUnitSSHPending,
		Message:       runtimev1alpha1.StatusMessageUnitSSHPending,
	}
	if sshFailure != "" {
		progress.Phase = runtimev1alpha1.UnitSSHPhaseFailed
		progress.Reason = runtimev1alpha1.ReasonUnitSSHFailed
		progress.Message = sshFailure
		return progress
	}
	if available >= 1 {
		progress.Phase = runtimev1alpha1.UnitSSHPhaseReady
		progress.Reason = runtimev1alpha1.ReasonUnitSSHReady
		progress.Message = runtimev1alpha1.StatusMessageUnitSSHReady
		progress.Ready = true
	}
	return progress
}

func buildGPUUnitStatus(
	instance runtimev1alpha1.GPUUnit,
	available int32,
	serviceName string,
	accessURL string,
	failureMessage string,
	storageReady bool,
	storageWaitMessage string,
	sshProgress unitSSHProgress,
) runtimev1alpha1.GPUUnitStatus {
	next := runtimev1alpha1.GPUUnitStatus{
		ReadyReplicas:      available,
		ObservedGeneration: instance.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
		ServiceName:        serviceName,
		AccessURL:          accessURL,
		SSH:                gpuUnitSSHStatusFromProgress(sshProgress),
	}

	input := unitReadyStatusInput{
		Available:          available,
		FailureMessage:     failureMessage,
		StorageReady:       storageReady,
		StorageWaitMessage: storageWaitMessage,
	}
	decision := resolveUnitReadyStatus(instance, input)
	apimeta.SetStatusCondition(&next.Conditions, statusConditionFromDecision(runtimev1alpha1.ConditionReady, instance.Generation, decision.Ready))
	apimeta.SetStatusCondition(&next.Conditions, buildUnitSSHCondition(instance.Generation, sshProgress))
	next.Phase = decision.Phase

	if lifecycleForUnit(instance) == runtimev1alpha1.LifecycleStock {
		next.ServiceName = ""
		next.AccessURL = ""
		next.SSH = runtimev1alpha1.GPUUnitSSHStatus{}
	}
	return next
}

func resolveUnitReadyStatus(instance runtimev1alpha1.GPUUnit, input unitReadyStatusInput) unitReadyStatusDecision {
	if lifecycleForUnit(instance) == runtimev1alpha1.LifecycleStock {
		return resolveStatusRule(input, stockUnitStatusRules, defaultStockUnitStatusDecision)
	}
	return resolveStatusRule(input, instanceUnitStatusRules, defaultInstanceUnitStatusDecision)
}

func defaultStockUnitStatusDecision(unitReadyStatusInput) unitReadyStatusDecision {
	return unitReadyStatusDecision{
		Phase: runtimev1alpha1.PhaseProgressing,
		Ready: conditionDecision{
			Status:  metav1.ConditionFalse,
			Reason:  runtimev1alpha1.ReasonStockNotReady,
			Message: runtimev1alpha1.StatusMessageStockWait,
		},
	}
}

func defaultInstanceUnitStatusDecision(unitReadyStatusInput) unitReadyStatusDecision {
	return unitReadyStatusDecision{
		Phase: runtimev1alpha1.PhaseProgressing,
		Ready: conditionDecision{
			Status:  metav1.ConditionFalse,
			Reason:  runtimev1alpha1.ReasonUnitProgressing,
			Message: runtimev1alpha1.StatusMessageUnitWait,
		},
	}
}

func gpuUnitSSHStatusFromProgress(progress unitSSHProgress) runtimev1alpha1.GPUUnitSSHStatus {
	return runtimev1alpha1.GPUUnitSSHStatus{
		Phase:         progress.Phase,
		Username:      progress.Username,
		TargetHost:    progress.TargetHost,
		ConnectHost:   progress.ConnectHost,
		ConnectPort:   progress.ConnectPort,
		AccessCommand: progress.AccessCommand,
	}
}

func buildUnitSSHCondition(generation int64, progress unitSSHProgress) metav1.Condition {
	decision, ok := unitSSHConditionByPhase[progress.Phase]
	if !ok {
		decision = unitSSHConditionByPhase[runtimev1alpha1.UnitSSHPhasePending]
	}
	decision.Reason = firstNonEmpty(progress.Reason, decision.Reason)
	decision.Message = firstNonEmpty(progress.Message, decision.Message)
	return statusConditionFromDecision(runtimev1alpha1.ConditionSSHReady, generation, decision)
}
