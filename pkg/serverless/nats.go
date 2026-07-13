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

	connectOptions, err := natsConnectOptions(normalized, connectTimeout)
	if err != nil {
		return nil, err
	}
	nc, err := nats.Connect(normalized.URL, connectOptions...)
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

func natsConnectOptions(cfg NATSConfig, connectTimeout time.Duration) ([]nats.Option, error) {
	opts := []nats.Option{
		nats.Timeout(connectTimeout),
		nats.Name("gpu-runtime-serverless-ingress"),
	}
	if cfg.Auth.CredentialsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.Auth.CredentialsFile))
	}
	if token, err := cfg.Auth.ResolvedToken(); err != nil {
		return nil, err
	} else if token != "" {
		opts = append(opts, nats.Token(token))
	}
	if username, password, err := cfg.Auth.ResolvedUserInfo(); err != nil {
		return nil, err
	} else if username != "" || password != "" {
		opts = append(opts, nats.UserInfo(username, password))
	}
	tlsConfig, err := cfg.TLS.BuildClientTLSConfig()
	if err != nil {
		return nil, err
	}
	if tlsConfig != nil {
		opts = append(opts, nats.Secure(tlsConfig))
	}
	return opts, nil
}

// Enabled reports whether the publisher is ready to publish.
func (p *NATSPublisher) Enabled() bool {
	return p != nil && p.nc != nil && p.js != nil
}

// Close releases the underlying NATS connection.
func (p *NATSPublisher) Close() {
	if p != nil && p.nc != nil {
		p.nc.Close()
	}
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
func (p *NATSPublisher) ConsumeInvocations(ctx context.Context, durable string, opts ConsumerOptions, handler func(context.Context, InvocationMessage) error) error {
	return consumeJSONMessages(p, ctx, durable, fmt.Sprintf("%s.invoke.*", p.cfg.SubjectPrefix), opts, DeadLetterSourceInvocation, invocationDeadLetterInfo, handler)
}

// ConsumeWorkerMetrics drains durable worker lifecycle events for the activator lifecycle manager.
func (p *NATSPublisher) ConsumeWorkerMetrics(ctx context.Context, durable string, opts ConsumerOptions, handler func(context.Context, WorkerMetricMessage) error) error {
	return consumeJSONMessages(p, ctx, durable, fmt.Sprintf("%s.metrics.*", p.cfg.SubjectPrefix), opts, DeadLetterSourceMetric, workerMetricDeadLetterInfo, handler)
}

// ConsumeInvocationResults drains durable invocation completion events for control-plane result storage.
func (p *NATSPublisher) ConsumeInvocationResults(ctx context.Context, durable string, opts ConsumerOptions, handler func(context.Context, InvocationResultMessage) error) error {
	return consumeJSONMessages(p, ctx, durable, fmt.Sprintf("%s.result.*", p.cfg.SubjectPrefix), opts, DeadLetterSourceResult, invocationResultDeadLetterInfo, handler)
}

// ConsumeWorkerDispatches drains worker-targeted dispatch messages for one concrete worker sidecar and acknowledges them on successful handling.
func (p *NATSPublisher) ConsumeWorkerDispatches(
	ctx context.Context,
	durable string,
	requestID string,
	workerName string,
	opts ConsumerOptions,
	handler func(context.Context, WorkerDispatchMessage) error,
) error {
	subject := DispatchSubject(p.cfg.SubjectPrefix, requestID, workerName)
	return consumeJSONMessages(p, ctx, durable, subject, opts, DeadLetterSourceDispatch, workerDispatchDeadLetterInfo, handler)
}

type deadLetterInfo struct {
	InvocationID        string
	ServerlessRequestID string
	WorkerName          string
	WorkerNamespace     string
}

func consumeJSONMessages[T any](
	p *NATSPublisher,
	ctx context.Context,
	durable string,
	filterSubject string,
	opts ConsumerOptions,
	source DeadLetterSource,
	infoFn func(T) deadLetterInfo,
	handler func(context.Context, T) error,
) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}
	if durable == "" {
		return fmt.Errorf("consumer durable name is required")
	}
	if handler == nil {
		return fmt.Errorf("message handler is required")
	}

	opts.DeadLetterSource = source
	normalizedOpts, err := opts.Normalized(30 * time.Second)
	if err != nil {
		return err
	}

	consumer, err := p.createConsumer(ctx, durable, filterSubject, normalizedOpts)
	if err != nil {
		return err
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
			var msg T
			if err := json.Unmarshal(jsMsg.Data(), &msg); err != nil {
				p.logger.Error("discarding malformed serverless queue payload", "subject", jsMsg.Subject(), "source", source, "error", err)
				if err := p.deadLetterAndTerm(ctx, jsMsg, normalizedOpts, source, deadLetterInfo{}, InvocationFailureMalformedMessage, err); err != nil {
					return err
				}
				continue
			}
			info := infoFn(msg)
			if err := handler(ctx, msg); err != nil {
				p.logger.Error("serverless queue handling failed",
					"source", source,
					"invocationID", info.InvocationID,
					"serverlessRequestID", info.ServerlessRequestID,
					"workerName", info.WorkerName,
					"error", err,
				)
				if terminalDelivery(jsMsg, normalizedOpts) {
					if err := p.deadLetterAndTerm(ctx, jsMsg, normalizedOpts, source, info, InvocationFailureRetryExhausted, err); err != nil {
						return err
					}
					continue
				}
				if err := jsMsg.NakWithDelay(normalizedOpts.Retry.DelayForDelivery(deliveryCount(jsMsg))); err != nil {
					return fmt.Errorf("nak serverless queue message %s: %w", info.InvocationID, err)
				}
				continue
			}
			if err := jsMsg.Ack(); err != nil {
				return fmt.Errorf("ack serverless queue message: %w", err)
			}
		}
		if err := batch.Error(); err != nil && ctx.Err() == nil {
			return fmt.Errorf("consume serverless queue batch: %w", err)
		}
	}
}

func (p *NATSPublisher) createConsumer(ctx context.Context, durable, filterSubject string, opts ConsumerOptions) (jetstream.Consumer, error) {
	consumer, err := p.js.CreateOrUpdateConsumer(ctx, p.cfg.StreamName, jetstream.ConsumerConfig{
		Durable:       durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: filterSubject,
		AckWait:       opts.AckWait,
		MaxDeliver:    opts.Retry.MaxDeliver,
		BackOff:       opts.Retry.BackoffDurations(),
	})
	if err != nil {
		return nil, fmt.Errorf("create or update consumer %s: %w", durable, err)
	}
	return consumer, nil
}

func (p *NATSPublisher) deadLetterAndTerm(ctx context.Context, jsMsg jetstream.Msg, opts ConsumerOptions, source DeadLetterSource, info deadLetterInfo, failureClass InvocationFailureClass, cause error) error {
	dlq := buildDeadLetterMessage(jsMsg, source, info, failureClass, cause)
	if err := p.publishDeadLetter(ctx, dlq); err != nil {
		return err
	}
	if err := jsMsg.TermWithReason(string(failureClass)); err != nil {
		if err := jsMsg.Term(); err != nil {
			return fmt.Errorf("term dead-lettered message: %w", err)
		}
	}
	p.logger.Error("serverless queue message moved to dead letter",
		"source", source,
		"invocationID", info.InvocationID,
		"serverlessRequestID", info.ServerlessRequestID,
		"deliveryCount", dlq.DeliveryCount,
		"maxDeliver", opts.Retry.MaxDeliver,
		"failureClass", failureClass,
		"subject", dlq.OriginalSubject,
	)
	return nil
}

func (p *NATSPublisher) publishDeadLetter(ctx context.Context, msg DeadLetterMessage) error {
	if !p.Enabled() {
		return fmt.Errorf("serverless queue publisher is not configured")
	}
	requestID := msg.ServerlessRequestID
	if requestID == "" {
		requestID = "unknown"
	}
	subject := DeadLetterSubject(p.cfg.SubjectPrefix, msg.Source, requestID)
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal dead letter message: %w", err)
	}
	msgID := fmt.Sprintf("dlq-%s-%s-%d", msg.Source, normalizeDispatchToken(msg.InvocationID), msg.StreamSequence)
	if msg.InvocationID == "" {
		msgID = fmt.Sprintf("dlq-%s-%d-%d", msg.Source, msg.StreamSequence, msg.FailedAt.UnixNano())
	}
	if _, err := p.js.Publish(ctx, subject, payload, jetstream.WithMsgID(msgID)); err != nil {
		return fmt.Errorf("publish dead letter to %s: %w", subject, err)
	}
	return nil
}

func buildDeadLetterMessage(jsMsg jetstream.Msg, source DeadLetterSource, info deadLetterInfo, failureClass InvocationFailureClass, cause error) DeadLetterMessage {
	meta, _ := jsMsg.Metadata()
	msg := DeadLetterMessage{
		Version:             InvocationVersion,
		Source:              source,
		State:               InvocationStateDeadLettered,
		FailureClass:        failureClass,
		InvocationID:        info.InvocationID,
		ServerlessRequestID: info.ServerlessRequestID,
		WorkerName:          info.WorkerName,
		WorkerNamespace:     info.WorkerNamespace,
		OriginalSubject:     jsMsg.Subject(),
		Error:               cause.Error(),
		Payload:             append([]byte(nil), jsMsg.Data()...),
		FailedAt:            time.Now().UTC(),
	}
	if meta != nil {
		msg.Stream = meta.Stream
		msg.Consumer = meta.Consumer
		msg.StreamSequence = meta.Sequence.Stream
		msg.ConsumerSequence = meta.Sequence.Consumer
		msg.DeliveryCount = meta.NumDelivered
	}
	return msg
}

func terminalDelivery(jsMsg jetstream.Msg, opts ConsumerOptions) bool {
	if opts.Retry.MaxDeliver <= 0 {
		return false
	}
	return deliveryCount(jsMsg) >= uint64(opts.Retry.MaxDeliver)
}

func deliveryCount(jsMsg jetstream.Msg) uint64 {
	meta, err := jsMsg.Metadata()
	if err != nil || meta == nil || meta.NumDelivered == 0 {
		return 1
	}
	return meta.NumDelivered
}

func invocationDeadLetterInfo(msg InvocationMessage) deadLetterInfo {
	return deadLetterInfo{
		InvocationID:        msg.InvocationID,
		ServerlessRequestID: msg.ServerlessRequestID,
	}
}

func workerDispatchDeadLetterInfo(msg WorkerDispatchMessage) deadLetterInfo {
	return deadLetterInfo{
		InvocationID:        msg.InvocationID,
		ServerlessRequestID: msg.ServerlessRequestID,
		WorkerName:          msg.WorkerName,
		WorkerNamespace:     msg.WorkerNamespace,
	}
}

func workerMetricDeadLetterInfo(msg WorkerMetricMessage) deadLetterInfo {
	return deadLetterInfo{
		InvocationID:        msg.InvocationID,
		ServerlessRequestID: msg.ServerlessRequestID,
		WorkerName:          msg.WorkerName,
		WorkerNamespace:     msg.WorkerNamespace,
	}
}

func invocationResultDeadLetterInfo(msg InvocationResultMessage) deadLetterInfo {
	return deadLetterInfo{
		InvocationID:        msg.InvocationID,
		ServerlessRequestID: msg.ServerlessRequestID,
		WorkerName:          msg.WorkerName,
		WorkerNamespace:     msg.WorkerNamespace,
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
