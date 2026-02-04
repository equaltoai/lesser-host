package aiworker

import (
	"context"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/comprehend"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

const ServiceName = "ai-worker"

func New(opts ...apptheory.Option) *apptheory.App {
	cfg := config.Load()

	db, err := store.LambdaInit()
	if err != nil {
		panic(err)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(err)
	}

	srv := NewServer(cfg, store.New(db), comprehend.NewFromConfig(awsCfg), rekognition.NewFromConfig(awsCfg))

	app := apptheory.New(opts...)
	Register(app, srv)
	return app
}

func Register(app *apptheory.App, srv *Server) *apptheory.App {
	if app == nil {
		return app
	}
	if srv != nil {
		srv.Register(app)
	}
	return app
}
