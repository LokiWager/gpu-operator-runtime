package resultstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gocql/gocql"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"github.com/scylladb/gocqlx"
	"github.com/scylladb/gocqlx/table"
)

// ScyllaStore persists invocation results for high-volume single-record lookup.
type ScyllaStore struct {
	cfg     ScyllaConfig
	table   *table.Table
	session *gocql.Session
	logger  *slog.Logger
}

// NewScyllaStore opens a ScyllaDB session and optionally creates the local tutorial schema.
func NewScyllaStore(ctx context.Context, cfg ScyllaConfig, logger *slog.Logger) (*ScyllaStore, error) {
	normalized, err := cfg.Normalized()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}

	if normalized.AutoMigrate {
		if err := ensureKeyspace(ctx, normalized); err != nil {
			return nil, err
		}
	}

	cluster, err := newCluster(normalized, normalized.Keyspace)
	if err != nil {
		return nil, err
	}
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("connect to scylla keyspace %s: %w", normalized.Keyspace, err)
	}

	store := &ScyllaStore{
		cfg:     normalized,
		table:   newInvocationResultTable(normalized),
		session: session,
		logger:  logger,
	}
	if normalized.AutoMigrate {
		if err := store.EnsureSchema(ctx); err != nil {
			session.Close()
			return nil, err
		}
	}
	return store, nil
}

// EnsureSchema creates the invocation result table when auto-migration is enabled for local development.
func (s *ScyllaStore) EnsureSchema(ctx context.Context) error {
	cql := createInvocationResultTableCQL(s.table.Name())
	if err := gocqlx.Query(s.session.Query(cql).WithContext(ctx), nil).ExecRelease(); err != nil {
		return fmt.Errorf("create scylla result table %s: %w", s.table.Name(), err)
	}
	return nil
}

// SaveInvocationResult upserts one completed invocation result.
func (s *ScyllaStore) SaveInvocationResult(ctx context.Context, result serverless.InvocationResultMessage) error {
	record, err := RecordFromResult(result, s.cfg.MaxInlineBodyBytes)
	if err != nil {
		return err
	}

	insertCQL, insertNames := s.table.Insert()
	if err := gocqlx.Query(s.session.Query(insertCQL).WithContext(ctx), insertNames).BindStruct(record).ExecRelease(); err != nil {
		return fmt.Errorf("store invocation result %s: %w", record.InvocationID, err)
	}

	s.logger.Info("stored invocation result",
		"invocationID", record.InvocationID,
		"serverlessRequestID", record.ServerlessRequestID,
		"statusCode", record.StatusCode,
		"bodyBytes", record.BodyBytes,
		"bodyTruncated", record.BodyTruncated,
	)
	return nil
}

// GetInvocationResult returns one invocation result by invocation ID.
func (s *ScyllaStore) GetInvocationResult(ctx context.Context, invocationID string) (InvocationRecord, error) {
	record := InvocationRecord{InvocationID: invocationID}
	getCQL, getNames := s.table.Get(invocationResultColumns...)
	if err := gocqlx.Query(s.session.Query(getCQL).WithContext(ctx), getNames).BindStruct(&record).GetRelease(&record); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return InvocationRecord{}, ErrNotFound
		}
		return InvocationRecord{}, fmt.Errorf("get invocation result %s: %w", invocationID, err)
	}
	return record, nil
}

// Close releases the ScyllaDB session.
func (s *ScyllaStore) Close() {
	if s != nil && s.session != nil {
		s.session.Close()
	}
}

func ensureKeyspace(ctx context.Context, cfg ScyllaConfig) error {
	cluster, err := newCluster(cfg, "system")
	if err != nil {
		return err
	}
	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("connect to scylla system keyspace: %w", err)
	}
	defer session.Close()

	cql := fmt.Sprintf(
		"CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'SimpleStrategy', 'replication_factor': %d}",
		cfg.Keyspace,
		cfg.ReplicationFactor,
	)
	if err := session.Query(cql).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("create scylla keyspace %s: %w", cfg.Keyspace, err)
	}
	return nil
}

func newCluster(cfg ScyllaConfig, keyspace string) (*gocql.ClusterConfig, error) {
	cluster := gocql.NewCluster(cfg.Hosts...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.LocalQuorum
	cluster.ConnectTimeout = cfg.ConnectTimeoutDuration()
	cluster.Timeout = cfg.RequestTimeoutDuration()
	if cfg.Datacenter != "" {
		cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(gocql.DCAwareRoundRobinPolicy(cfg.Datacenter))
	}
	username, password, err := cfg.ResolvedCredentials()
	if err != nil {
		return nil, err
	}
	if username != "" || password != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: username,
			Password: password,
		}
	}
	tlsConfig, err := cfg.TLS.BuildClientTLSConfig()
	if err != nil {
		return nil, err
	}
	if tlsConfig != nil {
		cluster.SslOpts = &gocql.SslOptions{
			Config:                 tlsConfig,
			EnableHostVerification: !cfg.TLS.InsecureSkipVerify,
		}
	}
	return cluster, nil
}

var invocationResultColumns = []string{
	"invocation_id",
	"serverless_request_id",
	"mode",
	"state",
	"failure_class",
	"worker_name",
	"worker_namespace",
	"status_code",
	"content_type",
	"headers",
	"body_inline",
	"body_bytes",
	"body_truncated",
	"error",
	"started_at",
	"completed_at",
	"stored_at",
}

var invocationResultColumnDefinitions = []string{
	"invocation_id text PRIMARY KEY",
	"serverless_request_id text",
	"mode text",
	"state text",
	"failure_class text",
	"worker_name text",
	"worker_namespace text",
	"status_code int",
	"content_type text",
	"headers map<text, text>",
	"body_inline blob",
	"body_bytes bigint",
	"body_truncated boolean",
	"error text",
	"started_at timestamp",
	"completed_at timestamp",
	"stored_at timestamp",
}

func newInvocationResultTable(cfg ScyllaConfig) *table.Table {
	return table.New(table.Metadata{
		Name:    cfg.Keyspace + "." + cfg.ResultsTable,
		Columns: invocationResultColumns,
		PartKey: []string{"invocation_id"},
	})
}

func createInvocationResultTableCQL(tableName string) string {
	definitions := make([]string, 0, len(invocationResultColumnDefinitions))
	for _, definition := range invocationResultColumnDefinitions {
		definitions = append(definitions, "\t"+definition)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n%s\n)", tableName, strings.Join(definitions, ",\n"))
}
