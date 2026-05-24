package contract

import (
	"encoding/json"
	"testing"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

func TestNormalizeCreateServerlessInvocationRequest_Defaults(t *testing.T) {
	req, err := NormalizeCreateServerlessInvocationRequest(CreateServerlessInvocationRequest{
		ServerlessRequestID: "SD-WEBUI",
		Mode:                serverless.InvocationModeSync,
		Payload:             json.RawMessage(`{"prompt":"hello"}`),
	})
	if err != nil {
		t.Fatalf("normalize create serverless invocation request: %v", err)
	}
	if req.ServerlessRequestID != "sd-webui" {
		t.Fatalf("expected normalized request id sd-webui, got %s", req.ServerlessRequestID)
	}
	if req.InvocationID == "" {
		t.Fatalf("expected generated invocation id")
	}
	if req.TimeoutSeconds != 30 {
		t.Fatalf("expected default sync timeout 30, got %d", req.TimeoutSeconds)
	}
	if req.ContentType != "application/json" {
		t.Fatalf("expected default content type application/json, got %s", req.ContentType)
	}
}

func TestNormalizeCreateServerlessInvocationRequest_RejectsInvalidRequestID(t *testing.T) {
	_, err := NormalizeCreateServerlessInvocationRequest(CreateServerlessInvocationRequest{
		ServerlessRequestID: "bad.request.id",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestNormalizeGPUUnitServerlessFramework_DefaultsAndCleansPaths(t *testing.T) {
	spec, err := NormalizeGPUUnitServerlessFramework(runtimev1alpha1.GPUUnitServerlessFrameworkSpec{
		SocketPath: "/tmp/serverless-framework/nested/framework.sock",
		InvokePath: "invoke/",
		HealthPath: "//healthz",
	})
	if err != nil {
		t.Fatalf("normalize framework spec: %v", err)
	}
	if spec.SocketPath != "/tmp/serverless-framework/nested/framework.sock" {
		t.Fatalf("expected normalized socket path, got %s", spec.SocketPath)
	}
	if spec.InvokePath != "/invoke" {
		t.Fatalf("expected invoke path /invoke, got %s", spec.InvokePath)
	}
	if spec.HealthPath != "/healthz" {
		t.Fatalf("expected health path /healthz, got %s", spec.HealthPath)
	}
}

func TestNormalizeGPUUnitServerlessFrameworkRejectsSocketOutsideSharedDir(t *testing.T) {
	_, err := NormalizeGPUUnitServerlessFramework(runtimev1alpha1.GPUUnitServerlessFrameworkSpec{
		SocketPath: "/tmp/framework.sock",
	})
	if err == nil {
		t.Fatalf("expected socket path validation error")
	}
}
