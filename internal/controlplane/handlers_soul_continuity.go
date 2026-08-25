package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulSignedRequestMaxFutureSkew = 2 * time.Minute
	soulSignedRequestMaxAge        = 2 * time.Minute
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
	entryData, appErr := parseSoulAppendContinuityData(req, time.Now().UTC())
	if appErr != nil {
		return nil, appErr
	}

	digest, appErr := computeSoulContinuityEntryDigest(entryData.entryType, entryData.timestampCanonical, entryData.summary, entryData.recovery, entryData.references, "")
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytes(identity.Wallet, digest, entryData.signature); err != nil {
		return nil, newAppTheoryError("app.bad_request", "invalid continuity signature")
	}
	entry := &models.SoulAgentContinuity{
		AgentID:      agentIDHex,
		Type:         entryData.entryType,
		Summary:      entryData.summary,
		Recovery:     entryData.recovery,
		ReferencesV2: entryData.references,
		Signature:    entryData.signature,
		Timestamp:    entryData.parsedTS.UTC(),
	}
	_ = entry.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(entry).Create(); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return nil, newAppTheoryError("app.internal", "failed to create continuity entry")
		}
		existing, replayErr := s.loadMatchingSoulContinuityReplay(ctx, entry, digest)
		if replayErr != nil {
			return nil, replayErr
		}
		return apptheory.JSON(http.StatusOK, soulAppendContinuityResponse{Entry: *existing})
	}

	// Audit log.
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.continuity.append",
		Target:    fmt.Sprintf("soul_agent_continuity:%s:%s", agentIDHex, entryData.entryType),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: entryData.now,
	})

	return apptheory.JSON(http.StatusCreated, soulAppendContinuityResponse{Entry: *entry})
}

func (s *Server) loadMatchingSoulContinuityReplay(ctx *apptheory.Context, entry *models.SoulAgentContinuity, requestDigest []byte) (*models.SoulAgentContinuity, *apptheory.AppTheoryError) {
	var existing models.SoulAgentContinuity
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentContinuity{}).
		Where("PK", "=", entry.PK).
		Where("SK", "=", entry.SK).
		First(&existing); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to load continuity entry")
	}

	references := existing.ReferencesV2
	if len(references) == 0 {
		references = parseLegacyContinuityReferences(existing.ReferencesJSON)
	}
	existingDigest, appErr := computeSoulContinuityEntryDigest(
		existing.Type,
		canonicalSoulSignedTimestamp(existing.Timestamp),
		existing.Summary,
		existing.Recovery,
		references,
		"",
	)
	if appErr != nil {
		return nil, newAppTheoryError("app.internal", "failed to validate continuity entry")
	}
	if !bytes.Equal(existingDigest, requestDigest) {
		return nil, newAppTheoryError("app.conflict", "continuity entry already exists")
	}
	return &existing, nil
}

type soulAppendContinuityData struct {
	entryType          string
	parsedTS           time.Time
	timestampCanonical string
	summary            string
	recovery           string
	references         []string
	signature          string
	now                time.Time
}

func parseSoulAppendContinuityData(req soulAppendContinuityRequest, now time.Time) (soulAppendContinuityData, *apptheory.AppTheoryError) {
	entryType := strings.ToLower(strings.TrimSpace(req.Type))
	if !isValidContinuityEntryType(entryType) {
		return soulAppendContinuityData{}, newAppTheoryError("app.bad_request", "invalid continuity entry type")
	}

	parsedTS, timestampCanonical, appErr := parseSoulContinuityTimestamp(strings.TrimSpace(req.Timestamp), now)
	if appErr != nil {
		return soulAppendContinuityData{}, appErr
	}

	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		return soulAppendContinuityData{}, newAppTheoryError("app.bad_request", "summary is required")
	}
	if len(summary) > 4096 {
		return soulAppendContinuityData{}, newAppTheoryError("app.bad_request", "summary is too long")
	}

	recovery := strings.TrimSpace(req.Recovery)
	if len(recovery) > 8192 {
		return soulAppendContinuityData{}, newAppTheoryError("app.bad_request", "recovery is too long")
	}

	signature := strings.TrimSpace(req.Signature)
	if signature == "" {
		return soulAppendContinuityData{}, newAppTheoryError("app.bad_request", "signature is required")
	}

	return soulAppendContinuityData{
		entryType:          entryType,
		parsedTS:           parsedTS,
		timestampCanonical: timestampCanonical,
		summary:            summary,
		recovery:           recovery,
		references:         normalizeContinuityReferences(req.References),
		signature:          signature,
		now:                now,
	}, nil
}

func parseSoulContinuityTimestamp(raw string, now time.Time) (time.Time, string, *apptheory.AppTheoryError) {
	return parseSoulSignedTimestamp(raw, now, "timestamp")
}

func parseSoulSignedTimestamp(raw string, now time.Time, field string) (time.Time, string, *apptheory.AppTheoryError) {
	raw = strings.TrimSpace(raw)
	field = strings.TrimSpace(field)
	if field == "" {
		field = "timestamp"
	}
	if raw == "" {
		return time.Time{}, "", newAppTheoryError("app.bad_request", field+" is required")
	}
	parsedTS, parseErr := time.Parse(time.RFC3339, raw)
	if parseErr != nil {
		var parsedNano time.Time
		parsedNano, parseErr = time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			return time.Time{}, "", newAppTheoryError("app.bad_request", field+" must be RFC3339")
		}
		parsedTS = parsedNano
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	parsedTS = parsedTS.UTC()
	if parsedTS.After(now.Add(soulSignedRequestMaxFutureSkew)) {
		return time.Time{}, "", newAppTheoryError("app.bad_request", field+" cannot be in the future")
	}
	if parsedTS.Before(now.Add(-soulSignedRequestMaxAge)) {
		return time.Time{}, "", newAppTheoryError("app.bad_request", field+" is too far in the past")
	}
	canonicalTS := parsedTS.Truncate(time.Millisecond)
	return canonicalTS, canonicalSoulSignedTimestamp(canonicalTS), nil
}

func canonicalSoulSignedTimestamp(ts time.Time) string {
	return ts.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func computeSoulContinuityEntryDigest(entryType string, timestamp string, summary string, recovery string, references []string, nonce string) ([]byte, *apptheory.AppTheoryError) {
	entryType = strings.ToLower(strings.TrimSpace(entryType))
	timestampStr := strings.TrimSpace(timestamp)
	summary = strings.TrimSpace(summary)
	recovery = strings.TrimSpace(recovery)
	references = normalizeContinuityReferences(references)
	nonce = strings.TrimSpace(nonce)

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
	if nonce != "" {
		unsigned["nonce"] = nonce
	}

	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return nil, newAppTheoryError("app.bad_request", "invalid continuity JSON")
	}
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		return nil, newAppTheoryError("app.bad_request", "invalid continuity JSON")
	}
	return crypto.Keccak256(jcsBytes), nil
}

func normalizeContinuityReferences(references []string) []string {
	if len(references) == 0 {
		return nil
	}
	out := make([]string, 0, len(references))
	for _, ref := range references {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// handleSoulPublicGetContinuity returns paginated continuity journal entries for an agent.
func (s *Server) handleSoulPublicGetContinuity(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, newAppTheoryError("app.not_found", "not found")
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	limit := envIntPositiveClampedFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50, 200)

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
		return nil, newAppTheoryError("app.internal", "failed to list continuity entries")
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
		return nil, newAppTheoryError("app.internal", "internal error")
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
