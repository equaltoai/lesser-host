package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/aiworker"
	"github.com/equaltoai/lesser-host/internal/observability"
)

func main() {
	app := aiworker.New(
		apptheory.WithObservability(observability.New(aiworker.ServiceName)),
	)
	lambda.Start(app.HandleLambda)
}
