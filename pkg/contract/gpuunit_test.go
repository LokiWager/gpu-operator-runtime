package contract

import (
	"testing"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

const testPackageRTX3080Pair = "gpu-rtx3080-2x-cpu10-mem40g"

func testRuntimePackageCatalog() RuntimePackageCatalog {
	return RuntimePackageCatalog{{
		ID:       testPackageRTX3080Pair,
		SpecName: "gpu.rtx3080.2x.10c.40g",
		CPU:      "10",
		Memory:   "40Gi",
		GPU:      2,
		Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
			DeviceClassName:  "nvidia-rtx-3080",
			ClaimRequestName: runtimev1alpha1.UnitDRAClaimRequestName,
			Count:            2,
		},
	}}
}

func TestNormalizeCreateGPUUnitRequest_AppliesDefaults(t *testing.T) {
	req, err := NormalizeCreateGPUUnitRequestWithCatalog(CreateGPUUnitRequest{
		OperationID: "gpu-op-1",
		Name:        "Demo-Instance",
		PackageID:   testPackageRTX3080Pair,
		Image:       "python:3.12",
		Template: runtimev1alpha1.GPUUnitTemplate{
			Ports: []runtimev1alpha1.GPUUnitPortSpec{{
				Name: "http",
				Port: 8080,
			}},
		},
		SSH: runtimev1alpha1.GPUUnitSSHSpec{
			Enabled:      true,
			Username:     "Runtime",
			ServerAddr:   "frps.internal",
			DomainSuffix: "ssh.example.com",
			AuthorizedKeys: []string{
				" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== demo@example ",
				"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== demo@example",
			},
		},
	}, testRuntimePackageCatalog())
	if err != nil {
		t.Fatalf("normalize create gpu unit request: %v", err)
	}

	if req.Name != "demo-instance" {
		t.Fatalf("expected normalized name demo-instance, got %s", req.Name)
	}
	if req.Access.PrimaryPort != "http" {
		t.Fatalf("expected default primary port http, got %s", req.Access.PrimaryPort)
	}
	if req.Access.Scheme != runtimev1alpha1.DefaultAccessScheme {
		t.Fatalf("expected default access scheme %s, got %s", runtimev1alpha1.DefaultAccessScheme, req.Access.Scheme)
	}
	if req.SSH.Username != "runtime" {
		t.Fatalf("expected normalized ssh username runtime, got %s", req.SSH.Username)
	}
	if req.SSH.ConnectHost != "frps.internal" {
		t.Fatalf("expected default ssh connect host frps.internal, got %s", req.SSH.ConnectHost)
	}
	if req.SSH.ConnectPort != runtimev1alpha1.DefaultUnitSSHProxyPort {
		t.Fatalf("expected default ssh connect port %d, got %d", runtimev1alpha1.DefaultUnitSSHProxyPort, req.SSH.ConnectPort)
	}
	if req.SSH.ClientDomain != "demo-instance.runtime-instance.ssh.example.com" {
		t.Fatalf("expected generated client domain, got %s", req.SSH.ClientDomain)
	}
	if len(req.SSH.AuthorizedKeys) != 1 {
		t.Fatalf("expected deduplicated keys, got %+v", req.SSH.AuthorizedKeys)
	}
	if req.Serverless.Enabled {
		t.Fatalf("expected serverless to remain disabled when omitted")
	}
	if req.SpecName != "gpu.rtx3080.2x.10c.40g" || req.CPU != "10" || req.Memory != "40Gi" || req.GPU != 2 {
		t.Fatalf("expected package defaults, got spec=%s cpu=%s memory=%s gpu=%d", req.SpecName, req.CPU, req.Memory, req.GPU)
	}
	if req.Allocation.DeviceClassName != "nvidia-rtx-3080" || req.Allocation.Count != 2 {
		t.Fatalf("expected package DRA allocation, got %+v", req.Allocation)
	}
}

func TestNormalizeCreateGPUUnitRequest_ExpandsPackageToDRAAllocation(t *testing.T) {
	req, err := NormalizeCreateGPUUnitRequestWithCatalog(CreateGPUUnitRequest{
		OperationID: "gpu-op-package",
		Name:        "demo-package",
		PackageID:   testPackageRTX3080Pair,
		Image:       "pytorch:2.6",
	}, testRuntimePackageCatalog())
	if err != nil {
		t.Fatalf("normalize create gpu unit request: %v", err)
	}
	if req.SpecName != "gpu.rtx3080.2x.10c.40g" {
		t.Fatalf("expected package spec name, got %s", req.SpecName)
	}
	if req.CPU != "10" || req.Memory != "40Gi" || req.GPU != 2 {
		t.Fatalf("expected package resources, got cpu=%s memory=%s gpu=%d", req.CPU, req.Memory, req.GPU)
	}
	if req.Allocation.DeviceClassName != "nvidia-rtx-3080" {
		t.Fatalf("expected package device class, got %+v", req.Allocation)
	}
	if req.Allocation.ClaimName != "unit-demo-package-gpu" {
		t.Fatalf("expected claim name for unit, got %+v", req.Allocation)
	}
	if req.Allocation.Count != 2 {
		t.Fatalf("expected DRA count 2, got %+v", req.Allocation)
	}
}

func TestNormalizeCreateGPUUnitRequest_RejectsPackageResourceOverride(t *testing.T) {
	_, err := NormalizeCreateGPUUnitRequestWithCatalog(CreateGPUUnitRequest{
		OperationID: "gpu-op-package-conflict",
		Name:        "demo-package",
		PackageID:   testPackageRTX3080Pair,
		Image:       "pytorch:2.6",
		Memory:      "32Gi",
	}, testRuntimePackageCatalog())
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestNormalizeCreateGPUUnitRequest_RejectsMissingImage(t *testing.T) {
	_, err := NormalizeCreateGPUUnitRequest(CreateGPUUnitRequest{
		OperationID: "gpu-op-2",
		Name:        "demo-instance",
		SpecName:    "g1.1",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestNormalizeCreateGPUUnitRequest_RejectsMissingPackageOrDRAAllocation(t *testing.T) {
	_, err := NormalizeCreateGPUUnitRequest(CreateGPUUnitRequest{
		OperationID: "gpu-op-no-resources",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "python:3.12",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestNormalizeCreateGPUUnitRequest_NormalizesServerless(t *testing.T) {
	req, err := NormalizeCreateGPUUnitRequestWithCatalog(CreateGPUUnitRequest{
		OperationID: "gpu-op-3",
		Name:        "demo-instance",
		PackageID:   testPackageRTX3080Pair,
		Image:       "python:3.12",
		Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
			RequestID: "SD-WEBUI",
		},
	}, testRuntimePackageCatalog())
	if err != nil {
		t.Fatalf("normalize create gpu unit request: %v", err)
	}
	if !req.Serverless.Enabled {
		t.Fatalf("expected serverless to be enabled when requestID is provided")
	}
	if req.Serverless.RequestID != "sd-webui" {
		t.Fatalf("expected normalized request id sd-webui, got %s", req.Serverless.RequestID)
	}
	if req.Serverless.IdleTimeoutSeconds != 300 {
		t.Fatalf("expected default idle timeout 300, got %d", req.Serverless.IdleTimeoutSeconds)
	}
	if req.Serverless.Framework.SocketPath != runtimev1alpha1.DefaultServerlessFrameworkSocketPath {
		t.Fatalf("expected default framework socket path %s, got %s", runtimev1alpha1.DefaultServerlessFrameworkSocketPath, req.Serverless.Framework.SocketPath)
	}
	if req.Serverless.Framework.InvokePath != runtimev1alpha1.DefaultServerlessFrameworkInvokePath {
		t.Fatalf("expected default framework invoke path %s, got %s", runtimev1alpha1.DefaultServerlessFrameworkInvokePath, req.Serverless.Framework.InvokePath)
	}
	if req.Serverless.Framework.HealthPath != runtimev1alpha1.DefaultServerlessFrameworkHealthPath {
		t.Fatalf("expected default framework health path %s, got %s", runtimev1alpha1.DefaultServerlessFrameworkHealthPath, req.Serverless.Framework.HealthPath)
	}
}
