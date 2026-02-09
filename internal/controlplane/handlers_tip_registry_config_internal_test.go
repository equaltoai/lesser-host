package controlplane

import (
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
)

func TestHandleTipRegistryConfig_NotFoundWhenNotConfigured(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{}}
	if _, err := s.handleTipRegistryConfig(&apptheory.Context{}); err == nil {
		t.Fatalf("expected not_found")
	} else if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.not_found" {
		t.Fatalf("expected app.not_found, got %#v", err)
	}
}

func TestHandleTipRegistryConfig_Success(t *testing.T) {
	t.Parallel()

	s := &Server{
		cfg: config.Config{
			TipEnabled:         true,
			TipChainID:         11155111,
			TipContractAddress: "0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe",
		},
	}

	resp, err := s.handleTipRegistryConfig(&apptheory.Context{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.Status)

	var out tipRegistryConfigResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.True(t, out.Enabled)
	require.Equal(t, int64(11155111), out.ChainID)
	require.Equal(t, "0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe", out.ContractAddress)

	require.Equal(t, []string{"public, max-age=3600"}, resp.Headers["cache-control"])
	require.Equal(t, []string{"*"}, resp.Headers["access-control-allow-origin"])
}
