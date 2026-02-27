package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/config"
	app "github.com/loki/gpu-operator-runtime/pkg/runtime"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtime, err := app.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup error: %v\n", err)
		os.Exit(1)
	}

	if err := runtime.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "runtime error: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig() (config.Config, error) {
	var (
		httpAddr       string
		reportInterval time.Duration
		kubeMode       string
		kubeconfig     string
	)

	flag.StringVar(&httpAddr, "http-addr", ":8080", "http listen address")
	flag.DurationVar(&reportInterval, "report-interval", 30*time.Second, "status report interval")
	flag.StringVar(&kubeMode, "kube-mode", "auto", "kubernetes mode: auto|off|required")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig path (optional)")
	flag.Parse()

	mode, err := config.ParseKubeMode(kubeMode)
	if err != nil {
		return config.Config{}, err
	}

	cfg := config.Config{
		HTTPAddr:       httpAddr,
		ReportInterval: reportInterval,
		KubeMode:       mode,
		Kubeconfig:     kubeconfig,
	}

	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}
