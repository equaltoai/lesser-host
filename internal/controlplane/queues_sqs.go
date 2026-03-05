package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/provisioning"
)

type queueClient struct {
	provisionQueueURL string
	commQueueURL      string

	once   sync.Once
	client *sqs.Client
	err    error
}

func newQueueClient(provisionQueueURL string, commQueueURL string) *queueClient {
	return &queueClient{
		provisionQueueURL: strings.TrimSpace(provisionQueueURL),
		commQueueURL:      strings.TrimSpace(commQueueURL),
	}
}

func (q *queueClient) sqsClient(ctx context.Context) (*sqs.Client, error) {
	if q == nil {
		return nil, fmt.Errorf("queue client is nil")
	}
	q.once.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			q.err = err
			return
		}
		q.client = sqs.NewFromConfig(cfg)
	})
	if q.err != nil {
		return nil, q.err
	}
	if q.client == nil {
		return nil, fmt.Errorf("sqs client not initialized")
	}
	return q.client, nil
}

func (q *queueClient) enqueueProvisionJob(ctx context.Context, msg provisioning.JobMessage) error {
	if q == nil {
		return fmt.Errorf("queue client is nil")
	}
	url := strings.TrimSpace(q.provisionQueueURL)
	if url == "" {
		return fmt.Errorf("provision queue url is not configured")
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	client, err := q.sqsClient(ctx)
	if err != nil {
		return err
	}

	_, err = client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(url),
		MessageBody: aws.String(string(body)),
	})
	return err
}

func (q *queueClient) enqueueCommMessage(ctx context.Context, msg commworker.QueueMessage) error {
	if q == nil {
		return fmt.Errorf("queue client is nil")
	}
	url := strings.TrimSpace(q.commQueueURL)
	if url == "" {
		return fmt.Errorf("comm queue url is not configured")
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	client, err := q.sqsClient(ctx)
	if err != nil {
		return err
	}

	_, err = client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(url),
		MessageBody: aws.String(string(body)),
	})
	return err
}
