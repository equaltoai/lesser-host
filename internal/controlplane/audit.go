package controlplane

import (
	"context"
	"log"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) tryWriteAuditLog(ctx *apptheory.Context, entry *models.AuditLogEntry) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || entry == nil {
		return
	}

	if strings.TrimSpace(entry.Actor) == "" {
		entry.Actor = strings.TrimSpace(ctx.AuthIdentity)
	}
	if strings.TrimSpace(entry.RequestID) == "" {
		entry.RequestID = strings.TrimSpace(ctx.RequestID)
	}
	applyAuditSourceProvenance(ctx, entry)

	_ = s.tryWriteAuditLogWithContext(ctx.Context(), entry)
}

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

func (s *Server) tryWriteAuditLogWithContext(ctx context.Context, entry *models.AuditLogEntry) bool {
	if s == nil || s.store == nil || s.store.DB == nil || entry == nil {
		return true
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if err := entry.UpdateKeys(); err != nil {
		log.Printf(
			"controlplane: audit log write failed audit_persisted=false action=%q actor=%q target=%q request_id=%q: %v",
			strings.TrimSpace(entry.Action),
			strings.TrimSpace(entry.Actor),
			strings.TrimSpace(entry.Target),
			strings.TrimSpace(entry.RequestID),
			err,
		)
		return false
	}
	if err := s.store.DB.WithContext(ctx).Model(entry).Create(); err != nil {
		log.Printf(
			"controlplane: audit log write failed audit_persisted=false action=%q actor=%q target=%q request_id=%q: %v",
			strings.TrimSpace(entry.Action),
			strings.TrimSpace(entry.Actor),
			strings.TrimSpace(entry.Target),
			strings.TrimSpace(entry.RequestID),
			err,
		)
		return false
	}
	return true
}
