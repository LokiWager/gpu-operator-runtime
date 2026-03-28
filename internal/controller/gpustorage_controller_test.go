package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestReconcileGPUStorageCreatesPVCAndMarksPending(t *testing.T) {
	scheme := newControllerScheme(t)

	storage := &runtimev1alpha1.GPUStorage{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUStorage",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUStorageSpec{
			Size: "20Gi",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUStorage{}).
		WithObjects(storage).
		Build()

	reconciler := &GPUStorageReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var pvc corev1.PersistentVolumeClaim
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}, &pvc); err != nil {
		t.Fatalf("expected pvc to be created: %v", err)
	}
	gotQty := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if got := gotQty.String(); got != "20Gi" {
		t.Fatalf("expected pvc request 20Gi, got %s", got)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != runtimev1alpha1.DefaultGPUStorageClassName {
		t.Fatalf("expected default storage class %s, got %+v", runtimev1alpha1.DefaultGPUStorageClassName, pvc.Spec.StorageClassName)
	}

	var got runtimev1alpha1.GPUStorage
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}, &got); err != nil {
		t.Fatalf("get gpu storage error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.StoragePhasePending {
		t.Fatalf("expected phase=%s, got %s", runtimev1alpha1.StoragePhasePending, got.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonStoragePending {
		t.Fatalf("expected pending condition, got %+v", cond)
	}
}

func mustParseQuantity(t *testing.T, raw string) resource.Quantity {
	t.Helper()

	qty, err := resource.ParseQuantity(raw)
	if err != nil {
		t.Fatalf("parse quantity %q: %v", raw, err)
	}
	return qty
}

func TestReconcileGPUStorageBoundPVCMarksReady(t *testing.T) {
	scheme := newControllerScheme(t)

	storage := &runtimev1alpha1.GPUStorage{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUStorage",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUStorageSpec{
			Size: "20Gi",
		},
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: func() *string {
				name := runtimev1alpha1.DefaultGPUStorageClassName
				return &name
			}(),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: mustParseQuantity(t, "20Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: mustParseQuantity(t, "20Gi"),
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUStorage{}).
		WithObjects(storage, pvc).
		Build()

	reconciler := &GPUStorageReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got runtimev1alpha1.GPUStorage
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}, &got); err != nil {
		t.Fatalf("get gpu storage error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.StoragePhaseReady {
		t.Fatalf("expected phase=%s, got %s", runtimev1alpha1.StoragePhaseReady, got.Status.Phase)
	}
	if got.Status.Capacity != "20Gi" {
		t.Fatalf("expected capacity 20Gi, got %s", got.Status.Capacity)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonStorageReady {
		t.Fatalf("expected ready condition, got %+v", cond)
	}
}
