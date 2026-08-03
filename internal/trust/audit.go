package trust

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func applyAuditSourceProvenance(ctx *apptheory.Context, entry *models.AuditLogEntry) {
	if ctx == nil || entry == nil {
		return
	}
	source := httpx.TrustedSourceFromContext(ctx)
	entry.SourceIP = strings.TrimSpace(source.SourceIP)
	entry.SourceProvider = strings.TrimSpace(source.Provider)
	entry.SourceProvenance = strings.TrimSpace(source.Source)
	entry.SourceValid = source.Valid
}
