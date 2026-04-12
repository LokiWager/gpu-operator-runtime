package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

const (
	unitSSHAuthorizedKeysConfigKey   = "authorized_keys"
	unitSSHAuthorizedKeysVolumeName  = "ssh-authorized-keys"
	unitSSHAuthorizedKeysMountPath   = "/ssh-keys"
	unitSSHAuthorizedKeysEnvName     = "PUBLIC_KEY_FILE"
	unitSSHAuthorizedKeysDigestEnv   = "SSH_AUTHORIZED_KEYS_HASH"
	unitSSHAuthorizedKeysDefaultMode = 0600
)

func (r *GPUUnitReconciler) reconcileGPUUnitSSHAuthorizedKeysConfig(ctx context.Context, instance *runtimev1alpha1.GPUUnit) (bool, error) {
	if lifecycleForUnit(*instance) != runtimev1alpha1.LifecycleInstance || !instance.Spec.SSH.Enabled {
		return r.ensureGPUUnitSSHAuthorizedKeysConfigAbsent(ctx, instance)
	}

	sshSpec, err := resolveUnitSSHSpec(*instance)
	if err != nil {
		return false, err
	}

	desired := desiredUnitSSHAuthorizedKeysConfigMap(*instance, sshSpec)
	key := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}

	var current corev1.ConfigMap
	if err := r.Get(ctx, key, &current); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
		if err := controllerutil.SetControllerReference(instance, desired, r.Scheme); err != nil {
			return false, err
		}
		if err := r.Create(ctx, desired); err != nil {
			return false, err
		}
		return true, nil
	}

	changed := false
	if !reflect.DeepEqual(current.Data, desired.Data) {
		current.Data = desired.Data
		changed = true
	}
	if nextLabels, labelsChanged := replaceStringMap(current.Labels, desired.Labels); labelsChanged {
		current.Labels = nextLabels
		changed = true
	}
	if !changed {
		return false, nil
	}
	if err := r.Update(ctx, &current); err != nil {
		return false, err
	}
	return true, nil
}

func (r *GPUUnitReconciler) ensureGPUUnitSSHAuthorizedKeysConfigAbsent(ctx context.Context, instance *runtimev1alpha1.GPUUnit) (bool, error) {
	var current corev1.ConfigMap
	key := types.NamespacedName{Namespace: instance.Namespace, Name: unitSSHAuthorizedKeysConfigMapName(instance.Name)}
	if err := r.Get(ctx, key, &current); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if err := r.Delete(ctx, &current); err != nil {
		return false, err
	}
	return true, nil
}

func desiredUnitSSHAuthorizedKeysConfigMap(
	instance runtimev1alpha1.GPUUnit,
	sshSpec runtimev1alpha1.GPUUnitSSHSpec,
) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unitSSHAuthorizedKeysConfigMapName(instance.Name),
			Namespace: instance.Namespace,
			Labels:    unitObjectLabels(instance),
		},
		Data: map[string]string{
			unitSSHAuthorizedKeysConfigKey: strings.Join(sshSpec.AuthorizedKeys, "\n") + "\n",
		},
	}
}

func unitSSHAuthorizedKeysConfigMapName(instanceName string) string {
	return prefixedRuntimeName("ssh-keys-", instanceName)
}

func unitSSHAuthorizedKeysFilePath() string {
	return unitSSHAuthorizedKeysMountPath + "/" + unitSSHAuthorizedKeysConfigKey
}

func unitSSHAuthorizedKeysDigest(sshSpec runtimev1alpha1.GPUUnitSSHSpec) string {
	sum := sha256.Sum256([]byte(strings.Join(sshSpec.AuthorizedKeys, "\n")))
	return hex.EncodeToString(sum[:])
}
