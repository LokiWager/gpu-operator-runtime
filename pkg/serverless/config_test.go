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

func TestNormalizeRequestID(t *testing.T) {
	got, err := NormalizeRequestID("SD-WEBUI")
	if err != nil {
		t.Fatalf("normalize request id: %v", err)
	}
	if got != "sd-webui" {
		t.Fatalf("expected sd-webui, got %s", got)
	}
}
