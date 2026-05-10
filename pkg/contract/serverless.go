package contract

import (
	"encoding/json"
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

	return spec, nil
}
