package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// --- Request / Response types ---

type soulAppendContinuityRequest struct {
	Type       string `json:"type"`
	Summary    string `json:"summary"`
	Recovery   string `json:"recovery,omitempty"`
	References string `json:"references,omitempty"` // JSON array of reference IDs
	Signature  string `json:"signature"`
}

type soulAppendContinuityResponse struct {
	Entry models.SoulAgentContinuity `json:"entry"`
}

type soulListContinuityResponse struct {
	Version    string                      `json:"version"`
	Entries    []models.SoulAgentContinuity `json:"entries"`
	Count      int                         `json:"count"`
	HasMore    bool                        `json:"has_more"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

// --- Handlers ---

// handleSoulAppendContinuity appends a new continuity journal entry for a soul agent.
func (s *Server) handleSoulAppendContinuity(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	var req soulAppendContinuityRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	entryType := strings.ToLower(strings.TrimSpace(req.Type))
	if !isValidContinuityEntryType(entryType) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid continuity entry type"}
	}
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "summary is required"}
	}
	signature := strings.TrimSpace(req.Signature)
	if signature == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}

	// Verify EIP-191 signature over keccak256(bytes(summary)).
	summaryDigest := crypto.Keccak256([]byte(summary))
	if err := verifyEthereumSignatureBytes(identity.Wallet, summaryDigest, signature); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid continuity signature"}
	}

	now := time.Now().UTC()
	entry := &models.SoulAgentContinuity{
		AgentID:    agentIDHex,
		Type:       entryType,
		Summary:    summary,
		Recovery:   strings.TrimSpace(req.Recovery),
		References: strings.TrimSpace(req.References),
		Signature:  signature,
		Timestamp:  now,
	}
	_ = entry.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(entry).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create continuity entry"}
	}

	// Audit log.
	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.continuity.append",
		Target:    fmt.Sprintf("soul_agent_continuity:%s:%s", agentIDHex, entryType),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusCreated, soulAppendContinuityResponse{Entry: *entry})
}

// handleSoulPublicGetContinuity returns paginated continuity journal entries for an agent.
func (s *Server) handleSoulPublicGetContinuity(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var items []*models.SoulAgentContinuity
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentContinuity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "CONTINUITY#").
		OrderBy("SK", "DESC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list continuity entries"}
	}

	out := make([]models.SoulAgentContinuity, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}

	nextCursor := ""
	hasMore := false
	if paged != nil {
		nextCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = paged.HasMore
	}

	resp, err := apptheory.JSON(http.StatusOK, soulListContinuityResponse{
		Version:    "1",
		Entries:    out,
		Count:      len(out),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	setSoulPublicHeaders(resp, "public, max-age=60")
	return resp, nil
}

// --- Helpers ---

func isValidContinuityEntryType(entryType string) bool {
	switch entryType {
	case models.SoulContinuityEntryTypeCapabilityAcquired,
		models.SoulContinuityEntryTypeCapabilityDeprecated,
		models.SoulContinuityEntryTypeSignificantFailure,
		models.SoulContinuityEntryTypeRecovery,
		models.SoulContinuityEntryTypeBoundaryAdded,
		models.SoulContinuityEntryTypeMigration,
		models.SoulContinuityEntryTypeModelChange,
		models.SoulContinuityEntryTypeRelationshipFormed,
		models.SoulContinuityEntryTypeRelationshipEnded,
		models.SoulContinuityEntryTypeSelfSuspension,
		models.SoulContinuityEntryTypeArchived,
		models.SoulContinuityEntryTypeSuccessionDeclared,
		models.SoulContinuityEntryTypeSuccessionReceived:
		return true
	}
	return false
}

// appendContinuityEntry is a shared helper for appending continuity entries from other milestones.
// It does not require signature verification — the caller is responsible for authorization.
func (s *Server) appendContinuityEntry(ctx *apptheory.Context, agentIDHex string, entryType string, summary string) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return
	}
	now := time.Now().UTC()
	entry := &models.SoulAgentContinuity{
		AgentID:   agentIDHex,
		Type:      entryType,
		Summary:   summary,
		Timestamp: now,
	}
	_ = entry.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(entry).Create()
}
