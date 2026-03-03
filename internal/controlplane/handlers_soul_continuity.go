package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// --- Request / Response types ---

type soulAppendContinuityRequest struct {
	Type       string   `json:"type"`
	Timestamp  string   `json:"timestamp"`
	Summary    string   `json:"summary"`
	Recovery   string   `json:"recovery,omitempty"`
	References []string `json:"references,omitempty"`
	Signature  string   `json:"signature"`
}

type soulAppendContinuityResponse struct {
	Entry models.SoulAgentContinuity `json:"entry"`
}

type soulListContinuityResponse struct {
	Version    string                       `json:"version"`
	Entries    []models.SoulAgentContinuity `json:"entries"`
	Count      int                          `json:"count"`
	HasMore    bool                         `json:"has_more"`
	NextCursor string                       `json:"next_cursor,omitempty"`
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
	tsRaw := strings.TrimSpace(req.Timestamp)
	if tsRaw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "timestamp is required"}
	}
	parsedTS, parseErr := time.Parse(time.RFC3339, tsRaw)
	if parseErr != nil {
		if parsedTS, parseErr = time.Parse(time.RFC3339Nano, tsRaw); parseErr != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "timestamp must be RFC3339"}
		}
	}
	now := time.Now().UTC()
	if parsedTS.After(now.Add(5 * time.Minute)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "timestamp cannot be in the future"}
	}
	if parsedTS.Before(now.Add(-10 * 365 * 24 * time.Hour)) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "timestamp is too far in the past"}
	}
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "summary is required"}
	}
	if len(summary) > 4096 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "summary is too long"}
	}
	recovery := strings.TrimSpace(req.Recovery)
	if len(recovery) > 8192 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "recovery is too long"}
	}
	signature := strings.TrimSpace(req.Signature)
	if signature == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}

	digest, appErr := computeSoulContinuityEntryDigest(entryType, tsRaw, summary, recovery, req.References)
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytes(identity.Wallet, digest, signature); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid continuity signature"}
	}
	refsJSON := ""
	if len(req.References) > 0 {
		b, _ := json.Marshal(req.References)
		refsJSON = strings.TrimSpace(string(b))
	}
	entry := &models.SoulAgentContinuity{
		AgentID:        agentIDHex,
		Type:           entryType,
		Summary:        summary,
		Recovery:       recovery,
		ReferencesJSON: refsJSON,
		ReferencesV2:   req.References,
		Signature:      signature,
		Timestamp:      parsedTS.UTC(),
	}
	_ = entry.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(entry).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create continuity entry"}
	}

	// Audit log.
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.continuity.append",
		Target:    fmt.Sprintf("soul_agent_continuity:%s:%s", agentIDHex, entryType),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusCreated, soulAppendContinuityResponse{Entry: *entry})
}

func computeSoulContinuityEntryDigest(entryType string, timestamp string, summary string, recovery string, references []string) ([]byte, *apptheory.AppError) {
	entryType = strings.ToLower(strings.TrimSpace(entryType))
	timestampStr := strings.TrimSpace(timestamp)
	summary = strings.TrimSpace(summary)
	recovery = strings.TrimSpace(recovery)

	unsigned := map[string]any{
		"type":      entryType,
		"timestamp": timestampStr,
		"summary":   summary,
	}
	if recovery != "" {
		unsigned["recovery"] = recovery
	}
	if len(references) > 0 {
		unsigned["references"] = references
	}

	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid continuity JSON"}
	}
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid continuity JSON"}
	}
	return crypto.Keccak256(jcsBytes), nil
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
		if len(item.ReferencesV2) == 0 {
			refs := parseLegacyContinuityReferences(item.ReferencesJSON)
			if len(refs) > 0 {
				item.ReferencesV2 = refs
			}
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
		Version:    "2",
		Entries:    out,
		Count:      len(out),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

func parseLegacyContinuityReferences(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var refs []string
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
