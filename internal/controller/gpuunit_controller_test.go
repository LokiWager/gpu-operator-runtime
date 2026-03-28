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

func TestReconcileStockGPUUnitCreatesDeploymentWithoutService(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stock-pool-a-1",
			Namespace: runtimev1alpha1.DefaultStockNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: "g1.1",
			Image:    "python:3.12",
			Memory:   "16Gi",
			GPU:      1,
			Template: runtimev1alpha1.GPUUnitTemplate{
				Command: []string{"python"},
				Args:    []string{"-m", "http.server", "8080"},
				Ports: []runtimev1alpha1.GPUUnitPortSpec{{
					Name: "http",
					Port: 8080,
				}},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance).
		Build()

	reconciler := &GPUUnitReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: deploymentNameForUnit(instance.Name)}, &dep); err != nil {
		t.Fatalf("deployment should be created: %v", err)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	if container.Image != "python:3.12" {
		t.Fatalf("expected stock image to be applied, got %s", container.Image)
	}

	var svc corev1.Service
	err = cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: serviceNameForUnit(instance.Name)}, &svc)
	if err == nil {
		t.Fatalf("stock unit should not have a service")
	}

	var got runtimev1alpha1.GPUUnit
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, &got); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.PhaseProgressing {
		t.Fatalf("expected phase=%s, got %s", runtimev1alpha1.PhaseProgressing, got.Status.Phase)
	}
	if got.Status.ServiceName != "" || got.Status.AccessURL != "" {
		t.Fatalf("stock unit should not publish service details")
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonStockNotReady {
		t.Fatalf("expected stock ready condition, got %+v", cond)
	}
}

func TestReconcileInstanceGPUUnitCreatesDeploymentAndService(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-instance",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: "g1.1",
			Image:    "python:3.12",
			Memory:   "16Gi",
			GPU:      1,
			Template: runtimev1alpha1.GPUUnitTemplate{
				Command: []string{"python"},
				Args:    []string{"-m", "http.server", "8080"},
				Envs: []runtimev1alpha1.GPUUnitEnvVar{{
					Name:  "MODEL_ID",
					Value: "demo",
				}},
				Ports: []runtimev1alpha1.GPUUnitPortSpec{{
					Name: "http",
					Port: 8080,
				}},
			},
			Access: runtimev1alpha1.GPUUnitAccess{
				PrimaryPort: "http",
				Scheme:      "http",
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance).
		Build()

	reconciler := &GPUUnitReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: deploymentNameForUnit(instance.Name)}, &dep); err != nil {
		t.Fatalf("deployment should be created: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Fatalf("expected deployment replicas=1, got %+v", dep.Spec.Replicas)
	}

	var svc corev1.Service
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: serviceNameForUnit(instance.Name)}, &svc); err != nil {
		t.Fatalf("service should be created: %v", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 {
		t.Fatalf("expected service port 8080, got %+v", svc.Spec.Ports)
	}

	var got runtimev1alpha1.GPUUnit
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, &got); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.PhaseProgressing {
		t.Fatalf("expected phase=%s, got %s", runtimev1alpha1.PhaseProgressing, got.Status.Phase)
	}
	if got.Status.ServiceName != serviceNameForUnit(instance.Name) {
		t.Fatalf("expected service name to be reported, got %s", got.Status.ServiceName)
	}
	expectedURL := "http://unit-demo-instance.runtime-instance.svc.cluster.local:8080"
	if got.Status.AccessURL != expectedURL {
		t.Fatalf("expected access url %s, got %s", expectedURL, got.Status.AccessURL)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonUnitProgressing {
		t.Fatalf("expected progressing ready condition, got %+v", cond)
	}
}

func TestReconcileInstanceGPUUnitMountsReadyStorage(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-instance",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: "g1.1",
			Image:    "python:3.12",
			Memory:   "16Gi",
			Template: runtimev1alpha1.GPUUnitTemplate{
				Ports: []runtimev1alpha1.GPUUnitPortSpec{{
					Name: "http",
					Port: 8080,
				}},
			},
			Access: runtimev1alpha1.GPUUnitAccess{
				PrimaryPort: "http",
				Scheme:      "http",
			},
			StorageMounts: []runtimev1alpha1.GPUUnitStorageMount{{
				Name:      "model-cache",
				MountPath: "/data",
			}},
		},
	}
	storage := &runtimev1alpha1.GPUStorage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUStorageSpec{Size: "20Gi"},
		Status: runtimev1alpha1.GPUStorageStatus{
			Phase: runtimev1alpha1.StoragePhaseReady,
			Conditions: []metav1.Condition{{
				Type:    runtimev1alpha1.ConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  runtimev1alpha1.ReasonStorageReady,
				Message: runtimev1alpha1.StatusMessageStorageReady,
			}},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance, storage).
		Build()

	reconciler := &GPUUnitReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: deploymentNameForUnit(instance.Name)}, &dep); err != nil {
		t.Fatalf("deployment should be created: %v", err)
	}
	if len(dep.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("expected one pvc-backed volume, got %+v", dep.Spec.Template.Spec.Volumes)
	}
	if dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim == nil || dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "model-cache" {
		t.Fatalf("expected pvc claim model-cache, got %+v", dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim)
	}
	if len(dep.Spec.Template.Spec.Containers[0].VolumeMounts) != 1 || dep.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath != "/data" {
		t.Fatalf("expected volume mount /data, got %+v", dep.Spec.Template.Spec.Containers[0].VolumeMounts)
	}
}

func TestReconcileInstanceGPUUnitPendingStorageMarksStatusWaiting(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-instance",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: "g1.1",
			Image:    "python:3.12",
			Memory:   "16Gi",
			StorageMounts: []runtimev1alpha1.GPUUnitStorageMount{{
				Name:      "model-cache",
				MountPath: "/data",
			}},
		},
	}
	storage := &runtimev1alpha1.GPUStorage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUStorageSpec{Size: "20Gi"},
		Status: runtimev1alpha1.GPUStorageStatus{
			Phase: runtimev1alpha1.StoragePhasePending,
			Conditions: []metav1.Condition{{
				Type:    runtimev1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  runtimev1alpha1.ReasonStoragePending,
				Message: runtimev1alpha1.StatusMessageStoragePending,
			}},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance, storage).
		Build()

	reconciler := &GPUUnitReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got runtimev1alpha1.GPUUnit
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, &got); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonStorageNotReady {
		t.Fatalf("expected storage-not-ready condition, got %+v", cond)
	}
}

func TestReconcileGPUUnitInvalidAccessMarksStatusFailed(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-instance",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: "g1.1",
			Image:    "python:3.12",
			Memory:   "16Gi",
			Template: runtimev1alpha1.GPUUnitTemplate{
				Ports: []runtimev1alpha1.GPUUnitPortSpec{{
					Name: "http",
					Port: 8080,
				}},
			},
			Access: runtimev1alpha1.GPUUnitAccess{
				PrimaryPort: "missing",
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance).
		Build()

	reconciler := &GPUUnitReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name},
	})
	if err != nil {
		t.Fatalf("reconcile should surface invalid access through status, got error: %v", err)
	}

	var got runtimev1alpha1.GPUUnit
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, &got); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.PhaseFailed {
		t.Fatalf("expected phase failed, got %s", got.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonAccessConfigInvalid {
		t.Fatalf("expected access config invalid condition, got %+v", cond)
	}
}

func TestReconcileStockGPUUnitPodFailureMarksStatusFailed(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stock-pool-a-1",
			Namespace: runtimev1alpha1.DefaultStockNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: "g1.1",
			Image:    "python:3.12",
			Memory:   "16Gi",
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stock-pool-a-1-abc123",
			Namespace: instance.Namespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelUnitKey: instance.Name,
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
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance, pod).
		Build()

	reconciler := &GPUUnitReconciler{Client: cl, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got runtimev1alpha1.GPUUnit
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, &got); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.PhaseFailed {
		t.Fatalf("expected phase=%s, got %s", runtimev1alpha1.PhaseFailed, got.Status.Phase)
	}
	if got.Status.ServiceName != "" || got.Status.AccessURL != "" {
		t.Fatalf("stock failure should not leak service details")
	}

	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil {
		t.Fatalf("expected ready condition to be set")
	}
	if cond.Reason != runtimev1alpha1.ReasonPodStartupFailed {
		t.Fatalf("expected reason=%s, got %s", runtimev1alpha1.ReasonPodStartupFailed, cond.Reason)
	}
	expected := "Pod stock-pool-a-1-abc123 container runtime-worker: model initialization failed: missing weights"
	if cond.Message != expected {
		t.Fatalf("unexpected pod failure message: %s", cond.Message)
	}
}
