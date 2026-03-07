package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/service"
)

type Server struct {
	service *service.Service
	logger  *slog.Logger
	mux     *http.ServeMux
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Data  any       `json:"data,omitempty"`
	Error *apiError `json:"error,omitempty"`
}

func NewServer(svc *service.Service, logger *slog.Logger) http.Handler {
	s := &Server{
		service: svc,
		logger:  logger,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s.loggingMiddleware(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/v1/health", s.handleHealth)
	s.mux.HandleFunc("/api/v1/operator/stockpools", s.handleOperatorStockPools)
	s.mux.HandleFunc("/api/v1/operator/jobs/", s.handleOperatorJobByID)
}

func (s *Server) handleOperatorStockPools(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		namespace := r.URL.Query().Get("namespace")
		items, err := s.service.ListStockPools(r.Context(), namespace)
		if err != nil {
			writeError(w, http.StatusBadRequest, "list_stockpools_failed", err.Error())
			return
		}
		writeData(w, http.StatusOK, items)
	case http.MethodPost:
		var req service.CreateStockPoolRequest
		if err := decodeBody(r.Body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		job, err := s.service.CreateStockPoolAsync(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "create_stockpool_job_failed", err.Error())
			return
		}
		writeData(w, http.StatusAccepted, job)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleOperatorJobByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	jobID := strings.TrimPrefix(r.URL.Path, "/api/v1/operator/jobs/")
	if strings.TrimSpace(jobID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "jobID is required")
		return
	}
	job, err := s.service.GetOperatorJob(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "job_not_found", err.Error())
		return
	}
	writeData(w, http.StatusOK, job)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	health, err := s.service.Health(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "health_failed", err.Error())
		return
	}
	writeData(w, http.StatusOK, health)
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start).String(),
		)
	})
}

func decodeBody(body io.ReadCloser, out any) error {
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, envelope{Data: data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, envelope{Error: &apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
