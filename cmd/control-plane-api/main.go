package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/controlplane"
	"github.com/equaltoai/lesser-host/internal/observability"
)

func main() {
	app := controlplane.New(
		apptheory.WithObservability(observability.New(controlplane.ServiceName)),
	)
	lambda.Start(app.HandleLambda)
}
