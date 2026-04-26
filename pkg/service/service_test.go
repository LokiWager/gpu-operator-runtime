package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestService_Health_WithAndWithoutKube(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svcNoKube := New(nil, nil, logger)
	health, err := svcNoKube.Health(ctx)
	if err != nil {
		t.Fatalf("health no kube error: %v", err)
	}
	if health.KubernetesConnected {
		t.Fatalf("expected kube not connected")
	}

	fakeKube := k8sfake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n1",
			Labels: map[string]string{
				runtimev1alpha1.NVIDIAGPUProductLabelKey: "NVIDIA-L40S",
			},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName): *resource.NewQuantity(4, resource.DecimalSI),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName): *resource.NewQuantity(3, resource.DecimalSI),
			},
			Conditions: []corev1.NodeCondition{{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionTrue,
			}},
		},
	})
	svcWithKube := New(fakeKube, nil, logger)
	health, err = svcWithKube.Health(ctx)
	if err != nil {
		t.Fatalf("health with kube error: %v", err)
	}
	if !health.KubernetesConnected {
		t.Fatalf("expected kube connected")
	}
	if health.NodeCount != 1 {
		t.Fatalf("expected node count 1, got %d", health.NodeCount)
	}
	if health.ReadyNodeCount != 1 {
		t.Fatalf("expected ready node count 1, got %d", health.ReadyNodeCount)
	}
	if health.TotalGPUCapacity != 4 {
		t.Fatalf("expected gpu capacity 4, got %d", health.TotalGPUCapacity)
	}
	if health.TotalGPUAllocatable != 3 {
		t.Fatalf("expected gpu allocatable 3, got %d", health.TotalGPUAllocatable)
	}
	if len(health.GPUProducts) != 1 || health.GPUProducts[0].Product != "NVIDIA-L40S" {
		t.Fatalf("expected one gpu product summary, got %+v", health.GPUProducts)
	}
	if health.UptimeSeconds < 0 {
		t.Fatalf("unexpected uptime seconds: %d", health.UptimeSeconds)
	}
}
