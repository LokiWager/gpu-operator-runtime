package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/loki/gpu-operator-runtime/pkg/resultconsumer"
	"github.com/loki/gpu-operator-runtime/pkg/resultstore"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to the result-store YAML configuration file.")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := resultconsumer.LoadConfig(configPath)
	if err != nil {
		logger.Error("failed to load result-store config", "config", configPath, "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	queue, err := serverless.NewNATSPublisher(ctx, cfg.Serverless, logger)
	if err != nil {
		logger.Error("failed to configure serverless queue", "error", err)
		os.Exit(1)
	}
	defer queue.Close()

	store, err := resultstore.NewScyllaStore(ctx, cfg.Scylla, logger)
	if err != nil {
		logger.Error("failed to configure scylla result store", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	service := resultconsumer.New(store, logger, cfg)
	logger.Info("starting serverless result store",
		"consumerName", cfg.ConsumerName,
		"streamName", cfg.Serverless.StreamName,
		"keyspace", cfg.Scylla.Keyspace,
		"resultsTable", cfg.Scylla.ResultsTable,
	)
	if err := service.Run(ctx, queue); err != nil {
		logger.Error("result store stopped with error", "error", err)
		os.Exit(1)
	}
}
