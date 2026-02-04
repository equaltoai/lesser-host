package trust

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/rendering"
)

type queueClient struct {
	previewQueueURL string
	safetyQueueURL  string

	once   sync.Once
	client *sqs.Client
	err    error
}

func newQueueClient(previewQueueURL string, safetyQueueURL string) *queueClient {
	return &queueClient{
		previewQueueURL: strings.TrimSpace(previewQueueURL),
		safetyQueueURL:  strings.TrimSpace(safetyQueueURL),
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

func (q *queueClient) enqueueRenderJob(ctx context.Context, msg rendering.RenderJobMessage) error {
	if q == nil {
		return fmt.Errorf("queue client is nil")
	}
	url := strings.TrimSpace(q.previewQueueURL)
	if url == "" {
		return fmt.Errorf("preview queue url is not configured")
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

func (q *queueClient) enqueueAIJob(ctx context.Context, msg ai.JobMessage) error {
	if q == nil {
		return fmt.Errorf("queue client is nil")
	}
	url := strings.TrimSpace(q.safetyQueueURL)
	if url == "" {
		return fmt.Errorf("safety queue url is not configured")
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
