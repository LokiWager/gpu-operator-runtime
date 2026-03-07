package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	types "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

func TestService_CreateStockPoolAsync(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.StockPool{}).
		Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(nil, cl, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.StartOperatorJobWorker(ctx)

	job, err := svc.CreateStockPoolAsync(ctx, CreateStockPoolRequest{
		Name:      "pool-g1",
		Namespace: "default",
		SpecName:  "g1.1",
		Image:     "nginx:1.27",
		Memory:    "16Gi",
		GPU:       1,
		Replicas:  2,
	})
	if err != nil {
		t.Fatalf("create async job error: %v", err)
	}

	var done bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.GetOperatorJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("get job error: %v", err)
		}
		if got.Status == domain.OperatorJobSucceeded {
			done = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !done {
		t.Fatalf("job did not reach succeeded status in time")
	}

	var pool runtimev1alpha1.StockPool
	if err := cl.Get(ctx, types.NamespacedName{Name: "pool-g1", Namespace: "default"}, &pool); err != nil {
		t.Fatalf("get stockpool error: %v", err)
	}
	if pool.Spec.Replicas != 2 {
		t.Fatalf("expected replicas=2, got %d", pool.Spec.Replicas)
	}
	if pool.Spec.Image != "nginx:1.27" {
		t.Fatalf("expected image=nginx:1.27, got %s", pool.Spec.Image)
	}
	if pool.Spec.Memory != "16Gi" {
		t.Fatalf("expected memory=16Gi, got %s", pool.Spec.Memory)
	}
	if pool.Spec.GPU != 1 {
		t.Fatalf("expected gpu=1, got %d", pool.Spec.GPU)
	}
}
