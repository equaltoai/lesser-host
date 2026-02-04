package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/observability"
	"github.com/equaltoai/lesser-host/internal/renderworker"
)

func main() {
	app := renderworker.New(
		apptheory.WithObservability(observability.New(renderworker.ServiceName)),
	)
	lambda.Start(app.HandleLambda)
}
