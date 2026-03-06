package controlplane

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func instanceBoolPtr(v bool) *bool {
	return &v
}

func instanceInt64Ptr(v int64) *int64 {
	return &v
}

func instanceStringPtr(v string) *string {
	return &v
}

func requireInstanceAppErrorCode(t *testing.T, err error, code string) {
	t.Helper()

	var appErr *apptheory.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, code, appErr.Code)
}

func seedInstanceLookup(t *testing.T, qInst interface {
	On(string, ...interface{}) *mock.Call
}, err error) {
	t.Helper()

	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(err).Run(func(args mock.Arguments) {
		if err != nil {
			return
		}
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive}
	}).Once()
}

func unsetMockQueryMethod(q *ttmocks.MockQuery, method string) {
	for _, call := range q.ExpectedCalls {
		if call.Method == method {
			call.Unset()
		}
	}
}

func TestBuildInstanceConfigUpdate_TipAndViralityBranches(t *testing.T) {
	t.Parallel()

	t.Run("rejects_negative_tip_chain", func(t *testing.T) {
		_, _, err := buildInstanceConfigUpdate("demo", updateInstanceConfigRequest{
			TipChainID: instanceInt64Ptr(-1),
		})
		requireInstanceAppErrorCode(t, err, appErrCodeBadRequest)
	})

	t.Run("rejects_negative_moderation_virality", func(t *testing.T) {
		_, _, err := buildInstanceConfigUpdate("demo", updateInstanceConfigRequest{
			ModerationViralityMin: instanceInt64Ptr(-1),
		})
		requireInstanceAppErrorCode(t, err, appErrCodeBadRequest)
	})

	t.Run("accepts_tip_and_lesser_fields", func(t *testing.T) {
		update, fields, err := buildInstanceConfigUpdate("demo", updateInstanceConfigRequest{
			TranslationEnabled:              instanceBoolPtr(true),
			TipEnabled:                      instanceBoolPtr(true),
			TipChainID:                      instanceInt64Ptr(10),
			TipContractAddress:              instanceStringPtr(" 0xabc  "),
			ModerationViralityMin:           instanceInt64Ptr(7),
			LesserAIEnabled:                 instanceBoolPtr(false),
			LesserAIModerationEnabled:       instanceBoolPtr(true),
			LesserAINsfwDetectionEnabled:    instanceBoolPtr(true),
			LesserAISpamDetectionEnabled:    instanceBoolPtr(false),
			LesserAIPiiDetectionEnabled:     instanceBoolPtr(true),
			LesserAIContentDetectionEnabled: instanceBoolPtr(false),
		})
		require.NoError(t, err)
		require.Equal(t, "demo", update.Slug)
		require.NotNil(t, update.TranslationEnabled)
		require.True(t, *update.TranslationEnabled)
		require.NotNil(t, update.TipEnabled)
		require.True(t, *update.TipEnabled)
		require.Equal(t, int64(10), update.TipChainID)
		require.Equal(t, "0xabc", update.TipContractAddress)
		require.Equal(t, int64(7), update.ModerationViralityMin)
		require.NotNil(t, update.LesserAIEnabled)
		require.False(t, *update.LesserAIEnabled)
		require.NotNil(t, update.LesserAIModerationEnabled)
		require.True(t, *update.LesserAIModerationEnabled)
		require.NotNil(t, update.LesserAINsfwDetectionEnabled)
		require.True(t, *update.LesserAINsfwDetectionEnabled)
		require.NotNil(t, update.LesserAISpamDetectionEnabled)
		require.False(t, *update.LesserAISpamDetectionEnabled)
		require.NotNil(t, update.LesserAIPiiDetectionEnabled)
		require.True(t, *update.LesserAIPiiDetectionEnabled)
		require.NotNil(t, update.LesserAIContentDetectionEnabled)
		require.False(t, *update.LesserAIContentDetectionEnabled)
		require.Contains(t, fields, "TranslationEnabled")
		require.Contains(t, fields, "TipEnabled")
		require.Contains(t, fields, "TipChainID")
		require.Contains(t, fields, "TipContractAddress")
		require.Contains(t, fields, "ModerationViralityMin")
		require.Contains(t, fields, "LesserAIEnabled")
		require.Contains(t, fields, "LesserAIModerationEnabled")
		require.Contains(t, fields, "LesserAINsfwDetectionEnabled")
		require.Contains(t, fields, "LesserAISpamDetectionEnabled")
		require.Contains(t, fields, "LesserAIPiiDetectionEnabled")
		require.Contains(t, fields, "LesserAIContentDetectionEnabled")
	})
}

func TestEffectiveInstanceConfigHelpers_MoreCoverage(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(0), effectiveModerationViralityMin(-1))
	require.Equal(t, int64(4), effectiveModerationViralityMin(4))
	require.Equal(t, int64(0), effectiveTipChainID(-1))
	require.Equal(t, int64(11155111), effectiveTipChainID(11155111))

	require.False(t, effectiveTranslationEnabled(nil))
	require.True(t, effectiveTranslationEnabled(instanceBoolPtr(true)))
	require.False(t, effectiveSoulEnabled(nil))
	require.True(t, effectiveSoulEnabled(instanceBoolPtr(true)))
	require.True(t, effectiveBodyEnabled(nil))
	require.False(t, effectiveBodyEnabled(instanceBoolPtr(false)))
	require.False(t, effectiveTipEnabled(nil))
	require.True(t, effectiveTipEnabled(instanceBoolPtr(true)))

	require.True(t, effectiveLesserAIEnabled(nil))
	require.False(t, effectiveLesserAIEnabled(instanceBoolPtr(false)))
	require.True(t, effectiveLesserAIModerationEnabled(nil))
	require.False(t, effectiveLesserAIModerationEnabled(instanceBoolPtr(false)))
	require.True(t, effectiveLesserAINsfwDetectionEnabled(nil))
	require.False(t, effectiveLesserAINsfwDetectionEnabled(instanceBoolPtr(false)))
	require.True(t, effectiveLesserAISpamDetectionEnabled(nil))
	require.False(t, effectiveLesserAISpamDetectionEnabled(instanceBoolPtr(false)))
	require.False(t, effectiveLesserAIPiiDetectionEnabled(nil))
	require.True(t, effectiveLesserAIPiiDetectionEnabled(instanceBoolPtr(true)))
	require.False(t, effectiveLesserAIContentDetectionEnabled(nil))
	require.True(t, effectiveLesserAIContentDetectionEnabled(instanceBoolPtr(true)))
}

func TestHandleUpdateInstanceConfig_Branches(t *testing.T) {
	t.Parallel()

	t.Run("rejects_missing_slug", func(t *testing.T) {
		tdb := newAdminInstanceTestDB()
		s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

		_, err := s.handleUpdateInstanceConfig(adminCtx())
		requireInstanceAppErrorCode(t, err, appErrCodeBadRequest)
	})

	t.Run("returns_not_found_when_instance_missing", func(t *testing.T) {
		tdb := newAdminInstanceTestDB()
		s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}
		seedInstanceLookup(t, tdb.qInst, theoryErrors.ErrItemNotFound)

		ctx := adminCtx()
		ctx.Params = map[string]string{"slug": "demo"}
		ctx.Request.Body = []byte(`{"tip_contract_address":"0xabc"}`)

		_, err := s.handleUpdateInstanceConfig(ctx)
		requireInstanceAppErrorCode(t, err, appErrCodeNotFound)
	})

	t.Run("returns_parse_error_for_invalid_json", func(t *testing.T) {
		tdb := newAdminInstanceTestDB()
		s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}
		seedInstanceLookup(t, tdb.qInst, nil)

		ctx := adminCtx()
		ctx.Params = map[string]string{"slug": "demo"}
		ctx.Request.Body = []byte(`{`)

		_, err := s.handleUpdateInstanceConfig(ctx)
		require.Error(t, err)
	})

	t.Run("maps_condition_failure_to_not_found", func(t *testing.T) {
		tdb := newAdminInstanceTestDB()
		s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}
		seedInstanceLookup(t, tdb.qInst, nil)
		unsetMockQueryMethod(tdb.qInst, "Update")
		tdb.qInst.On("Update", mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"slug": "demo"}
		ctx.Request.Body = []byte(`{"tip_contract_address":"0xabc"}`)

		_, err := s.handleUpdateInstanceConfig(ctx)
		requireInstanceAppErrorCode(t, err, appErrCodeNotFound)
	})

	t.Run("returns_internal_when_audit_write_fails", func(t *testing.T) {
		tdb := newAdminInstanceTestDB()
		s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}
		seedInstanceLookup(t, tdb.qInst, nil)
		tdb.qInst.On("Update", "TipContractAddress").Return(nil).Once()
		unsetMockQueryMethod(tdb.qAudit, "Create")
		tdb.qAudit.On("Create").Return(errors.New("boom")).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"slug": "demo"}
		ctx.Request.Body = []byte(`{"tip_contract_address":"0xabc"}`)

		_, err := s.handleUpdateInstanceConfig(ctx)
		requireInstanceAppErrorCode(t, err, appErrCodeInternal)
	})

	t.Run("returns_internal_when_reload_fails", func(t *testing.T) {
		tdb := newAdminInstanceTestDB()
		s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}
		seedInstanceLookup(t, tdb.qInst, nil)
		seedInstanceLookup(t, tdb.qInst, errors.New("boom"))
		tdb.qInst.On("Update", "TipContractAddress").Return(nil).Once()
		tdb.qAudit.On("Create").Return(nil).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"slug": "demo"}
		ctx.Request.Body = []byte(`{"tip_contract_address":"0xabc"}`)

		_, err := s.handleUpdateInstanceConfig(ctx)
		requireInstanceAppErrorCode(t, err, appErrCodeInternal)
	})
}
