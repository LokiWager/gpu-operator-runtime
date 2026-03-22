package api

import (
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

// HealthResponse wraps health payloads in the API response envelope.
type HealthResponse struct {
	Data domain.HealthStatus `json:"data"`
}

// GPUUnitListResponse wraps a list of runtime views for Swagger generation.
type GPUUnitListResponse struct {
	Data []domain.GPUUnitRuntime `json:"data"`
}

// GPUUnitResponse wraps one runtime view for Swagger generation.
type GPUUnitResponse struct {
	Data domain.GPUUnitRuntime `json:"data"`
}

// OperatorJobResponse wraps one operator job payload for Swagger generation.
type OperatorJobResponse struct {
	Data domain.OperatorJob `json:"data"`
}

// ErrorResponse wraps one API error payload for Swagger generation.
type ErrorResponse struct {
	Error *APIError `json:"error"`
}
