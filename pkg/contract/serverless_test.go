package contract

import (
	"encoding/json"
	"testing"

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
