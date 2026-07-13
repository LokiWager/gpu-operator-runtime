package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	echoSwagger "github.com/swaggo/echo-swagger"

	_ "github.com/loki/gpu-operator-runtime/docs/swagger"
	"github.com/loki/gpu-operator-runtime/pkg/contract"
	runtimemetrics "github.com/loki/gpu-operator-runtime/pkg/metrics"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"github.com/loki/gpu-operator-runtime/pkg/service"
)

// Server owns the Echo HTTP surface for the runtime control plane.
type Server struct {
	service        *service.Service
	logger         *slog.Logger
	echo           *echo.Echo
	metricsHandler http.Handler
}

// APIError is the stable error payload returned by the HTTP API.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// envelope keeps success and error responses under one JSON shape.
type envelope struct {
	Data  any       `json:"data,omitempty"`
	Error *APIError `json:"error,omitempty"`
}

// NewServer wires the runtime service into a ready-to-run Echo instance.
func NewServer(svc *service.Service, logger *slog.Logger) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	s := &Server{
		service:        svc,
		logger:         logger,
		echo:           e,
		metricsHandler: newMetricsHandler(svc, logger),
	}
	s.routes()
	return e
}

func newMetricsHandler(svc *service.Service, logger *slog.Logger) http.Handler {
	registry := prometheus.NewRegistry()
	if svc != nil {
		if err := registry.Register(runtimemetrics.NewRuntimeCollector(svc, 5*time.Second)); err != nil && logger != nil {
			logger.Warn("failed to register runtime metrics collector", "error", err)
		}
	}
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// routes registers middleware and all HTTP endpoints.
func (s *Server) routes() {
	s.echo.Use(middleware.Recover())
	s.echo.Use(s.loggingMiddleware)

	s.echo.GET("/metrics", echo.WrapHandler(s.metricsHandler))
	s.echo.GET("/swagger/*", echoSwagger.WrapHandler)
	s.echo.GET("/api/v1/health", s.handleHealth)
	s.echo.GET("/api/v1/gpu-storages", s.handleListGPUStorages)
	s.echo.POST("/api/v1/gpu-storages", s.handleCreateGPUStorage)
	s.echo.GET("/api/v1/gpu-storages/:name", s.handleGetGPUStorage)
	s.echo.PUT("/api/v1/gpu-storages/:name", s.handleUpdateGPUStorage)
	s.echo.POST("/api/v1/gpu-storages/:name/recover", s.handleRecoverGPUStorage)
	s.echo.DELETE("/api/v1/gpu-storages/:name", s.handleDeleteGPUStorage)
	s.echo.GET("/api/v1/gpu-units", s.handleListGPUUnits)
	s.echo.POST("/api/v1/gpu-units", s.handleCreateGPUUnit)
	s.echo.GET("/api/v1/gpu-units/:name", s.handleGetGPUUnit)
	s.echo.PUT("/api/v1/gpu-units/:name", s.handleUpdateGPUUnit)
	s.echo.DELETE("/api/v1/gpu-units/:name", s.handleDeleteGPUUnit)
	s.echo.POST("/api/v1/serverless/invocations", s.handleCreateServerlessInvocation)
	s.echo.GET("/api/v1/operator/inventory", s.handleRuntimeInventory)
}

// handleListGPUStorages godoc
// @Summary List GPU storages
// @Description List RBD-backed GPU storage resources in the shared runtime instance namespace, including prepare-job and accessor status.
// @Tags storage
// @Produce json
// @Success 200 {object} GPUStorageListResponse
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-storages [get]
func (s *Server) handleListGPUStorages(c echo.Context) error {
	items, err := s.service.ListGPUStorages(c.Request().Context(), "")
	if err != nil {
		return writeServiceError(c, err, "list_gpustorages_failed")
	}
	return writeData(c, http.StatusOK, items)
}

// handleCreateGPUStorage godoc
// @Summary Create a GPU storage
// @Description Persist a GPUStorage resource. By default it targets the rook-ceph-block RBD StorageClass, and the controller reconciles the backing PersistentVolumeClaim, prepare job, and optional accessor asynchronously.
// @Tags storage
// @Accept json
// @Produce json
// @Param request body service.CreateGPUStorageRequest true "Create GPU storage request"
// @Success 201 {object} GPUStorageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-storages [post]
func (s *Server) handleCreateGPUStorage(c echo.Context) error {
	var req service.CreateGPUStorageRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
	}

	storage, err := s.service.CreateGPUStorage(c.Request().Context(), req)
	if err != nil {
		return writeServiceError(c, err, "create_gpustorage_failed")
	}
	return writeData(c, http.StatusCreated, storage)
}

// handleGetGPUStorage godoc
// @Summary Get a GPU storage
// @Description Get the desired and observed state of one GPUStorage resource.
// @Tags storage
// @Produce json
// @Param name path string true "GPU storage name"
// @Success 200 {object} GPUStorageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-storages/{name} [get]
func (s *Server) handleGetGPUStorage(c echo.Context) error {
	storage, err := s.service.GetGPUStorage(c.Request().Context(), "", c.Param("name"))
	if err != nil {
		return writeServiceError(c, err, "gpustorage_not_found")
	}
	return writeData(c, http.StatusOK, storage)
}

// handleUpdateGPUStorage godoc
// @Summary Update a GPU storage
// @Description Update the mutable storage fields on a GPUStorage resource. This chapter allows storage expansion and accessor toggling, while prepare workflows stay immutable and recoverable through a dedicated action.
// @Tags storage
// @Accept json
// @Produce json
// @Param name path string true "GPU storage name"
// @Param request body service.UpdateGPUStorageRequest true "Update GPU storage request"
// @Success 200 {object} GPUStorageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-storages/{name} [put]
func (s *Server) handleUpdateGPUStorage(c echo.Context) error {
	var req service.UpdateGPUStorageRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
	}

	storage, err := s.service.UpdateGPUStorage(c.Request().Context(), "", c.Param("name"), req)
	if err != nil {
		return writeServiceError(c, err, "update_gpustorage_failed")
	}
	return writeData(c, http.StatusOK, storage)
}

// handleRecoverGPUStorage godoc
// @Summary Recover a GPU storage
// @Description Request a new prepare attempt for one GPUStorage resource after a failed data job.
// @Tags storage
// @Produce json
// @Param name path string true "GPU storage name"
// @Success 200 {object} GPUStorageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-storages/{name}/recover [post]
func (s *Server) handleRecoverGPUStorage(c echo.Context) error {
	storage, err := s.service.RecoverGPUStorage(c.Request().Context(), "", c.Param("name"))
	if err != nil {
		return writeServiceError(c, err, "recover_gpustorage_failed")
	}
	return writeData(c, http.StatusOK, storage)
}

// handleDeleteGPUStorage godoc
// @Summary Delete a GPU storage
// @Description Delete a GPUStorage resource after verifying that no active GPU unit still mounts it.
// @Tags storage
// @Produce json
// @Param name path string true "GPU storage name"
// @Success 204 {string} string ""
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-storages/{name} [delete]
func (s *Server) handleDeleteGPUStorage(c echo.Context) error {
	if err := s.service.DeleteGPUStorage(c.Request().Context(), "", c.Param("name")); err != nil {
		return writeServiceError(c, err, "delete_gpustorage_failed")
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListGPUUnits godoc
// @Summary List GPU units
// @Description List active GPU unit resources in the fixed runtime instance namespace.
// @Tags runtime
// @Produce json
// @Success 200 {object} GPUUnitListResponse
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-units [get]
func (s *Server) handleListGPUUnits(c echo.Context) error {
	items, err := s.service.ListGPUUnits(c.Request().Context(), "")
	if err != nil {
		return writeServiceError(c, err, "list_gpuunits_failed")
	}
	return writeData(c, http.StatusOK, items)
}

// handleCreateGPUUnit godoc
// @Summary Create a GPU unit
// @Description Persist an active DRA-backed GPUUnit from a configured packageID, runtime image, template, access settings, and storage mounts. Replays with the same operationID and payload are idempotent.
// @Tags runtime
// @Accept json
// @Produce json
// @Param request body contract.CreateGPUUnitRequest true "Create GPU unit request"
// @Success 200 {object} GPUUnitResponse
// @Success 201 {object} GPUUnitResponse
// @Failure 400 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-units [post]
func (s *Server) handleCreateGPUUnit(c echo.Context) error {
	var req service.CreateGPUUnitRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
	}
	instance, created, err := s.service.CreateGPUUnit(c.Request().Context(), req)
	if err != nil {
		return writeServiceError(c, err, "create_gpuunit_failed")
	}
	if created {
		return writeData(c, http.StatusCreated, instance)
	}
	return writeData(c, http.StatusOK, instance)
}

// handleGetGPUUnit godoc
// @Summary Get a GPU unit
// @Description Get the current desired and observed state of one active GPU unit.
// @Tags runtime
// @Produce json
// @Param name path string true "GPU unit name"
// @Success 200 {object} GPUUnitResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-units/{name} [get]
func (s *Server) handleGetGPUUnit(c echo.Context) error {
	instance, err := s.service.GetGPUUnit(c.Request().Context(), "", c.Param("name"))
	if err != nil {
		return writeServiceError(c, err, "gpuunit_not_found")
	}
	return writeData(c, http.StatusOK, instance)
}

// handleUpdateGPUUnit godoc
// @Summary Update a GPU unit
// @Description Update the mutable runtime contract of an active GPU unit, including image, template, access settings, and storage mounts.
// @Tags runtime
// @Accept json
// @Produce json
// @Param name path string true "GPU unit name"
// @Param request body contract.UpdateGPUUnitRequest true "Update GPU unit request"
// @Success 200 {object} GPUUnitResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-units/{name} [put]
func (s *Server) handleUpdateGPUUnit(c echo.Context) error {
	namespace, name, err := contract.NormalizeGPUUnitObjectKey("", c.Param("name"))
	if err != nil {
		return writeServiceError(c, err, "invalid_request")
	}

	var req service.UpdateGPUUnitRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
	}
	req, err = contract.NormalizeUpdateGPUUnitRequest(name, namespace, req)
	if err != nil {
		return writeServiceError(c, err, "invalid_request")
	}

	instance, err := s.service.UpdateGPUUnit(c.Request().Context(), namespace, name, req)
	if err != nil {
		return writeServiceError(c, err, "update_gpuunit_failed")
	}
	return writeData(c, http.StatusOK, instance)
}

// handleDeleteGPUUnit godoc
// @Summary Delete a GPU unit
// @Description Delete an active GPU unit resource and let Kubernetes garbage collection clean up the owned runtime objects.
// @Tags runtime
// @Produce json
// @Param name path string true "GPU unit name"
// @Success 204 {string} string ""
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gpu-units/{name} [delete]
func (s *Server) handleDeleteGPUUnit(c echo.Context) error {
	if err := s.service.DeleteGPUUnit(c.Request().Context(), "", c.Param("name")); err != nil {
		return writeServiceError(c, err, "delete_gpuunit_failed")
	}
	return c.NoContent(http.StatusNoContent)
}

// handleCreateServerlessInvocation godoc
// @Summary Enqueue a serverless invocation
// @Description Persist one serverless invocation into the configured NATS JetStream ingress stream. Async mode returns the durable enqueue acknowledgement, while sync mode waits on the invocation-specific reply subject for the worker-side result path.
// @Tags serverless
// @Accept json
// @Produce json
// @Param request body contract.CreateServerlessInvocationRequest true "Create serverless invocation request"
// @Success 200 {object} ServerlessInvocationResponse
// @Success 202 {object} ServerlessInvocationResponse
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /serverless/invocations [post]
func (s *Server) handleCreateServerlessInvocation(c echo.Context) error {
	var req service.CreateServerlessInvocationRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
	}
	req, err := contract.NormalizeCreateServerlessInvocationRequest(req)
	if err != nil {
		return writeServiceError(c, err, "invalid_request")
	}

	if req.Mode == serverless.InvocationModeSync {
		result, err := s.service.InvokeServerlessSync(c.Request().Context(), req)
		if err != nil {
			return writeServiceError(c, err, "invoke_serverless_sync_failed")
		}
		return writeData(c, http.StatusOK, result)
	}

	ack, accepted, err := s.service.CreateServerlessInvocation(c.Request().Context(), req)
	if err != nil {
		return writeServiceError(c, err, "enqueue_serverless_invocation_failed")
	}
	if accepted {
		return writeData(c, http.StatusAccepted, ack)
	}
	return writeData(c, http.StatusOK, ack)
}

// handleRuntimeInventory godoc
// @Summary Get runtime inventory
// @Description Return the Kubernetes-derived allocation view used by the runtime API: node GPU allocatable, requested GPU from GPUUnit objects, and ResourceQuota status.
// @Tags operator
// @Produce json
// @Success 200 {object} InventoryResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /operator/inventory [get]
func (s *Server) handleRuntimeInventory(c echo.Context) error {
	inventory, err := s.service.RuntimeInventory(c.Request().Context())
	if err != nil {
		return writeServiceError(c, err, "runtime_inventory_failed")
	}
	return writeData(c, http.StatusOK, inventory)
}

// handleHealth godoc
// @Summary Health check
// @Description Return process and Kubernetes connectivity health for the runtime server.
// @Tags system
// @Produce json
// @Success 200 {object} HealthResponse
// @Failure 500 {object} ErrorResponse
// @Router /health [get]
func (s *Server) handleHealth(c echo.Context) error {
	health, err := s.service.Health(c.Request().Context())
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "health_failed", err.Error())
	}
	return writeData(c, http.StatusOK, health)
}

// loggingMiddleware records one structured log line per HTTP request.
func (s *Server) loggingMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		start := time.Now()
		err := next(c)
		req := c.Request()
		s.logger.Info("http request",
			"method", req.Method,
			"path", req.URL.Path,
			"status", c.Response().Status,
			"duration", time.Since(start).String(),
		)
		return err
	}
}

// writeData renders a successful response envelope.
func writeData(c echo.Context, status int, data any) error {
	return c.JSON(status, envelope{Data: data})
}

// writeError renders an error response envelope with a stable API code.
func writeError(c echo.Context, status int, code, message string) error {
	return c.JSON(status, envelope{Error: &APIError{Code: code, Message: message}})
}

// writeServiceError maps service-layer errors to HTTP status codes and API codes.
func writeServiceError(c echo.Context, err error, fallbackCode string) error {
	var validationErr *service.ValidationError
	if errors.As(err, &validationErr) {
		return writeError(c, http.StatusBadRequest, "invalid_request", validationErr.Error())
	}

	var conflictErr *service.ConflictError
	if errors.As(err, &conflictErr) {
		return writeError(c, http.StatusConflict, "operation_conflict", conflictErr.Error())
	}

	var capacityErr *service.CapacityError
	if errors.As(err, &capacityErr) {
		return writeError(c, http.StatusConflict, "insufficient_capacity", capacityErr.Error())
	}

	var notFoundErr *service.NotFoundError
	if errors.As(err, &notFoundErr) {
		return writeError(c, http.StatusNotFound, fallbackCode, notFoundErr.Error())
	}

	var unavailableErr *service.UnavailableError
	if errors.As(err, &unavailableErr) {
		return writeError(c, http.StatusServiceUnavailable, "service_unavailable", unavailableErr.Error())
	}

	return writeError(c, http.StatusInternalServerError, fallbackCode, err.Error())
}
