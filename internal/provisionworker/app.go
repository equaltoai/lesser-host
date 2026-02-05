package provisionworker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

// ServiceName is the canonical service identifier for the provisioning worker.
const ServiceName = "provision-worker"

// New constructs the provisioning worker app.
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
	orgClient := organizations.NewFromConfig(awsCfg)
	stsClient := baseSTS
	if strings.TrimSpace(cfg.ManagedOrgVendingRoleARN) != "" {
		roleArn := strings.TrimSpace(cfg.ManagedOrgVendingRoleARN)
		provider := stscreds.NewAssumeRoleProvider(baseSTS, roleArn, func(o *stscreds.AssumeRoleOptions) {
			sessionName := fmt.Sprintf("lesser-host-%s-org-vending", strings.TrimSpace(cfg.Stage))
			if len(sessionName) > 64 {
				sessionName = sessionName[:64]
			}
			o.RoleSessionName = sessionName
			o.Duration = 3600 * time.Second
		})

		mgmtCfg := awsCfg
		mgmtCfg.Credentials = aws.NewCredentialsCache(provider)
		orgClient = organizations.NewFromConfig(mgmtCfg)
		stsClient = sts.NewFromConfig(mgmtCfg)
	}

	srv := NewServer(
		cfg,
		store.New(db),
		orgClient,
		route53.NewFromConfig(awsCfg),
		stsClient,
		sqs.NewFromConfig(awsCfg),
		codebuild.NewFromConfig(awsCfg),
		s3.NewFromConfig(awsCfg),
	)

	app := apptheory.New(opts...)
	Register(app, srv)
	return app
}

// Register registers SQS handlers with the provided app.
func Register(app *apptheory.App, srv *Server) *apptheory.App {
	if app == nil {
		return app
	}
	if srv != nil {
		srv.Register(app)
	}
	return app
}
