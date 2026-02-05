package attestations

import (
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// PayloadTypeV1 is the canonical "type" value for v1 attestation payloads.
const (
	PayloadTypeV1 = "lesser.host/attestation/v1"
)

// PayloadV1 is the signed payload schema for v1 attestations.
type PayloadV1 struct {
	Type string `json:"type"`

	ActorURI    string `json:"actor_uri,omitempty"`
	ObjectURI   string `json:"object_uri,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`

	Module        string `json:"module"`
	PolicyVersion string `json:"policy_version"`
	ModelSet      string `json:"model_set,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`

	Evidence any `json:"evidence,omitempty"`
	Result   any `json:"result,omitempty"`
}

// LinkSafetyBasicResultV1 is the result schema for link safety basic attestations.
type LinkSafetyBasicResultV1 struct {
	PolicyVersion string `json:"policy_version"`
	LinksHash     string `json:"links_hash"`

	Links   []models.LinkSafetyBasicLinkResult `json:"links"`
	Summary models.LinkSafetyBasicSummary      `json:"summary"`
}
