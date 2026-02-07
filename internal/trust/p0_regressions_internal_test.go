package trust

import (
	"context"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/mock"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestP0_InstanceAuthHook_RevokedKeyIsNotAuthenticated(t *testing.T) {
	t.Parallel()

	qKey := new(ttmocks.MockQuery)
	db := newTestDBWithModelQueries(modelQueryPair{
		model: &models.InstanceKey{},
		query: qKey,
	})

	s := &Server{store: store.New(db)}

	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{
			ID:           "keyid",
			InstanceSlug: "slug",
			RevokedAt:    time.Now().UTC(),
		}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{
		RequestID: "rid",
		Request: apptheory.Request{
			Headers: map[string][]string{"authorization": {"Bearer raw"}},
		},
	}

	slug, err := s.InstanceAuthHook(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slug != "" {
		t.Fatalf("expected empty slug, got %q", slug)
	}
}

func TestP0_PreviewFetcher_BlocksPrivateIP(t *testing.T) {
	t.Parallel()

	_, u, err := normalizeLinkURL("http://127.0.0.1/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}

	err = validateOutboundURL(context.Background(), nil, u)
	if err == nil {
		t.Fatal("expected error")
	}
	pe, ok := err.(*linkPreviewError)
	if !ok {
		t.Fatalf("expected *linkPreviewError, got %T: %v", err, err)
	}
	if pe.Code != errorCodeBlockedSSRF {
		t.Fatalf("expected %q, got %q", errorCodeBlockedSSRF, pe.Code)
	}
}
