package controller

import (
	"context"
	"net"
	"reflect"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

const kubeNamespaceMetadataLabelKey = "kubernetes.io/metadata.name"

func desiredUnitNetworkPolicy(instance runtimev1alpha1.GPUUnit, blockedEgressCIDRs []string) (*networkingv1.NetworkPolicy, error) {
	labels := unitObjectLabels(instance)
	normalizedBlockedCIDRs := normalizeBlockedEgressCIDRs(blockedEgressCIDRs)
	egressRules := []networkingv1.NetworkPolicyEgressRule{
		allowClusterDNSRule(),
		allowPublicEgressRule(normalizedBlockedCIDRs),
	}

	sshSpec, err := resolveUnitSSHSpec(instance)
	if err != nil {
		return nil, err
	}
	if sshRule, ok := explicitSSHServerEgressRule(sshSpec, normalizedBlockedCIDRs); ok {
		egressRules = append(egressRules, sshRule)
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkPolicyNameForUnit(instance.Name),
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					runtimev1alpha1.LabelUnitKey: instance.Name,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      egressRules,
		},
	}, nil
}

func (r *GPUUnitReconciler) reconcileGPUUnitNetworkPolicy(ctx context.Context, instance *runtimev1alpha1.GPUUnit) (bool, error) {
	desired, err := desiredUnitNetworkPolicy(*instance, r.BlockedEgressCIDRs)
	if err != nil {
		return false, err
	}

	var current networkingv1.NetworkPolicy
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
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
	if nextLabels, labelsChanged := replaceStringMap(current.Labels, desired.Labels); labelsChanged {
		current.Labels = nextLabels
		changed = true
	}
	if !reflect.DeepEqual(current.Spec, desired.Spec) {
		current.Spec = desired.Spec
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

func networkPolicyNameForUnit(instanceName string) string {
	return prefixedRuntimeName("egress-", instanceName)
}

func allowClusterDNSRule() networkingv1.NetworkPolicyEgressRule {
	tcp := corev1.ProtocolTCP
	udp := corev1.ProtocolUDP

	return networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					kubeNamespaceMetadataLabelKey: "kube-system",
				},
			},
		}},
		Ports: []networkingv1.NetworkPolicyPort{
			{
				Protocol: &tcp,
				Port:     ptr.To(intstr.FromInt(53)),
			},
			{
				Protocol: &udp,
				Port:     ptr.To(intstr.FromInt(53)),
			},
		},
	}
}

func allowPublicEgressRule(blockedEgressCIDRs []string) networkingv1.NetworkPolicyEgressRule {
	return networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{
				CIDR:   "0.0.0.0/0",
				Except: append([]string(nil), blockedEgressCIDRs...),
			},
		}},
	}
}

func explicitSSHServerEgressRule(
	sshSpec runtimev1alpha1.GPUUnitSSHSpec,
	blockedEgressCIDRs []string,
) (networkingv1.NetworkPolicyEgressRule, bool) {
	if !sshSpec.Enabled {
		return networkingv1.NetworkPolicyEgressRule{}, false
	}

	serverIP := net.ParseIP(strings.TrimSpace(sshSpec.ServerAddr))
	if serverIP == nil || !ipOverlapsBlockedCIDRs(serverIP, blockedEgressCIDRs) {
		return networkingv1.NetworkPolicyEgressRule{}, false
	}

	tcp := corev1.ProtocolTCP
	return networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{
				CIDR: singleIPCIDR(serverIP),
			},
		}},
		Ports: []networkingv1.NetworkPolicyPort{{
			Protocol: &tcp,
			Port:     ptr.To(intstr.FromInt(int(sshSpec.ServerPort))),
		}},
	}, true
}

func ipOverlapsBlockedCIDRs(ip net.IP, blockedEgressCIDRs []string) bool {
	for _, rawCIDR := range blockedEgressCIDRs {
		_, network, err := net.ParseCIDR(rawCIDR)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func singleIPCIDR(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}

func normalizeBlockedEgressCIDRs(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}
