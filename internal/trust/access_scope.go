package trust

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func renderArtifactOwnedByInstance(item *models.RenderArtifact, instanceSlug string) bool {
	if item == nil {
		return false
	}
	instanceSlug = strings.TrimSpace(instanceSlug)
	if instanceSlug == "" {
		return false
	}
	requestedBy := strings.TrimSpace(item.RequestedBy)
	if requestedBy == "" {
		return false
	}
	return requestedBy == instanceSlug
}

func linkPreviewOwnedByInstance(item *models.LinkPreview, instanceSlug string) bool {
	if item == nil {
		return false
	}
	instanceSlug = strings.TrimSpace(instanceSlug)
	if instanceSlug == "" {
		return false
	}
	storedBy := strings.TrimSpace(item.StoredBy)
	if storedBy == "" {
		return false
	}
	return storedBy == instanceSlug
}
