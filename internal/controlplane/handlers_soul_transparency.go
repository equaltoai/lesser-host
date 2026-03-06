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

type soulTransparencyResponse struct {
	Version      string `json:"version"`
	Transparency any    `json:"transparency"`
}

// --- Handler ---

// handleSoulPublicGetTransparency returns transparency information for an agent,
// extracted from the registration file.
func (s *Server) handleSoulPublicGetTransparency(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	// Extract transparency section from registration file.
	transparency := extractTransparency(reg)

	resp, err := apptheory.JSON(http.StatusOK, soulTransparencyResponse{
		Version:      "1",
		Transparency: transparency,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

// extractTransparency extracts the transparency section from a registration file.
// Returns the raw object if present, or a default empty object.
func extractTransparency(reg map[string]any) any {
	if t, ok := reg["transparency"]; ok {
		return t
	}

	// Build a minimal transparency object from known fields.
	out := map[string]any{}
	if model := extractStringField(reg, "model"); model != "" {
		out["model"] = model
	}
	if provider := extractStringField(reg, "provider"); provider != "" {
		out["provider"] = provider
	}
	if desc := extractStringField(reg, "selfDescription"); desc != "" {
		out["selfDescription"] = desc
	}

	if strings.TrimSpace(extractStringField(reg, "self_description")) != "" {
		out["selfDescription"] = extractStringField(reg, "self_description")
	}
	return out
}
