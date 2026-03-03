package controlplane

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttquery "github.com/theory-cloud/tabletheory/pkg/query"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// --- Request / Response types ---

type soulCreateRelationshipRequest struct {
	FromAgentID string          `json:"from_agent_id"`
	Type        string          `json:"type"`
	Context     json.RawMessage `json:"context,omitempty"` // JSON object (or legacy JSON string)
	Message     string          `json:"message,omitempty"`
	CreatedAt   string          `json:"created_at,omitempty"`
	Signature   string          `json:"signature"`
}

type soulCreateRelationshipResponse struct {
	Relationship models.SoulAgentRelationship `json:"relationship"`
}

type soulListRelationshipsResponse struct {
	Version       string                         `json:"version"`
	Relationships []models.SoulAgentRelationship `json:"relationships"`
	Count         int                            `json:"count"`
	HasMore       bool                           `json:"has_more"`
	NextCursor    string                         `json:"next_cursor,omitempty"`
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
	if fromAgentIDHex == toAgentIDHex {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "cannot create self-relationship"}
	}

	relType := strings.ToLower(strings.TrimSpace(req.Type))
	if !isValidRelationshipType(relType) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "type must be one of: endorsement, delegation, collaboration, trust_grant, trust_revocation"}
	}

	contextMap, contextJSON, taskType, appErr := parseRelationshipContext(req.Context)
	if appErr != nil {
		return nil, appErr
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

	// Verify the "to" agent exists (no domain access required).
	_, err := s.getSoulAgentIdentity(ctx.Context(), toAgentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	// Verify EIP-191 signature over keccak256(bytes(message)).
	if message == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
	}
	now := time.Now().UTC()
	createdAtRaw := strings.TrimSpace(req.CreatedAt)

	// Legacy scheme: signature over keccak256(bytes(message)).
	// Strict v2 integrity: signature over keccak256(JCS(record_without_signature)) to prevent replay/under-scoping.
	useScopedSig := createdAtRaw != "" || s.cfg.SoulV2StrictIntegrity
	var createdAt time.Time
	if useScopedSig {
		if createdAtRaw == "" {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "created_at is required"}
		}
		parsedTS, parseErr := time.Parse(time.RFC3339, createdAtRaw)
		if parseErr != nil {
			if parsedTS, parseErr = time.Parse(time.RFC3339Nano, createdAtRaw); parseErr != nil {
				return nil, &apptheory.AppError{Code: "app.bad_request", Message: "created_at must be RFC3339"}
			}
		}
		if parsedTS.After(now.Add(5 * time.Minute)) {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "created_at cannot be in the future"}
		}
		if parsedTS.Before(now.Add(-10 * 365 * 24 * time.Hour)) {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "created_at is too far in the past"}
		}
		createdAt = parsedTS.UTC()

		digest, appErr := computeSoulRelationshipDigest(fromAgentIDHex, toAgentIDHex, relType, contextMap, message, createdAtRaw)
		if appErr != nil {
			return nil, appErr
		}
		if err := verifyEthereumSignatureBytes(fromIdentity.Wallet, digest, signature); err != nil {
			log.Printf(
				"controlplane: soul_integrity invalid_relationship_signature scoped=1 from=%s to=%s type=%s request_id=%s",
				fromAgentIDHex,
				toAgentIDHex,
				relType,
				strings.TrimSpace(ctx.RequestID),
			)
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid relationship signature"}
		}
	} else {
		messageDigest := crypto.Keccak256([]byte(message))
		if err := verifyEthereumSignatureBytes(fromIdentity.Wallet, messageDigest, signature); err != nil {
			log.Printf(
				"controlplane: soul_integrity invalid_relationship_signature scoped=0 from=%s to=%s type=%s request_id=%s",
				fromAgentIDHex,
				toAgentIDHex,
				relType,
				strings.TrimSpace(ctx.RequestID),
			)
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid relationship signature"}
		}
		createdAt = now
		log.Printf(
			"controlplane: soul_integrity legacy_relationship_signature scoped=0 from=%s to=%s type=%s request_id=%s",
			fromAgentIDHex,
			toAgentIDHex,
			relType,
			strings.TrimSpace(ctx.RequestID),
		)
	}

	// Primary record: stored under the "to" agent's partition.
	if len(contextMap) == 0 {
		contextMap = nil
		contextJSON = ""
		taskType = ""
	}
	rel := &models.SoulAgentRelationship{
		FromAgentID: fromAgentIDHex,
		ToAgentID:   toAgentIDHex,
		Type:        relType,
		ContextJSON: strings.TrimSpace(contextJSON),
		ContextV2:   contextMap,
		TaskType:    taskType,
		Message:     message,
		Signature:   signature,
		CreatedAt:   createdAt,
	}
	_ = rel.UpdateKeys()

	// Dual-write: reverse index under "from" agent's partition for outbound queries.
	fromIdx := &models.SoulRelationshipFromIndex{
		FromAgentID: fromAgentIDHex,
		ToAgentID:   toAgentIDHex,
		Type:        relType,
		CreatedAt:   createdAt,
	}
	_ = fromIdx.UpdateKeys()

	// Write both records in a transaction to ensure dual-write consistency.
	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(rel)
		tx.Create(fromIdx)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "relationship already exists"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create relationship"}
	}

	// Audit log.
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.relationship.create",
		Target:    fmt.Sprintf("soul_agent_relationship:%s:%s", toAgentIDHex, fromAgentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusCreated, soulCreateRelationshipResponse{Relationship: *rel})
}

func encodeSoulRelationshipCursor(rel *models.SoulAgentRelationship) string {
	if rel == nil || strings.TrimSpace(rel.PK) == "" || strings.TrimSpace(rel.SK) == "" {
		return ""
	}
	cursor, err := ttquery.EncodeCursor(map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: rel.PK},
		"SK": &types.AttributeValueMemberS{Value: rel.SK},
	}, "", "ASC")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cursor)
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
	taskTypeFilter := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "taskType")))
	if taskTypeFilter == "" {
		taskTypeFilter = strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "task_type")))
	}
	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	out := make([]models.SoulAgentRelationship, 0, limit)
	nextCursor := ""
	hasMore := false

	pageCursor := cursor
	pageLimit := limit
	if pageLimit < 25 {
		pageLimit = 25
	}
	if pageLimit > 200 {
		pageLimit = 200
	}

	for len(out) < limit {
		var items []*models.SoulAgentRelationship
		qb := s.store.DB.WithContext(ctx.Context()).
			Model(&models.SoulAgentRelationship{}).
			Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
			Where("SK", "BEGINS_WITH", "RELATIONSHIP#").
			OrderBy("SK", "ASC").
			Limit(pageLimit)
		if pageCursor != "" {
			qb = qb.Cursor(pageCursor)
		}

		paged, err := qb.AllPaginated(&items)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list relationships"}
		}

		for idx, item := range items {
			if item == nil {
				continue
			}
			normalizeRelationshipForPublicRead(item)
			// Apply type filter client-side if specified.
			if typeFilter != "" && strings.ToLower(item.Type) != typeFilter {
				continue
			}
			if taskTypeFilter != "" {
				if tt := strings.ToLower(strings.TrimSpace(item.TaskType)); tt != taskTypeFilter {
					continue
				}
			}
			out = append(out, *item)
			if len(out) >= limit {
				if idx < len(items)-1 {
					nextCursor = encodeSoulRelationshipCursor(item)
					if nextCursor == "" {
						return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode cursor"}
					}
					hasMore = true
				} else if paged != nil && strings.TrimSpace(paged.NextCursor) != "" {
					nextCursor = strings.TrimSpace(paged.NextCursor)
					hasMore = true
				} else {
					nextCursor = ""
					hasMore = false
				}
				break
			}
		}

		if len(out) >= limit {
			break
		}

		if paged == nil || strings.TrimSpace(paged.NextCursor) == "" {
			nextCursor = ""
			hasMore = false
			break
		}

		pageCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = true
	}

	// V1 backward compat: merge peer endorsements into relationship reads (first page only).
	shouldMergeV1Endorsements := cursor == "" &&
		taskTypeFilter == "" &&
		(typeFilter == "" || typeFilter == models.SoulRelationshipTypeEndorsement)
	if shouldMergeV1Endorsements {
		var endorsements []*models.SoulAgentPeerEndorsement
		if err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.SoulAgentPeerEndorsement{}).
			Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
			Where("SK", "BEGINS_WITH", "ENDORSEMENT#").
			All(&endorsements); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list endorsements"}
		}

		for _, e := range endorsements {
			if e == nil {
				continue
			}
			out = append(out, models.SoulAgentRelationship{
				FromAgentID: e.EndorserAgentID,
				ToAgentID:   agentIDHex,
				Type:        models.SoulRelationshipTypeEndorsement,
				Message:     e.Message,
				Signature:   e.Signature,
				CreatedAt:   e.CreatedAt,
			})
		}
	}

	resp, err := apptheory.JSON(http.StatusOK, soulListRelationshipsResponse{
		Version:       "2",
		Relationships: out,
		Count:         len(out),
		HasMore:       hasMore,
		NextCursor:    nextCursor,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
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

func parseRelationshipContext(raw json.RawMessage) (contextMap map[string]any, contextJSON string, taskType string, appErr *apptheory.AppError) {
	rawStr := strings.TrimSpace(string(raw))
	if rawStr == "" || rawStr == "null" {
		return map[string]any{}, "", "", nil
	}

	// Preferred: context is a JSON object.
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil && obj != nil {
		return obj, rawStr, extractRelationshipTaskTypeFromMap(obj), nil
	}

	// Legacy: context is a JSON string containing an object.
	var legacyStr string
	if err := json.Unmarshal(raw, &legacyStr); err != nil {
		return nil, "", "", &apptheory.AppError{Code: "app.bad_request", Message: "context must be a JSON object"}
	}
	legacyStr = strings.TrimSpace(legacyStr)
	if legacyStr == "" {
		return map[string]any{}, "", "", nil
	}
	if err := json.Unmarshal([]byte(legacyStr), &obj); err != nil {
		return nil, "", "", &apptheory.AppError{Code: "app.bad_request", Message: "context must be a JSON object"}
	}
	if obj == nil {
		obj = map[string]any{}
	}
	return obj, legacyStr, extractRelationshipTaskTypeFromMap(obj), nil
}

func normalizeRelationshipForPublicRead(item *models.SoulAgentRelationship) {
	if item == nil {
		return
	}

	// Prefer the typed map; fallback to legacy string.
	if item.ContextV2 == nil && strings.TrimSpace(item.ContextJSON) != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(item.ContextJSON), &obj); err == nil && obj != nil {
			item.ContextV2 = obj
		}
	}

	if strings.TrimSpace(item.TaskType) == "" {
		item.TaskType = extractRelationshipTaskTypeFromMap(item.ContextV2)
	}
}

func extractRelationshipTaskTypeFromMap(m map[string]any) string {
	if m == nil {
		return ""
	}
	raw, _ := m["taskType"].(string)
	if raw == "" {
		raw, _ = m["task_type"].(string)
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func computeSoulRelationshipDigest(fromAgentIDHex string, toAgentIDHex string, relType string, context map[string]any, message string, createdAt string) ([]byte, *apptheory.AppError) {
	fromAgentIDHex = strings.ToLower(strings.TrimSpace(fromAgentIDHex))
	toAgentIDHex = strings.ToLower(strings.TrimSpace(toAgentIDHex))
	relType = strings.ToLower(strings.TrimSpace(relType))
	message = strings.TrimSpace(message)
	createdAt = strings.TrimSpace(createdAt)
	if fromAgentIDHex == "" || toAgentIDHex == "" || relType == "" || message == "" || createdAt == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid relationship payload"}
	}
	if context == nil {
		context = map[string]any{}
	}

	unsigned := map[string]any{
		"kind":        "soul_relationship",
		"version":     "1",
		"fromAgentId": fromAgentIDHex,
		"toAgentId":   toAgentIDHex,
		"type":        relType,
		"context":     context,
		"message":     message,
		"createdAt":   createdAt,
	}

	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid relationship JSON"}
	}
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid relationship JSON"}
	}
	return crypto.Keccak256(jcsBytes), nil
}
