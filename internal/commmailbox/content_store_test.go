package commmailbox

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type fakeS3PutClient struct {
	input       *s3.PutObjectInput
	headInput   *s3.HeadObjectInput
	getBody     string
	contentType string
	putErr      error
	headOutput  *s3.HeadObjectOutput
}

func (f *fakeS3PutClient) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.input = in
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3PutClient) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.contentType = strings.TrimSpace(f.contentType)
	return &s3.GetObjectOutput{
		Body:        io.NopCloser(strings.NewReader(f.getBody)),
		ContentType: aws.String(f.contentType),
	}, nil
}

func (f *fakeS3PutClient) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	f.headInput = in
	if f.headOutput != nil {
		return f.headOutput, nil
	}
	return &s3.HeadObjectOutput{}, nil
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
	if aws.ToString(client.input.IfNoneMatch) != "*" {
		t.Fatalf("expected immutable put with If-None-Match, got %#v", client.input.IfNoneMatch)
	}
	body, _ := io.ReadAll(client.input.Body)
	if string(body) != "Hello mailbox" {
		t.Fatalf("unexpected stored body: %q", string(body))
	}
	if client.input.Metadata["delivery-id"] != "comm-delivery-1" || client.input.Metadata["sha256"] != ptr.SHA256 {
		t.Fatalf("unexpected metadata: %#v", client.input.Metadata)
	}
}

func TestS3StorePutContentReturnsExistingPointerOnReplay(t *testing.T) {
	preconditionErr := &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "object exists"}
	client := &fakeS3PutClient{
		putErr: preconditionErr,
		headOutput: &s3.HeadObjectOutput{
			ContentLength: aws.Int64(13),
			ContentType:   aws.String("text/plain"),
			Metadata:      map[string]string{"sha256": "existing-digest"},
		},
	}
	store := NewS3StoreWithClient("mailbox-bucket", client)

	ptr, err := store.PutContent(context.Background(), ContentInput{
		DeliveryID:   "comm-delivery-replay",
		InstanceSlug: "Demo",
		AgentID:      "0xABC",
		MessageID:    "msg-1",
		Direction:    "Inbound",
		ChannelType:  "Email",
		Body:         "Replay body",
	})
	if err != nil {
		t.Fatalf("PutContent replay: %v", err)
	}
	if ptr.SHA256 != "existing-digest" || ptr.Bytes != 13 || ptr.ContentType != "text/plain" {
		t.Fatalf("unexpected replay pointer: %#v", ptr)
	}
	if client.headInput == nil || aws.ToString(client.headInput.Key) != ptr.Key {
		t.Fatalf("expected head of existing key, got %#v", client.headInput)
	}
}

func TestS3StorePutContentReturnsNonPreconditionErrors(t *testing.T) {
	client := &fakeS3PutClient{putErr: errors.New("boom")}
	store := NewS3StoreWithClient("mailbox-bucket", client)
	_, err := store.PutContent(context.Background(), ContentInput{DeliveryID: "d", AgentID: "a", Body: "body"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected put error, got %v", err)
	}
}

func TestS3StoreGetContent(t *testing.T) {
	client := &fakeS3PutClient{getBody: "Hello mailbox", contentType: "text/plain"}
	store := NewS3StoreWithClient("mailbox-bucket", client)
	ptr, err := store.PutContent(context.Background(), ContentInput{
		DeliveryID: "comm-delivery-1",
		AgentID:    "0xabc",
		Body:       "Hello mailbox",
	})
	if err != nil {
		t.Fatalf("PutContent: %v", err)
	}
	out, err := store.GetContent(context.Background(), ptr, 1024)
	if err != nil {
		t.Fatalf("GetContent: %v", err)
	}
	if string(out.Body) != "Hello mailbox" || out.ContentType != "text/plain" || out.SHA256 != ptr.SHA256 || out.Bytes != ptr.Bytes {
		t.Fatalf("unexpected content: %#v", out)
	}
}

func TestS3StoreGetContentValidation(t *testing.T) {
	store := NewS3StoreWithClient("mailbox-bucket", &fakeS3PutClient{getBody: "Hello"})
	if _, err := store.GetContent(context.Background(), ContentPointer{Bucket: "other", Key: "k"}, 1024); err == nil {
		t.Fatalf("expected bucket mismatch")
	}
	if _, err := store.GetContent(context.Background(), ContentPointer{Bucket: "mailbox-bucket"}, 1024); err == nil {
		t.Fatalf("expected missing key")
	}
	if _, err := store.GetContent(context.Background(), ContentPointer{Bucket: "mailbox-bucket", Key: "k", SHA256: "bad"}, 1024); err == nil {
		t.Fatalf("expected sha mismatch")
	}
	if _, err := store.GetContent(context.Background(), ContentPointer{Bucket: "mailbox-bucket", Key: "k"}, 2); err == nil {
		t.Fatalf("expected maxBytes error")
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
