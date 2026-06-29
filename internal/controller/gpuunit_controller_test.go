package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	types "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	appconfig "github.com/loki/gpu-operator-runtime/pkg/config"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

func newControllerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme error: %v", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add batch scheme error: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme error: %v", err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add networking scheme error: %v", err)
	}
	if err := resourcev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add resource scheme error: %v", err)
	}
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add runtime scheme error: %v", err)
	}
	return scheme
}

func newGPUUnitReconciler(cl ctrlclient.Client, scheme *runtime.Scheme) *GPUUnitReconciler {
	workerCfg, err := appconfig.DefaultManagerConfig().ServerlessWorker.Normalized()
	if err != nil {
		panic(err)
	}
	return &GPUUnitReconciler{
		Client:                cl,
		Scheme:                scheme,
		BlockedEgressCIDRs:    append([]string(nil), appconfig.DefaultManagerConfig().BlockedEgressCIDRs...),
		ServerlessQueueConfig: appconfig.DefaultManagerConfig().Serverless,
		ServerlessWorker:      workerCfg,
	}
}

func testDRAAllocation(count int64) runtimev1alpha1.GPUUnitAllocationSpec {
	return runtimev1alpha1.GPUUnitAllocationSpec{
		DeviceClassName:  "nvidia-rtx-3080",
		ClaimRequestName: runtimev1alpha1.UnitDRAClaimRequestName,
		Count:            count,
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
			SpecName:   "g1.1",
			Image:      "python:3.12",
			Memory:     "16Gi",
			GPU:        1,
			Allocation: testDRAAllocation(1),
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

	reconciler := newGPUUnitReconciler(cl, scheme)
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
	runtimeContainer := dep.Spec.Template.Spec.Containers[0]
	if runtimeContainer.SecurityContext == nil || runtimeContainer.SecurityContext.AllowPrivilegeEscalation == nil || *runtimeContainer.SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("expected runtime container to disable privilege escalation, got %+v", runtimeContainer.SecurityContext)
	}
	if runtimeContainer.SecurityContext.SeccompProfile == nil || runtimeContainer.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("expected runtime container seccomp=RuntimeDefault, got %+v", runtimeContainer.SecurityContext)
	}
	foundShmMount := false
	for _, mount := range runtimeContainer.VolumeMounts {
		if mount.Name == unitSharedMemoryVolumeName && mount.MountPath == unitSharedMemoryMountPath {
			foundShmMount = true
		}
	}
	if !foundShmMount {
		t.Fatalf("expected runtime container to mount %s, got %+v", unitSharedMemoryMountPath, runtimeContainer.VolumeMounts)
	}
	foundShmVolume := false
	for _, volume := range dep.Spec.Template.Spec.Volumes {
		if volume.Name != unitSharedMemoryVolumeName || volume.EmptyDir == nil {
			continue
		}
		foundShmVolume = true
		if volume.EmptyDir.Medium != corev1.StorageMediumMemory {
			t.Fatalf("expected shm volume to use memory medium, got %+v", volume.EmptyDir)
		}
		if volume.EmptyDir.SizeLimit == nil || volume.EmptyDir.SizeLimit.String() != "8Gi" {
			t.Fatalf("expected shm volume size limit 8Gi, got %+v", volume.EmptyDir.SizeLimit)
		}
	}
	if !foundShmVolume {
		t.Fatalf("expected shm volume, got %+v", dep.Spec.Template.Spec.Volumes)
	}

	var svc corev1.Service
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: serviceNameForUnit(instance.Name)}, &svc); err != nil {
		t.Fatalf("service should be created: %v", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 {
		t.Fatalf("expected service port 8080, got %+v", svc.Spec.Ports)
	}

	var networkPolicy networkingv1.NetworkPolicy
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: networkPolicyNameForUnit(instance.Name)}, &networkPolicy); err != nil {
		t.Fatalf("network policy should be created: %v", err)
	}
	if len(networkPolicy.Spec.Egress) < 2 {
		t.Fatalf("expected dns and public egress rules, got %+v", networkPolicy.Spec.Egress)
	}
	foundBlockedRanges := map[string]bool{
		"10.0.0.0/8":     false,
		"100.64.0.0/10":  false,
		"169.254.0.0/16": false,
		"172.16.0.0/12":  false,
		"192.168.0.0/16": false,
	}
	for _, rule := range networkPolicy.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock == nil || peer.IPBlock.CIDR != "0.0.0.0/0" {
				continue
			}
			for _, blockedCIDR := range peer.IPBlock.Except {
				if _, ok := foundBlockedRanges[blockedCIDR]; ok {
					foundBlockedRanges[blockedCIDR] = true
				}
			}
		}
	}
	for blockedCIDR, found := range foundBlockedRanges {
		if !found {
			t.Fatalf("expected blocked cidr %s in egress policy, got %+v", blockedCIDR, networkPolicy.Spec.Egress)
		}
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

func TestReconcileInstanceGPUUnitCreatesDRAClaimAndClaimBackedPod(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-dra",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			PackageID: "gpu-rtx3080-2x-cpu10-mem40g",
			SpecName:  "gpu.rtx3080.2x.10c.40g",
			Image:     "pytorch:2.6",
			CPU:       "10",
			Memory:    "40Gi",
			GPU:       2,
			Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
				DeviceClassName:  "nvidia-rtx-3080",
				ClaimName:        "unit-demo-dra-gpu",
				ClaimRequestName: runtimev1alpha1.UnitDRAClaimRequestName,
				Count:            2,
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithStatusSubresource(&resourcev1.ResourceClaim{}).
		WithObjects(instance).
		Build()

	reconciler := newGPUUnitReconciler(cl, scheme)
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name},
	})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var claim resourcev1.ResourceClaim
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: "unit-demo-dra-gpu"}, &claim); err != nil {
		t.Fatalf("resource claim should be created: %v", err)
	}
	if len(claim.Spec.Devices.Requests) != 1 {
		t.Fatalf("expected one DRA request, got %+v", claim.Spec.Devices.Requests)
	}
	request := claim.Spec.Devices.Requests[0]
	if request.Name != runtimev1alpha1.UnitDRAClaimRequestName || request.Exactly == nil {
		t.Fatalf("expected exact gpu request, got %+v", request)
	}
	if request.Exactly.DeviceClassName != "nvidia-rtx-3080" || request.Exactly.Count != 2 {
		t.Fatalf("expected DRA device class/count, got %+v", request.Exactly)
	}

	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: deploymentNameForUnit(instance.Name)}, &dep); err != nil {
		t.Fatalf("deployment should be created: %v", err)
	}
	if len(dep.Spec.Template.Spec.ResourceClaims) != 1 {
		t.Fatalf("expected pod resource claim, got %+v", dep.Spec.Template.Spec.ResourceClaims)
	}
	podClaim := dep.Spec.Template.Spec.ResourceClaims[0]
	if podClaim.Name != runtimev1alpha1.UnitDRAClaimName || podClaim.ResourceClaimName == nil || *podClaim.ResourceClaimName != "unit-demo-dra-gpu" {
		t.Fatalf("unexpected pod resource claim %+v", podClaim)
	}
	runtimeContainer := dep.Spec.Template.Spec.Containers[0]
	if len(runtimeContainer.Resources.Claims) != 1 {
		t.Fatalf("expected runtime container claim reference, got %+v", runtimeContainer.Resources.Claims)
	}
	containerClaim := runtimeContainer.Resources.Claims[0]
	if containerClaim.Name != runtimev1alpha1.UnitDRAClaimName || containerClaim.Request != runtimev1alpha1.UnitDRAClaimRequestName {
		t.Fatalf("unexpected runtime container claim %+v", containerClaim)
	}
	if _, ok := runtimeContainer.Resources.Requests[corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName)]; ok {
		t.Fatalf("DRA mode must not keep traditional gpu resource requests: %+v", runtimeContainer.Resources.Requests)
	}
	cpuRequest := runtimeContainer.Resources.Requests[corev1.ResourceCPU]
	if cpuRequest.String() != "10" {
		t.Fatalf("expected cpu request 10, got %+v", runtimeContainer.Resources.Requests)
	}
	memoryRequest := runtimeContainer.Resources.Requests[corev1.ResourceMemory]
	if memoryRequest.String() != "40Gi" {
		t.Fatalf("expected memory request 40Gi, got %+v", runtimeContainer.Resources.Requests)
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
			SpecName:   "g1.1",
			Image:      "python:3.12",
			Memory:     "16Gi",
			GPU:        1,
			Allocation: testDRAAllocation(1),
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

	reconciler := newGPUUnitReconciler(cl, scheme)
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
	foundPVCVolume := false
	for _, volume := range dep.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == "model-cache" {
			foundPVCVolume = true
		}
	}
	if !foundPVCVolume {
		t.Fatalf("expected pvc claim model-cache, got %+v", dep.Spec.Template.Spec.Volumes)
	}
	foundDataMount := false
	for _, mount := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
		if mount.MountPath == "/data" {
			foundDataMount = true
		}
	}
	if !foundDataMount {
		t.Fatalf("expected volume mount /data, got %+v", dep.Spec.Template.Spec.Containers[0].VolumeMounts)
	}
}

func TestReconcileInstanceGPUUnitAddsSSHSidecarsAndStatus(t *testing.T) {
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
			SpecName:   "g1.1",
			Image:      "python:3.12",
			Memory:     "16Gi",
			GPU:        1,
			Allocation: testDRAAllocation(1),
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
			SSH: runtimev1alpha1.GPUUnitSSHSpec{
				Enabled:        true,
				Username:       "runtime",
				AuthorizedKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== demo@example"},
				ServerAddr:     "frps.internal",
				ServerPort:     7000,
				ConnectHost:    "ssh.example.com",
				ConnectPort:    1337,
				DomainSuffix:   "ssh.example.com",
				Token:          "secret",
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance).
		Build()

	reconciler := newGPUUnitReconciler(cl, scheme)
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
	var sshKeysConfig corev1.ConfigMap
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: unitSSHAuthorizedKeysConfigMapName(instance.Name)}, &sshKeysConfig); err != nil {
		t.Fatalf("ssh authorized keys config should be created: %v", err)
	}
	expectedAuthorizedKeys := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== demo@example\n"
	if sshKeysConfig.Data[unitSSHAuthorizedKeysConfigKey] != expectedAuthorizedKeys {
		t.Fatalf("expected authorized_keys config %q, got %+v", expectedAuthorizedKeys, sshKeysConfig.Data)
	}
	if len(dep.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected one runtime container, got %+v", dep.Spec.Template.Spec.Containers)
	}
	if dep.Spec.Template.Spec.Containers[0].Name != runtimev1alpha1.RuntimeWorkerContainerName {
		t.Fatalf("expected runtime container %s, got %+v", runtimev1alpha1.RuntimeWorkerContainerName, dep.Spec.Template.Spec.Containers)
	}
	if len(dep.Spec.Template.Spec.InitContainers) != 2 {
		t.Fatalf("expected ssh sidecars to run as restartable init containers, got %+v", dep.Spec.Template.Spec.InitContainers)
	}

	foundSSH := false
	foundFRP := false
	for _, container := range dep.Spec.Template.Spec.InitContainers {
		switch container.Name {
		case runtimev1alpha1.UnitSSHContainerName:
			foundSSH = true
			if container.StartupProbe == nil || container.StartupProbe.TCPSocket == nil || container.StartupProbe.TCPSocket.Port.IntVal != runtimev1alpha1.DefaultUnitSSHPort {
				t.Fatalf("expected ssh init sidecar startup probe on port %d, got %+v", runtimev1alpha1.DefaultUnitSSHPort, container.StartupProbe)
			}
			env := map[string]string{}
			for _, item := range container.Env {
				env[item.Name] = item.Value
			}
			if env[unitSSHAuthorizedKeysEnvName] != unitSSHAuthorizedKeysFilePath() {
				t.Fatalf("expected %s=%s, got %+v", unitSSHAuthorizedKeysEnvName, unitSSHAuthorizedKeysFilePath(), container.Env)
			}
			if env[unitSSHAuthorizedKeysDigestEnv] == "" {
				t.Fatalf("expected %s to be populated, got %+v", unitSSHAuthorizedKeysDigestEnv, container.Env)
			}
			foundAuthorizedKeysMount := false
			for _, mount := range container.VolumeMounts {
				if mount.Name == unitSSHAuthorizedKeysVolumeName && mount.MountPath == unitSSHAuthorizedKeysMountPath && mount.ReadOnly {
					foundAuthorizedKeysMount = true
				}
			}
			if !foundAuthorizedKeysMount {
				t.Fatalf("expected authorized_keys volume mount, got %+v", container.VolumeMounts)
			}
		case runtimev1alpha1.UnitSSHFRPContainerName:
			foundFRP = true
		}
		if container.RestartPolicy == nil || *container.RestartPolicy != corev1.ContainerRestartPolicyAlways {
			t.Fatalf("expected init container %s restartPolicy=Always, got %+v", container.Name, container.RestartPolicy)
		}
	}
	if !foundSSH || !foundFRP {
		t.Fatalf("expected ssh and frpc init containers, got %+v", dep.Spec.Template.Spec.InitContainers)
	}
	foundAuthorizedKeysVolume := false
	for _, volume := range dep.Spec.Template.Spec.Volumes {
		if volume.Name == unitSSHAuthorizedKeysVolumeName &&
			volume.ConfigMap != nil &&
			volume.ConfigMap.LocalObjectReference.Name == unitSSHAuthorizedKeysConfigMapName(instance.Name) {
			foundAuthorizedKeysVolume = true
		}
	}
	if !foundAuthorizedKeysVolume {
		t.Fatalf("expected authorized_keys config volume, got %+v", dep.Spec.Template.Spec.Volumes)
	}

	var got runtimev1alpha1.GPUUnit
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, &got); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	if got.Status.SSH.Phase != runtimev1alpha1.UnitSSHPhasePending {
		t.Fatalf("expected pending ssh phase, got %+v", got.Status.SSH)
	}
	expectedCommand := "ssh -o ProxyCommand='nc -X connect -x ssh.example.com:1337 %h %p' runtime@demo-instance.runtime-instance.ssh.example.com"
	if got.Status.SSH.AccessCommand != expectedCommand {
		t.Fatalf("expected ssh access command %q, got %q", expectedCommand, got.Status.SSH.AccessCommand)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionSSHReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonUnitSSHPending {
		t.Fatalf("expected pending ssh condition, got %+v", cond)
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
			SpecName:   "g1.1",
			Image:      "python:3.12",
			Memory:     "16Gi",
			GPU:        1,
			Allocation: testDRAAllocation(1),
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

	reconciler := newGPUUnitReconciler(cl, scheme)
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
			SpecName:   "g1.1",
			Image:      "python:3.12",
			Memory:     "16Gi",
			GPU:        1,
			Allocation: testDRAAllocation(1),
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

	reconciler := newGPUUnitReconciler(cl, scheme)
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

func TestReconcileInstanceGPUUnitInitSidecarFailureMarksStatusFailed(t *testing.T) {
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
			SpecName:   "g1.1",
			Image:      "python:3.12",
			Memory:     "16Gi",
			GPU:        1,
			Allocation: testDRAAllocation(1),
			SSH: runtimev1alpha1.GPUUnitSSHSpec{
				Enabled:        true,
				Username:       "runtime",
				AuthorizedKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== demo@example"},
				ServerAddr:     "frps.internal",
				ServerPort:     7000,
				ConnectHost:    "ssh.example.com",
				ConnectPort:    1337,
				DomainSuffix:   "ssh.example.com",
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-instance-abc123",
			Namespace: instance.Namespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelUnitKey: instance.Name,
			},
		},
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{{
				Name: runtimev1alpha1.UnitSSHContainerName,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{
						Reason:  "CrashLoopBackOff",
						Message: "back-off restarting failed container",
					},
				},
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 1,
						Message:  "sshd failed to load authorized keys",
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

	reconciler := newGPUUnitReconciler(cl, scheme)
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
	readyCond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Reason != runtimev1alpha1.ReasonPodStartupFailed {
		t.Fatalf("expected pod startup failure condition, got %+v", readyCond)
	}
	expectedReadyMessage := "Pod demo-instance-abc123 container ssh-server: sshd failed to load authorized keys"
	if readyCond.Message != expectedReadyMessage {
		t.Fatalf("unexpected ready condition message: %s", readyCond.Message)
	}
	sshCond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionSSHReady)
	if sshCond == nil || sshCond.Reason != runtimev1alpha1.ReasonUnitSSHFailed {
		t.Fatalf("expected ssh failure condition, got %+v", sshCond)
	}
	if sshCond.Message != expectedReadyMessage {
		t.Fatalf("unexpected ssh condition message: %s", sshCond.Message)
	}
}

func TestReconcileInstanceGPUUnitInjectsServerlessSidecar(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sd-webui-template",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName:   "g1.1",
			Image:      "python:3.12",
			Memory:     "16Gi",
			GPU:        1,
			Allocation: testDRAAllocation(1),
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
			Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
				RequestID: "sd-webui",
				Framework: runtimev1alpha1.GPUUnitServerlessFrameworkSpec{
					SocketPath: runtimev1alpha1.DefaultServerlessFrameworkSocketPath,
					InvokePath: "/invoke",
					HealthPath: "/healthz",
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance).
		Build()

	reconciler := newGPUUnitReconciler(cl, scheme)
	reconciler.ServerlessQueueConfig.URL = "nats://nats.messaging.svc.cluster.local:4222"
	reconciler.ServerlessQueueConfig.NetworkPolicyTarget.Namespace = "messaging"
	reconciler.ServerlessQueueConfig.NetworkPolicyTarget.PodLabels = map[string]string{
		"app.kubernetes.io/name": "nats",
	}

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
	if len(dep.Spec.Template.Spec.InitContainers) != 1 {
		t.Fatalf("expected one restartable init sidecar, got %+v", dep.Spec.Template.Spec.InitContainers)
	}
	sidecar := dep.Spec.Template.Spec.InitContainers[0]
	if sidecar.Name != runtimev1alpha1.ServerlessSidecarContainerName {
		t.Fatalf("expected serverless sidecar %s, got %+v", runtimev1alpha1.ServerlessSidecarContainerName, sidecar)
	}
	if sidecar.RestartPolicy == nil || *sidecar.RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Fatalf("expected serverless sidecar restartPolicy=Always, got %+v", sidecar.RestartPolicy)
	}
	if sidecar.StartupProbe == nil || sidecar.StartupProbe.HTTPGet == nil || sidecar.StartupProbe.HTTPGet.Path != "/healthz" {
		t.Fatalf("expected serverless sidecar startup probe on /healthz, got %+v", sidecar.StartupProbe)
	}

	runtimeContainer := dep.Spec.Template.Spec.Containers[0]
	foundFrameworkSocketEnv := false
	for _, env := range runtimeContainer.Env {
		if env.Name == serverless.EnvFrameworkSocketPath && env.Value == runtimev1alpha1.DefaultServerlessFrameworkSocketPath {
			foundFrameworkSocketEnv = true
		}
	}
	if !foundFrameworkSocketEnv {
		t.Fatalf("expected runtime container to receive framework envs, got %+v", runtimeContainer.Env)
	}
	if len(runtimeContainer.VolumeMounts) == 0 {
		t.Fatalf("expected runtime container volume mounts, got %+v", runtimeContainer.VolumeMounts)
	}
	if len(sidecar.VolumeMounts) == 0 {
		t.Fatalf("expected sidecar volume mounts, got %+v", sidecar.VolumeMounts)
	}

	var networkPolicy networkingv1.NetworkPolicy
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: networkPolicyNameForUnit(instance.Name)}, &networkPolicy); err != nil {
		t.Fatalf("network policy should be created: %v", err)
	}
	foundNATSRule := false
	for _, rule := range networkPolicy.Spec.Egress {
		for _, peer := range rule.To {
			if peer.NamespaceSelector == nil || peer.PodSelector == nil {
				continue
			}
			if peer.NamespaceSelector.MatchLabels[kubeNamespaceMetadataLabelKey] != "messaging" {
				continue
			}
			if peer.PodSelector.MatchLabels["app.kubernetes.io/name"] != "nats" {
				continue
			}
			foundNATSRule = true
		}
	}
	if !foundNATSRule {
		t.Fatalf("expected explicit NATS egress rule, got %+v", networkPolicy.Spec.Egress)
	}

	var got runtimev1alpha1.GPUUnit
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, &got); err != nil {
		t.Fatalf("get gpu unit error: %v", err)
	}
	if got.Status.Serverless.Phase != runtimev1alpha1.UnitServerlessPhasePending {
		t.Fatalf("expected pending serverless phase, got %+v", got.Status.Serverless)
	}
	if got.Status.Serverless.DispatchSubject != "runtime.serverless.dispatch.sd-webui.sd-webui-template" {
		t.Fatalf("expected dispatch subject to be recorded, got %+v", got.Status.Serverless)
	}
	if got.Status.Serverless.SocketPath != runtimev1alpha1.DefaultServerlessFrameworkSocketPath {
		t.Fatalf("expected socket path to be recorded, got %+v", got.Status.Serverless)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, runtimev1alpha1.ConditionServerlessReady)
	if cond == nil || cond.Reason != runtimev1alpha1.ReasonUnitServerlessPending {
		t.Fatalf("expected pending serverless condition, got %+v", cond)
	}
}

func TestReconcileInstanceGPUUnitFailsWithoutClusterNATSTarget(t *testing.T) {
	scheme := newControllerScheme(t)

	instance := &runtimev1alpha1.GPUUnit{
		TypeMeta: metav1.TypeMeta{
			APIVersion: runtimev1alpha1.GroupVersion.String(),
			Kind:       "GPUUnit",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "missing-nats-target",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName:   "g1.1",
			Image:      "python:3.12",
			Memory:     "16Gi",
			GPU:        1,
			Allocation: testDRAAllocation(1),
			Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
				RequestID: "sd-webui",
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithObjects(instance).
		Build()

	reconciler := newGPUUnitReconciler(cl, scheme)
	reconciler.ServerlessQueueConfig.URL = "nats://nats.messaging.svc.cluster.local:4222"

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name},
	})
	if err == nil {
		t.Fatalf("expected reconcile error when cluster NATS target is missing")
	}
	if !strings.Contains(err.Error(), "networkPolicyTarget") {
		t.Fatalf("expected networkPolicyTarget error, got %v", err)
	}
}
