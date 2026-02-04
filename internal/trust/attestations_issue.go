package trust

import (
	"context"
	"encoding/json"
	"strings"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/attestations"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) ensureLinkSafetyBasicAttestation(ctx context.Context, result *models.LinkSafetyBasicResult) (string, error) {
	if s == nil || s.store == nil || s.store.DB == nil || s.attest == nil || !s.attest.Enabled() || result == nil {
		return "", nil
	}

	actorURI := strings.TrimSpace(result.ActorURI)
	objectURI := strings.TrimSpace(result.ObjectURI)
	contentHash := strings.TrimSpace(result.ContentHash)
	if actorURI == "" || objectURI == "" || contentHash == "" {
		return "", nil
	}

	id := attestations.AttestationID(actorURI, objectURI, contentHash, "link_safety_basic", linkSafetyBasicPolicyVersion)

	if existing, err := s.store.GetAttestation(ctx, id); err == nil && existing != nil {
		return id, nil
	} else if err != nil && !theoryErrors.IsNotFound(err) {
		return "", err
	}

	payload := attestations.PayloadV1{
		Type: attestations.PayloadTypeV1,

		ActorURI:    actorURI,
		ObjectURI:   objectURI,
		ContentHash: contentHash,

		Module:        "link_safety_basic",
		PolicyVersion: linkSafetyBasicPolicyVersion,
		ModelSet:      "deterministic",

		CreatedAt: result.CreatedAt,
		ExpiresAt: result.ExpiresAt,

		Result: attestations.LinkSafetyBasicResultV1{
			PolicyVersion: linkSafetyBasicPolicyVersion,
			LinksHash:     strings.TrimSpace(result.LinksHash),
			Links:         append([]models.LinkSafetyBasicLinkResult(nil), result.Links...),
			Summary:       result.Summary,
		},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	jws, _, err := s.attest.SignPayloadJWS(ctx, payloadBytes)
	if err != nil {
		return "", err
	}

	item := &models.Attestation{
		ID:          id,
		ActorURI:    actorURI,
		ObjectURI:   objectURI,
		ContentHash: contentHash,

		Module:        "link_safety_basic",
		PolicyVersion: linkSafetyBasicPolicyVersion,
		ModelSet:      "deterministic",
		JWS:           jws,

		CreatedAt: payload.CreatedAt,
		ExpiresAt: payload.ExpiresAt,
	}
	_ = item.UpdateKeys()

	err = s.store.DB.WithContext(ctx).Model(item).Create()
	if theoryErrors.IsConditionFailed(err) {
		return id, nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}
