package config

import (
	"os"
	"path/filepath"
	"testing"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

func TestLoadManagerConfigMergesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manager.yaml")
	if err := os.WriteFile(path, []byte("httpAddr: \":9090\"\nreportInterval: \"45s\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadManagerConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("expected httpAddr :9090, got %s", cfg.HTTPAddr)
	}
	if cfg.MetricsBindAddress != "0" {
		t.Fatalf("expected default metrics bind address, got %s", cfg.MetricsBindAddress)
	}
	if len(cfg.BlockedEgressCIDRs) != len(defaultBlockedEgressCIDRs) {
		t.Fatalf("expected default blocked cidrs, got %+v", cfg.BlockedEgressCIDRs)
	}
	if cfg.Serverless.SubjectPrefix != "runtime.serverless" {
		t.Fatalf("expected default serverless subject prefix, got %s", cfg.Serverless.SubjectPrefix)
	}
	if cfg.ServerlessWorker.Image != serverless.DefaultWorkerSidecarImage {
		t.Fatalf("expected default serverless worker image %s, got %s", serverless.DefaultWorkerSidecarImage, cfg.ServerlessWorker.Image)
	}
	for i := range defaultBlockedEgressCIDRs {
		if cfg.BlockedEgressCIDRs[i] != defaultBlockedEgressCIDRs[i] {
			t.Fatalf("expected blocked cidr %q at index %d, got %+v", defaultBlockedEgressCIDRs[i], i, cfg.BlockedEgressCIDRs)
		}
	}

	interval, err := cfg.ReportIntervalDuration()
	if err != nil {
		t.Fatalf("parse interval: %v", err)
	}
	if interval.String() != "45s" {
		t.Fatalf("expected 45s interval, got %s", interval)
	}

	blockedCIDRs, err := cfg.NormalizedBlockedEgressCIDRs()
	if err != nil {
		t.Fatalf("normalize blocked cidrs: %v", err)
	}
	if len(blockedCIDRs) != len(defaultBlockedEgressCIDRs) {
		t.Fatalf("expected normalized blocked cidrs, got %+v", blockedCIDRs)
	}
	for i := range defaultBlockedEgressCIDRs {
		if blockedCIDRs[i] != defaultBlockedEgressCIDRs[i] {
			t.Fatalf("expected normalized blocked cidr %q at index %d, got %+v", defaultBlockedEgressCIDRs[i], i, blockedCIDRs)
		}
	}
}

func TestLoadManagerConfigLoadsServerlessNetworkPolicyTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manager.yaml")
	content := []byte(
		"httpAddr: \":9090\"\n" +
			"serverless:\n" +
			"  url: \"nats://nats.messaging.svc.cluster.local:4222\"\n" +
			"  networkPolicyTarget:\n" +
			"    namespace: \"messaging\"\n" +
			"    podLabels:\n" +
			"      app.kubernetes.io/name: \"nats\"\n",
	)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadManagerConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	normalized, err := cfg.Serverless.Normalized()
	if err != nil {
		t.Fatalf("normalize serverless config: %v", err)
	}
	if got := normalized.EffectiveNetworkPolicyNamespace(); got != "messaging" {
		t.Fatalf("expected messaging namespace, got %q", got)
	}
	if got := normalized.NetworkPolicyTarget.PodLabels["app.kubernetes.io/name"]; got != "nats" {
		t.Fatalf("expected nats pod label, got %q", got)
	}
}

func TestLoadControllerManagerConfigMergesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "controller-manager.yaml")
	content := []byte(
		"leaderElect: true\n" +
			"blockedEgressCIDRs:\n" +
			"  - \"192.168.0.0/16\"\n" +
			"  - \"10.0.0.0/8\"\n" +
			"serverlessWorker:\n" +
			"  image: \"example.com/runtime/sidecar:test\"\n",
	)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadControllerManagerConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.LeaderElect {
		t.Fatalf("expected leader election to be enabled")
	}
	if cfg.MetricsBindAddress != "0" {
		t.Fatalf("expected default metrics bind address, got %s", cfg.MetricsBindAddress)
	}
	if cfg.Serverless.SubjectPrefix != "runtime.serverless" {
		t.Fatalf("expected default serverless subject prefix, got %s", cfg.Serverless.SubjectPrefix)
	}
	if cfg.ServerlessWorker.Image != "example.com/runtime/sidecar:test" {
		t.Fatalf("expected custom worker sidecar image, got %s", cfg.ServerlessWorker.Image)
	}

	blockedCIDRs, err := cfg.NormalizedBlockedEgressCIDRs()
	if err != nil {
		t.Fatalf("normalize blocked cidrs: %v", err)
	}
	if len(blockedCIDRs) != 2 || blockedCIDRs[0] != "10.0.0.0/8" || blockedCIDRs[1] != "192.168.0.0/16" {
		t.Fatalf("expected sorted controller blocked cidrs, got %+v", blockedCIDRs)
	}
}

func TestLoadRuntimeAPIConfigMergesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-api.yaml")
	content := []byte(
		"httpAddr: \":9090\"\n" +
			"reportInterval: \"2m\"\n" +
			"nvidiaMetricsEndpoint: \"http://dcgm-exporter.runtime-system.svc:9400/metrics\"\n" +
			"packages:\n" +
			"  - id: \"gpu-rtx3080-2x-cpu10-mem40g\"\n" +
			"    specName: \"gpu.rtx3080.2x.10c.40g\"\n" +
			"    cpu: \"10\"\n" +
			"    memory: \"40Gi\"\n" +
			"    gpu: 2\n" +
			"    allocation:\n" +
			"      deviceClassName: \"nvidia-rtx-3080\"\n" +
			"      count: 2\n" +
			"serverless:\n" +
			"  url: \"nats://nats.messaging.svc.cluster.local:4222\"\n",
	)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadRuntimeAPIConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("expected runtime API address :9090, got %s", cfg.HTTPAddr)
	}
	if cfg.NvidiaMetricsEndpoint == "" {
		t.Fatalf("expected nvidia metrics endpoint")
	}
	if cfg.Serverless.StreamName != "RUNTIME_SERVERLESS" {
		t.Fatalf("expected default stream name, got %s", cfg.Serverless.StreamName)
	}
	packages, err := cfg.Packages.Normalized()
	if err != nil {
		t.Fatalf("normalize packages: %v", err)
	}
	if len(packages) != 1 || packages[0].ID != "gpu-rtx3080-2x-cpu10-mem40g" {
		t.Fatalf("expected runtime package to load, got %+v", packages)
	}
	if packages[0].Allocation.ClaimRequestName != runtimev1alpha1.UnitDRAClaimRequestName {
		t.Fatalf("expected default claim request name, got %+v", packages[0].Allocation)
	}

	interval, err := cfg.ReportIntervalDuration()
	if err != nil {
		t.Fatalf("parse interval: %v", err)
	}
	if interval.String() != "2m0s" {
		t.Fatalf("expected 2m interval, got %s", interval)
	}
}
