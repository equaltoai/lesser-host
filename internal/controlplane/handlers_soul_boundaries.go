package controlplane

import (
	"encoding/json"
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

type soulAppendBoundaryRequest struct {
	BoundaryID string `json:"boundary_id"`
	Category   string `json:"category"`
	Statement  string `json:"statement"`
	Rationale  string `json:"rationale,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`
	Signature  string `json:"signature"`
}

type soulAppendBoundaryResponse struct {
	Boundary models.SoulAgentBoundary `json:"boundary"`
}

type soulListBoundariesResponse struct {
	Version    string                     `json:"version"`
	Boundaries []models.SoulAgentBoundary `json:"boundaries"`
	Count      int                        `json:"count"`
	HasMore    bool                       `json:"has_more"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

// --- Handlers ---

// handleSoulAppendBoundary appends a new boundary declaration for a soul agent.
// Boundaries are append-only: no delete or update. Supersession is via the `supersedes` field.
func (s *Server) handleSoulAppendBoundary(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var req soulAppendBoundaryRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	// Validate required fields.
	boundaryID := strings.TrimSpace(req.BoundaryID)
	if boundaryID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "boundary_id is required"}
	}
	if len(boundaryID) > 128 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "boundary_id is too long"}
	}
	category := strings.ToLower(strings.TrimSpace(req.Category))
	if !isValidBoundaryCategory(category) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "category must be one of: refusal, scope_limit, ethical_commitment, circuit_breaker"}
	}
	statement := strings.TrimSpace(req.Statement)
	if statement == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "statement is required"}
	}
	if len(statement) > 4096 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "statement is too long"}
	}
	rationale := strings.TrimSpace(req.Rationale)
	if len(rationale) > 8192 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "rationale is too long"}
	}
	signature := strings.TrimSpace(req.Signature)
	if signature == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}

	// Verify EIP-191 signature over keccak256(bytes(statement)).
	statementDigest := crypto.Keccak256([]byte(statement))
	if err := verifyEthereumSignatureBytes(identity.Wallet, statementDigest, signature); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid boundary signature"}
	}

	// If supersedes is set, verify the referenced boundary exists.
	supersedes := strings.TrimSpace(req.Supersedes)
	if supersedes != "" {
		if len(supersedes) > 128 {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "supersedes is too long"}
		}
		_, err := getSoulAgentItemBySK[models.SoulAgentBoundary](s, ctx.Context(), agentIDHex, fmt.Sprintf("BOUNDARY#%s", supersedes))
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "superseded boundary not found"}
		}
	}

	now := time.Now().UTC()
	boundary := &models.SoulAgentBoundary{
		AgentID:        agentIDHex,
		BoundaryID:     boundaryID,
		Category:       category,
		Statement:      statement,
		Rationale:      rationale,
		AddedInVersion: identity.SelfDescriptionVersion,
		Supersedes:     supersedes,
		Signature:      signature,
		AddedAt:        now,
	}
	_ = boundary.UpdateKeys()

	// Append-only: use IfNotExists to prevent overwriting an existing boundary with the same ID.
	if err := s.store.DB.WithContext(ctx.Context()).Model(boundary).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists"}
	}

	// Re-publish registration file with updated boundaries.
	_ = s.republishRegistrationOnBoundaryChange(ctx, identity)

	// Audit log.
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.boundary.append",
		Target:    fmt.Sprintf("soul_agent_boundary:%s:%s", agentIDHex, boundaryID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusCreated, soulAppendBoundaryResponse{Boundary: *boundary})
}

// handleSoulPublicGetBoundaries returns paginated boundary declarations for an agent.
func (s *Server) handleSoulPublicGetBoundaries(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var items []*models.SoulAgentBoundary
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentBoundary{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "BOUNDARY#").
		OrderBy("SK", "ASC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list boundaries"}
	}

	out := make([]models.SoulAgentBoundary, 0, len(items))
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

	resp, err := apptheory.JSON(http.StatusOK, soulListBoundariesResponse{
		Version:    "1",
		Boundaries: out,
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

// --- Helpers ---

func isValidBoundaryCategory(category string) bool {
	switch category {
	case models.SoulBoundaryCategoryRefusal,
		models.SoulBoundaryCategoryScopeLimit,
		models.SoulBoundaryCategoryEthicalCommitment,
		models.SoulBoundaryCategoryCircuitBreaker:
		return true
	}
	return false
}

// republishRegistrationOnBoundaryChange re-fetches the current registration file from S3,
// patches the boundaries array, and re-publishes. This is a best-effort operation;
// errors are logged but not returned.
func (s *Server) republishRegistrationOnBoundaryChange(ctx *apptheory.Context, identity *models.SoulAgentIdentity) *apptheory.AppError {
	if s.soulPacks == nil || identity == nil {
		return nil
	}

	agentIDHex := strings.ToLower(strings.TrimSpace(identity.AgentID))
	s3Key := soulRegistrationS3Key(agentIDHex)

	// Fetch current registration file.
	regBytes, _, _, err := s.soulPacks.GetObject(ctx.Context(), s3Key, 10*1024*1024)
	if err != nil || len(regBytes) == 0 {
		return nil // No existing file yet; nothing to patch.
	}

	var reg map[string]any
	if unmarshalErr := json.Unmarshal(regBytes, &reg); unmarshalErr != nil {
		return nil
	}
	isV2 := extractStringField(reg, "version") == "2"

	// Fetch all boundaries from DB.
	var boundaries []*models.SoulAgentBoundary
	_, queryErr := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentBoundary{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "BOUNDARY#").
		OrderBy("SK", "ASC").
		Limit(200).
		AllPaginated(&boundaries)
	if queryErr != nil {
		return nil
	}

	// Build boundaries array for registration file.
	boundariesJSON := make([]map[string]any, 0, len(boundaries))
	for _, b := range boundaries {
		if b == nil {
			continue
		}
		entry := map[string]any{
			"id":        b.BoundaryID,
			"category":  b.Category,
			"statement": b.Statement,
			"addedAt":   b.AddedAt.Format(time.RFC3339),
			"signature": b.Signature,
		}
		if b.Rationale != "" {
			entry["rationale"] = b.Rationale
		}
		if b.Supersedes != "" {
			entry["supersedes"] = b.Supersedes
		}
		if isV2 {
			addedInVersion := b.AddedInVersion
			if addedInVersion <= 0 && identity != nil && identity.SelfDescriptionVersion > 0 {
				addedInVersion = identity.SelfDescriptionVersion
			}
			if addedInVersion <= 0 {
				addedInVersion = 1
			}
			entry["addedInVersion"] = fmt.Sprintf("%d", addedInVersion)
		}
		boundariesJSON = append(boundariesJSON, entry)
	}

	// Patch the boundaries field and re-publish.
	reg["boundaries"] = boundariesJSON
	reg["updated"] = time.Now().UTC().Format(time.RFC3339)

	patchedBytes, marshalErr := json.Marshal(reg)
	if marshalErr != nil {
		return nil
	}
	_ = s.soulPacks.PutObject(ctx.Context(), s3Key, patchedBytes, "application/json", "private, max-age=0")

	return nil
}
