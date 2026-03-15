package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	types "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func newControllerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme error: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme error: %v", err)
	}
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add runtime scheme error: %v", err)
	}
	return scheme
}

func TestReconcileStockPoolCreatesDeploymentAndService(t *testing.T) {
	scheme := newControllerScheme(t)

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
			Image:    "python:3.12",
			Memory:   "16Gi",
			GPU:      2,
			Replicas: 3,
			Template: runtimev1alpha1.StockPoolTemplate{
				Command: []string{"python"},
				Args:    []string{"-m", "http.server", "8080"},
				Envs: []runtimev1alpha1.StockPoolEnvVar{{
					Name:  "MODEL_ID",
					Value: "demo",
				}},
				Ports: []runtimev1alpha1.StockPoolPortSpec{{
					Name: "http",
					Port: 8080,
				}},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.StockPool{}).
		WithObjects(pool).
		Build()

	reconciler := &StockPoolReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "pool-a"},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: deploymentNameForPool(pool.Name)}, &dep); err != nil {
		t.Fatalf("deployment should be created: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 3 {
		t.Fatalf("unexpected deployment replicas: %+v", dep.Spec.Replicas)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	if container.Name != runtimev1alpha1.RuntimeWorkerContainerName {
		t.Fatalf("expected %s container, got %s", runtimev1alpha1.RuntimeWorkerContainerName, container.Name)
	}
	if container.Command[0] != "python" {
		t.Fatalf("expected runtime command to be applied")
	}
	if container.Args[2] != "8080" {
		t.Fatalf("expected runtime args to be applied")
	}
	if len(container.Ports) != 1 || container.Ports[0].ContainerPort != 8080 {
		t.Fatalf("expected runtime port 8080, got %+v", container.Ports)
	}

	var svc corev1.Service
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: serviceNameForPool(pool.Name)}, &svc); err != nil {
		t.Fatalf("service should be created: %v", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 {
		t.Fatalf("expected service port 8080, got %+v", svc.Spec.Ports)
	}

	var got runtimev1alpha1.StockPool
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "pool-a"}, &got); err != nil {
		t.Fatalf("get stockpool error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.PhaseProgressing {
		t.Fatalf("expected phase=%s, got %s", runtimev1alpha1.PhaseProgressing, got.Status.Phase)
	}
	if got.Status.ServiceName != serviceNameForPool(pool.Name) {
		t.Fatalf("expected service name to be reported, got %s", got.Status.ServiceName)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonDeploymentProgressing {
		t.Fatalf("expected progressing ready condition, got %+v", cond)
	}
}

func TestReconcileStockPoolInvalidSpecMarksStatusFailed(t *testing.T) {
	scheme := newControllerScheme(t)

	pool := &runtimev1alpha1.StockPool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "StockPool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pool-invalid",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.StockPoolSpec{
			SpecName: "g1.1",
			Memory:   "not-a-quantity",
			Replicas: 1,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.StockPool{}).
		WithObjects(pool).
		Build()

	reconciler := &StockPoolReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "pool-invalid"},
	})
	if err != nil {
		t.Fatalf("reconcile should surface invalid spec through status, got error: %v", err)
	}

	var got runtimev1alpha1.StockPool
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "pool-invalid"}, &got); err != nil {
		t.Fatalf("get stockpool error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.PhaseFailed {
		t.Fatalf("expected phase failed, got %s", got.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonInvalidSpec {
		t.Fatalf("expected invalid spec condition, got %+v", cond)
	}

	var dep appsv1.Deployment
	err = cl.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: deploymentNameForPool(pool.Name)}, &dep)
	if err == nil {
		t.Fatalf("deployment should not be created for invalid spec")
	}
}

func TestReconcileStockPoolPodFailureMessageMarksStatusFailed(t *testing.T) {
	scheme := newControllerScheme(t)

	pool := &runtimev1alpha1.StockPool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "StockPool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pool-crash",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.StockPoolSpec{
			SpecName: "g1.1",
			Image:    "python:3.12",
			Replicas: 1,
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pool-crash-abc123",
			Namespace: "default",
			Labels: map[string]string{
				runtimev1alpha1.LabelPoolKey: pool.Name,
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: runtimev1alpha1.RuntimeWorkerContainerName,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{
						Reason:  "CrashLoopBackOff",
						Message: "back-off restarting failed container",
					},
				},
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 1,
						Message:  "model initialization failed: missing weights",
					},
				},
			}},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.StockPool{}).
		WithObjects(pool, pod).
		Build()

	reconciler := &StockPoolReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: pool.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got runtimev1alpha1.StockPool
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: pool.Namespace, Name: pool.Name}, &got); err != nil {
		t.Fatalf("get stockpool error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.PhaseFailed {
		t.Fatalf("expected phase=%s, got %s", runtimev1alpha1.PhaseFailed, got.Status.Phase)
	}

	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil {
		t.Fatalf("expected ready condition to be set")
	}
	if cond.Reason != runtimev1alpha1.ReasonPodStartupFailed {
		t.Fatalf("expected reason=%s, got %s", runtimev1alpha1.ReasonPodStartupFailed, cond.Reason)
	}
	if cond.Message != "Pod pool-crash-abc123 container runtime-worker: model initialization failed: missing weights" {
		t.Fatalf("unexpected pod failure message: %s", cond.Message)
	}
}

func TestReconcileNotFound(t *testing.T) {
	scheme := newControllerScheme(t)

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
