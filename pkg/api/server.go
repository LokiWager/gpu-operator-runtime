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
	s.mux.HandleFunc("/api/v1/stocks", s.handleStocks)
	s.mux.HandleFunc("/api/v1/vms", s.handleVMs)
	s.mux.HandleFunc("/api/v1/vms/", s.handleVMByID)
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

func (s *Server) handleStocks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		stocks, err := s.service.ListStocks(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_stocks_failed", err.Error())
			return
		}
		writeData(w, http.StatusOK, stocks)
	case http.MethodPost:
		var req service.CreateStockRequest
		if err := decodeBody(r.Body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		stocks, err := s.service.CreateStocks(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "create_stocks_failed", err.Error())
			return
		}
		writeData(w, http.StatusCreated, stocks)
	case http.MethodDelete:
		var req service.DeleteStockRequest
		if err := decodeBody(r.Body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		ids, err := s.service.DeleteStocks(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "delete_stocks_failed", err.Error())
			return
		}
		writeData(w, http.StatusOK, ids)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleVMs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		vms, err := s.service.ListVMs(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_vms_failed", err.Error())
			return
		}
		writeData(w, http.StatusOK, vms)
	case http.MethodPost:
		var req service.CreateVMRequest
		if err := decodeBody(r.Body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		vm, err := s.service.CreateVM(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "create_vm_failed", err.Error())
			return
		}
		writeData(w, http.StatusCreated, vm)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleVMByID(w http.ResponseWriter, r *http.Request) {
	vmID := strings.TrimPrefix(r.URL.Path, "/api/v1/vms/")
	if strings.TrimSpace(vmID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "vmID is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		vm, err := s.service.GetVM(r.Context(), vmID)
		if err != nil {
			writeError(w, http.StatusNotFound, "vm_not_found", err.Error())
			return
		}
		writeData(w, http.StatusOK, vm)
	case http.MethodDelete:
		if err := s.service.DeleteVM(r.Context(), vmID); err != nil {
			writeError(w, http.StatusNotFound, "delete_vm_failed", err.Error())
			return
		}
		writeData(w, http.StatusOK, map[string]string{"vmID": vmID, "status": "deleted"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
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
