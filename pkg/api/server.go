package api

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/loki/gpu-operator-runtime/pkg/service"
)

type Server struct {
	service *service.Service
	logger  *slog.Logger
	echo    *echo.Echo
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Data  any       `json:"data,omitempty"`
	Error *apiError `json:"error,omitempty"`
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

	// Keep routes flat and explicit for now; grouping/version helpers can come later when the surface grows.
	s.echo.GET("/api/v1/health", s.handleHealth)
	s.echo.GET("/api/v1/operator/stockpools", s.handleOperatorStockPools)
	s.echo.POST("/api/v1/operator/stockpools", s.handleOperatorStockPools)
	s.echo.GET("/api/v1/operator/jobs/:jobID", s.handleOperatorJobByID)
}

func (s *Server) handleOperatorStockPools(c echo.Context) error {
	switch c.Request().Method {
	case echo.GET:
		namespace := c.QueryParam("namespace")
		items, err := s.service.ListStockPools(c.Request().Context(), namespace)
		if err != nil {
			return writeError(c, 400, "list_stockpools_failed", err.Error())
		}
		return writeData(c, 200, items)
	case echo.POST:
		var req service.CreateStockPoolRequest
		if err := c.Bind(&req); err != nil {
			return writeError(c, 400, "invalid_request", err.Error())
		}
		job, err := s.service.CreateStockPoolAsync(c.Request().Context(), req)
		if err != nil {
			return writeError(c, 400, "create_stockpool_job_failed", err.Error())
		}
		return writeData(c, 202, job)
	default:
		return writeError(c, 405, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleOperatorJobByID(c echo.Context) error {
	jobID := c.Param("jobID")
	if jobID == "" {
		return writeError(c, 400, "invalid_request", "jobID is required")
	}
	job, err := s.service.GetOperatorJob(c.Request().Context(), jobID)
	if err != nil {
		return writeError(c, 404, "job_not_found", err.Error())
	}
	return writeData(c, 200, job)
}

func (s *Server) handleHealth(c echo.Context) error {
	health, err := s.service.Health(c.Request().Context())
	if err != nil {
		return writeError(c, 500, "health_failed", err.Error())
	}
	return writeData(c, 200, health)
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
	return c.JSON(status, envelope{Error: &apiError{Code: code, Message: message}})
}
