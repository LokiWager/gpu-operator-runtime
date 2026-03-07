package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
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

	fakeKube := k8sfake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}})
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
	if health.UptimeSeconds < 0 {
		t.Fatalf("unexpected uptime seconds: %d", health.UptimeSeconds)
	}
}
