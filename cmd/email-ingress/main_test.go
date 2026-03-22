package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambdacontext"
	apptheory "github.com/theory-cloud/apptheory/runtime"
)

type fakeSESEventHandler struct {
	err error
}

func (f fakeSESEventHandler) HandleSESEvent(context.Context, events.SimpleEmailEvent) error {
	return f.err
}

func TestHandleSESEvent_LogsSuccess(t *testing.T) {
	t.Parallel()

	var got apptheory.LogRecord
	hooks := apptheory.ObservabilityHooks{
		Log: func(rec apptheory.LogRecord) {
			got = rec
		},
	}

	ctx := lambdacontext.NewContext(context.Background(), &lambdacontext.LambdaContext{AwsRequestID: "req-123"})
	if err := handleSESEvent(ctx, fakeSESEventHandler{}, hooks, events.SimpleEmailEvent{}); err != nil {
		t.Fatalf("handleSESEvent: %v", err)
	}
	if got.Level != "info" || got.Status != 200 || got.RequestID != "req-123" || got.ErrorCode != "" {
		t.Fatalf("unexpected success log: %#v", got)
	}
	if got.Method != "SES" || got.Path != "/ses/email-ingress" {
		t.Fatalf("unexpected route log: %#v", got)
	}
}

func TestHandleSESEvent_LogsFailure(t *testing.T) {
	t.Parallel()

	var got apptheory.LogRecord
	hooks := apptheory.ObservabilityHooks{
		Log: func(rec apptheory.LogRecord) {
			got = rec
		},
	}

	wantErr := errors.New("boom")
	err := handleSESEvent(context.Background(), fakeSESEventHandler{err: wantErr}, hooks, events.SimpleEmailEvent{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if got.Level != "error" || got.Status != 500 || got.ErrorCode != "email.ingress_failed" {
		t.Fatalf("unexpected failure log: %#v", got)
	}
}

func TestLogSESEventResult_NoOpWithoutLogger(t *testing.T) {
	t.Parallel()

	logSESEventResult(context.Background(), apptheory.ObservabilityHooks{}, nil)
}
