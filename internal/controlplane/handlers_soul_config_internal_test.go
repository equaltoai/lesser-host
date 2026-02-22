package controlplane

import (
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

const (
	appErrCodeInternal = "app.internal"
	appErrCodeNotFound = "app.not_found"

	testSoulContractAddr = "0x0000000000000000000000000000000000000001"
	testSoulSafeAddr     = "0x0000000000000000000000000000000000000002"
)

func TestSoulConfig_RequireConfiguredAndRPC(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()

	t.Run("requireSoulRegistryConfigured", func(t *testing.T) {
		t.Parallel()

		var nilServer *Server
		appErr := nilServer.requireSoulRegistryConfigured()
		require.NotNil(t, appErr)
		require.Equal(t, appErrCodeInternal, appErr.Code)

		s := &Server{store: store.New(db), cfg: config.Config{}}
		appErr = s.requireSoulRegistryConfigured()
		require.NotNil(t, appErr)
		require.Equal(t, appErrCodeConflict, appErr.Code)

		s.cfg.SoulEnabled = true
		appErr = s.requireSoulRegistryConfigured()
		require.NotNil(t, appErr)
		require.Equal(t, appErrCodeConflict, appErr.Code)

		s.cfg.SoulChainID = 1
		appErr = s.requireSoulRegistryConfigured()
		require.NotNil(t, appErr)
		require.Equal(t, appErrCodeConflict, appErr.Code)

		s.cfg.SoulRegistryContractAddress = testNotAnAddress
		appErr = s.requireSoulRegistryConfigured()
		require.NotNil(t, appErr)
		require.Equal(t, appErrCodeConflict, appErr.Code)

		s.cfg.SoulRegistryContractAddress = testSoulContractAddr
		appErr = s.requireSoulRegistryConfigured()
		require.Nil(t, appErr)
	})

	t.Run("requireSoulRPCConfigured", func(t *testing.T) {
		t.Parallel()

		s := &Server{store: store.New(db), cfg: config.Config{SoulRPCURL: ""}}
		appErr := s.requireSoulRPCConfigured()
		require.NotNil(t, appErr)
		require.Equal(t, appErrCodeConflict, appErr.Code)

		s.cfg.SoulRPCURL = "http://127.0.0.1:8545"
		require.Nil(t, s.requireSoulRPCConfigured())
	})
}

func TestHandleSoulConfig_ErrorsAndSuccess(t *testing.T) {
	t.Parallel()

	t.Run("internal_errors", func(t *testing.T) {
		t.Parallel()

		var nilServer *Server
		resp, err := nilServer.handleSoulConfig(&apptheory.Context{})
		require.Nil(t, resp)
		require.NotNil(t, err)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, appErrCodeInternal, appErr.Code)

		s := &Server{cfg: config.Config{SoulEnabled: true}}
		resp, err = s.handleSoulConfig(nil)
		require.Nil(t, resp)
		require.NotNil(t, err)
		appErr = nil
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, appErrCodeInternal, appErr.Code)
	})

	t.Run("not_found_when_unconfigured", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{SoulEnabled: false}}
		resp, err := s.handleSoulConfig(&apptheory.Context{})
		require.Nil(t, resp)
		require.NotNil(t, err)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, appErrCodeNotFound, appErr.Code)

		s.cfg.SoulEnabled = true
		s.cfg.SoulChainID = 1
		s.cfg.SoulRegistryContractAddress = testNotAnAddress
		resp, err = s.handleSoulConfig(&apptheory.Context{})
		require.Nil(t, resp)
		require.NotNil(t, err)
		appErr = nil
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, appErrCodeNotFound, appErr.Code)
	})

	t.Run("success_response_and_headers", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 5,
			SoulRegistryContractAddress: testSoulContractAddr,
			SoulAdminSafeAddress:        testSoulSafeAddr,
			SoulTxMode:                  "SAFE",
			SoulSupportedCapabilities: []string{
				"  B  ",
				"",
				"a",
				"b",
				"has space",
				"b",
				"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			},
		}}

		resp, err := s.handleSoulConfig(&apptheory.Context{})
		require.NotNil(t, resp)
		require.Nil(t, err)
		require.Equal(t, 200, resp.Status)

		var parsed soulConfigResponse
		require.NoError(t, json.Unmarshal(resp.Body, &parsed))

		require.True(t, parsed.Enabled)
		require.Equal(t, int64(5), parsed.ChainID)
		require.Equal(t, testSoulContractAddr, parsed.RegistryContractAddress)
		require.Equal(t, testSoulSafeAddr, parsed.AdminSafeAddress)
		require.Equal(t, "safe", parsed.TxMode)
		require.Equal(t, []string{"a", "b"}, parsed.SupportedCapabilities)

		require.NotNil(t, resp.Headers)
		require.Equal(t, []string{"public, max-age=3600"}, resp.Headers["cache-control"])
		require.Equal(t, []string{"*"}, resp.Headers["access-control-allow-origin"])
	})
}
