package commmailbox

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type fakeS3PutClient struct {
	input *s3.PutObjectInput
}

func (f *fakeS3PutClient) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.input = in
	return &s3.PutObjectOutput{}, nil
}

func TestS3StorePutContent(t *testing.T) {
	client := &fakeS3PutClient{}
	store := NewS3StoreWithClient("mailbox-bucket", client)

	ptr, err := store.PutContent(context.Background(), ContentInput{
		DeliveryID:      "comm-delivery-1",
		InstanceSlug:    "Demo",
		AgentID:         "0xABC",
		MessageID:       "msg-1",
		Direction:       "Inbound",
		ChannelType:     "Email",
		Body:            "Hello mailbox",
		ContentMimeType: "text/plain",
	})
	if err != nil {
		t.Fatalf("PutContent: %v", err)
	}
	if ptr.Storage != ContentStorageS3 || ptr.Bucket != "mailbox-bucket" || ptr.Bytes != int64(len("Hello mailbox")) || ptr.SHA256 == "" {
		t.Fatalf("unexpected pointer: %#v", ptr)
	}
	if !strings.HasPrefix(ptr.Key, ContentKeyPrefix+"instances/demo/agents/0xabc/deliveries/comm-delivery-1/") {
		t.Fatalf("unexpected key: %q", ptr.Key)
	}
	if client.input == nil || aws.ToString(client.input.Bucket) != "mailbox-bucket" || aws.ToString(client.input.Key) != ptr.Key {
		t.Fatalf("unexpected put input: %#v", client.input)
	}
	body, _ := io.ReadAll(client.input.Body)
	if string(body) != "Hello mailbox" {
		t.Fatalf("unexpected stored body: %q", string(body))
	}
	if client.input.Metadata["delivery-id"] != "comm-delivery-1" || client.input.Metadata["sha256"] != ptr.SHA256 {
		t.Fatalf("unexpected metadata: %#v", client.input.Metadata)
	}
}

func TestS3StorePutContentValidation(t *testing.T) {
	if _, err := NewS3StoreWithClient("", &fakeS3PutClient{}).PutContent(context.Background(), ContentInput{DeliveryID: "d", AgentID: "a", Body: "b"}); err == nil {
		t.Fatalf("expected missing bucket error")
	}
	if _, err := NewS3StoreWithClient("bucket", &fakeS3PutClient{}).PutContent(context.Background(), ContentInput{AgentID: "a", Body: "b"}); err == nil {
		t.Fatalf("expected missing delivery error")
	}
	if _, err := NewS3StoreWithClient("bucket", &fakeS3PutClient{}).PutContent(context.Background(), ContentInput{DeliveryID: "d", AgentID: "a", Body: "   "}); err == nil {
		t.Fatalf("expected missing body error")
	}
}
