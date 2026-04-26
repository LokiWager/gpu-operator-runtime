package controller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

const (
	unitSharedMemoryVolumeName = "runtime-shm"
	unitSharedMemoryMountPath  = "/dev/shm"
)

var droppedWorkloadCapabilities = []corev1.Capability{
	"AUDIT_CONTROL",
	"AUDIT_READ",
	"BLOCK_SUSPEND",
	"DAC_READ_SEARCH",
	"IPC_LOCK",
	"IPC_OWNER",
	"KILL",
	"MAC_ADMIN",
	"MAC_OVERRIDE",
	"MKNOD",
	"SETPCAP",
	"SYS_ADMIN",
	"SYS_MODULE",
	"SYS_RAWIO",
	"SYS_RESOURCE",
	"SYS_TIME",
	"WAKE_ALARM",
}

func restrictedContainerSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities: &corev1.Capabilities{
			Drop: append([]corev1.Capability(nil), droppedWorkloadCapabilities...),
		},
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

func desiredSharedMemoryVolume(memoryLimit string) (corev1.Volume, error) {
	volume := corev1.Volume{
		Name: unitSharedMemoryVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium: corev1.StorageMediumMemory,
			},
		},
	}

	trimmed := strings.TrimSpace(memoryLimit)
	if trimmed == "" {
		return volume, nil
	}

	qty, err := resource.ParseQuantity(trimmed)
	if err != nil {
		return corev1.Volume{}, err
	}

	sizeLimitBytes := qty.Value() / 2
	if sizeLimitBytes < 1 {
		sizeLimitBytes = 1
	}
	volume.EmptyDir.SizeLimit = resource.NewQuantity(sizeLimitBytes, resource.BinarySI)
	return volume, nil
}

func desiredSharedMemoryVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      unitSharedMemoryVolumeName,
		MountPath: unitSharedMemoryMountPath,
	}
}
