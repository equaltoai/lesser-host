package controlplane

import (
	"bytes"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func encodeMintConversationBlob(raw string) string {
	return models.EncodeSoulMintConversationBlob(raw)
}

func decodeMintConversationBlob(raw string) string {
	return models.DecodeSoulMintConversationBlob(raw)
}

func decodeMintConversationFields(conv *models.SoulAgentMintConversation) {
	if conv == nil {
		return
	}
	conv.Messages = decodeMintConversationBlob(conv.Messages)
	conv.ProducedDeclarations = decodeMintConversationBlob(conv.ProducedDeclarations)
}

func parseMintConversationFinalizeBeginRequestBody(ctx *apptheory.Context) (soulMintConversationFinalizeBeginRequest, error) {
	var req soulMintConversationFinalizeBeginRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return req, parseErr
	}
	if len(req.BoundarySignatures) == 0 {
		return req, newAppTheoryError("app.bad_request", "boundary_signatures is required")
	}
	return req, nil
}

func parseMintConversationFinalizeBeginRequestBodyOptional(ctx *apptheory.Context) (soulMintConversationFinalizeBeginRequest, error) {
	var req soulMintConversationFinalizeBeginRequest
	if ctx != nil && len(bytes.TrimSpace(ctx.Request.Body)) > 0 {
		if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
			return req, parseErr
		}
	}
	if req.BoundarySignatures == nil {
		req.BoundarySignatures = map[string]string{}
	}
	return req, nil
}

func parseMintConversationFinalizeRequestBody(ctx *apptheory.Context) (soulMintConversationFinalizeRequest, time.Time, *int, string, error) {
	var req soulMintConversationFinalizeRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return req, time.Time{}, nil, "", parseErr
	}
	if len(req.BoundarySignatures) == 0 {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "boundary_signatures is required")
	}
	issuedAtRaw := strings.TrimSpace(req.IssuedAt)
	if issuedAtRaw == "" {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "issued_at is required")
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if err != nil {
		issuedAt, err = time.Parse(time.RFC3339, issuedAtRaw)
	}
	if err != nil {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "issued_at must be an RFC3339 timestamp")
	}
	if req.ExpectedVersion == nil {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "expected_version is required")
	}
	if *req.ExpectedVersion < 0 {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "expected_version is invalid")
	}
	selfSig := strings.TrimSpace(req.SelfAttestation)
	if selfSig == "" {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "self_attestation is required")
	}
	return req, issuedAt, req.ExpectedVersion, selfSig, nil
}

func parseMintConversationFinalizeInstanceTrustRequestBody(ctx *apptheory.Context, currentVersion int) (soulMintConversationFinalizeRequest, time.Time, *int, error) {
	var req soulMintConversationFinalizeRequest
	if ctx != nil && len(bytes.TrimSpace(ctx.Request.Body)) > 0 {
		if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
			return req, time.Time{}, nil, parseErr
		}
	}
	if req.BoundarySignatures == nil {
		req.BoundarySignatures = map[string]string{}
	}
	if strings.TrimSpace(req.SelfAttestation) != "" {
		return req, time.Time{}, nil, newAppTheoryError("app.bad_request", "self_attestation must be omitted for authority_model=instance_trust")
	}

	issuedAt := time.Now().UTC()
	issuedAtRaw := strings.TrimSpace(req.IssuedAt)
	if issuedAtRaw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, issuedAtRaw)
		}
		if err != nil {
			return req, time.Time{}, nil, newAppTheoryError("app.bad_request", "issued_at must be an RFC3339 timestamp")
		}
		issuedAt = parsed.UTC()
	}

	if req.ExpectedVersion == nil {
		expected := currentVersion
		req.ExpectedVersion = &expected
	}
	if *req.ExpectedVersion < 0 {
		return req, time.Time{}, nil, newAppTheoryError("app.bad_request", "expected_version is invalid")
	}
	return req, issuedAt, req.ExpectedVersion, nil
}
