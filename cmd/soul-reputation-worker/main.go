package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/observability"
	"github.com/equaltoai/lesser-host/internal/soulreputationworker"
)

func main() {
	app := soulreputationworker.New(
		apptheory.WithObservability(observability.New(soulreputationworker.ServiceName)),
	)
	lambda.Start(app.HandleLambda)
}
