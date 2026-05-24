package config

import (
	"os"
	"path/filepath"
	"testing"

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
