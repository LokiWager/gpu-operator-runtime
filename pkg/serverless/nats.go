package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NATSPublisher persists serverless invocations into JetStream subjects.
type NATSPublisher struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	cfg    NATSConfig
	logger *slog.Logger
}

// NewNATSPublisher connects to NATS, ensures the queue ingress stream exists, and returns a ready publisher.
func NewNATSPublisher(ctx context.Context, cfg NATSConfig, logger *slog.Logger) (*NATSPublisher, error) {
	normalized, err := cfg.Normalized()
	if err != nil {
		return nil, err
	}
	if !normalized.Enabled() {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	connectTimeout, _ := normalized.ConnectTimeoutDuration()
	streamMaxAge, _ := normalized.StreamMaxAgeDuration()
	duplicatesWindow, _ := normalized.DuplicatesWindowDuration()

	nc, err := nats.Connect(normalized.URL, nats.Timeout(connectTimeout), nats.Name("gpu-runtime-serverless-ingress"))
	if err != nil {
		return nil, fmt.Errorf("connect to nats %s: %w", normalized.URL, err)
	}

	js, err := jetstream.New(nc, jetstream.WithDefaultTimeout(connectTimeout))
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create jetstream context: %w", err)
	}

	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        normalized.StreamName,
		Description: "Queue-first serverless ingress for runtime invocations, results, and metrics.",
		Subjects:    StreamSubjects(normalized.SubjectPrefix),
		Retention:   jetstream.LimitsPolicy,
		Storage:     jetstream.FileStorage,
		Replicas:    normalized.StreamReplicas,
		MaxAge:      streamMaxAge,
		Duplicates:  duplicatesWindow,
	}); err != nil {
		nc.Close()
		return nil, fmt.Errorf("create or update stream %s: %w", normalized.StreamName, err)
	}

	return &NATSPublisher{
		nc:     nc,
		js:     js,
		cfg:    normalized,
		logger: logger,
	}, nil
}

// Enabled reports whether the publisher is ready to publish.
func (p *NATSPublisher) Enabled() bool {
	return p != nil && p.nc != nil && p.js != nil
}

// PublishInvocation marshals one invocation contract and publishes it durably through JetStream.
func (p *NATSPublisher) PublishInvocation(ctx context.Context, msg InvocationMessage) (PublishAck, error) {
	if !p.Enabled() {
		return PublishAck{}, fmt.Errorf("serverless queue publisher is not configured")
	}

	msg.Version = InvocationVersion
	msg.ResultSubject = ResultSubject(p.cfg.SubjectPrefix, msg.ServerlessRequestID)
	msg.MetricsSubject = MetricsSubject(p.cfg.SubjectPrefix, msg.ServerlessRequestID)
	subject := InvocationSubject(p.cfg.SubjectPrefix, msg.ServerlessRequestID)

	payload, err := json.Marshal(msg)
	if err != nil {
		return PublishAck{}, fmt.Errorf("marshal invocation message: %w", err)
	}

	ack, err := p.js.Publish(ctx, subject, payload, jetstream.WithMsgID(msg.InvocationID))
	if err != nil {
		return PublishAck{}, fmt.Errorf("publish invocation %s: %w", msg.InvocationID, err)
	}

	p.logger.Info("published serverless invocation",
		"invocationID", msg.InvocationID,
		"serverlessRequestID", msg.ServerlessRequestID,
		"subject", subject,
		"stream", ack.Stream,
		"duplicate", ack.Duplicate,
	)

	return PublishAck{
		InvocationID:        msg.InvocationID,
		ServerlessRequestID: msg.ServerlessRequestID,
		Mode:                msg.Mode,
		Subject:             subject,
		ResultSubject:       msg.ResultSubject,
		MetricsSubject:      msg.MetricsSubject,
		Stream:              ack.Stream,
		Sequence:            ack.Sequence,
		Duplicate:           ack.Duplicate,
		AcceptedAt:          time.Now().UTC(),
	}, nil
}
