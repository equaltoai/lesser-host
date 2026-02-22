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
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type rendersBudgetTestDB struct {
	db      *ttmocks.MockExtendedDB
	qBudget *ttmocks.MockQuery
}

const testNormalizedURL = "https://example.com/"

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
	renderID := rendering.RenderArtifactIDForInstance(rendering.RenderPolicyVersion, "inst", testNormalizedURL)
	normalized := testNormalizedURL

	t.Run("budget_not_configured", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 200, resp.Status)

		var out renderArtifactResponse
		require.NoError(t, json.Unmarshal(resp.Body, &out))
		require.Equal(t, statusNotCheckedBudget, out.ErrorCode)
		require.Equal(t, budgetReasonNotConfigured, out.ErrorMessage)
	})

	t.Run("budget_lookup_error", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(errors.New("boom")).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		require.Nil(t, resp)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
	})

	t.Run("budget_exceeded", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
			*dest = models.InstanceBudgetMonth{IncludedCredits: 0, UsedCredits: 0}
			_ = dest.UpdateKeys()
		}).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 200, resp.Status)

		var out renderArtifactResponse
		require.NoError(t, json.Unmarshal(resp.Body, &out))
		require.Equal(t, statusNotCheckedBudget, out.ErrorCode)
		require.Equal(t, budgetReasonExceeded, out.ErrorMessage)
	})

	t.Run("transact_error_returns_budget_exceeded", func(t *testing.T) {
		t.Parallel()

		tdb := newRendersBudgetTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
			*dest = models.InstanceBudgetMonth{IncludedCredits: 100, UsedCredits: 0}
			_ = dest.UpdateKeys()
		}).Once()
		tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(errors.New("boom")).Once()

		resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, false, renderID, normalized, models.RenderRetentionClassBenign)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 200, resp.Status)

		var out renderArtifactResponse
		require.NoError(t, json.Unmarshal(resp.Body, &out))
		require.Equal(t, statusNotCheckedBudget, out.ErrorCode)
		require.Equal(t, budgetReasonExceeded, out.ErrorMessage)
	})

	t.Run("transact_success_no_overage", func(t *testing.T) {
		t.Parallel()

		runTransactSuccess := func(t *testing.T, allowOverage bool, includedCredits int64) {
			t.Helper()

			tdb := newRendersBudgetTestDB()
			s := &Server{store: store.New(tdb.db)}

			tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
				dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
				*dest = models.InstanceBudgetMonth{IncludedCredits: includedCredits, UsedCredits: 0}
				_ = dest.UpdateKeys()
			}).Once()
			tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

			resp, err := s.debitBudgetForCreateRender(&apptheory.Context{RequestID: "rid"}, "inst", now, allowOverage, renderID, normalized, models.RenderRetentionClassBenign)
			require.NoError(t, err)
			require.Nil(t, resp)
		}

		cases := []struct {
			name            string
			allowOverage    bool
			includedCredits int64
		}{
			{name: "no_overage", allowOverage: false, includedCredits: 100},
			{name: "allow_overage", allowOverage: true, includedCredits: 0},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				runTransactSuccess(t, tc.allowOverage, tc.includedCredits)
			})
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

	require.NoError(t, art.PutObject(ctx.Context(), "thumbKey", thumbBody, "", ""))
	require.NoError(t, art.PutObject(ctx.Context(), "snapKey", snapBody, "", ""))

	tdb := newRendersFlowTestDB()
	s := &Server{store: store.New(tdb.db), artifacts: art}

	renderID := rendering.RenderArtifactIDForInstance(rendering.RenderPolicyVersion, "inst", "https://example.com/")
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		*dest = models.RenderArtifact{ID: renderID, ThumbnailObjectKey: "thumbKey", SnapshotObjectKey: "snapKey", RequestedBy: "inst"}
		_ = dest.UpdateKeys()
	}).Twice()

	// Thumbnail: content type is detected when empty.
	resp, err := s.handleGetRenderThumbnail(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"renderId": renderID}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)
	require.Equal(t, string(thumbBody), string(resp.Body))
	require.Len(t, resp.Headers["content-type"], 1)
	require.Equal(t, http.DetectContentType(thumbBody), resp.Headers["content-type"][0])
	require.NotEmpty(t, resp.Headers["cache-control"])
	require.NotEmpty(t, resp.Headers["cache-control"][0])
	require.NotEmpty(t, resp.Headers["etag"])
	require.NotEmpty(t, resp.Headers["etag"][0])

	// Snapshot: defaults to text/plain when content type is empty.
	resp, err = s.handleGetRenderSnapshot(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"renderId": renderID}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)
	require.Equal(t, string(snapBody), string(resp.Body))
	require.Len(t, resp.Headers["content-type"], 1)
	require.Equal(t, "text/plain; charset=utf-8", resp.Headers["content-type"][0])
	require.Len(t, resp.Headers["cache-control"], 1)
	require.Equal(t, "private, max-age=600", resp.Headers["cache-control"][0])
	require.NotEmpty(t, resp.Headers["etag"])
	require.NotEmpty(t, resp.Headers["etag"][0])
}
