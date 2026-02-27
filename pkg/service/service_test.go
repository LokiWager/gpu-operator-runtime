package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/loki/gpu-operator-runtime/pkg/store"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func newTestService() *Service {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(store.NewMemoryStore(), nil, logger)
}

func TestService_CreateDeleteVM_ReleasesStock(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	_, err := svc.CreateStocks(ctx, CreateStockRequest{Number: 1, SpecName: "g1.1"})
	if err != nil {
		t.Fatalf("create stocks error: %v", err)
	}

	vm, err := svc.CreateVM(ctx, CreateVMRequest{SpecName: "g1.1", TenantID: "t1"})
	if err != nil {
		t.Fatalf("create vm error: %v", err)
	}

	summary := svc.Summary()
	if summary.AvailableStocks != 0 || summary.AllocatedStocks != 1 || summary.RunningVMs != 1 {
		t.Fatalf("unexpected summary after create vm: %+v", summary)
	}

	if err := svc.DeleteVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete vm error: %v", err)
	}

	summary = svc.Summary()
	if summary.AvailableStocks != 1 || summary.AllocatedStocks != 0 || summary.RunningVMs != 0 {
		t.Fatalf("unexpected summary after delete vm: %+v", summary)
	}
}

func TestService_Health_WithAndWithoutKube(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svcNoKube := New(store.NewMemoryStore(), nil, logger)
	health, err := svcNoKube.Health(ctx)
	if err != nil {
		t.Fatalf("health no kube error: %v", err)
	}
	if health.KubernetesConnected {
		t.Fatalf("expected kube not connected")
	}

	svcWithKube := New(store.NewMemoryStore(), k8sfake.NewSimpleClientset(), logger)
	health, err = svcWithKube.Health(ctx)
	if err != nil {
		t.Fatalf("health with kube error: %v", err)
	}
	if !health.KubernetesConnected {
		t.Fatalf("expected kube connected")
	}
	if health.NodeCount != 0 {
		t.Fatalf("expected node count 0 for fake client, got %d", health.NodeCount)
	}
	if health.UptimeSeconds < 0 {
		t.Fatalf("unexpected uptime seconds: %d", health.UptimeSeconds)
	}
}
