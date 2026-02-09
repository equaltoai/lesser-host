package controlplane

import (
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

type tipRegistryConfigResponse struct {
	Enabled         bool   `json:"enabled"`
	ChainID         int64  `json:"chain_id"`
	ContractAddress string `json:"contract_address"`
}

func (s *Server) handleTipRegistryConfig(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	contractAddr := strings.TrimSpace(s.cfg.TipContractAddress)
	if !s.cfg.TipEnabled || s.cfg.TipChainID <= 0 || contractAddr == "" {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	resp, err := apptheory.JSON(http.StatusOK, tipRegistryConfigResponse{
		Enabled:         true,
		ChainID:         s.cfg.TipChainID,
		ContractAddress: contractAddr,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	resp.Headers["cache-control"] = []string{"public, max-age=3600"}
	resp.Headers["access-control-allow-origin"] = []string{"*"}
	return resp, nil
}
