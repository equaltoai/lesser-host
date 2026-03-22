package main

import (
	"context"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/emailingress"
	"github.com/equaltoai/lesser-host/internal/observability"
)

type sesEventHandler interface {
	HandleSESEvent(ctx context.Context, event events.SimpleEmailEvent) error
}

func main() {
	obsHooks := observability.New(emailingress.ServiceName)
	// AppTheory does not yet natively dispatch SES events, but we still instantiate
	// the standard observability hook set so this Lambda follows the same entrypoint
	// contract as the rest of the control plane.
	obsApp := apptheory.New(
		apptheory.WithObservability(observability.New(emailingress.ServiceName)),
	)
	_ = obsApp

	srv := emailingress.NewServer()
	lambda.Start(func(ctx context.Context, event events.SimpleEmailEvent) error {
		return handleSESEvent(ctx, srv, obsHooks, event)
	})
}

func handleSESEvent(ctx context.Context, srv sesEventHandler, obsHooks apptheory.ObservabilityHooks, event events.SimpleEmailEvent) error {
	err := srv.HandleSESEvent(ctx, event)
	logSESEventResult(ctx, obsHooks, err)
	return err
}

func logSESEventResult(ctx context.Context, obsHooks apptheory.ObservabilityHooks, err error) {
	if obsHooks.Log == nil {
		return
	}

	requestID := ""
	if lc, ok := lambdacontext.FromContext(ctx); ok {
		requestID = strings.TrimSpace(lc.AwsRequestID)
	}

	status := 200
	level := "info"
	errorCode := ""
	if err != nil {
		status = 500
		level = "error"
		errorCode = "email.ingress_failed"
	}

	obsHooks.Log(apptheory.LogRecord{
		Level:     level,
		Event:     "request.completed",
		RequestID: requestID,
		Method:    "SES",
		Path:      "/ses/email-ingress",
		Status:    status,
		ErrorCode: errorCode,
	})
}
