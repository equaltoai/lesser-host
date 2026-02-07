package trust

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type rendersBudgetTestDB struct {
	db      *ttmocks.MockExtendedDB
	qBudget *ttmocks.MockQuery
}

func newRendersBudgetTestDB() rendersBudgetTestDB {
	db := ttmocks.NewMockExtendedDBStrict()
	qBudget := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()

	qBudget.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qBudget).Maybe()
	qBudget.On("ConsistentRead").Return(qBudget).Maybe()

	return rendersBudgetTestDB{db: db, qBudget: qBudget}
}

func TestDebitBudgetForCreateRender_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, "https://example.com/")
	normalized := "https://example.com/"

	t.Run("budget_not_configured", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out renderArtifactResponse
		if unmarshalErr := json.Unmarshal(resp.Body, &out); unmarshalErr != nil {
			t.Fatalf("unmarshal: %v", unmarshalErr)
		}
		if out.ErrorCode != "not_checked_budget" || out.ErrorMessage != "budget not configured" {
			t.Fatalf("unexpected response: %#v", out)
		}
	})

	t.Run("budget_lookup_error", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(errors.New("boom")).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		if resp != nil {
			t.Fatalf("expected nil resp, got %#v", resp)
		}
		if _, ok := err.(*apptheory.AppError); !ok {
			t.Fatalf("expected AppError, got %T", err)
		}
	})

	t.Run("budget_exceeded", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.InstanceBudgetMonth)
			*dest = models.InstanceBudgetMonth{IncludedCredits: 0, UsedCredits: 0}
			_ = dest.UpdateKeys()
		}).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out renderArtifactResponse
		_ = json.Unmarshal(resp.Body, &out)
		if out.ErrorCode != "not_checked_budget" || out.ErrorMessage != "budget exceeded" {
			t.Fatalf("unexpected response: %#v", out)
		}
	})

	t.Run("transact_error_returns_budget_exceeded", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.InstanceBudgetMonth)
			*dest = models.InstanceBudgetMonth{IncludedCredits: 100, UsedCredits: 0}
			_ = dest.UpdateKeys()
		}).Once()
		tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(errors.New("boom")).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out renderArtifactResponse
		_ = json.Unmarshal(resp.Body, &out)
		if out.ErrorCode != "not_checked_budget" || out.ErrorMessage != "budget exceeded" {
			t.Fatalf("unexpected response: %#v", out)
		}
	})

	t.Run("transact_success_no_overage", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.InstanceBudgetMonth)
			*dest = models.InstanceBudgetMonth{IncludedCredits: 100, UsedCredits: 0}
			_ = dest.UpdateKeys()
		}).Once()
		tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		if err != nil || resp != nil {
			t.Fatalf("expected nil response, got resp=%#v err=%v", resp, err)
		}
	})

	t.Run("transact_success_allow_overage", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.InstanceBudgetMonth)
			*dest = models.InstanceBudgetMonth{IncludedCredits: 0, UsedCredits: 0}
			_ = dest.UpdateKeys()
		}).Once()
		tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, true, renderID, normalized, models.RenderRetentionClassBenign)
		if err != nil || resp != nil {
			t.Fatalf("expected nil response, got resp=%#v err=%v", resp, err)
		}
	})
}

func TestHandleGetRenderThumbnail_AndSnapshot_Success(t *testing.T) {
	t.Parallel()

	ctx := apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}

	art, cleanup := newTestArtifactsStore(t, "bucket")
	t.Cleanup(cleanup)

	thumbBody := []byte("thumb")
	snapBody := []byte("snapshot")

	if err := art.PutObject(ctx.Context(), "thumbKey", thumbBody, "", ""); err != nil {
		t.Fatalf("PutObject thumb: %v", err)
	}
	if err := art.PutObject(ctx.Context(), "snapKey", snapBody, "", ""); err != nil {
		t.Fatalf("PutObject snap: %v", err)
	}

	tdb := newRendersFlowTestDB()
	s := &Server{store: store.New(tdb.db), artifacts: art}

	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, "https://example.com/")
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.RenderArtifact)
		*dest = models.RenderArtifact{ID: renderID, ThumbnailObjectKey: "thumbKey", SnapshotObjectKey: "snapKey"}
		_ = dest.UpdateKeys()
	}).Twice()

	// Thumbnail: content type is detected when empty.
	resp, err := s.handleGetRenderThumbnail(&apptheory.Context{Params: map[string]string{"renderId": renderID}})
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("thumbnail resp=%#v err=%v", resp, err)
	}
	if string(resp.Body) != string(thumbBody) {
		t.Fatalf("unexpected thumbnail body: %q", string(resp.Body))
	}
	if got := resp.Headers["content-type"]; len(got) != 1 || got[0] != http.DetectContentType(thumbBody) {
		t.Fatalf("unexpected thumbnail content-type: %#v", resp.Headers)
	}
	if got := resp.Headers["cache-control"]; len(got) == 0 || got[0] == "" {
		t.Fatalf("expected cache-control header, got %#v", resp.Headers)
	}
	if got := resp.Headers["etag"]; len(got) == 0 || got[0] == "" {
		t.Fatalf("expected etag header, got %#v", resp.Headers)
	}

	// Snapshot: defaults to text/plain when content type is empty.
	resp, err = s.handleGetRenderSnapshot(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"renderId": renderID}})
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("snapshot resp=%#v err=%v", resp, err)
	}
	if string(resp.Body) != string(snapBody) {
		t.Fatalf("unexpected snapshot body: %q", string(resp.Body))
	}
	if got := resp.Headers["content-type"]; len(got) != 1 || got[0] != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected snapshot content-type: %#v", resp.Headers)
	}
	if got := resp.Headers["cache-control"]; len(got) != 1 || got[0] != "private, max-age=600" {
		t.Fatalf("unexpected snapshot cache-control: %#v", resp.Headers)
	}
	if got := resp.Headers["etag"]; len(got) == 0 || got[0] == "" {
		t.Fatalf("expected snapshot etag header, got %#v", resp.Headers)
	}
}
