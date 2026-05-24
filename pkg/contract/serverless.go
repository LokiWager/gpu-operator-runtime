package contract

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

// CreateServerlessInvocationRequest captures one queue-first invocation submission.
type CreateServerlessInvocationRequest struct {
	InvocationID        string                    `json:"invocationID,omitempty"`
	ServerlessRequestID string                    `json:"serverlessRequestID"`
	Mode                serverless.InvocationMode `json:"mode,omitempty"`
	ContentType         string                    `json:"contentType,omitempty"`
	Headers             map[string]string         `json:"headers,omitempty"`
	Attributes          map[string]string         `json:"attributes,omitempty"`
	Payload             json.RawMessage           `json:"payload,omitempty"`
	TimeoutSeconds      int32                     `json:"timeoutSeconds,omitempty"`
}

// NormalizeCreateServerlessInvocationRequest trims, defaults, and validates one invocation submission.
func NormalizeCreateServerlessInvocationRequest(req CreateServerlessInvocationRequest) (CreateServerlessInvocationRequest, error) {
	var err error
	req.ServerlessRequestID, err = serverless.NormalizeRequestID(req.ServerlessRequestID)
	if err != nil {
		return CreateServerlessInvocationRequest{}, &ValidationError{Message: err.Error()}
	}

	req.Mode, err = serverless.NormalizeInvocationMode(req.Mode)
	if err != nil {
		return CreateServerlessInvocationRequest{}, &ValidationError{Message: err.Error()}
	}

	req.InvocationID = strings.TrimSpace(req.InvocationID)
	if req.InvocationID == "" {
		req.InvocationID, err = serverless.NewInvocationID()
		if err != nil {
			return CreateServerlessInvocationRequest{}, &ValidationError{Message: err.Error()}
		}
	}

	req.ContentType = strings.TrimSpace(req.ContentType)
	if req.ContentType == "" {
		req.ContentType = "application/json"
	}

	if req.TimeoutSeconds < 0 {
		return CreateServerlessInvocationRequest{}, &ValidationError{Message: "timeoutSeconds should be >= 0"}
	}
	if req.Mode == serverless.InvocationModeSync && req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = 30
	}

	headers := make(map[string]string, len(req.Headers))
	for key, value := range req.Headers {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return CreateServerlessInvocationRequest{}, &ValidationError{Message: "header name is required"}
		}
		headers[trimmedKey] = strings.TrimSpace(value)
	}
	req.Headers = headers

	attributes := make(map[string]string, len(req.Attributes))
	for key, value := range req.Attributes {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return CreateServerlessInvocationRequest{}, &ValidationError{Message: "attribute name is required"}
		}
		attributes[trimmedKey] = strings.TrimSpace(value)
	}
	req.Attributes = attributes

	if req.Payload != nil {
		req.Payload = append(json.RawMessage(nil), req.Payload...)
	}

	return req, nil
}

// NormalizeGPUUnitServerless validates and defaults the runtime-side serverless contract stored on a GPUUnit.
func NormalizeGPUUnitServerless(spec runtimev1alpha1.GPUUnitServerlessSpec) (runtimev1alpha1.GPUUnitServerlessSpec, error) {
	enabled := spec.Enabled || strings.TrimSpace(spec.RequestID) != "" || spec.MinAvailableCount > 0 || spec.IdleTimeoutSeconds > 0 || spec.MinRequestCount > 0
	if !enabled {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, nil
	}

	requestID, err := serverless.NormalizeRequestID(spec.RequestID)
	if err != nil {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, &ValidationError{Message: err.Error()}
	}
	spec.Enabled = true
	spec.RequestID = requestID

	if spec.MinAvailableCount < 0 {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, &ValidationError{Message: "serverless.minAvailableCount should be >= 0"}
	}
	if spec.IdleTimeoutSeconds < 0 {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, &ValidationError{Message: "serverless.idleTimeoutSeconds should be >= 0"}
	}
	if spec.MinRequestCount < 0 {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, &ValidationError{Message: "serverless.minRequestCount should be >= 0"}
	}
	if spec.IdleTimeoutSeconds == 0 {
		spec.IdleTimeoutSeconds = 300
	}

	framework, err := NormalizeGPUUnitServerlessFramework(spec.Framework)
	if err != nil {
		return runtimev1alpha1.GPUUnitServerlessSpec{}, err
	}
	spec.Framework = framework

	return spec, nil
}

// NormalizeGPUUnitServerlessFramework validates and defaults the pod-local unix socket contract exposed by the user framework.
func NormalizeGPUUnitServerlessFramework(spec runtimev1alpha1.GPUUnitServerlessFrameworkSpec) (runtimev1alpha1.GPUUnitServerlessFrameworkSpec, error) {
	spec.SocketPath = normalizeServerlessFrameworkSocketPath(spec.SocketPath, runtimev1alpha1.DefaultServerlessFrameworkSocketPath)
	socketDir := path.Clean(runtimev1alpha1.DefaultServerlessFrameworkSocketDir)
	if spec.SocketPath == socketDir || !strings.HasPrefix(spec.SocketPath, socketDir+"/") {
		return runtimev1alpha1.GPUUnitServerlessFrameworkSpec{}, &ValidationError{
			Message: fmt.Sprintf("serverless.framework.socketPath %q must stay under %s", spec.SocketPath, runtimev1alpha1.DefaultServerlessFrameworkSocketDir),
		}
	}
	spec.InvokePath = normalizeServerlessFrameworkPath(spec.InvokePath, runtimev1alpha1.DefaultServerlessFrameworkInvokePath)
	spec.HealthPath = normalizeServerlessFrameworkPath(spec.HealthPath, runtimev1alpha1.DefaultServerlessFrameworkHealthPath)
	return spec, nil
}

func normalizeServerlessFrameworkSocketPath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return path.Clean(trimmed)
}

func normalizeServerlessFrameworkPath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	normalized := "/" + strings.TrimPrefix(trimmed, "/")
	if normalized == "/" {
		return fallback
	}
	return path.Clean(normalized)
}
