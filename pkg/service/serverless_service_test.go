package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

type fakeQueuePublisher struct {
	enabled bool
	ack     serverless.PublishAck
	err     error
}

func (f fakeQueuePublisher) Enabled() bool {
	return f.enabled
}

func (f fakeQueuePublisher) PublishInvocation(_ context.Context, msg serverless.InvocationMessage) (serverless.PublishAck, error) {
	if f.err != nil {
		return serverless.PublishAck{}, f.err
	}
	ack := f.ack
	if ack.InvocationID == "" {
		ack.InvocationID = msg.InvocationID
	}
	if ack.ServerlessRequestID == "" {
		ack.ServerlessRequestID = msg.ServerlessRequestID
	}
	if ack.Mode == "" {
		ack.Mode = msg.Mode
	}
	if ack.AcceptedAt.IsZero() {
		ack.AcceptedAt = time.Unix(1700000000, 0).UTC()
	}
	return ack, nil
}

func TestService_CreateServerlessInvocation(t *testing.T) {
	svc, _, cancel := newOperatorService(t)
	defer cancel()

	svc.ConfigureServerlessPublisher(fakeQueuePublisher{
		enabled: true,
		ack: serverless.PublishAck{
			Subject:        "runtime.serverless.invoke.sd-webui",
			ResultSubject:  "runtime.serverless.result.sd-webui",
			MetricsSubject: "runtime.serverless.metrics.sd-webui",
			Stream:         "RUNTIME_SERVERLESS",
			Sequence:       42,
		},
	})

	ack, accepted, err := svc.CreateServerlessInvocation(context.Background(), CreateServerlessInvocationRequest{
		InvocationID:        "inv-1",
		ServerlessRequestID: "sd-webui",
		Mode:                serverless.InvocationModeAsync,
		Payload:             json.RawMessage(`{"prompt":"hello"}`),
	})
	if err != nil {
		t.Fatalf("create serverless invocation: %v", err)
	}
	if !accepted {
		t.Fatalf("expected invocation to be accepted")
	}
	if ack.Sequence != 42 || ack.Subject == "" {
		t.Fatalf("expected ack details, got %+v", ack)
	}
}

func TestService_CreateServerlessInvocation_RequiresQueue(t *testing.T) {
	svc, _, cancel := newOperatorService(t)
	defer cancel()

	_, _, err := svc.CreateServerlessInvocation(context.Background(), CreateServerlessInvocationRequest{
		InvocationID:        "inv-1",
		ServerlessRequestID: "sd-webui",
	})
	if err == nil {
		t.Fatalf("expected unavailable error")
	}

	var unavailableErr *UnavailableError
	if !errors.As(err, &unavailableErr) {
		t.Fatalf("expected unavailable error, got %T", err)
	}
}
