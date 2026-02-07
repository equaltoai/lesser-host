package trust

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store"
)

func TestAIRateLimitMiddleware_Basic(t *testing.T) {
	t.Parallel()

	original := os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")
	t.Cleanup(func() {
		_ = os.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", original)
	})
	_ = os.Unsetenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")

	require.Nil(t, (&Server{}).aiRateLimitMiddleware())

	require.Nil(t, (&Server{store: store.New(nil)}).aiRateLimitMiddleware())

	db := ttmocks.NewMockExtendedDB()

	require.Nil(t, (&Server{store: store.New(db)}).aiRateLimitMiddleware())

	s := &Server{
		cfg:   configForTests(),
		store: store.New(db),
	}
	s.cfg.StateTableName = "tbl"

	mw := s.aiRateLimitMiddleware()
	require.NotNil(t, mw)

	var called int
	next := func(_ *apptheory.Context) (*apptheory.Response, error) {
		called++
		return apptheory.JSON(200, map[string]any{"ok": true})
	}

	h := mw(next)
	require.NotNil(t, h)

	resp, err := h(&apptheory.Context{Request: apptheory.Request{Path: "/healthz"}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)
	require.Equal(t, 1, called)

	resp, err = h(&apptheory.Context{Request: apptheory.Request{Path: ""}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)
	require.Equal(t, 2, called)

	resp, err = h(nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)
	require.Equal(t, 3, called)

	require.Nil(t, mw(nil))
}
