package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	types "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestReconcileStockPoolStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme error: %v", err)
	}
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}

	pool := &runtimev1alpha1.StockPool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "StockPool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pool-a",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.StockPoolSpec{
			SpecName: "g1.1",
			Replicas: 3,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.StockPool{}).
		WithObjects(pool).
		Build()

	reconciler := &StockPoolReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "pool-a",
		},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "pool-pool-a"}, &dep); err != nil {
		t.Fatalf("deployment should be created: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 3 {
		t.Fatalf("unexpected deployment replicas: %+v", dep.Spec.Replicas)
	}

	_, err = reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "pool-a",
		},
	})
	if err != nil {
		t.Fatalf("second reconcile error: %v", err)
	}

	var got runtimev1alpha1.StockPool
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "pool-a"}, &got); err != nil {
		t.Fatalf("get stockpool error: %v", err)
	}

	if got.Status.Available != 0 {
		t.Fatalf("expected available=0 before deployment ready, got %d", got.Status.Available)
	}
	if got.Status.Allocated != 3 {
		t.Fatalf("expected allocated=3 before deployment ready, got %d", got.Status.Allocated)
	}
	if got.Status.Phase != runtimev1alpha1.PhaseProgressing {
		t.Fatalf("expected phase=%s, got %s", runtimev1alpha1.PhaseProgressing, got.Status.Phase)
	}
}

func TestReconcileNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme error: %v", err)
	}
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &StockPoolReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "missing",
		},
	})
	if err != nil {
		t.Fatalf("expected no error for missing resource, got %v", err)
	}
}
