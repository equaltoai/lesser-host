package commworker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

// ServiceName is the canonical service identifier for the comm worker.
const ServiceName = "comm-worker"

// New constructs the comm worker app.
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

	baseSTS := sts.NewFromConfig(awsCfg)
	stsClient := baseSTS
	if strings.TrimSpace(cfg.ManagedOrgVendingRoleARN) != "" {
		roleArn := strings.TrimSpace(cfg.ManagedOrgVendingRoleARN)
		provider := stscreds.NewAssumeRoleProvider(baseSTS, roleArn, func(o *stscreds.AssumeRoleOptions) {
			sessionName := fmt.Sprintf("lesser-host-%s-comm-vending", strings.TrimSpace(cfg.Stage))
			if len(sessionName) > 64 {
				sessionName = sessionName[:64]
			}
			o.RoleSessionName = sessionName
			o.Duration = 3600 * time.Second
		})

		mgmtCfg := awsCfg
		mgmtCfg.Credentials = aws.NewCredentialsCache(provider)
		stsClient = sts.NewFromConfig(mgmtCfg)
	}

	srv := NewServer(cfg, newDynamoStore(store.New(db)), stsClient, secretsmanager.NewFromConfig(awsCfg))

	app := apptheory.New(opts...)
	Register(app, srv)
	return app
}

// Register registers comm worker routes and hooks with an app.
func Register(app *apptheory.App, srv *Server) *apptheory.App {
	if app == nil {
		return app
	}
	if srv != nil {
		srv.Register(app)
	}
	return app
}

