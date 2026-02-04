package rendering

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	RenderPolicyVersion = "v1"
)

type RenderJobMessage struct {
	Kind          string `json:"kind"` // "render"
	RenderID      string `json:"render_id"`
	NormalizedURL string `json:"normalized_url"`

	RetentionClass string `json:"retention_class,omitempty"`
	RetentionDays  int    `json:"retention_days,omitempty"`

	RequestedBy string `json:"requested_by,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}

func RenderArtifactID(policyVersion, normalizedURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(policyVersion) + ":" + strings.TrimSpace(normalizedURL)))
	return hex.EncodeToString(sum[:])
}

func RetentionForClass(class string) (days int, classOut string) {
	class = strings.ToLower(strings.TrimSpace(class))
	switch class {
	case models.RenderRetentionClassEvidence:
		return 180, models.RenderRetentionClassEvidence
	default:
		return 30, models.RenderRetentionClassBenign
	}
}

func ExpiresAtForRetention(now time.Time, retentionDays int) time.Time {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if retentionDays <= 0 {
		retentionDays = 30
	}
	return now.UTC().Add(time.Duration(retentionDays) * 24 * time.Hour)
}

func ThumbnailObjectKey(renderID string) string {
	renderID = strings.TrimSpace(renderID)
	if renderID == "" {
		return ""
	}
	return "renders/" + renderID + "/thumbnail.jpg"
}

func SnapshotObjectKey(renderID string) string {
	renderID = strings.TrimSpace(renderID)
	if renderID == "" {
		return ""
	}
	return "renders/" + renderID + "/snapshot.txt"
}
