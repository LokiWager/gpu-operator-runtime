package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"github.com/loki/gpu-operator-runtime/pkg/workersidecar"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := workersidecar.LoadConfigFromEnv()
	if err != nil {
		logger.Error("failed to load worker sidecar config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	queue, err := serverless.NewNATSPublisher(ctx, cfg.Serverless, logger)
	if err != nil {
		logger.Error("failed to configure serverless queue", "error", err)
		os.Exit(1)
	}
	if queue == nil {
		logger.Error("serverless queue publisher is required")
		os.Exit(1)
	}

	sidecar := workersidecar.New(cfg, workersidecar.NewUDSFrameworkClient(cfg), queue, queue, logger)
	srv := &http.Server{
		Addr:              ":" + itoa(cfg.HealthPort),
		Handler:           healthHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	go func() {
		errCh <- sidecar.Run(ctx, queue)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil {
			logger.Error("serverless sidecar stopped with error", "error", err)
			os.Exit(1)
		}
	}
}

func healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func itoa(value int32) string {
	return strconv.FormatInt(int64(value), 10)
}
