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

func TestRetryPolicyNormalizedDefaults(t *testing.T) {
	policy, err := (RetryPolicy{}).Normalized()
	if err != nil {
		t.Fatalf("normalize retry policy: %v", err)
	}
	if policy.MaxDeliver != DefaultRetryMaxDeliver {
		t.Fatalf("expected default max deliver, got %+v", policy)
	}
	if len(policy.Backoff) == 0 {
		t.Fatalf("expected default backoff")
	}
	if delay := policy.DelayForDelivery(10); delay <= 0 {
		t.Fatalf("expected retry delay, got %s", delay)
	}
}

func TestRetryPolicyRejectsTooManyBackoffEntries(t *testing.T) {
	_, err := (RetryPolicy{
		MaxDeliver: 2,
		Backoff:    []string{"1s", "2s", "3s"},
	}).Normalized()
	if err == nil {
		t.Fatalf("expected invalid retry policy")
	}
}

func TestStreamSubjectsIncludeDeadLetterSubjects(t *testing.T) {
	subjects := StreamSubjects("runtime.serverless")
	found := false
	for _, subject := range subjects {
		if subject == "runtime.serverless.dlq.*.*" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dlq wildcard subject, got %+v", subjects)
	}
	if got := DeadLetterSubject("runtime.serverless", DeadLetterSourceInvocation, "sd-webui"); got != "runtime.serverless.dlq.invocation.sd-webui" {
		t.Fatalf("unexpected dead letter subject: %s", got)
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
	if cfg.DispatchRetry.MaxDeliver != DefaultRetryMaxDeliver {
		t.Fatalf("expected default dispatch retry, got %+v", cfg.DispatchRetry)
	}
}
