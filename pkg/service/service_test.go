package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestService_HealthAndMetricsSnapshot_WithNvidiaTelemetry(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `# HELP DCGM_FI_DEV_FB_USED Used frame buffer memory
# TYPE DCGM_FI_DEV_FB_USED gauge
DCGM_FI_DEV_FB_USED{gpu="0",UUID="GPU-0",Hostname="node-a",modelName="NVIDIA-L40S"} 12000
DCGM_FI_DEV_FB_USED{gpu="1",UUID="GPU-1",Hostname="node-a",modelName="NVIDIA-L40S"} 8000
# HELP DCGM_FI_DEV_FB_FREE Free frame buffer memory
# TYPE DCGM_FI_DEV_FB_FREE gauge
DCGM_FI_DEV_FB_FREE{gpu="0",UUID="GPU-0",Hostname="node-a",modelName="NVIDIA-L40S"} 36000
DCGM_FI_DEV_FB_FREE{gpu="1",UUID="GPU-1",Hostname="node-a",modelName="NVIDIA-L40S"} 40000
# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-0",Hostname="node-a",modelName="NVIDIA-L40S"} 50
DCGM_FI_DEV_GPU_UTIL{gpu="1",UUID="GPU-1",Hostname="node-a",modelName="NVIDIA-L40S"} 25
`)
	}))
	defer server.Close()

	svc := New(nil, nil, logger)
	svc.ConfigureNvidiaMetrics(server.URL, server.Client())

	health, err := svc.Health(ctx)
	if err != nil {
		t.Fatalf("health with nvidia telemetry error: %v", err)
	}
	if !health.NvidiaMetricsConnected {
		t.Fatalf("expected nvidia metrics to be connected")
	}
	if health.GPUDeviceCount != 2 {
		t.Fatalf("expected gpu device count 2, got %d", health.GPUDeviceCount)
	}
	if health.UsedGPUMemoryMiB != 20000 {
		t.Fatalf("expected used gpu memory 20000 MiB, got %v", health.UsedGPUMemoryMiB)
	}
	if health.FreeGPUMemoryMiB != 76000 {
		t.Fatalf("expected free gpu memory 76000 MiB, got %v", health.FreeGPUMemoryMiB)
	}
	if health.TotalGPUMemoryMiB != 96000 {
		t.Fatalf("expected total gpu memory 96000 MiB, got %v", health.TotalGPUMemoryMiB)
	}
	if health.AverageGPUUtilizationPercent != 37.5 {
		t.Fatalf("expected average gpu utilization 37.5, got %v", health.AverageGPUUtilizationPercent)
	}

	snapshot, err := svc.RuntimeMetricsSnapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot with nvidia telemetry error: %v", err)
	}
	if len(snapshot.GPUDevices) != 2 {
		t.Fatalf("expected two gpu devices in snapshot, got %+v", snapshot.GPUDevices)
	}
	if snapshot.GPUDevices[0].NodeName != "node-a" || snapshot.GPUDevices[0].GPU != "0" {
		t.Fatalf("unexpected first gpu device %+v", snapshot.GPUDevices[0])
	}
	if snapshot.GPUDevices[0].UsedMemoryMiB != 12000 || snapshot.GPUDevices[0].TotalMemoryMiB != 48000 {
		t.Fatalf("unexpected first gpu memory snapshot %+v", snapshot.GPUDevices[0])
	}
}
