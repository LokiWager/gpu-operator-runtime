package contract

import (
	"testing"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestNormalizeCreateGPUUnitRequest_AppliesDefaults(t *testing.T) {
	req, err := NormalizeCreateGPUUnitRequest(CreateGPUUnitRequest{
		OperationID: "gpu-op-1",
		Name:        "Demo-Instance",
		SpecName:    "g1.1",
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
	})
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

func TestNormalizeCreateGPUUnitRequest_NormalizesServerless(t *testing.T) {
	req, err := NormalizeCreateGPUUnitRequest(CreateGPUUnitRequest{
		OperationID: "gpu-op-3",
		Name:        "demo-instance",
		SpecName:    "g1.1",
		Image:       "python:3.12",
		Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
			RequestID: "SD-WEBUI",
		},
	})
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
}
