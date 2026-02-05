// Command provision-worker runs the managed instance provisioning worker Lambda.
package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/observability"
	"github.com/equaltoai/lesser-host/internal/provisionworker"
)

func main() {
	app := provisionworker.New(
		apptheory.WithObservability(observability.New(provisionworker.ServiceName)),
	)
	lambda.Start(app.HandleLambda)
}
