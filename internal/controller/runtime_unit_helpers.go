package controller

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

const (
	statusOnlyMessage      = "status already updated"
	parseMemoryErrorFormat = "parse memory %q: %w"
	requeueAfterUpdate     = 2 * time.Second
)

type resolvedGPUUnitStorageMount struct {
	Mount     runtimev1alpha1.GPUUnitStorageMount
	ClaimName string
	Ready     bool
}

type unitPodSpecParts struct {
	Containers     []corev1.Container
	InitContainers []corev1.Container
	Volumes        []corev1.Volume
	ResourceClaims []corev1.PodResourceClaim
}

type unitSSHSidecarParts struct {
	InitContainers []corev1.Container
	Volumes        []corev1.Volume
}

type unitServerlessSidecarParts struct {
	InitContainers []corev1.Container
}

// desiredUnitDeployment builds the single-replica workload owned by one GPUUnit.
func desiredUnitDeployment(
	instance runtimev1alpha1.GPUUnit,
	storageMounts []resolvedGPUUnitStorageMount,
	queueConfig serverless.NATSConfig,
	workerConfig serverless.WorkerSidecarConfig,
) (*appsv1.Deployment, error) {
	name := deploymentNameForUnit(instance.Name)
	labels := unitObjectLabels(instance)
	template, err := desiredUnitPodTemplate(instance, storageMounts, queueConfig, workerConfig)
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
func desiredUnitPodTemplate(
	instance runtimev1alpha1.GPUUnit,
	storageMounts []resolvedGPUUnitStorageMount,
	queueConfig serverless.NATSConfig,
	workerConfig serverless.WorkerSidecarConfig,
) (corev1.PodTemplateSpec, error) {
	labels := unitPodLabels(instance)
	parts, err := desiredUnitPodSpecParts(instance, storageMounts, queueConfig, workerConfig)
	if err != nil {
		return corev1.PodTemplateSpec{}, err
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec: corev1.PodSpec{
			Containers:     parts.Containers,
			InitContainers: parts.InitContainers,
			Volumes:        parts.Volumes,
			ResourceClaims: parts.ResourceClaims,
		},
	}, nil
}

func desiredUnitPodSpecParts(
	instance runtimev1alpha1.GPUUnit,
	storageMounts []resolvedGPUUnitStorageMount,
	queueConfig serverless.NATSConfig,
	workerConfig serverless.WorkerSidecarConfig,
) (unitPodSpecParts, error) {
	runtimeContainer, err := desiredUnitRuntimeContainer(instance, storageMounts)
	if err != nil {
		return unitPodSpecParts{}, err
	}
	sharedMemoryVolume, err := desiredSharedMemoryVolume(instance.Spec.Memory)
	if err != nil {
		return unitPodSpecParts{}, fmt.Errorf(parseMemoryErrorFormat, instance.Spec.Memory, err)
	}

	parts := unitPodSpecParts{
		Containers:     []corev1.Container{runtimeContainer},
		Volumes:        append(desiredStorageVolumes(storageMounts), sharedMemoryVolume),
		ResourceClaims: desiredDRAResourceClaims(instance),
	}
	if lifecycleForUnit(instance) == runtimev1alpha1.LifecycleInstance && unitServerlessEnabled(instance.Spec.Serverless) {
		parts.Volumes = append(parts.Volumes, desiredUnitServerlessFrameworkSocketVolume())
		serverlessParts, err := desiredUnitServerlessSidecar(instance, queueConfig, workerConfig)
		if err != nil {
			return unitPodSpecParts{}, err
		}
		parts.InitContainers = append(parts.InitContainers, serverlessParts.InitContainers...)
	}
	if lifecycleForUnit(instance) != runtimev1alpha1.LifecycleInstance || !instance.Spec.SSH.Enabled {
		return parts, nil
	}

	sshParts, err := desiredUnitSSHSidecars(instance, storageMounts)
	if err != nil {
		return unitPodSpecParts{}, err
	}
	parts.InitContainers = append(parts.InitContainers, sshParts.InitContainers...)
	parts.Volumes = append(parts.Volumes, sshParts.Volumes...)
	return parts, nil
}

func desiredUnitRuntimeContainer(
	instance runtimev1alpha1.GPUUnit,
	storageMounts []resolvedGPUUnitStorageMount,
) (corev1.Container, error) {
	resources, err := desiredUnitRuntimeResources(instance)
	if err != nil {
		return corev1.Container{}, err
	}

	image := firstNonEmpty(instance.Spec.Image, runtimev1alpha1.DefaultRuntimeImage)
	container := corev1.Container{
		Name:            runtimev1alpha1.RuntimeWorkerContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             defaultGPUUnitEnv(instance),
		Ports:           desiredContainerPorts(instance.Spec.Template.Ports),
		Resources:       resources,
		SecurityContext: restrictedContainerSecurityContext(),
		VolumeMounts:    desiredUnitRuntimeVolumeMounts(storageMounts),
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
	if unitServerlessEnabled(instance.Spec.Serverless) {
		container.Env = append(container.Env, defaultGPUUnitServerlessFrameworkEnv(instance)...)
		container.VolumeMounts = append(container.VolumeMounts, desiredUnitServerlessFrameworkSocketVolumeMount())
	}
	return container, nil
}

func desiredUnitRuntimeVolumeMounts(storageMounts []resolvedGPUUnitStorageMount) []corev1.VolumeMount {
	mounts := desiredStorageVolumeMounts(storageMounts)
	return append(mounts, desiredSharedMemoryVolumeMount())
}

func desiredUnitRuntimeResources(instance runtimev1alpha1.GPUUnit) (corev1.ResourceRequirements, error) {
	resources := corev1.ResourceRequirements{}
	if instance.Spec.CPU != "" {
		qty, err := resource.ParseQuantity(instance.Spec.CPU)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("parse cpu %q: %w", instance.Spec.CPU, err)
		}
		resources.Requests = corev1.ResourceList{corev1.ResourceCPU: qty}
		resources.Limits = corev1.ResourceList{corev1.ResourceCPU: qty}
	}
	if instance.Spec.Memory != "" {
		qty, err := resource.ParseQuantity(instance.Spec.Memory)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf(parseMemoryErrorFormat, instance.Spec.Memory, err)
		}
		if resources.Requests == nil {
			resources.Requests = corev1.ResourceList{}
		}
		if resources.Limits == nil {
			resources.Limits = corev1.ResourceList{}
		}
		resources.Requests[corev1.ResourceMemory] = qty
		resources.Limits[corev1.ResourceMemory] = qty
	}
	resources.Claims = []corev1.ResourceClaim{{
		Name:    runtimev1alpha1.UnitDRAClaimName,
		Request: unitDRAClaimRequestName(instance),
	}}
	return resources, nil
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

func defaultGPUUnitServerlessFrameworkEnv(instance runtimev1alpha1.GPUUnit) []corev1.EnvVar {
	spec, err := resolveUnitServerlessSpec(instance)
	if err != nil {
		return nil
	}
	return []corev1.EnvVar{
		{Name: serverless.EnvServerlessRequestID, Value: spec.RequestID},
		{Name: serverless.EnvWorkerName, Value: instance.Name},
		{Name: serverless.EnvWorkerNamespace, Value: instance.Namespace},
		{Name: serverless.EnvFrameworkSocketPath, Value: spec.Framework.SocketPath},
		{Name: serverless.EnvFrameworkInvokePath, Value: spec.Framework.InvokePath},
		{Name: serverless.EnvFrameworkHealthPath, Value: spec.Framework.HealthPath},
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

// desiredStorageVolumeMounts renders container mounts for each resolved storage attachment.
func desiredStorageVolumeMounts(storageMounts []resolvedGPUUnitStorageMount) []corev1.VolumeMount {
	out := make([]corev1.VolumeMount, 0, len(storageMounts))
	for i, mount := range storageMounts {
		out = append(out, corev1.VolumeMount{
			Name:      volumeNameForStorageMount(i),
			MountPath: mount.Mount.MountPath,
			ReadOnly:  mount.Mount.ReadOnly,
		})
	}
	return out
}

// desiredStorageVolumes renders pod volumes for each resolved storage attachment.
func desiredStorageVolumes(storageMounts []resolvedGPUUnitStorageMount) []corev1.Volume {
	out := make([]corev1.Volume, 0, len(storageMounts))
	for i, mount := range storageMounts {
		out = append(out, corev1.Volume{
			Name: volumeNameForStorageMount(i),
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: mount.ClaimName,
					ReadOnly:  mount.Mount.ReadOnly,
				},
			},
		})
	}
	return out
}

func desiredUnitServerlessSidecar(
	instance runtimev1alpha1.GPUUnit,
	queueConfig serverless.NATSConfig,
	workerConfig serverless.WorkerSidecarConfig,
) (unitServerlessSidecarParts, error) {
	spec, err := resolveUnitServerlessSpec(instance)
	if err != nil {
		return unitServerlessSidecarParts{}, err
	}
	if !spec.Enabled {
		return unitServerlessSidecarParts{}, nil
	}
	if !queueConfig.Enabled() {
		return unitServerlessSidecarParts{}, fmt.Errorf("serverless queue is not configured but spec.serverless is enabled")
	}

	normalizedWorkerConfig, err := workerConfig.Normalized()
	if err != nil {
		return unitServerlessSidecarParts{}, err
	}
	queueConfig, err = queueConfig.Normalized()
	if err != nil {
		return unitServerlessSidecarParts{}, err
	}

	container := corev1.Container{
		Name:            runtimev1alpha1.ServerlessSidecarContainerName,
		Image:           normalizedWorkerConfig.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: restrictedContainerSecurityContext(),
		Env: []corev1.EnvVar{
			{Name: serverless.EnvNATSURL, Value: queueConfig.URL},
			{Name: serverless.EnvSubjectPrefix, Value: queueConfig.SubjectPrefix},
			{Name: serverless.EnvStreamName, Value: queueConfig.StreamName},
			{Name: serverless.EnvWorkerName, Value: instance.Name},
			{Name: serverless.EnvWorkerNamespace, Value: instance.Namespace},
			{Name: serverless.EnvServerlessRequestID, Value: spec.RequestID},
			{Name: serverless.EnvWorkerConsumerName, Value: "serverless-sidecar-" + instance.Name},
			{Name: serverless.EnvHeartbeatInterval, Value: normalizedWorkerConfig.HeartbeatInterval},
			{Name: serverless.EnvDispatchMaxDeliver, Value: strconv.FormatInt(int64(normalizedWorkerConfig.DispatchRetry.MaxDeliver), 10)},
			{Name: serverless.EnvDispatchBackoff, Value: strings.Join(normalizedWorkerConfig.DispatchRetry.Backoff, ",")},
			{Name: serverless.EnvFrameworkSocketPath, Value: spec.Framework.SocketPath},
			{Name: serverless.EnvFrameworkInvokePath, Value: spec.Framework.InvokePath},
			{Name: serverless.EnvFrameworkHealthPath, Value: spec.Framework.HealthPath},
			{Name: serverless.EnvSidecarHealthPort, Value: strconv.FormatInt(int64(normalizedWorkerConfig.HealthPort), 10)},
		},
		VolumeMounts: []corev1.VolumeMount{
			desiredUnitServerlessFrameworkSocketVolumeMount(),
		},
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/healthz",
					Port: intstr.FromInt32(normalizedWorkerConfig.HealthPort),
				},
			},
			PeriodSeconds:    1,
			FailureThreshold: 30,
		},
	}
	return unitServerlessSidecarParts{
		InitContainers: []corev1.Container{asRestartableInitSidecar(container)},
	}, nil
}

func desiredUnitSSHSidecars(
	instance runtimev1alpha1.GPUUnit,
	storageMounts []resolvedGPUUnitStorageMount,
) (unitSSHSidecarParts, error) {
	sshSpec, err := resolveUnitSSHSpec(instance)
	if err != nil {
		return unitSSHSidecarParts{}, err
	}
	return unitSSHSidecarParts{
		InitContainers: []corev1.Container{
			asRestartableInitSidecar(desiredUnitSSHServerSidecar(sshSpec, storageMounts)),
			asRestartableInitSidecar(desiredUnitSSHFRPSidecar(instance, sshSpec)),
		},
		Volumes: []corev1.Volume{
			desiredUnitSSHConfigVolume(),
			desiredUnitSSHAuthorizedKeysVolume(instance.Name),
		},
	}, nil
}

func desiredUnitSSHServerSidecar(
	sshSpec runtimev1alpha1.GPUUnitSSHSpec,
	storageMounts []resolvedGPUUnitStorageMount,
) corev1.Container {
	return corev1.Container{
		Name:            runtimev1alpha1.UnitSSHContainerName,
		Image:           sshSpec.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: []corev1.EnvVar{
			{Name: "PUID", Value: "1000"},
			{Name: "PGID", Value: "1000"},
			{Name: "TZ", Value: "Etc/UTC"},
			{Name: unitSSHAuthorizedKeysEnvName, Value: unitSSHAuthorizedKeysFilePath()},
			{Name: unitSSHAuthorizedKeysDigestEnv, Value: unitSSHAuthorizedKeysDigest(sshSpec)},
			{Name: "USER_NAME", Value: sshSpec.Username},
			{Name: "PASSWORD_ACCESS", Value: "false"},
			{Name: "SUDO_ACCESS", Value: "false"},
			{Name: "LOG_STDOUT", Value: "true"},
		},
		SecurityContext: restrictedContainerSecurityContext(),
		Ports: []corev1.ContainerPort{{
			Name:          "ssh",
			ContainerPort: sshSpec.Port,
			Protocol:      corev1.ProtocolTCP,
		}},
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(sshSpec.Port),
				},
			},
			PeriodSeconds:    1,
			FailureThreshold: 30,
		},
		VolumeMounts: desiredUnitSSHVolumeMounts(storageMounts),
	}
}

func desiredUnitSSHFRPSidecar(
	instance runtimev1alpha1.GPUUnit,
	sshSpec runtimev1alpha1.GPUUnitSSHSpec,
) corev1.Container {
	return corev1.Container{
		Name:            runtimev1alpha1.UnitSSHFRPContainerName,
		Image:           sshSpec.FRPImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: restrictedContainerSecurityContext(),
		Command:         []string{"sh", "-c"},
		Args:            []string{desiredUnitSSHFRPConfig(instance, sshSpec)},
	}
}

func desiredUnitSSHVolumeMounts(storageMounts []resolvedGPUUnitStorageMount) []corev1.VolumeMount {
	return append([]corev1.VolumeMount{
		{
			Name:      "ssh-config",
			MountPath: "/config",
		},
		{
			Name:      unitSSHAuthorizedKeysVolumeName,
			MountPath: unitSSHAuthorizedKeysMountPath,
			ReadOnly:  true,
		},
	}, desiredStorageVolumeMounts(storageMounts)...)
}

func desiredUnitServerlessFrameworkSocketVolume() corev1.Volume {
	return corev1.Volume{
		Name: "serverless-framework-socket",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

func desiredUnitServerlessFrameworkSocketVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      "serverless-framework-socket",
		MountPath: runtimev1alpha1.DefaultServerlessFrameworkSocketDir,
	}
}

func desiredUnitSSHConfigVolume() corev1.Volume {
	return corev1.Volume{
		Name: "ssh-config",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

func desiredUnitSSHAuthorizedKeysVolume(instanceName string) corev1.Volume {
	defaultMode := int32(unitSSHAuthorizedKeysDefaultMode)
	return corev1.Volume{
		Name: unitSSHAuthorizedKeysVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: unitSSHAuthorizedKeysConfigMapName(instanceName),
				},
				DefaultMode: &defaultMode,
				Items: []corev1.KeyToPath{{
					Key:  unitSSHAuthorizedKeysConfigKey,
					Path: unitSSHAuthorizedKeysConfigKey,
				}},
			},
		},
	}
}

func asRestartableInitSidecar(container corev1.Container) corev1.Container {
	container.RestartPolicy = ptr.To(corev1.ContainerRestartPolicyAlways)
	return container
}

func resolveUnitServerlessSpec(instance runtimev1alpha1.GPUUnit) (runtimev1alpha1.GPUUnitServerlessSpec, error) {
	spec := instance.Spec.Serverless
	if !spec.Enabled && strings.TrimSpace(spec.RequestID) == "" {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, nil
	}

	requestID, err := serverless.NormalizeRequestID(spec.RequestID)
	if err != nil {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, fmt.Errorf("%w: %s", errUnitServerlessSpecIncomplete, err.Error())
	}
	spec.Enabled = true
	spec.RequestID = requestID
	if spec.MinAvailableCount < 0 {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, fmt.Errorf("%w: serverless.minAvailableCount should be >= 0", errUnitServerlessSpecIncomplete)
	}
	if spec.IdleTimeoutSeconds < 0 {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, fmt.Errorf("%w: serverless.idleTimeoutSeconds should be >= 0", errUnitServerlessSpecIncomplete)
	}
	if spec.MinRequestCount < 0 {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, fmt.Errorf("%w: serverless.minRequestCount should be >= 0", errUnitServerlessSpecIncomplete)
	}
	if spec.IdleTimeoutSeconds == 0 {
		spec.IdleTimeoutSeconds = 300
	}
	spec.Framework.SocketPath = normalizeControllerServerlessSocketPath(spec.Framework.SocketPath, runtimev1alpha1.DefaultServerlessFrameworkSocketPath)
	socketDir := path.Clean(runtimev1alpha1.DefaultServerlessFrameworkSocketDir)
	if spec.Framework.SocketPath == socketDir || !strings.HasPrefix(spec.Framework.SocketPath, socketDir+"/") {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, fmt.Errorf("%w: serverless.framework.socketPath %q must stay under %s", errUnitServerlessSpecIncomplete, spec.Framework.SocketPath, runtimev1alpha1.DefaultServerlessFrameworkSocketDir)
	}
	spec.Framework.InvokePath = normalizeControllerServerlessPath(spec.Framework.InvokePath, runtimev1alpha1.DefaultServerlessFrameworkInvokePath)
	spec.Framework.HealthPath = normalizeControllerServerlessPath(spec.Framework.HealthPath, runtimev1alpha1.DefaultServerlessFrameworkHealthPath)
	return spec, nil
}

func normalizeControllerServerlessSocketPath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return path.Clean(trimmed)
}

func resolveUnitSSHSpec(instance runtimev1alpha1.GPUUnit) (runtimev1alpha1.GPUUnitSSHSpec, error) {
	ssh := instance.Spec.SSH
	if !ssh.Enabled {
		return runtimev1alpha1.GPUUnitSSHSpec{}, nil
	}

	username := strings.ToLower(firstNonEmpty(ssh.Username, runtimev1alpha1.DefaultUnitSSHUsername))
	if errs := validation.IsDNS1123Label(username); len(errs) > 0 {
		return runtimev1alpha1.GPUUnitSSHSpec{}, fmt.Errorf("%w: ssh.username %q is invalid: %s", errUnitSSHSpecIncomplete, username, strings.Join(errs, ", "))
	}

	keys := make([]string, 0, len(ssh.AuthorizedKeys))
	seen := map[string]struct{}{}
	for _, key := range ssh.AuthorizedKeys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		keys = append(keys, trimmed)
	}
	if len(keys) == 0 {
		return runtimev1alpha1.GPUUnitSSHSpec{}, fmt.Errorf("%w: ssh.authorizedKeys requires at least one public key when ssh.enabled is true", errUnitSSHSpecIncomplete)
	}

	port := ssh.Port
	if port == 0 {
		port = runtimev1alpha1.DefaultUnitSSHPort
	}
	serverAddr := strings.TrimSpace(ssh.ServerAddr)
	if serverAddr == "" {
		return runtimev1alpha1.GPUUnitSSHSpec{}, fmt.Errorf("%w: ssh.serverAddr is required when ssh.enabled is true", errUnitSSHSpecIncomplete)
	}
	serverPort := ssh.ServerPort
	if serverPort == 0 {
		serverPort = runtimev1alpha1.DefaultUnitSSHFRPPort
	}
	connectHost := firstNonEmpty(ssh.ConnectHost, serverAddr)
	connectPort := ssh.ConnectPort
	if connectPort == 0 {
		connectPort = runtimev1alpha1.DefaultUnitSSHProxyPort
	}
	domainSuffix := strings.TrimPrefix(strings.TrimSpace(ssh.DomainSuffix), ".")
	clientName := strings.ToLower(strings.TrimSpace(ssh.ClientName))
	if clientName == "" {
		clientName = prefixedRuntimeName("ssh-", instance.Namespace+"-"+instance.Name)
	}
	clientDomain := strings.ToLower(strings.TrimSpace(ssh.ClientDomain))
	if clientDomain == "" {
		if domainSuffix == "" {
			return runtimev1alpha1.GPUUnitSSHSpec{}, fmt.Errorf("%w: ssh.domainSuffix is required when ssh.clientDomain is not set", errUnitSSHSpecIncomplete)
		}
		clientDomain = fmt.Sprintf("%s.%s.%s", instance.Name, instance.Namespace, domainSuffix)
	}
	for field, value := range map[string]int32{
		"ssh.port":        port,
		"ssh.serverPort":  serverPort,
		"ssh.connectPort": connectPort,
	} {
		if value < 1 || value > 65535 {
			return runtimev1alpha1.GPUUnitSSHSpec{}, fmt.Errorf("%w: %s must be between 1 and 65535", errUnitSSHSpecIncomplete, field)
		}
	}
	if errs := validation.IsDNS1123Subdomain(clientName); len(errs) > 0 {
		return runtimev1alpha1.GPUUnitSSHSpec{}, fmt.Errorf("%w: ssh.clientName %q is invalid: %s", errUnitSSHSpecIncomplete, clientName, strings.Join(errs, ", "))
	}
	if errs := validation.IsDNS1123Subdomain(clientDomain); len(errs) > 0 {
		return runtimev1alpha1.GPUUnitSSHSpec{}, fmt.Errorf("%w: ssh.clientDomain %q is invalid: %s", errUnitSSHSpecIncomplete, clientDomain, strings.Join(errs, ", "))
	}

	image := firstNonEmpty(ssh.Image, runtimev1alpha1.DefaultUnitSSHImage)
	frpImage := firstNonEmpty(ssh.FRPImage, runtimev1alpha1.DefaultUnitSSHFRPImage)

	return runtimev1alpha1.GPUUnitSSHSpec{
		Enabled:        true,
		Username:       username,
		AuthorizedKeys: keys,
		Port:           port,
		ServerAddr:     serverAddr,
		ServerPort:     serverPort,
		ConnectHost:    connectHost,
		ConnectPort:    connectPort,
		DomainSuffix:   domainSuffix,
		ClientName:     clientName,
		ClientDomain:   clientDomain,
		Token:          strings.TrimSpace(ssh.Token),
		Image:          image,
		FRPImage:       frpImage,
	}, nil
}

func normalizeControllerServerlessPath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	normalized := "/" + strings.TrimPrefix(trimmed, "/")
	if normalized == "/" {
		return fallback
	}
	return normalized
}

func desiredUnitSSHFRPConfig(instance runtimev1alpha1.GPUUnit, sshSpec runtimev1alpha1.GPUUnitSSHSpec) string {
	lines := []string{
		fmt.Sprintf("serverAddr = %q", sshSpec.ServerAddr),
		fmt.Sprintf("serverPort = %d", sshSpec.ServerPort),
	}
	if strings.TrimSpace(sshSpec.Token) != "" {
		lines = append(lines,
			`auth.method = "token"`,
			fmt.Sprintf("auth.token = %q", sshSpec.Token),
		)
	}
	lines = append(lines,
		"",
		"[[proxies]]",
		fmt.Sprintf("name = %q", sshProxyNameForUnit(instance, sshSpec)),
		`type = "tcpmux"`,
		`multiplexer = "httpconnect"`,
		fmt.Sprintf("customDomains = [%q]", sshTargetHostForUnit(instance, sshSpec)),
		`localIP = "127.0.0.1"`,
		fmt.Sprintf("localPort = %d", sshSpec.Port),
	)

	return "cat <<'EOF' >/tmp/frpc.toml\n" + strings.Join(lines, "\n") + "\nEOF\nexec frpc -c /tmp/frpc.toml"
}

func sshProxyNameForUnit(instance runtimev1alpha1.GPUUnit, sshSpec runtimev1alpha1.GPUUnitSSHSpec) string {
	if sshSpec.ClientName != "" {
		return sshSpec.ClientName
	}
	return prefixedRuntimeName("ssh-", instance.Namespace+"-"+instance.Name)
}

func sshTargetHostForUnit(instance runtimev1alpha1.GPUUnit, sshSpec runtimev1alpha1.GPUUnitSSHSpec) string {
	if sshSpec.ClientDomain != "" {
		return sshSpec.ClientDomain
	}
	suffix := strings.TrimPrefix(strings.TrimSpace(sshSpec.DomainSuffix), ".")
	return fmt.Sprintf("%s.%s.%s", instance.Name, instance.Namespace, suffix)
}

func buildUnitSSHAccessCommand(instance runtimev1alpha1.GPUUnit, sshSpec runtimev1alpha1.GPUUnitSSHSpec) string {
	targetHost := sshTargetHostForUnit(instance, sshSpec)
	return fmt.Sprintf(
		`ssh -o ProxyCommand='nc -X connect -x %s:%d %%h %%p' %s@%s`,
		sshSpec.ConnectHost,
		sshSpec.ConnectPort,
		sshSpec.Username,
		targetHost,
	)
}

// volumeNameForStorageMount returns the deterministic pod volume name for one attachment.
func volumeNameForStorageMount(index int) string {
	return fmt.Sprintf("storage-%d", index)
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

// firstNonEmpty returns the first trimmed non-empty value from the candidates.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// lifecycleForUnit derives the runtime lifecycle label.
func lifecycleForUnit(instance runtimev1alpha1.GPUUnit) string {
	return runtimev1alpha1.LifecycleInstance
}

func unitServerlessEnabled(spec runtimev1alpha1.GPUUnitServerlessSpec) bool {
	return spec.Enabled ||
		strings.TrimSpace(spec.RequestID) != "" ||
		spec.MinAvailableCount > 0 ||
		spec.IdleTimeoutSeconds > 0 ||
		spec.MinRequestCount > 0 ||
		strings.TrimSpace(spec.Framework.SocketPath) != "" ||
		strings.TrimSpace(spec.Framework.InvokePath) != "" ||
		strings.TrimSpace(spec.Framework.HealthPath) != ""
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
