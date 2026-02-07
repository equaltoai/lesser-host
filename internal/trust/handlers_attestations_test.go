package trust

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/attestations"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestHandleWellKnownJWKS_NotFoundWhenDisabled(t *testing.T) {
	t.Parallel()

	s := NewServer(configForTests(), nil)
	_, err := s.handleWellKnownJWKS(&apptheory.Context{})
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.not_found" {
		t.Fatalf("expected not_found, got %T: %v", err, err)
	}
}

func TestHandleGetAttestation_InvalidID(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	s := &Server{store: store.New(db)}

	ctx := &apptheory.Context{Params: map[string]string{"id": "nope"}}
	_, err := s.handleGetAttestation(ctx)
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.bad_request" {
		t.Fatalf("expected bad_request, got %T: %v", err, err)
	}
}

func TestServeAttestationByID_NotFound(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("ConsistentRead").Return(q).Maybe()
	q.On("First", mock.Anything).Return(theoryErrors.ErrItemNotFound).Once()

	s := &Server{store: store.New(db)}
	ctx := &apptheory.Context{}
	_, err := s.serveAttestationByID(ctx, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.not_found" {
		t.Fatalf("expected not_found, got %T: %v", err, err)
	}
}

func TestServeAttestationByID_Success(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"ok":true}`)
	jws, err := attestations.BuildCompactJWSRS256(context.Background(), "kid", payload, func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("sig"), nil
	})
	if err != nil {
		t.Fatalf("BuildCompactJWSRS256: %v", err)
	}

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("ConsistentRead").Return(q).Maybe()
	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Attestation)
		*dest = models.Attestation{
			ID:        "id",
			JWS:       jws,
			ExpiresAt: time.Now().UTC().Add(1 * time.Hour),
		}
	}).Once()

	s := &Server{store: store.New(db)}

	ctx := &apptheory.Context{}
	resp, err := s.serveAttestationByID(ctx, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("serveAttestationByID: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200 response, got %#v", resp)
	}
	if len(resp.Headers["cache-control"]) == 0 {
		t.Fatalf("expected cache-control header")
	}

	var parsed attestationResponse
	if unmarshalErr := json.Unmarshal(resp.Body, &parsed); unmarshalErr != nil {
		t.Fatalf("unmarshal response: %v", unmarshalErr)
	}
	if parsed.JWS == "" || parsed.ID != "id" {
		t.Fatalf("unexpected parsed response: %#v", parsed)
	}
	if string(parsed.Payload) != string(payload) {
		t.Fatalf("payload mismatch: %s", string(parsed.Payload))
	}
}

func configForTests() config.Config { return config.Config{ArtifactBucketName: "bucket"} }
