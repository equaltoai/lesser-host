package controlplane

import (
	"context"
	"fmt"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

type route53Client struct {
	once   sync.Once
	client *route53.Client
	err    error
}

func newRoute53Client() *route53Client {
	return &route53Client{}
}

func (r *route53Client) get(ctx context.Context) (*route53.Client, error) {
	if r == nil {
		return nil, fmt.Errorf("route53 client is nil")
	}
	r.once.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			r.err = err
			return
		}
		r.client = route53.NewFromConfig(cfg)
	})
	if r.err != nil {
		return nil, r.err
	}
	if r.client == nil {
		return nil, fmt.Errorf("route53 client not initialized")
	}
	return r.client, nil
}

