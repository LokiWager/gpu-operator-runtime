package controller

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

type observedPodContainerStatus struct {
	PodName string
	Status  corev1.ContainerStatus
}

func podFailureMessage(pod corev1.Pod) (string, bool) {
	for _, status := range observedPodContainerStatuses(pod) {
		if message, ok := containerFailureMessage(status.Status); ok {
			return fmt.Sprintf("Pod %s container %s: %s", status.PodName, status.Status.Name, message), true
		}
	}
	if pod.Status.Phase == corev1.PodFailed {
		if message := firstNonEmpty(pod.Status.Message, pod.Status.Reason); message != "" {
			return fmt.Sprintf("Pod %s: %s", pod.Name, message), true
		}
	}
	return "", false
}

func namedContainerFailureMessage(pod corev1.Pod, containerNames ...string) (string, bool) {
	nameSet := map[string]struct{}{}
	for _, name := range containerNames {
		nameSet[name] = struct{}{}
	}
	for _, status := range observedPodContainerStatuses(pod) {
		if _, ok := nameSet[status.Status.Name]; !ok {
			continue
		}
		if message, ok := containerFailureMessage(status.Status); ok {
			return fmt.Sprintf("Pod %s container %s: %s", status.PodName, status.Status.Name, message), true
		}
	}
	return "", false
}

func observedPodContainerStatuses(pod corev1.Pod) []observedPodContainerStatus {
	statuses := make([]observedPodContainerStatus, 0, len(pod.Status.InitContainerStatuses)+len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.InitContainerStatuses {
		statuses = append(statuses, observedPodContainerStatus{PodName: pod.Name, Status: status})
	}
	for _, status := range pod.Status.ContainerStatuses {
		statuses = append(statuses, observedPodContainerStatus{PodName: pod.Name, Status: status})
	}
	return statuses
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
