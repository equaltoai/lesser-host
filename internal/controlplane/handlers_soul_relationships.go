package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
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

type soulCreateRelationshipInput struct {
	ToAgentIDHex       string
	FromAgentIDHex     string
	RelationshipType   string
	ContextMap         map[string]any
	TaskType           string
	Message            string
	Signature          string
	CreatedAt          time.Time
	CreatedAtCanonical string
	Now                time.Time
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

	input, err := parseSoulCreateRelationshipInput(ctx)
	if err != nil {
		return nil, err
	}

	fromIdentity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, input.FromAgentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulRelationshipTarget(ctx.Context(), input.ToAgentIDHex); appErr != nil {
		return nil, appErr
	}
	if appErr := verifySoulRelationshipCreateSignature(fromIdentity.Wallet, input); appErr != nil {
		return nil, appErr
	}

	rel, fromIdx := buildSoulRelationshipModels(input)
	if appErr := s.writeSoulRelationshipRecords(ctx.Context(), rel, fromIdx); appErr != nil {
		return nil, appErr
	}

	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.relationship.create",
		Target:    fmt.Sprintf("soul_agent_relationship:%s:%s", input.ToAgentIDHex, input.FromAgentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: input.Now,
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

	params := parseSoulRelationshipListParams(ctx)
	out, hasMore, nextCursor, appErr := s.listSoulPublicRelationships(ctx.Context(), agentIDHex, params)
	if appErr != nil {
		return nil, appErr
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

func parseSoulCreateRelationshipInput(ctx *apptheory.Context) (soulCreateRelationshipInput, error) {
	var req soulCreateRelationshipRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return soulCreateRelationshipInput{}, err
	}

	toAgentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return soulCreateRelationshipInput{}, appErr
	}
	fromAgentIDHex := strings.ToLower(strings.TrimSpace(req.FromAgentID))
	if fromAgentIDHex == "" {
		return soulCreateRelationshipInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "from_agent_id is required"}
	}
	if fromAgentIDHex == toAgentIDHex {
		return soulCreateRelationshipInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "cannot create self-relationship"}
	}

	relType := strings.ToLower(strings.TrimSpace(req.Type))
	if !isValidRelationshipType(relType) {
		return soulCreateRelationshipInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "type must be one of: endorsement, delegation, collaboration, trust_grant, trust_revocation"}
	}

	contextMap, _, taskType, appErr := parseRelationshipContext(req.Context)
	if appErr != nil {
		return soulCreateRelationshipInput{}, appErr
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return soulCreateRelationshipInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
	}
	signature := strings.TrimSpace(req.Signature)
	if signature == "" {
		return soulCreateRelationshipInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}
	now := time.Now().UTC()
	createdAt, createdAtCanonical, appErr := parseSoulRelationshipCreatedAt(strings.TrimSpace(req.CreatedAt), now)
	if appErr != nil {
		return soulCreateRelationshipInput{}, appErr
	}
	return soulCreateRelationshipInput{
		ToAgentIDHex:       toAgentIDHex,
		FromAgentIDHex:     fromAgentIDHex,
		RelationshipType:   relType,
		ContextMap:         contextMap,
		TaskType:           taskType,
		Message:            message,
		Signature:          signature,
		CreatedAt:          createdAt,
		CreatedAtCanonical: createdAtCanonical,
		Now:                now,
	}, nil
}

func parseSoulRelationshipCreatedAt(raw string, now time.Time) (time.Time, string, *apptheory.AppError) {
	if raw == "" {
		return time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "created_at is required"}
	}
	parsedTS, parseErr := time.Parse(time.RFC3339, raw)
	if parseErr != nil {
		if parsedTS, parseErr = time.Parse(time.RFC3339Nano, raw); parseErr != nil {
			return time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "created_at must be RFC3339"}
		}
	}
	if parsedTS.After(now.Add(5 * time.Minute)) {
		return time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "created_at cannot be in the future"}
	}
	if parsedTS.Before(now.Add(-10 * 365 * 24 * time.Hour)) {
		return time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "created_at is too far in the past"}
	}
	createdAt := parsedTS.UTC()
	return createdAt, createdAt.Format(time.RFC3339Nano), nil
}

func (s *Server) requireSoulRelationshipTarget(ctx context.Context, toAgentIDHex string) *apptheory.AppError {
	_, err := s.getSoulAgentIdentity(ctx, toAgentIDHex)
	if theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return nil
}

func verifySoulRelationshipCreateSignature(wallet string, input soulCreateRelationshipInput) *apptheory.AppError {
	contextForDigest := input.ContextMap
	if len(contextForDigest) == 0 {
		contextForDigest = nil
	}
	digest, appErr := computeSoulRelationshipDigest(
		input.FromAgentIDHex,
		input.ToAgentIDHex,
		input.RelationshipType,
		contextForDigest,
		input.Message,
		input.CreatedAtCanonical,
	)
	if appErr != nil {
		return appErr
	}
	if err := verifyEthereumSignatureBytesNonMalleable(wallet, digest, input.Signature); err != nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid relationship signature"}
	}
	return nil
}

func buildSoulRelationshipModels(input soulCreateRelationshipInput) (*models.SoulAgentRelationship, *models.SoulRelationshipFromIndex) {
	contextMap := input.ContextMap
	taskType := input.TaskType
	if len(contextMap) == 0 {
		contextMap = nil
		taskType = ""
	}

	rel := &models.SoulAgentRelationship{
		FromAgentID: input.FromAgentIDHex,
		ToAgentID:   input.ToAgentIDHex,
		Type:        input.RelationshipType,
		ContextJSON: "",
		ContextV2:   contextMap,
		TaskType:    taskType,
		Message:     input.Message,
		Signature:   input.Signature,
		CreatedAt:   input.CreatedAt,
	}
	_ = rel.UpdateKeys()

	fromIdx := &models.SoulRelationshipFromIndex{
		FromAgentID: input.FromAgentIDHex,
		ToAgentID:   input.ToAgentIDHex,
		Type:        input.RelationshipType,
		CreatedAt:   input.CreatedAt,
	}
	_ = fromIdx.UpdateKeys()
	return rel, fromIdx
}

func (s *Server) writeSoulRelationshipRecords(ctx context.Context, rel *models.SoulAgentRelationship, fromIdx *models.SoulRelationshipFromIndex) *apptheory.AppError {
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Create(rel)
		tx.Create(fromIdx)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return &apptheory.AppError{Code: "app.conflict", Message: "relationship already exists"}
		}
		return &apptheory.AppError{Code: "app.internal", Message: "failed to create relationship"}
	}
	return nil
}

type soulRelationshipListParams struct {
	cursor         string
	typeFilter     string
	taskTypeFilter string
	limit          int
	pageLimit      int
}

func parseSoulRelationshipListParams(ctx *apptheory.Context) soulRelationshipListParams {
	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	pageLimit := limit
	if pageLimit < 25 {
		pageLimit = 25
	}
	if pageLimit > 200 {
		pageLimit = 200
	}

	taskTypeFilter := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "taskType")))
	if taskTypeFilter == "" {
		taskTypeFilter = strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "task_type")))
	}

	return soulRelationshipListParams{
		cursor:         strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor")),
		typeFilter:     strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "type"))),
		taskTypeFilter: taskTypeFilter,
		limit:          limit,
		pageLimit:      pageLimit,
	}
}

func (s *Server) listSoulPublicRelationships(ctx context.Context, agentIDHex string, params soulRelationshipListParams) ([]models.SoulAgentRelationship, bool, string, *apptheory.AppError) {
	out := make([]models.SoulAgentRelationship, 0, params.limit)
	pageCursor := params.cursor
	nextCursor := ""
	hasMore := false

	for len(out) < params.limit {
		items, paged, appErr := s.loadSoulRelationshipPage(ctx, agentIDHex, pageCursor, params.pageLimit)
		if appErr != nil {
			return nil, false, "", appErr
		}

		consumed, cursor, more, appErr := consumeSoulRelationshipPage(items, paged, params, out)
		if appErr != nil {
			return nil, false, "", appErr
		}
		out = append(out, consumed...)
		nextCursor = cursor
		hasMore = more
		if len(out) >= params.limit || !hasMore {
			break
		}
		pageCursor = nextCursor
	}

	if shouldMergeLegacyRelationshipEndorsements(params) {
		endorsements, appErr := s.loadLegacyRelationshipEndorsements(ctx, agentIDHex)
		if appErr != nil {
			return nil, false, "", appErr
		}
		out = append(out, endorsements...)
	}

	return out, hasMore, nextCursor, nil
}

func (s *Server) loadSoulRelationshipPage(ctx context.Context, agentIDHex string, cursor string, limit int) ([]*models.SoulAgentRelationship, *core.PaginatedResult, *apptheory.AppError) {
	var items []*models.SoulAgentRelationship
	qb := s.store.DB.WithContext(ctx).
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
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list relationships"}
	}
	return items, paged, nil
}

func consumeSoulRelationshipPage(items []*models.SoulAgentRelationship, paged *core.PaginatedResult, params soulRelationshipListParams, existing []models.SoulAgentRelationship) ([]models.SoulAgentRelationship, string, bool, *apptheory.AppError) {
	out := make([]models.SoulAgentRelationship, 0, len(items))
	nextCursor := ""
	hasMore := false
	for idx, item := range items {
		if !relationshipMatchesFilters(item, params.typeFilter, params.taskTypeFilter) {
			continue
		}
		out = append(out, *item)
		if len(existing)+len(out) >= params.limit {
			pageCursor, pageHasMore, appErr := resolveRelationshipPageCursor(items, idx, item, paged)
			return out, pageCursor, pageHasMore, appErr
		}
	}
	if paged != nil && strings.TrimSpace(paged.NextCursor) != "" {
		nextCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = true
	}
	return out, nextCursor, hasMore, nil
}

func relationshipMatchesFilters(item *models.SoulAgentRelationship, typeFilter string, taskTypeFilter string) bool {
	if item == nil {
		return false
	}
	if typeFilter != "" && strings.ToLower(strings.TrimSpace(item.Type)) != typeFilter {
		return false
	}
	if taskTypeFilter != "" {
		taskType := strings.ToLower(strings.TrimSpace(item.TaskType))
		if taskType == "" {
			taskType = extractRelationshipTaskTypeFromMap(item.ContextV2)
		}
		if taskType != taskTypeFilter {
			return false
		}
		item.TaskType = taskType
	}
	normalizeRelationshipForPublicRead(item)
	return true
}

func resolveRelationshipPageCursor(items []*models.SoulAgentRelationship, idx int, item *models.SoulAgentRelationship, paged *core.PaginatedResult) (string, bool, *apptheory.AppError) {
	if idx < len(items)-1 {
		nextCursor := encodeSoulRelationshipCursor(item)
		if nextCursor == "" {
			return "", false, &apptheory.AppError{Code: "app.internal", Message: "failed to encode cursor"}
		}
		return nextCursor, true, nil
	}
	if paged != nil && strings.TrimSpace(paged.NextCursor) != "" {
		return strings.TrimSpace(paged.NextCursor), true, nil
	}
	return "", false, nil
}

func shouldMergeLegacyRelationshipEndorsements(params soulRelationshipListParams) bool {
	return params.cursor == "" &&
		params.taskTypeFilter == "" &&
		(params.typeFilter == "" || params.typeFilter == models.SoulRelationshipTypeEndorsement)
}

func (s *Server) loadLegacyRelationshipEndorsements(ctx context.Context, agentIDHex string) ([]models.SoulAgentRelationship, *apptheory.AppError) {
	var endorsements []*models.SoulAgentPeerEndorsement
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentPeerEndorsement{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "ENDORSEMENT#").
		All(&endorsements); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list endorsements"}
	}

	out := make([]models.SoulAgentRelationship, 0, len(endorsements))
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
	return out, nil
}

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
	if len(context) == 0 {
		context = nil
	}

	unsigned := map[string]any{
		"kind":        "soul_relationship",
		"version":     "1",
		"fromAgentId": fromAgentIDHex,
		"toAgentId":   toAgentIDHex,
		"type":        relType,
		"message":     message,
		"createdAt":   createdAt,
	}
	if context != nil {
		unsigned["context"] = context
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
