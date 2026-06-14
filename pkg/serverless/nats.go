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

// NATSPublisher persists serverless ingress, dispatch, result, and metrics messages into JetStream subjects.
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
		Description: "Queue-first serverless ingress, worker dispatch, results, and metrics.",
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
	return p.publishInvocation(ctx, msg)
}

// PublishWorkerDispatch marshals one worker-targeted dispatch contract and publishes it durably for a worker sidecar.
func (p *NATSPublisher) PublishWorkerDispatch(ctx context.Context, msg WorkerDispatchMessage) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}

	msg.Version = InvocationVersion
	subject := DispatchSubject(p.cfg.SubjectPrefix, msg.ServerlessRequestID, msg.WorkerName)

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal worker dispatch message: %w", err)
	}

	if _, err := p.js.Publish(ctx, subject, payload, jetstream.WithMsgID("dispatch-"+msg.InvocationID+"-"+normalizeDispatchToken(msg.WorkerName))); err != nil {
		return fmt.Errorf("publish worker dispatch %s to %s: %w", msg.InvocationID, msg.WorkerName, err)
	}

	p.logger.Info("published worker dispatch",
		"invocationID", msg.InvocationID,
		"serverlessRequestID", msg.ServerlessRequestID,
		"workerName", msg.WorkerName,
		"subject", subject,
	)
	return nil
}

// PublishWorkerMetric marshals one worker lifecycle or execution event and publishes it durably for later autoscaling or debugging consumers.
func (p *NATSPublisher) PublishWorkerMetric(ctx context.Context, metric WorkerMetricMessage) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}

	metric.Version = InvocationVersion
	subject := MetricsSubject(p.cfg.SubjectPrefix, metric.ServerlessRequestID)

	payload, err := json.Marshal(metric)
	if err != nil {
		return fmt.Errorf("marshal worker metric message: %w", err)
	}

	msgID := fmt.Sprintf(
		"metric-%s-%s-%d",
		normalizeDispatchToken(metric.WorkerName),
		normalizeDispatchToken(string(metric.EventType)),
		metric.ReportedAt.UnixNano(),
	)
	if metric.InvocationID != "" {
		msgID = "metric-" + metric.InvocationID + "-" + normalizeDispatchToken(string(metric.EventType))
	}
	if _, err := p.js.Publish(ctx, subject, payload, jetstream.WithMsgID(msgID)); err != nil {
		return fmt.Errorf("publish worker metric %s for %s: %w", metric.EventType, metric.WorkerName, err)
	}

	p.logger.Info("published worker metric",
		"serverlessRequestID", metric.ServerlessRequestID,
		"workerName", metric.WorkerName,
		"eventType", metric.EventType,
		"subject", subject,
	)
	return nil
}

// RequestSyncInvocation publishes one invocation and waits for the dedicated reply subject to return the worker-side result.
func (p *NATSPublisher) RequestSyncInvocation(ctx context.Context, msg InvocationMessage) (PublishAck, InvocationResultMessage, error) {
	if !p.Enabled() {
		return PublishAck{}, InvocationResultMessage{}, fmt.Errorf("serverless queue publisher is not configured")
	}

	replySubject := nats.NewInbox()
	sub, err := p.nc.SubscribeSync(replySubject)
	if err != nil {
		return PublishAck{}, InvocationResultMessage{}, fmt.Errorf("subscribe to reply subject %s: %w", replySubject, err)
	}
	defer func() {
		_ = sub.Unsubscribe()
	}()

	if err := p.nc.Flush(); err != nil {
		return PublishAck{}, InvocationResultMessage{}, fmt.Errorf("flush reply subscription %s: %w", replySubject, err)
	}

	msg.ReplySubject = replySubject
	ack, err := p.publishInvocation(ctx, msg)
	if err != nil {
		return PublishAck{}, InvocationResultMessage{}, err
	}

	reply, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		return PublishAck{}, InvocationResultMessage{}, fmt.Errorf("wait for invocation result %s: %w", msg.InvocationID, err)
	}

	var result InvocationResultMessage
	if err := json.Unmarshal(reply.Data, &result); err != nil {
		return PublishAck{}, InvocationResultMessage{}, fmt.Errorf("decode invocation result %s: %w", msg.InvocationID, err)
	}
	return ack, result, nil
}

// PublishInvocationResult persists one invocation result and also replies directly to a sync waiter when replySubject is set.
func (p *NATSPublisher) PublishInvocationResult(ctx context.Context, result InvocationResultMessage) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}

	result.Version = InvocationVersion
	subject := ResultSubject(p.cfg.SubjectPrefix, result.ServerlessRequestID)

	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal invocation result: %w", err)
	}

	if _, err := p.js.Publish(ctx, subject, payload, jetstream.WithMsgID("result-"+result.InvocationID)); err != nil {
		return fmt.Errorf("publish invocation result %s: %w", result.InvocationID, err)
	}
	if result.ReplySubject != "" {
		if err := p.nc.Publish(result.ReplySubject, payload); err != nil {
			return fmt.Errorf("publish sync reply for invocation %s: %w", result.InvocationID, err)
		}
	}

	p.logger.Info("published serverless invocation result",
		"invocationID", result.InvocationID,
		"serverlessRequestID", result.ServerlessRequestID,
		"subject", subject,
		"statusCode", result.StatusCode,
		"workerName", result.WorkerName,
	)
	return nil
}

// ConsumeInvocations drains invocation messages from the durable invocation subject family and acknowledges them on successful handling.
func (p *NATSPublisher) ConsumeInvocations(ctx context.Context, durable string, ackWait time.Duration, handler func(context.Context, InvocationMessage) error) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}
	if durable == "" {
		return fmt.Errorf("consumer durable name is required")
	}
	if handler == nil {
		return fmt.Errorf("invocation handler is required")
	}

	consumer, err := p.js.CreateOrUpdateConsumer(ctx, p.cfg.StreamName, jetstream.ConsumerConfig{
		Durable:       durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: fmt.Sprintf("%s.invoke.*", p.cfg.SubjectPrefix),
		AckWait:       ackWait,
	})
	if err != nil {
		return fmt.Errorf("create or update invocation consumer %s: %w", durable, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("fetch invocation batch: %w", err)
		}

		for jsMsg := range batch.Messages() {
			var msg InvocationMessage
			if err := json.Unmarshal(jsMsg.Data(), &msg); err != nil {
				p.logger.Error("discarding invalid invocation payload", "subject", jsMsg.Subject(), "error", err)
				_ = jsMsg.Term()
				continue
			}
			if err := handler(ctx, msg); err != nil {
				p.logger.Error("invocation handling failed", "invocationID", msg.InvocationID, "serverlessRequestID", msg.ServerlessRequestID, "error", err)
				_ = jsMsg.NakWithDelay(2 * time.Second)
				continue
			}
			if err := jsMsg.Ack(); err != nil {
				return fmt.Errorf("ack invocation %s: %w", msg.InvocationID, err)
			}
		}
		if err := batch.Error(); err != nil && ctx.Err() == nil {
			return fmt.Errorf("consume invocation batch: %w", err)
		}
	}
}

// ConsumeWorkerMetrics drains durable worker lifecycle events for the activator lifecycle manager.
func (p *NATSPublisher) ConsumeWorkerMetrics(ctx context.Context, durable string, ackWait time.Duration, handler func(context.Context, WorkerMetricMessage) error) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}
	if durable == "" {
		return fmt.Errorf("worker metrics durable name is required")
	}
	if handler == nil {
		return fmt.Errorf("worker metrics handler is required")
	}

	consumer, err := p.js.CreateOrUpdateConsumer(ctx, p.cfg.StreamName, jetstream.ConsumerConfig{
		Durable:       durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: fmt.Sprintf("%s.metrics.*", p.cfg.SubjectPrefix),
		AckWait:       ackWait,
	})
	if err != nil {
		return fmt.Errorf("create or update worker metrics consumer %s: %w", durable, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("fetch worker metrics batch: %w", err)
		}

		for jsMsg := range batch.Messages() {
			var msg WorkerMetricMessage
			if err := json.Unmarshal(jsMsg.Data(), &msg); err != nil {
				p.logger.Error("discarding invalid worker metric payload", "subject", jsMsg.Subject(), "error", err)
				_ = jsMsg.Term()
				continue
			}
			if err := handler(ctx, msg); err != nil {
				p.logger.Error("worker metric handling failed", "workerName", msg.WorkerName, "serverlessRequestID", msg.ServerlessRequestID, "eventType", msg.EventType, "error", err)
				_ = jsMsg.NakWithDelay(2 * time.Second)
				continue
			}
			if err := jsMsg.Ack(); err != nil {
				return fmt.Errorf("ack worker metric %s/%s: %w", msg.WorkerName, msg.EventType, err)
			}
		}
		if err := batch.Error(); err != nil && ctx.Err() == nil {
			return fmt.Errorf("consume worker metrics batch: %w", err)
		}
	}
}

// ConsumeInvocationResults drains durable invocation completion events for control-plane result storage.
func (p *NATSPublisher) ConsumeInvocationResults(ctx context.Context, durable string, ackWait time.Duration, handler func(context.Context, InvocationResultMessage) error) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}
	if durable == "" {
		return fmt.Errorf("invocation result durable name is required")
	}
	if handler == nil {
		return fmt.Errorf("invocation result handler is required")
	}

	consumer, err := p.js.CreateOrUpdateConsumer(ctx, p.cfg.StreamName, jetstream.ConsumerConfig{
		Durable:       durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: fmt.Sprintf("%s.result.*", p.cfg.SubjectPrefix),
		AckWait:       ackWait,
	})
	if err != nil {
		return fmt.Errorf("create or update invocation result consumer %s: %w", durable, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("fetch invocation result batch: %w", err)
		}

		for jsMsg := range batch.Messages() {
			var msg InvocationResultMessage
			if err := json.Unmarshal(jsMsg.Data(), &msg); err != nil {
				p.logger.Error("discarding invalid invocation result payload", "subject", jsMsg.Subject(), "error", err)
				_ = jsMsg.Term()
				continue
			}
			if err := handler(ctx, msg); err != nil {
				p.logger.Error("invocation result handling failed", "invocationID", msg.InvocationID, "serverlessRequestID", msg.ServerlessRequestID, "error", err)
				_ = jsMsg.NakWithDelay(2 * time.Second)
				continue
			}
			if err := jsMsg.Ack(); err != nil {
				return fmt.Errorf("ack invocation result %s: %w", msg.InvocationID, err)
			}
		}
		if err := batch.Error(); err != nil && ctx.Err() == nil {
			return fmt.Errorf("consume invocation result batch: %w", err)
		}
	}
}

// ConsumeWorkerDispatches drains worker-targeted dispatch messages for one concrete worker sidecar and acknowledges them on successful handling.
func (p *NATSPublisher) ConsumeWorkerDispatches(
	ctx context.Context,
	durable string,
	requestID string,
	workerName string,
	ackWait time.Duration,
	handler func(context.Context, WorkerDispatchMessage) error,
) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}
	if durable == "" {
		return fmt.Errorf("worker dispatch durable name is required")
	}
	if handler == nil {
		return fmt.Errorf("worker dispatch handler is required")
	}

	subject := DispatchSubject(p.cfg.SubjectPrefix, requestID, workerName)
	consumer, err := p.js.CreateOrUpdateConsumer(ctx, p.cfg.StreamName, jetstream.ConsumerConfig{
		Durable:       durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: subject,
		AckWait:       ackWait,
	})
	if err != nil {
		return fmt.Errorf("create or update worker dispatch consumer %s: %w", durable, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("fetch worker dispatch batch: %w", err)
		}

		for jsMsg := range batch.Messages() {
			var msg WorkerDispatchMessage
			if err := json.Unmarshal(jsMsg.Data(), &msg); err != nil {
				p.logger.Error("discarding invalid worker dispatch payload", "subject", jsMsg.Subject(), "error", err)
				_ = jsMsg.Term()
				continue
			}
			if err := handler(ctx, msg); err != nil {
				p.logger.Error("worker dispatch handling failed", "invocationID", msg.InvocationID, "workerName", msg.WorkerName, "error", err)
				_ = jsMsg.NakWithDelay(2 * time.Second)
				continue
			}
			if err := jsMsg.Ack(); err != nil {
				return fmt.Errorf("ack worker dispatch %s: %w", msg.InvocationID, err)
			}
		}
		if err := batch.Error(); err != nil && ctx.Err() == nil {
			return fmt.Errorf("consume worker dispatch batch: %w", err)
		}
	}
}

func (p *NATSPublisher) publishInvocation(ctx context.Context, msg InvocationMessage) (PublishAck, error) {
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
