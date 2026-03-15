package api

import (
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

type HealthResponse struct {
	Data domain.HealthStatus `json:"data"`
}

type StockPoolListResponse struct {
	Data []domain.StockPoolRuntime `json:"data"`
}

type OperatorJobResponse struct {
	Data domain.OperatorJob `json:"data"`
}

type ErrorResponse struct {
	Error *APIError `json:"error"`
}
