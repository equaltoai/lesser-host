// Command cost-telemetry-worker runs the cost telemetry collection worker Lambda.
package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/costtelemetry"
	"github.com/equaltoai/lesser-host/internal/observability"
)

func main() {
	app := costtelemetry.New(
		apptheory.WithObservability(observability.New(costtelemetry.ServiceName)),
	)
	lambda.Start(app.HandleLambda)
}
