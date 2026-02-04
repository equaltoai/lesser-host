package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/observability"
	"github.com/equaltoai/lesser-host/internal/trust"
)

func main() {
	app := trust.New(
		apptheory.WithObservability(observability.New(trust.ServiceName)),
	)
	lambda.Start(app.HandleLambda)
}
