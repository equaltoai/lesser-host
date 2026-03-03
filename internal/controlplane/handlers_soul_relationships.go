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

type soulCreateRelationshipRequest struct {
	FromAgentID string `json:"from_agent_id"`
	Type        string `json:"type"`
	Context     string `json:"context,omitempty"` // JSON object
	Message     string `json:"message,omitempty"`
	Signature   string `json:"signature"`
}

type soulCreateRelationshipResponse struct {
	Relationship models.SoulAgentRelationship `json:"relationship"`
}

type soulListRelationshipsResponse struct {
	Version       string                        `json:"version"`
	Relationships []models.SoulAgentRelationship `json:"relationships"`
	Count         int                           `json:"count"`
	HasMore       bool                          `json:"has_more"`
	NextCursor    string                        `json:"next_cursor,omitempty"`
}

// --- Handlers ---

// handleSoulCreateRelationship creates a new relationship record for an agent.
// The "to" agent is in the URL path; the "from" agent signs the relationship.
func (s *Server) handleSoulCreateRelationship(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	toAgentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	var req soulCreateRelationshipRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	fromAgentIDHex := strings.ToLower(strings.TrimSpace(req.FromAgentID))
	if fromAgentIDHex == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "from_agent_id is required"}
	}

	relType := strings.ToLower(strings.TrimSpace(req.Type))
	if !isValidRelationshipType(relType) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "type must be one of: endorsement, delegation, collaboration, trust_grant, trust_revocation"}
	}

	message := strings.TrimSpace(req.Message)
	signature := strings.TrimSpace(req.Signature)
	if signature == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}

	// Verify the "from" agent exists and the signer has domain access to it.
	fromIdentity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, fromAgentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	// Verify EIP-191 signature over keccak256(bytes(message)).
	if message == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
	}
	messageDigest := crypto.Keccak256([]byte(message))
	if err := verifyEthereumSignatureBytes(fromIdentity.Wallet, messageDigest, signature); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid relationship signature"}
	}

	now := time.Now().UTC()

	// Primary record: stored under the "to" agent's partition.
	rel := &models.SoulAgentRelationship{
		FromAgentID: fromAgentIDHex,
		ToAgentID:   toAgentIDHex,
		Type:        relType,
		Context:     strings.TrimSpace(req.Context),
		Message:     message,
		Signature:   signature,
		CreatedAt:   now,
	}
	_ = rel.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(rel).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create relationship"}
	}

	// Dual-write: reverse index under "from" agent's partition for outbound queries.
	fromIdx := &models.SoulRelationshipFromIndex{
		FromAgentID: fromAgentIDHex,
		ToAgentID:   toAgentIDHex,
		Type:        relType,
		CreatedAt:   now,
	}
	_ = fromIdx.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(fromIdx).Create()

	// Continuity entries on both agents.
	s.appendContinuityEntry(ctx, toAgentIDHex, models.SoulContinuityEntryTypeRelationshipFormed,
		fmt.Sprintf("Relationship %s from %s", relType, fromAgentIDHex))
	s.appendContinuityEntry(ctx, fromAgentIDHex, models.SoulContinuityEntryTypeRelationshipFormed,
		fmt.Sprintf("Relationship %s to %s", relType, toAgentIDHex))

	// Audit log.
	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.relationship.create",
		Target:    fmt.Sprintf("soul_agent_relationship:%s:%s", toAgentIDHex, fromAgentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusCreated, soulCreateRelationshipResponse{Relationship: *rel})
}

// handleSoulPublicGetRelationships returns paginated relationships for an agent.
func (s *Server) handleSoulPublicGetRelationships(ctx *apptheory.Context) (*apptheory.Response, error) {
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
	typeFilter := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "type")))
	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var items []*models.SoulAgentRelationship
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentRelationship{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "RELATIONSHIP#").
		OrderBy("SK", "ASC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list relationships"}
	}

	out := make([]models.SoulAgentRelationship, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		// Apply type filter client-side if specified.
		if typeFilter != "" && strings.ToLower(item.Type) != typeFilter {
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

	resp, err := apptheory.JSON(http.StatusOK, soulListRelationshipsResponse{
		Version:       "1",
		Relationships: out,
		Count:         len(out),
		HasMore:       hasMore,
		NextCursor:    nextCursor,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	setSoulPublicHeaders(resp, "public, max-age=60")
	return resp, nil
}

// --- Helpers ---

func isValidRelationshipType(relType string) bool {
	switch relType {
	case models.SoulRelationshipTypeEndorsement,
		models.SoulRelationshipTypeDelegation,
		models.SoulRelationshipTypeCollaboration,
		models.SoulRelationshipTypeTrustGrant,
		models.SoulRelationshipTypeTrustRevocation:
		return true
	}
	return false
}
