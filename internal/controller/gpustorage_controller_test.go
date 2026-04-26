package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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

func TestReconcileGPUStorageBoundPVCStartsPrepareJob(t *testing.T) {
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
			Prepare: runtimev1alpha1.GPUStoragePrepareSpec{
				FromImage: "busybox:1.36",
				Command:   []string{"sh", "-c"},
				Args:      []string{"echo seeded > /workspace/README.txt"},
			},
		},
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
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

	digest, err := storagePrepareDigest(*storage)
	if err != nil {
		t.Fatalf("prepare digest error: %v", err)
	}
	jobName := storagePrepareJobName(storage.Name, digest)

	var job batchv1.Job
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: jobName}, &job); err != nil {
		t.Fatalf("expected prepare job to be created: %v", err)
	}

	var got runtimev1alpha1.GPUStorage
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}, &got); err != nil {
		t.Fatalf("get gpu storage error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.StoragePhasePending {
		t.Fatalf("expected pending storage phase, got %s", got.Status.Phase)
	}
	if got.Status.Prepare.Phase != runtimev1alpha1.StoragePreparePhasePending {
		t.Fatalf("expected pending prepare phase, got %+v", got.Status.Prepare)
	}
	if got.Status.Prepare.JobName != jobName {
		t.Fatalf("expected prepare job name %s, got %s", jobName, got.Status.Prepare.JobName)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionPrepared)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonStoragePreparePending {
		t.Fatalf("expected prepared pending condition, got %+v", cond)
	}
}

func TestReconcileGPUStorageAccessorUsesDufs(t *testing.T) {
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
			Accessor: runtimev1alpha1.GPUStorageAccessorSpec{
				Enabled: true,
			},
		},
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
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

	reconciler := &GPUStorageReconciler{
		Client: cl,
		Scheme: scheme,
	}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storageAccessorDeploymentName(storage.Name)}, &dep); err != nil {
		t.Fatalf("expected accessor deployment to be created: %v", err)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	if container.Image != runtimev1alpha1.DefaultStorageAccessorImage {
		t.Fatalf("expected dufs image %s, got %s", runtimev1alpha1.DefaultStorageAccessorImage, container.Image)
	}
	if len(container.Command) != 1 || container.Command[0] != "dufs" {
		t.Fatalf("expected dufs command, got %+v", container.Command)
	}
	if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("expected accessor to disable privilege escalation, got %+v", container.SecurityContext)
	}
	if container.SecurityContext.SeccompProfile == nil || container.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("expected accessor seccomp=RuntimeDefault, got %+v", container.SecurityContext)
	}

	var got runtimev1alpha1.GPUStorage
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}, &got); err != nil {
		t.Fatalf("get gpu storage error: %v", err)
	}
	if got.Status.Accessor.AccessURL != "http://storage-accessor-model-cache.runtime-instance.svc.cluster.local:5000/storage/runtime-instance/model-cache/" {
		t.Fatalf("expected internal accessor url, got %+v", got.Status.Accessor)
	}
}

func TestReconcileGPUStoragePrepareFailureMarksRecoveryRequired(t *testing.T) {
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
			Prepare: runtimev1alpha1.GPUStoragePrepareSpec{
				FromImage: "busybox:1.36",
				Command:   []string{"sh", "-c"},
				Args:      []string{"exit 1"},
			},
		},
	}
	digest, err := storagePrepareDigest(*storage)
	if err != nil {
		t.Fatalf("prepare digest error: %v", err)
	}
	jobName := storagePrepareJobName(storage.Name, digest)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: mustParseQuantity(t, "20Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Status: batchv1.JobStatus{
			Failed: 1,
			Conditions: []batchv1.JobCondition{{
				Type:    batchv1.JobFailed,
				Status:  corev1.ConditionTrue,
				Reason:  "BackoffLimitExceeded",
				Message: "Job has reached the specified backoff limit",
			}},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUStorage{}).
		WithObjects(storage, pvc, job).
		Build()

	reconciler := &GPUStorageReconciler{Client: cl, Scheme: scheme}
	_, err = reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got runtimev1alpha1.GPUStorage
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}, &got); err != nil {
		t.Fatalf("get gpu storage error: %v", err)
	}
	if got.Status.Phase != runtimev1alpha1.StoragePhaseFailed {
		t.Fatalf("expected failed storage phase, got %s", got.Status.Phase)
	}
	if got.Status.Prepare.Phase != runtimev1alpha1.StoragePreparePhaseFailed {
		t.Fatalf("expected failed prepare phase, got %+v", got.Status.Prepare)
	}
	if got.Status.Prepare.RecoveryPhase != runtimev1alpha1.StorageRecoveryPhaseRequired {
		t.Fatalf("expected recovery required, got %+v", got.Status.Prepare)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonStoragePrepareFailed {
		t.Fatalf("expected prepare failed ready condition, got %+v", cond)
	}
}

func TestReconcileGPUStorageCreatesAccessorResources(t *testing.T) {
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
			Accessor: runtimev1alpha1.GPUStorageAccessorSpec{
				Enabled: true,
			},
		},
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: mustParseQuantity(t, "20Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
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

	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storageAccessorDeploymentName(storage.Name)}, &dep); err != nil {
		t.Fatalf("expected accessor deployment to be created: %v", err)
	}
	var svc corev1.Service
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storageAccessorServiceName(storage.Name)}, &svc); err != nil {
		t.Fatalf("expected accessor service to be created: %v", err)
	}

	var got runtimev1alpha1.GPUStorage
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: storage.Namespace, Name: storage.Name}, &got); err != nil {
		t.Fatalf("get gpu storage error: %v", err)
	}
	if got.Status.Accessor.Phase != runtimev1alpha1.StorageAccessorPhasePending {
		t.Fatalf("expected pending accessor phase, got %+v", got.Status.Accessor)
	}
	if got.Status.Accessor.ServiceName != storageAccessorServiceName(storage.Name) {
		t.Fatalf("expected accessor service name to be reported, got %+v", got.Status.Accessor)
	}
}
