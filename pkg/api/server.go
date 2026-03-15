package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	echoSwagger "github.com/swaggo/echo-swagger"

	_ "github.com/loki/gpu-operator-runtime/docs/swagger"
	"github.com/loki/gpu-operator-runtime/pkg/service"
)

type Server struct {
	service *service.Service
	logger  *slog.Logger
	echo    *echo.Echo
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Data  any       `json:"data,omitempty"`
	Error *APIError `json:"error,omitempty"`
}

func NewServer(svc *service.Service, logger *slog.Logger) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	s := &Server{
		service: svc,
		logger:  logger,
		echo:    e,
	}
	s.routes()
	return e
}

func (s *Server) routes() {
	s.echo.Use(middleware.Recover())
	s.echo.Use(s.loggingMiddleware)

	s.echo.GET("/swagger/*", echoSwagger.WrapHandler)
	s.echo.GET("/api/v1/health", s.handleHealth)
	s.echo.GET("/api/v1/operator/stockpools", s.handleListStockPools)
	s.echo.POST("/api/v1/operator/stockpools", s.handleCreateStockPool)
	s.echo.GET("/api/v1/operator/jobs/:operationID", s.handleOperatorJobByID)
}

// handleListStockPools godoc
// @Summary List stock pools
// @Description List StockPool custom resources and their observed runtime state.
// @Tags operator
// @Produce json
// @Param namespace query string false "Namespace filter"
// @Success 200 {object} StockPoolListResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /operator/stockpools [get]
func (s *Server) handleListStockPools(c echo.Context) error {
	namespace := c.QueryParam("namespace")
	items, err := s.service.ListStockPools(c.Request().Context(), namespace)
	if err != nil {
		return writeServiceError(c, err, "list_stockpools_failed")
	}
	return writeData(c, http.StatusOK, items)
}

// handleCreateStockPool godoc
// @Summary Create a stock pool operation
// @Description Submit a StockPool creation request. Replays with the same operationID and payload are idempotent.
// @Tags operator
// @Accept json
// @Produce json
// @Param request body service.CreateStockPoolRequest true "Create stock pool request"
// @Success 200 {object} OperatorJobResponse
// @Success 202 {object} OperatorJobResponse
// @Failure 400 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /operator/stockpools [post]
func (s *Server) handleCreateStockPool(c echo.Context) error {
	var req service.CreateStockPoolRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
	}

	job, accepted, err := s.service.CreateStockPoolAsync(c.Request().Context(), req)
	if err != nil {
		return writeServiceError(c, err, "create_stockpool_job_failed")
	}
	if accepted {
		return writeData(c, http.StatusAccepted, job)
	}
	return writeData(c, http.StatusOK, job)
}

// handleOperatorJobByID godoc
// @Summary Get operation status
// @Description Get the current state of an asynchronous stock pool creation operation.
// @Tags operator
// @Produce json
// @Param operationID path string true "Operation ID"
// @Success 200 {object} OperatorJobResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /operator/jobs/{operationID} [get]
func (s *Server) handleOperatorJobByID(c echo.Context) error {
	operationID := c.Param("operationID")
	if operationID == "" {
		return writeError(c, http.StatusBadRequest, "invalid_request", "operationID is required")
	}

	job, err := s.service.GetOperatorJob(c.Request().Context(), operationID)
	if err != nil {
		return writeServiceError(c, err, "job_not_found")
	}
	return writeData(c, http.StatusOK, job)
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

func writeData(c echo.Context, status int, data any) error {
	return c.JSON(status, envelope{Data: data})
}

func writeError(c echo.Context, status int, code, message string) error {
	return c.JSON(status, envelope{Error: &APIError{Code: code, Message: message}})
}

func writeServiceError(c echo.Context, err error, fallbackCode string) error {
	var validationErr *service.ValidationError
	if errors.As(err, &validationErr) {
		return writeError(c, http.StatusBadRequest, "invalid_request", validationErr.Error())
	}

	var conflictErr *service.ConflictError
	if errors.As(err, &conflictErr) {
		return writeError(c, http.StatusConflict, "operation_conflict", conflictErr.Error())
	}

	var notFoundErr *service.NotFoundError
	if errors.As(err, &notFoundErr) {
		return writeError(c, http.StatusNotFound, fallbackCode, notFoundErr.Error())
	}

	var unavailableErr *service.UnavailableError
	if errors.As(err, &unavailableErr) {
		return writeError(c, http.StatusServiceUnavailable, "operator_unavailable", unavailableErr.Error())
	}

	return writeError(c, http.StatusInternalServerError, fallbackCode, err.Error())
}
