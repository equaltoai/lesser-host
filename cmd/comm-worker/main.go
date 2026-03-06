package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/observability"
)

func main() {
	app := commworker.New(
		apptheory.WithObservability(observability.New(commworker.ServiceName)),
	)
	lambda.Start(app.HandleLambda)
}
