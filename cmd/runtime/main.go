package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/config"
	"github.com/loki/gpu-operator-runtime/pkg/operator"
	app "github.com/loki/gpu-operator-runtime/pkg/runtime"
)

func main() {
	opts, err := loadOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup options error: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch opts.Mode {
	case modeRuntime:
		runtime, err := app.New(opts.RuntimeConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "runtime startup error: %v\n", err)
			os.Exit(1)
		}
		if err := runtime.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "runtime error: %v\n", err)
			os.Exit(1)
		}
	case modeOperator:
		if err := operator.Run(ctx, opts.OperatorConfig); err != nil {
			fmt.Fprintf(os.Stderr, "operator error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unsupported mode %q\n", opts.Mode)
		os.Exit(1)
	}
}

const (
	modeRuntime  = "runtime"
	modeOperator = "operator"
)

type startupOptions struct {
	Mode           string
	RuntimeConfig  config.Config
	OperatorConfig operator.Config
}

func loadOptions() (startupOptions, error) {
	var (
		mode string

		httpAddr       string
		reportInterval time.Duration
		kubeMode       string
		kubeconfig     string

		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
	)

	flag.StringVar(&mode, "mode", modeRuntime, "process mode: runtime|operator")
	flag.StringVar(&httpAddr, "http-addr", ":8080", "http listen address")
	flag.DurationVar(&reportInterval, "report-interval", 30*time.Second, "status report interval")
	flag.StringVar(&kubeMode, "kube-mode", "auto", "kubernetes mode: auto|off|required")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig path (optional)")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8081", "operator metrics bind address")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8082", "operator health probe bind address")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "enable operator leader election")
	flag.Parse()

	resolvedMode := strings.ToLower(strings.TrimSpace(mode))
	if resolvedMode != modeRuntime && resolvedMode != modeOperator {
		return startupOptions{}, fmt.Errorf("unsupported mode %q", mode)
	}

	opts := startupOptions{
		Mode: resolvedMode,
		OperatorConfig: operator.Config{
			MetricsBindAddress:     metricsAddr,
			HealthProbeBindAddress: probeAddr,
			LeaderElection:         enableLeaderElection,
		},
	}

	if resolvedMode == modeOperator {
		return opts, nil
	}

	kubeModeValue, err := config.ParseKubeMode(kubeMode)
	if err != nil {
		return startupOptions{}, err
	}

	cfg := config.Config{
		HTTPAddr:       httpAddr,
		ReportInterval: reportInterval,
		KubeMode:       kubeModeValue,
		Kubeconfig:     kubeconfig,
	}

	if err := cfg.Validate(); err != nil {
		return startupOptions{}, err
	}
	opts.RuntimeConfig = cfg
	return opts, nil
}
