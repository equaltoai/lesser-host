package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
)

// --- Response types ---

type soulCapability struct {
	Capability    string         `json:"capability"`
	Scope         string         `json:"scope,omitempty"`
	Constraints   map[string]any `json:"constraints,omitempty"`
	ClaimLevel    string         `json:"claim_level"`
	LastValidated string         `json:"last_validated,omitempty"`
	ValidationRef string         `json:"validation_ref,omitempty"`
	DegradesTo    string         `json:"degrades_to,omitempty"`
}

type soulListCapabilitiesResponse struct {
	Version      string           `json:"version"`
	Capabilities []soulCapability `json:"capabilities"`
	Count        int              `json:"count"`
}

// --- Handler ---

// handleSoulPublicGetCapabilities returns structured capabilities for an agent,
// extracted from the registration file.
func (s *Server) handleSoulPublicGetCapabilities(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	// Read the registration file from S3.
	key := soulRegistrationS3Key(agentIDHex)
	body, _, _, err := s.soulPacks.GetObject(ctx.Context(), key, 512*1024)
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to fetch registration"}
	}

	var reg map[string]any
	unmarshalErr := json.Unmarshal(body, &reg)
	if unmarshalErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to parse registration"}
	}

	caps := extractStructuredCapabilities(reg)

	resp, err := apptheory.JSON(http.StatusOK, soulListCapabilitiesResponse{
		Version:      "2",
		Capabilities: caps,
		Count:        len(caps),
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

// extractStructuredCapabilities extracts capabilities from a registration file,
// promoting v1 flat strings to structured format with the default self-declared claim level.
func extractStructuredCapabilities(reg map[string]any) []soulCapability {
	raw, ok := reg["capabilities"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	out := make([]soulCapability, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			// v1: flat string -> promote to structured with the default claim level.
			name := strings.TrimSpace(v)
			if name != "" {
				out = append(out, soulCapability{
					Capability: name,
					ClaimLevel: soulClaimLevelSelfDeclared,
				})
			}
		case map[string]any:
			cap := extractStringField(v, "capability")
			if cap == "" {
				continue
			}
			cl := extractStringField(v, "claimLevel")
			if cl == "" {
				cl = extractStringField(v, "claim_level")
			}
			if cl == "" {
				cl = soulClaimLevelSelfDeclared
			}
			out = append(out, soulCapability{
				Capability:    cap,
				Scope:         extractStringField(v, "scope"),
				Constraints:   extractCapabilityConstraintsObject(v),
				ClaimLevel:    strings.ToLower(strings.TrimSpace(cl)),
				LastValidated: extractStringField(v, "lastValidated"),
				ValidationRef: extractStringField(v, "validationRef"),
				DegradesTo:    extractStringField(v, "degradesTo"),
			})
		}
	}
	return out
}

func extractCapabilityConstraintsObject(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	raw, ok := m["constraints"]
	if !ok || raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case map[string]any:
		return v
	case string:
		// Backward-compatible: sometimes constraints may be encoded as a JSON string.
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			return nil
		}
		return obj
	default:
		return nil
	}
}
