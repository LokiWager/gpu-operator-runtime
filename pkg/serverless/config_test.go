package serverless

import "testing"

func TestNATSConfigNormalizedDefaults(t *testing.T) {
	cfg, err := DefaultNATSConfig().Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if cfg.SubjectPrefix != DefaultSubjectPrefix {
		t.Fatalf("expected subject prefix %s, got %s", DefaultSubjectPrefix, cfg.SubjectPrefix)
	}
	if cfg.StreamName != DefaultStreamName {
		t.Fatalf("expected stream name %s, got %s", DefaultStreamName, cfg.StreamName)
	}
	if cfg.StreamReplicas != 1 {
		t.Fatalf("expected stream replicas 1, got %d", cfg.StreamReplicas)
	}
}

func TestNATSConfigNormalizesNetworkPolicyTarget(t *testing.T) {
	cfg, err := (NATSConfig{
		URL: "nats://nats.messaging.svc.cluster.local:4222",
		NetworkPolicyTarget: NATSNetworkPolicyTarget{
			PodLabels: map[string]string{
				" app.kubernetes.io/name ": " nats ",
			},
		},
	}).Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if !cfg.UsesClusterServiceHost() {
		t.Fatalf("expected cluster service host")
	}
	if got := cfg.EffectiveNetworkPolicyNamespace(); got != "messaging" {
		t.Fatalf("expected inferred namespace messaging, got %q", got)
	}
	if got := cfg.NetworkPolicyTarget.PodLabels["app.kubernetes.io/name"]; got != "nats" {
		t.Fatalf("expected normalized pod label value nats, got %q", got)
	}
	if got := cfg.URLPort(); got != 4222 {
		t.Fatalf("expected default nats port 4222, got %d", got)
	}
}

func TestNATSNetworkPolicyTargetRejectsInvalidLabels(t *testing.T) {
	_, err := (NATSConfig{
		URL: "nats://nats.messaging.svc.cluster.local:4222",
		NetworkPolicyTarget: NATSNetworkPolicyTarget{
			PodLabels: map[string]string{
				"bad key": "nats",
			},
		},
	}).Normalized()
	if err == nil {
		t.Fatalf("expected invalid pod label error")
	}
}

func TestNormalizeRequestID(t *testing.T) {
	got, err := NormalizeRequestID("SD-WEBUI")
	if err != nil {
		t.Fatalf("normalize request id: %v", err)
	}
	if got != "sd-webui" {
		t.Fatalf("expected sd-webui, got %s", got)
	}
}

func TestWorkerSidecarConfigNormalizedDefaults(t *testing.T) {
	cfg, err := DefaultWorkerSidecarConfig().Normalized()
	if err != nil {
		t.Fatalf("normalize worker sidecar config: %v", err)
	}
	if cfg.Image != DefaultWorkerSidecarImage {
		t.Fatalf("expected worker sidecar image %s, got %s", DefaultWorkerSidecarImage, cfg.Image)
	}
	if cfg.HealthPort != DefaultWorkerSidecarHealthPort {
		t.Fatalf("expected worker sidecar health port %d, got %d", DefaultWorkerSidecarHealthPort, cfg.HealthPort)
	}
}
