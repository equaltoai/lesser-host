package controlplane

import (
	"fmt"
	"log"
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

	// NOTE: Do not patch-and-republish registration.json in place; v2 integrity requires a new signed version record.
	// M2/M4 unify boundary mutations with the registration publishing pipeline.
	log.Printf("controlplane: soul_integrity boundary_append_no_republish agent=%s boundary=%s request_id=%s", agentIDHex, boundaryID, strings.TrimSpace(ctx.RequestID))

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
