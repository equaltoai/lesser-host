package rendering

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// RenderPolicyVersion is the current version string used when generating render artifact IDs.
const (
	RenderPolicyVersion = "v1"
)

// RenderJobMessage is the payload sent to the render worker queue.
type RenderJobMessage struct {
	Kind          string `json:"kind"` // "render"
	RenderID      string `json:"render_id"`
	NormalizedURL string `json:"normalized_url"`

	RetentionClass string `json:"retention_class,omitempty"`
	RetentionDays  int    `json:"retention_days,omitempty"`

	RequestedBy string `json:"requested_by,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}

// RenderArtifactID deterministically derives a render artifact ID from policy version and normalized URL.
func RenderArtifactID(policyVersion, normalizedURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(policyVersion) + ":" + strings.TrimSpace(normalizedURL)))
	return hex.EncodeToString(sum[:])
}

// RetentionForClass returns retention days and normalized class for a retention class input.
func RetentionForClass(class string) (days int, classOut string) {
	class = strings.ToLower(strings.TrimSpace(class))
	switch class {
	case models.RenderRetentionClassEvidence:
		return 180, models.RenderRetentionClassEvidence
	default:
		return 30, models.RenderRetentionClassBenign
	}
}

// ExpiresAtForRetention returns an expiry time given a start time and retention in days.
func ExpiresAtForRetention(now time.Time, retentionDays int) time.Time {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if retentionDays <= 0 {
		retentionDays = 30
	}
	return now.UTC().Add(time.Duration(retentionDays) * 24 * time.Hour)
}

// ThumbnailObjectKey returns the S3 object key for a render thumbnail.
func ThumbnailObjectKey(renderID string) string {
	renderID = strings.TrimSpace(renderID)
	if renderID == "" {
		return ""
	}
	return "renders/" + renderID + "/thumbnail.jpg"
}

// SnapshotObjectKey returns the S3 object key for a render snapshot.
func SnapshotObjectKey(renderID string) string {
	renderID = strings.TrimSpace(renderID)
	if renderID == "" {
		return ""
	}
	return "renders/" + renderID + "/snapshot.txt"
}
