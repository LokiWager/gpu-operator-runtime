package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

type storagePrepareProgress struct {
	Phase         string
	JobName       string
	Digest        string
	RecoveryPhase string
	Reason        string
	Message       string
	Ready         bool
	Changed       bool
}

type storageAccessorProgress struct {
	Phase       string
	ServiceName string
	AccessURL   string
	Reason      string
	Message     string
	Ready       bool
	Changed     bool
}

func newStoragePrepareProgress(phase, recoveryPhase, reason, message string, ready bool) storagePrepareProgress {
	return storagePrepareProgress{
		Phase:         phase,
		RecoveryPhase: recoveryPhase,
		Reason:        reason,
		Message:       message,
		Ready:         ready,
	}
}

func storagePrepareNotRequestedProgress() storagePrepareProgress {
	return newStoragePrepareProgress(
		runtimev1alpha1.StoragePreparePhaseNotRequested,
		runtimev1alpha1.StorageRecoveryPhaseNone,
		runtimev1alpha1.ReasonStoragePrepareReady,
		runtimev1alpha1.StatusMessageStoragePrepared,
		true,
	)
}

func storagePreparePVCWaitingProgress() storagePrepareProgress {
	return newStoragePrepareProgress(
		runtimev1alpha1.StoragePreparePhasePending,
		runtimev1alpha1.StorageRecoveryPhaseNone,
		runtimev1alpha1.ReasonStoragePreparePending,
		runtimev1alpha1.StatusMessageStoragePending,
		false,
	)
}

func newStoragePrepareJobProgress(storage runtimev1alpha1.GPUStorage) (storagePrepareProgress, error) {
	digest, err := storagePrepareDigest(storage)
	if err != nil {
		return storagePrepareProgress{}, err
	}

	return storagePrepareProgress{
		Phase:         runtimev1alpha1.StoragePreparePhasePending,
		Digest:        digest,
		JobName:       storagePrepareJobName(storage.Name, digest),
		RecoveryPhase: runtimev1alpha1.StorageRecoveryPhaseNone,
		Reason:        runtimev1alpha1.ReasonStoragePreparePending,
		Message:       runtimev1alpha1.StatusMessageStoragePreparePending,
	}, nil
}

func storagePrepareSucceededProgress(storage *runtimev1alpha1.GPUStorage, progress storagePrepareProgress) storagePrepareProgress {
	progress.Phase = runtimev1alpha1.StoragePreparePhaseSucceeded
	progress.Reason = runtimev1alpha1.ReasonStoragePrepareReady
	progress.Message = runtimev1alpha1.StatusMessageStoragePrepared
	progress.Ready = true
	progress.RecoveryPhase = recoveryPhaseForPrepare(storage, progress.Phase)
	return progress
}

func storagePrepareProgressFromJob(
	storage *runtimev1alpha1.GPUStorage,
	progress storagePrepareProgress,
	job batchv1.Job,
) storagePrepareProgress {
	switch {
	case job.Status.Succeeded > 0:
		progress = storagePrepareSucceededProgress(storage, progress)
	case job.Status.Failed > 0:
		progress.Phase = runtimev1alpha1.StoragePreparePhaseFailed
		progress.Reason = runtimev1alpha1.ReasonStoragePrepareFailed
		progress.Message = firstNonEmpty(jobFailureMessage(job), runtimev1alpha1.StatusMessageStoragePrepareFailed)
	case job.Status.Active > 0:
		progress.Phase = runtimev1alpha1.StoragePreparePhaseRunning
		progress.Reason = runtimev1alpha1.ReasonStoragePrepareRunning
		progress.Message = runtimev1alpha1.StatusMessageStoragePrepareRunning
	default:
		progress.Phase = runtimev1alpha1.StoragePreparePhasePending
		progress.Reason = runtimev1alpha1.ReasonStoragePreparePending
		progress.Message = runtimev1alpha1.StatusMessageStoragePreparePending
	}
	progress.RecoveryPhase = recoveryPhaseForPrepare(storage, progress.Phase)
	return progress
}

func newStorageAccessorProgress(
	phase string,
	serviceName string,
	accessURL string,
	reason string,
	message string,
	ready bool,
	changed bool,
) storageAccessorProgress {
	return storageAccessorProgress{
		Phase:       phase,
		ServiceName: serviceName,
		AccessURL:   accessURL,
		Reason:      reason,
		Message:     message,
		Ready:       ready,
		Changed:     changed,
	}
}

func storageAccessorDisabledProgress(changed bool) storageAccessorProgress {
	return newStorageAccessorProgress(
		runtimev1alpha1.StorageAccessorPhaseDisabled,
		"",
		"",
		runtimev1alpha1.ReasonStorageAccessorReady,
		runtimev1alpha1.StatusMessageStorageAccessorDisabled,
		true,
		changed,
	)
}

func storageAccessorPendingProgress(changed bool) storageAccessorProgress {
	return newStorageAccessorProgress(
		runtimev1alpha1.StorageAccessorPhasePending,
		"",
		"",
		runtimev1alpha1.ReasonStorageAccessorPending,
		runtimev1alpha1.StatusMessageStorageAccessorPending,
		false,
		changed,
	)
}

func storageAccessorProgressFromDeployment(
	storage runtimev1alpha1.GPUStorage,
	serviceName string,
	availableReplicas int32,
	changed bool,
) storageAccessorProgress {
	accessURL := storageAccessorURL(storage.Namespace, serviceName, storage.Name)
	if availableReplicas > 0 {
		return newStorageAccessorProgress(
			runtimev1alpha1.StorageAccessorPhaseReady,
			serviceName,
			accessURL,
			runtimev1alpha1.ReasonStorageAccessorReady,
			runtimev1alpha1.StatusMessageStorageAccessorReady,
			true,
			changed,
		)
	}
	return newStorageAccessorProgress(
		runtimev1alpha1.StorageAccessorPhasePending,
		serviceName,
		accessURL,
		runtimev1alpha1.ReasonStorageAccessorPending,
		runtimev1alpha1.StatusMessageStorageAccessorPending,
		false,
		changed,
	)
}

func storagePrepareDigest(storage runtimev1alpha1.GPUStorage) (string, error) {
	payload := struct {
		Prepare       runtimev1alpha1.GPUStoragePrepareSpec `json:"prepare"`
		RecoveryNonce string                                `json:"recoveryNonce,omitempty"`
	}{
		Prepare:       storage.Spec.Prepare,
		RecoveryNonce: storage.GetAnnotations()[runtimev1alpha1.AnnotationStorageRecoveryNonce],
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12], nil
}

func storagePrepareJobName(storageName, digest string) string {
	return clampName("storage-prepare-"+storageName+"-"+digest, 63)
}

func storageAccessorDeploymentName(storageName string) string {
	return runtimev1alpha1.StorageAccessorServiceResourceName(storageName)
}

func storageAccessorServiceName(storageName string) string {
	return runtimev1alpha1.StorageAccessorServiceResourceName(storageName)
}

func clampName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	return strings.TrimRight(name[:limit], "-")
}

func desiredGPUStoragePrepareJob(
	storage runtimev1alpha1.GPUStorage,
	jobName string,
	targetClaimName string,
	sourceClaimName string,
) *batchv1.Job {
	labels := storageOwnedLabels(storage.Name)
	labels["runtime.lokiwager.io/storage-prepare"] = storage.Name

	container := corev1.Container{
		Name:            "storage-prepare",
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: restrictedContainerSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "workspace",
			MountPath: runtimev1alpha1.StoragePrepareMountPath,
		}},
	}

	volumes := []corev1.Volume{{
		Name: "workspace",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: targetClaimName,
			},
		},
	}}

	if storage.Spec.Prepare.FromStorageName != "" {
		container.Image = runtimev1alpha1.DefaultStoragePrepareCopyImage
		container.Command = []string{"sh", "-c"}
		container.Args = []string{
			fmt.Sprintf(
				"mkdir -p %[1]s && if [ -d %[2]s ] && [ \"$(ls -A %[2]s 2>/dev/null)\" ]; then cp -a %[2]s/. %[1]s/; fi",
				runtimev1alpha1.StoragePrepareMountPath,
				runtimev1alpha1.StoragePrepareSourceMountPath,
			),
		}
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "source",
			MountPath: runtimev1alpha1.StoragePrepareSourceMountPath,
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "source",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: sourceClaimName,
					ReadOnly:  true,
				},
			},
		})
	} else {
		container.Image = storage.Spec.Prepare.FromImage
		container.Command = append([]string(nil), storage.Spec.Prepare.Command...)
		container.Args = append([]string(nil), storage.Spec.Prepare.Args...)
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: storage.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(0)),
			TTLSecondsAfterFinished: ptr.To(int32(300)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{container},
					Volumes:       volumes,
				},
			},
		},
	}
}

func desiredGPUStorageAccessorDeployment(storage runtimev1alpha1.GPUStorage, claimName string) *appsv1.Deployment {
	name := storageAccessorDeploymentName(storage.Name)
	labels := storageOwnedLabels(storage.Name)
	labels["runtime.lokiwager.io/storage-accessor"] = storage.Name
	proxyPath := storageProxyBasePath(storage.Namespace, storage.Name)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: storage.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"runtime.lokiwager.io/storage-accessor": storage.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyAlways,
					Containers: []corev1.Container{{
						Name:            runtimev1alpha1.StorageAccessorContainerName,
						Image:           runtimev1alpha1.DefaultStorageAccessorImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						SecurityContext: restrictedContainerSecurityContext(),
						Command:         []string{"dufs"},
						Args: []string{
							"/data",
							"--bind",
							"0.0.0.0",
							"--port",
							strconv.Itoa(int(runtimev1alpha1.DefaultStorageAccessorPort)),
							"--path-prefix",
							proxyPath,
							"--allow-search",
							"--allow-archive",
						},
						Ports: []corev1.ContainerPort{{
							Name:          "http",
							ContainerPort: runtimev1alpha1.DefaultStorageAccessorPort,
							Protocol:      corev1.ProtocolTCP,
						}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "workspace",
							MountPath: "/data",
							ReadOnly:  true,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "workspace",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: claimName,
								ReadOnly:  true,
							},
						},
					}},
				},
			},
		},
	}
}

func desiredGPUStorageAccessorService(storage runtimev1alpha1.GPUStorage) *corev1.Service {
	name := storageAccessorServiceName(storage.Name)
	labels := storageOwnedLabels(storage.Name)
	labels["runtime.lokiwager.io/storage-accessor"] = storage.Name

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: storage.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"runtime.lokiwager.io/storage-accessor": storage.Name,
			},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       runtimev1alpha1.DefaultStorageAccessorPort,
				TargetPort: intstr.FromInt32(runtimev1alpha1.DefaultStorageAccessorPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

func storageAccessorURL(namespace, serviceName, storageName string) string {
	return fmt.Sprintf(
		"http://%s.%s.svc.cluster.local:%d%s",
		serviceName,
		namespace,
		runtimev1alpha1.DefaultStorageAccessorPort,
		storageProxyPath(namespace, storageName),
	)
}

func storageProxyPath(namespace, storageName string) string {
	return storageProxyBasePath(namespace, storageName) + "/"
}

func storageProxyBasePath(namespace, storageName string) string {
	return path.Join(runtimev1alpha1.DefaultStorageProxyPathPrefix, namespace, storageName)
}

func storageOwnedLabels(storageName string) map[string]string {
	return map[string]string{
		runtimev1alpha1.LabelAppNameKey:   runtimev1alpha1.LabelAppNameValue,
		runtimev1alpha1.LabelManagedByKey: runtimev1alpha1.LabelManagedByValue,
		runtimev1alpha1.LabelStorageKey:   storageName,
	}
}

func jobFailureMessage(job batchv1.Job) string {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		if cond.Type == batchv1.JobFailed {
			if message := firstNonEmpty(cond.Message, cond.Reason); message != "" {
				return message
			}
		}
	}
	if job.Status.Failed > 0 {
		return "storage prepare job failed"
	}
	return ""
}

func isGPUStorageClaimBound(pvc *corev1.PersistentVolumeClaim) bool {
	return pvc != nil && pvc.Status.Phase == corev1.ClaimBound
}

func syncGPUStoragePVC(current, desired *corev1.PersistentVolumeClaim) bool {
	changed := false

	if nextLabels, labelsChanged := ensureStringMapValues(current.Labels, desired.Labels); labelsChanged {
		current.Labels = nextLabels
		changed = true
	}
	if !reflect.DeepEqual(current.Spec.AccessModes, desired.Spec.AccessModes) {
		current.Spec.AccessModes = append([]corev1.PersistentVolumeAccessMode(nil), desired.Spec.AccessModes...)
		changed = true
	}

	if current.Spec.Resources.Requests == nil {
		current.Spec.Resources.Requests = corev1.ResourceList{}
	}
	desiredQty := desired.Spec.Resources.Requests[corev1.ResourceStorage]
	if currentQty := current.Spec.Resources.Requests[corev1.ResourceStorage]; currentQty.Cmp(desiredQty) != 0 {
		current.Spec.Resources.Requests[corev1.ResourceStorage] = desiredQty
		changed = true
	}

	desiredStorageClassName := ptr.Deref(desired.Spec.StorageClassName, "")
	if current.Spec.StorageClassName == nil || ptr.Deref(current.Spec.StorageClassName, "") != desiredStorageClassName {
		current.Spec.StorageClassName = ptr.To(desiredStorageClassName)
		changed = true
	}

	return changed
}

func syncGPUStorageAccessorService(current, desired *corev1.Service) bool {
	changed := false

	if nextLabels, labelsChanged := ensureStringMapValues(current.Labels, desired.Labels); labelsChanged {
		current.Labels = nextLabels
		changed = true
	}
	if current.Spec.Type != desired.Spec.Type {
		current.Spec.Type = desired.Spec.Type
		changed = true
	}
	if !reflect.DeepEqual(current.Spec.Ports, desired.Spec.Ports) {
		current.Spec.Ports = append([]corev1.ServicePort(nil), desired.Spec.Ports...)
		changed = true
	}
	if nextSelector, selectorChanged := replaceStringMap(current.Spec.Selector, desired.Spec.Selector); selectorChanged {
		current.Spec.Selector = nextSelector
		changed = true
	}

	return changed
}

func syncGPUStorageAccessorDeployment(current, desired *appsv1.Deployment) bool {
	changed := false

	if nextLabels, labelsChanged := ensureStringMapValues(current.Labels, desired.Labels); labelsChanged {
		current.Labels = nextLabels
		changed = true
	}

	desiredReplicas := int32(1)
	if desired.Spec.Replicas != nil {
		desiredReplicas = *desired.Spec.Replicas
	}
	if current.Spec.Replicas == nil || *current.Spec.Replicas != desiredReplicas {
		current.Spec.Replicas = ptr.To(desiredReplicas)
		changed = true
	}
	if !reflect.DeepEqual(current.Spec.Selector, desired.Spec.Selector) {
		current.Spec.Selector = desired.Spec.Selector.DeepCopy()
		changed = true
	}
	if nextTemplateLabels, templateLabelsChanged := replaceStringMap(current.Spec.Template.Labels, desired.Spec.Template.Labels); templateLabelsChanged {
		current.Spec.Template.Labels = nextTemplateLabels
		changed = true
	}
	if !reflect.DeepEqual(current.Spec.Template.Spec, desired.Spec.Template.Spec) {
		current.Spec.Template.Spec = desired.Spec.Template.Spec
		changed = true
	}

	return changed
}

func ensureStringMapValues(current map[string]string, desired map[string]string) (map[string]string, bool) {
	if len(desired) == 0 {
		return current, false
	}
	if current == nil {
		current = map[string]string{}
	}

	changed := false
	for key, value := range desired {
		if current[key] == value {
			continue
		}
		current[key] = value
		changed = true
	}
	return current, changed
}

func replaceStringMap(current map[string]string, desired map[string]string) (map[string]string, bool) {
	if reflect.DeepEqual(current, desired) {
		return current, false
	}
	return cloneStringMap(desired), true
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
