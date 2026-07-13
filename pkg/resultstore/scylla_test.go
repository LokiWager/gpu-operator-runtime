package resultstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loki/gpu-operator-runtime/pkg/secureconfig"
)

func TestInvocationResultTableUsesGocqlxMetadata(t *testing.T) {
	table := newInvocationResultTable(ScyllaConfig{
		Keyspace:     "runtime_serverless",
		ResultsTable: "invocation_results",
	})

	if table.Name() != "runtime_serverless.invocation_results" {
		t.Fatalf("unexpected table name: %s", table.Name())
	}

	metadata := table.Metadata()
	if got, want := len(metadata.Columns), len(invocationResultColumns); got != want {
		t.Fatalf("expected %d columns, got %d", want, got)
	}
	if got, want := metadata.PartKey, []string{"invocation_id"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected partition key: %+v", got)
	}

	insertCQL, insertNames := table.Insert()
	if !strings.Contains(insertCQL, "INSERT INTO runtime_serverless.invocation_results") {
		t.Fatalf("unexpected insert cql: %s", insertCQL)
	}
	if got, want := len(insertNames), len(invocationResultColumns); got != want {
		t.Fatalf("expected %d insert bind names, got %d", want, got)
	}

	getCQL, getNames := table.Get(invocationResultColumns...)
	if !strings.Contains(getCQL, "WHERE invocation_id=?") {
		t.Fatalf("unexpected get cql: %s", getCQL)
	}
	if got, want := len(getNames), 1; got != want {
		t.Fatalf("expected %d get bind name, got %d", want, got)
	}
}

func TestCreateInvocationResultTableCQLUsesSchemaDefinitions(t *testing.T) {
	createCQL := createInvocationResultTableCQL("runtime_serverless.invocation_results")
	if !strings.Contains(createCQL, "CREATE TABLE IF NOT EXISTS runtime_serverless.invocation_results") {
		t.Fatalf("unexpected create cql: %s", createCQL)
	}
	if !strings.Contains(createCQL, "invocation_id text PRIMARY KEY") {
		t.Fatalf("expected primary key definition, got %s", createCQL)
	}
	for _, column := range invocationResultColumns {
		if !strings.Contains(createCQL, column) {
			t.Fatalf("expected create cql to contain column %s: %s", column, createCQL)
		}
	}
}

func TestScyllaConfigReadsCredentialFiles(t *testing.T) {
	dir := t.TempDir()
	usernamePath := filepath.Join(dir, "username")
	passwordPath := filepath.Join(dir, "password")
	if err := os.WriteFile(usernamePath, []byte("runtime\n"), 0o600); err != nil {
		t.Fatalf("write username: %v", err)
	}
	if err := os.WriteFile(passwordPath, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write password: %v", err)
	}

	cfg, err := (ScyllaConfig{
		Hosts:        []string{"scylla-client.runtime-data.svc.cluster.local:9042"},
		UsernameFile: usernamePath,
		PasswordFile: passwordPath,
	}).Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	username, password, err := cfg.ResolvedCredentials()
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if username != "runtime" || password != "secret" {
		t.Fatalf("unexpected credentials: %q/%q", username, password)
	}
}

func TestNewClusterAppliesAuthAndTLS(t *testing.T) {
	cluster, err := newCluster(ScyllaConfig{
		Hosts:    []string{"scylla-client.runtime-data.svc.cluster.local:9042"},
		Keyspace: "runtime_serverless",
		Username: "runtime",
		Password: "secret",
		TLS:      secureconfig.TLSConfig{Enabled: true, InsecureSkipVerify: true},
	}, "runtime_serverless")
	if err != nil {
		t.Fatalf("new cluster: %v", err)
	}
	if cluster.Authenticator == nil {
		t.Fatalf("expected authenticator")
	}
	if cluster.SslOpts == nil || cluster.SslOpts.Config == nil {
		t.Fatalf("expected tls config")
	}
}
