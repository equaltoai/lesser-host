package trust

import (
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestRequestBaseURL(t *testing.T) {
	t.Parallel()

	if got := requestBaseURL(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
		"x-forwarded-host":  {"example.com"},
		"x-forwarded-proto": {"http"},
	}}}
	if got := requestBaseURL(ctx); got != "http://example.com" {
		t.Fatalf("unexpected base URL: %q", got)
	}

	ctx = &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
		"host": {"example.com"},
	}}}
	if got := requestBaseURL(ctx); got != testURLExampleCom {
		t.Fatalf("unexpected base URL: %q", got)
	}
}

func TestLinkPreviewBadRequestError(t *testing.T) {
	t.Parallel()

	err := linkPreviewBadRequestError(&linkPreviewError{Code: errorCodeInvalidURL, Message: "nope"})
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != appErrCodeBadRequest || appErr.Message != "nope" {
		t.Fatalf("unexpected error: %T: %v", err, err)
	}

	err = linkPreviewBadRequestError(assertErr{})
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("unexpected error: %T: %v", err, err)
	}
}

func TestLinkPreviewResponseDisabled(t *testing.T) {
	t.Parallel()

	out := linkPreviewResponseDisabled(" id ", " url ")
	if out.Status != statusDisabled || out.ErrorCode != statusDisabled || out.ID != "id" || out.NormalizedURL != testURL {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestParseLinkPreviewNormalizedURL(t *testing.T) {
	t.Parallel()

	got, err := parseLinkPreviewNormalizedURL("https://bücher.example/path/../?b=2&a=1#frag")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != testNormalizedBucherURL {
		t.Fatalf("unexpected normalized: %q", got)
	}

	if _, err := parseLinkPreviewNormalizedURL("not a url"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestHandleLinkPreview_DisabledByConfig(t *testing.T) {
	t.Parallel()

	hpe := false

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:                  "inst",
			HostedPreviewsEnabled: &hpe,
		}
	}).Once()

	s := &Server{store: store.New(db)}

	body, _ := json.Marshal(linkPreviewRequest{URL: testURLExampleCom})
	ctx := &apptheory.Context{
		AuthIdentity: "inst",
		Request:      apptheory.Request{Body: body},
	}

	resp, err := s.handleLinkPreview(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200 response, got %#v", resp)
	}

	var parsed linkPreviewResponse
	if unmarshalErr := json.Unmarshal(resp.Body, &parsed); unmarshalErr != nil {
		t.Fatalf("unmarshal response: %v", unmarshalErr)
	}
	if parsed.Status != statusDisabled || parsed.ErrorCode != statusDisabled {
		t.Fatalf("unexpected parsed response: %#v", parsed)
	}
}

func TestPreviewHelpers_ImageKeysAndIDs(t *testing.T) {
	t.Parallel()

	if got := linkPreviewImageObjectKey("inst", " img "); got != "link-previews/inst/images/img" {
		t.Fatalf("unexpected object key: %q", got)
	}

	id1 := imageIDFromNormalizedURL("https://example.com/")
	id2 := imageIDFromNormalizedURL("https://example.com/")
	if id1 == "" || id1 != id2 || len(id1) != 64 {
		t.Fatalf("unexpected imageID: %q %q", id1, id2)
	}
}

func TestAttestationURL(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{}
	if got := attestationURL(ctx, " "); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	ctx = &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"host": {"example.com"}}}}
	if got := attestationURL(ctx, "id"); got != "https://example.com/attestations/id" {
		t.Fatalf("unexpected url: %q", got)
	}

	ctx = &apptheory.Context{}
	if got := attestationURL(ctx, "id"); got != "/attestations/id" {
		t.Fatalf("unexpected url: %q", got)
	}
}
