package controlplane

import (
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/runtime"
)

type soulConfigResponse struct {
	Enabled                 bool     `json:"enabled"`
	ChainID                 int64    `json:"chain_id"`
	RegistryContractAddress string   `json:"registry_contract_address"`
	AdminSafeAddress        string   `json:"admin_safe_address,omitempty"`
	TxMode                  string   `json:"tx_mode,omitempty"`
	SupportedCapabilities   []string `json:"supported_capabilities,omitempty"`
}

func (s *Server) requireSoulRegistryConfigured() *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return &apptheory.AppError{Code: "app.conflict", Message: "soul registry is not configured"}
	}
	if s.cfg.SoulChainID <= 0 || strings.TrimSpace(s.cfg.SoulRegistryContractAddress) == "" {
		return &apptheory.AppError{Code: "app.conflict", Message: "soul registry is not configured"}
	}
	if !common.IsHexAddress(strings.TrimSpace(s.cfg.SoulRegistryContractAddress)) {
		return &apptheory.AppError{Code: "app.conflict", Message: "soul registry is not configured"}
	}
	return nil
}

func (s *Server) handleSoulConfig(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	contractAddr := strings.TrimSpace(s.cfg.SoulRegistryContractAddress)
	if !s.cfg.SoulEnabled || s.cfg.SoulChainID <= 0 || contractAddr == "" || !common.IsHexAddress(contractAddr) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	caps := normalizeSoulCapabilitiesLoose(s.cfg.SoulSupportedCapabilities)

	resp, err := apptheory.JSON(http.StatusOK, soulConfigResponse{
		Enabled:                 true,
		ChainID:                 s.cfg.SoulChainID,
		RegistryContractAddress: strings.ToLower(contractAddr),
		AdminSafeAddress:        strings.ToLower(strings.TrimSpace(s.cfg.SoulAdminSafeAddress)),
		TxMode:                  strings.ToLower(strings.TrimSpace(s.cfg.SoulTxMode)),
		SupportedCapabilities:   caps,
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
