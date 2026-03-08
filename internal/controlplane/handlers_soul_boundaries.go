package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
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

type soulAppendBoundaryBeginResponse struct {
	Version         string `json:"version"`
	DigestHex       string `json:"digest_hex"`
	IssuedAt        string `json:"issued_at"`
	ExpectedVersion int    `json:"expected_version"`
	NextVersion     int    `json:"next_version"`
}

type soulConfirmAppendBoundaryRequest struct {
	BoundaryID string `json:"boundary_id"`
	Category   string `json:"category"`
	Statement  string `json:"statement"`
	Rationale  string `json:"rationale,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`
	Signature  string `json:"signature"`

	IssuedAt        string `json:"issued_at"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
	SelfAttestation string `json:"self_attestation"`
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

// handleSoulBeginAppendBoundary prepares a new boundary append by returning the canonical v2 self-attestation digest
// for the updated registration document (so the client can sign it).
func (s *Server) handleSoulBeginAppendBoundary(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentIDHex, identity, appErr := s.requireSoulWritableAgent(ctx)
	if appErr != nil {
		return nil, appErr
	}
	appErr = requirePublishedSoulIdentity(identity)
	if appErr != nil {
		return nil, appErr
	}

	var req soulAppendBoundaryRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	boundaryID, category, statement, rationale, supersedes, signature, appErr := parseAndValidateSoulBoundaryAppendInput(
		req.BoundaryID,
		req.Category,
		req.Statement,
		req.Rationale,
		req.Supersedes,
		req.Signature,
	)
	if appErr != nil {
		return nil, appErr
	}

	appErr = verifySoulBoundaryStatementSignature(identity.Wallet, statement, signature)
	if appErr != nil {
		return nil, appErr
	}
	appErr = s.ensureSoulBoundaryReferenceExists(ctx.Context(), agentIDHex, supersedes, "superseded boundary not found")
	if appErr != nil {
		return nil, appErr
	}
	appErr = s.ensureSoulBoundaryAvailable(ctx.Context(), agentIDHex, boundaryID)
	if appErr != nil {
		return nil, appErr
	}

	baseReg, baseVersion, appErr := s.loadSoulBoundaryAppendBase(ctx.Context(), agentIDHex, identity, boundaryID)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	expectedVersion := identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1

	regMap, _, _, digest, _, _, appErr := s.buildSoulBoundaryAppendRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, soulBoundaryAppendBuildInput{
		BoundaryID:      boundaryID,
		Category:        category,
		Statement:       statement,
		Rationale:       rationale,
		Supersedes:      supersedes,
		Signature:       signature,
		IssuedAt:        now,
		ExpectedPrev:    expectedVersion,
		NextVersion:     nextVersion,
		SelfAttestation: "0x00", // placeholder; digest excludes selfAttestation
	})
	if appErr != nil {
		return nil, appErr
	}
	_ = regMap // keep for debugging hooks; digest computed from it

	return apptheory.JSON(http.StatusOK, soulAppendBoundaryBeginResponse{
		Version:         "1",
		DigestHex:       "0x" + hex.EncodeToString(digest),
		IssuedAt:        now.Format(time.RFC3339Nano),
		ExpectedVersion: expectedVersion,
		NextVersion:     nextVersion,
	})
}

// handleSoulAppendBoundary appends a new boundary declaration for a soul agent.
// Boundaries are append-only: no delete or update. Supersession is via the `supersedes` field.
func (s *Server) handleSoulAppendBoundary(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentIDHex, identity, appErr := s.requireSoulWritableAgent(ctx)
	if appErr != nil {
		return nil, appErr
	}

	var req soulConfirmAppendBoundaryRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	boundaryID, category, statement, rationale, supersedes, signature, appErr := parseAndValidateSoulBoundaryAppendInput(
		req.BoundaryID,
		req.Category,
		req.Statement,
		req.Rationale,
		req.Supersedes,
		req.Signature,
	)
	if appErr != nil {
		return nil, appErr
	}

	issuedAt, appErr := parseSoulRequestIssuedAt(req.IssuedAt)
	if appErr != nil {
		return nil, appErr
	}
	expectedVersion, appErr := parseSoulExpectedVersion(req.ExpectedVersion)
	if appErr != nil {
		return nil, appErr
	}
	selfSig, appErr := requireSoulSelfAttestation(req.SelfAttestation)
	if appErr != nil {
		return nil, appErr
	}

	// Retry-friendly: if the agent has already advanced and the boundary exists, treat as idempotent success.
	if expectedVersion < identity.SelfDescriptionVersion {
		if existing, getErr := getSoulAgentItemBySK[models.SoulAgentBoundary](s, ctx.Context(), agentIDHex, fmt.Sprintf("BOUNDARY#%s", boundaryID)); getErr == nil && existing != nil {
			return apptheory.JSON(http.StatusOK, soulAppendBoundaryResponse{Boundary: *existing})
		}
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}
	if expectedVersion != identity.SelfDescriptionVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	appErr = verifySoulBoundaryStatementSignature(identity.Wallet, statement, signature)
	if appErr != nil {
		return nil, appErr
	}
	appErr = s.ensureSoulBoundaryReferenceExists(ctx.Context(), agentIDHex, supersedes, "superseded boundary not found")
	if appErr != nil {
		return nil, appErr
	}

	baseReg, baseVersion, appErr := s.loadSoulBoundaryAppendBase(ctx.Context(), agentIDHex, identity, boundaryID)
	if appErr != nil {
		return nil, appErr
	}

	nextVersion := expectedVersion + 1
	boundary, appErr := s.publishSoulBoundaryAppend(ctx, agentIDHex, identity, baseReg, baseVersion, soulBoundaryAppendBuildInput{
		BoundaryID:      boundaryID,
		Category:        category,
		Statement:       statement,
		Rationale:       rationale,
		Supersedes:      supersedes,
		Signature:       signature,
		IssuedAt:        issuedAt.UTC(),
		ExpectedPrev:    expectedVersion,
		NextVersion:     nextVersion,
		SelfAttestation: selfSig,
	})
	if appErr != nil {
		return nil, appErr
	}

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

	cursor, limit := soulPublicCursorAndLimit(ctx)
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

	resp, err := s.buildSoulPublicBoundariesResponse(ctx, items, paged)
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

func (s *Server) requireSoulWritableAgent(ctx *apptheory.Context) (string, *models.SoulAgentIdentity, *apptheory.AppError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return "", nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return "", nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return "", nil, appErr
	}
	if s == nil || s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return "", nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return "", nil, appErr
	}
	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return "", nil, appErr
	}
	return agentIDHex, identity, nil
}

func requirePublishedSoulIdentity(identity *models.SoulAgentIdentity) *apptheory.AppError {
	if identity == nil || identity.SelfDescriptionVersion <= 0 {
		return &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet published; update registration first"}
	}
	return nil
}

func parseSoulRequestIssuedAt(raw string) (time.Time, *apptheory.AppError) {
	issuedAtRaw := strings.TrimSpace(raw)
	if issuedAtRaw == "" {
		return time.Time{}, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at is required"}
	}
	issuedAt, parseErr := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if parseErr == nil {
		return issuedAt, nil
	}
	issuedAt, parseErr = time.Parse(time.RFC3339, issuedAtRaw)
	if parseErr != nil {
		return time.Time{}, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at must be an RFC3339 timestamp"}
	}
	return issuedAt, nil
}

func parseSoulExpectedVersion(expectedVersion *int) (int, *apptheory.AppError) {
	if expectedVersion == nil {
		return 0, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is required"}
	}
	if *expectedVersion < 0 {
		return 0, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is invalid"}
	}
	return *expectedVersion, nil
}

func requireSoulSelfAttestation(raw string) (string, *apptheory.AppError) {
	selfSig := strings.TrimSpace(raw)
	if selfSig == "" {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "self_attestation is required"}
	}
	return selfSig, nil
}

func verifySoulBoundaryStatementSignature(wallet string, statement string, signature string) *apptheory.AppError {
	statementDigest := crypto.Keccak256([]byte(statement))
	if verifyErr := verifyEthereumSignatureBytes(wallet, statementDigest, signature); verifyErr != nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid boundary signature"}
	}
	return nil
}

func (s *Server) ensureSoulBoundaryReferenceExists(ctx context.Context, agentIDHex string, boundaryID string, message string) *apptheory.AppError {
	boundaryID = strings.TrimSpace(boundaryID)
	if boundaryID == "" {
		return nil
	}
	_, getErr := getSoulAgentItemBySK[models.SoulAgentBoundary](s, ctx, agentIDHex, fmt.Sprintf("BOUNDARY#%s", boundaryID))
	if getErr != nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: message}
	}
	return nil
}

func (s *Server) ensureSoulBoundaryAvailable(ctx context.Context, agentIDHex string, boundaryID string) *apptheory.AppError {
	_, getErr := getSoulAgentItemBySK[models.SoulAgentBoundary](s, ctx, agentIDHex, fmt.Sprintf("BOUNDARY#%s", boundaryID))
	if getErr == nil {
		return &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists"}
	}
	if !theoryErrors.IsNotFound(getErr) {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to check boundary"}
	}
	return nil
}

func (s *Server) loadSoulBoundaryAppendBase(ctx context.Context, agentIDHex string, identity *models.SoulAgentIdentity, boundaryID string) (map[string]any, string, *apptheory.AppError) {
	baseReg, baseVersion, appErr := s.loadSoulAgentRegistrationMap(ctx, agentIDHex, identity)
	if appErr != nil {
		return nil, "", appErr
	}
	if soulRegistrationMapHasBoundaryID(baseReg, boundaryID) {
		return nil, "", &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists in registration"}
	}
	return baseReg, baseVersion, nil
}

func soulPublicCursorAndLimit(ctx *apptheory.Context) (string, int) {
	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	limit := envIntPositiveClampedFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50, 200)
	return cursor, limit
}

func soulBoundariesFromModels(items []*models.SoulAgentBoundary) []models.SoulAgentBoundary {
	out := make([]models.SoulAgentBoundary, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}
	return out
}

func soulPaginatedResultMeta(paged *core.PaginatedResult) (string, bool) {
	if paged == nil {
		return "", false
	}
	return strings.TrimSpace(paged.NextCursor), paged.HasMore
}

func (s *Server) buildSoulPublicBoundariesResponse(ctx *apptheory.Context, items []*models.SoulAgentBoundary, paged *core.PaginatedResult) (*apptheory.Response, error) {
	out := soulBoundariesFromModels(items)
	nextCursor, hasMore := soulPaginatedResultMeta(paged)
	resp, err := apptheory.JSON(http.StatusOK, soulListBoundariesResponse{
		Version:    "1",
		Boundaries: out,
		Count:      len(out),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
	if err != nil {
		return nil, err
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

type soulBoundaryAppendBuildInput struct {
	BoundaryID string
	Category   string
	Statement  string
	Rationale  string
	Supersedes string
	Signature  string

	IssuedAt        time.Time
	ExpectedPrev    int
	NextVersion     int
	SelfAttestation string
}

func parseAndValidateSoulBoundaryAppendInput(boundaryIDRaw, categoryRaw, statementRaw, rationaleRaw, supersedesRaw, signatureRaw string) (boundaryID, category, statement, rationale, supersedes, signature string, appErr *apptheory.AppError) {
	boundaryID = strings.TrimSpace(boundaryIDRaw)
	if boundaryID == "" {
		return "", "", "", "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "boundary_id is required"}
	}
	if len(boundaryID) > 128 {
		return "", "", "", "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "boundary_id is too long"}
	}

	category = strings.ToLower(strings.TrimSpace(categoryRaw))
	if !isValidBoundaryCategory(category) {
		return "", "", "", "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "category must be one of: refusal, scope_limit, ethical_commitment, circuit_breaker"}
	}

	statement = strings.TrimSpace(statementRaw)
	if statement == "" {
		return "", "", "", "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "statement is required"}
	}
	if len(statement) > 4096 {
		return "", "", "", "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "statement is too long"}
	}

	rationale = strings.TrimSpace(rationaleRaw)
	if len(rationale) > 8192 {
		return "", "", "", "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "rationale is too long"}
	}

	supersedes = strings.TrimSpace(supersedesRaw)
	if supersedes != "" && len(supersedes) > 128 {
		return "", "", "", "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "supersedes is too long"}
	}

	signature = strings.TrimSpace(signatureRaw)
	if signature == "" {
		return "", "", "", "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}

	return boundaryID, category, statement, rationale, supersedes, signature, nil
}

func cloneSoulRegistrationMap(base map[string]any) map[string]any {
	reg := make(map[string]any, len(base))
	for k, v := range base {
		reg[k] = v
	}
	return reg
}

func setSoulRegistrationPreviousVersionURI(reg map[string]any, bucketName string, agentIDHex string, nextVersion int) {
	if nextVersion <= 1 {
		delete(reg, "previousVersionUri")
		return
	}
	prevKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion-1)
	reg["previousVersionUri"] = fmt.Sprintf("s3://%s/%s", strings.TrimSpace(bucketName), prevKey)
}

func ensureSoulRegistrationAttestations(reg map[string]any) map[string]any {
	attAny, ok := reg["attestations"]
	att, ok2 := attAny.(map[string]any)
	if !ok || !ok2 || att == nil {
		att = map[string]any{}
	}
	reg["attestations"] = att
	return att
}

func parseSoulRegistrationByVersion(baseVersion string, reg map[string]any) (*soul.RegistrationFileV2, *soul.RegistrationFileV3, *apptheory.AppError) {
	regBytes, marshalErr := json.Marshal(reg)
	if marshalErr != nil {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	if baseVersion == "2" {
		parsed, parseErr := soul.ParseRegistrationFileV2(regBytes)
		if parseErr != nil {
			return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v2 registration schema"}
		}
		if validateErr := parsed.Validate(); validateErr != nil {
			return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: validateErr.Error()}
		}
		return parsed, nil, nil
	}

	parsed, parseErr := soul.ParseRegistrationFileV3(regBytes)
	if parseErr != nil {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v3 registration schema"}
	}
	if validateErr := parsed.Validate(); validateErr != nil {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: validateErr.Error()}
	}
	return nil, parsed, nil
}

func soulRegistrationMapHasBoundaryID(reg map[string]any, boundaryID string) bool {
	if reg == nil {
		return false
	}
	boundaryID = strings.TrimSpace(boundaryID)
	if boundaryID == "" {
		return false
	}
	raw, ok := reg["boundaries"]
	if !ok {
		return false
	}
	arr, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		if strings.TrimSpace(id) == boundaryID {
			return true
		}
	}
	return false
}

func (s *Server) loadSoulAgentRegistrationMap(ctx context.Context, agentIDHex string, identity *models.SoulAgentIdentity) (reg map[string]any, schemaVersion string, appErr *apptheory.AppError) {
	if s == nil || identity == nil {
		return nil, "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.soulPacks == nil {
		return nil, "", &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	if strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return nil, "", &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	key := soulRegistrationS3Key(agentIDHex)
	body, _, _, err := s.soulPacks.GetObject(ctx, key, 1024*1024)
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, "", &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet published; update registration first"}
		}
		return nil, "", &apptheory.AppError{Code: "app.internal", Message: "failed to fetch registration"}
	}

	if err := json.Unmarshal(body, &reg); err != nil {
		return nil, "", &apptheory.AppError{Code: "app.internal", Message: "failed to parse registration"}
	}
	schemaVersion = strings.TrimSpace(extractStringField(reg, "version"))
	if schemaVersion != "2" && schemaVersion != "3" {
		return nil, "", &apptheory.AppError{Code: "app.conflict", Message: "registration version is unsupported; update registration first"}
	}

	if validateErr := validateSoulUpdateRegistrationIdentityFields(reg, agentIDHex, identity); validateErr != nil {
		return nil, "", validateErr
	}
	if wallet := extractStringField(reg, "wallet"); wallet != "" && !strings.EqualFold(wallet, strings.TrimSpace(identity.Wallet)) {
		return nil, "", &apptheory.AppError{Code: "app.conflict", Message: "agent wallet is out of sync; update registration first"}
	}

	return reg, schemaVersion, nil
}

func computeSoulRegistrationSelfAttestationDigest(reg map[string]any) ([]byte, *apptheory.AppError) {
	if reg == nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	attAny, ok := reg["attestations"]
	if !ok {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "attestations are required"}
	}
	att, ok := attAny.(map[string]any)
	if !ok {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "attestations must be an object"}
	}

	// Copy reg + attestations so computeSoulUpdateRegistrationDigest can delete selfAttestation without mutating the caller map.
	regCopy := make(map[string]any, len(reg))
	for k, v := range reg {
		regCopy[k] = v
	}
	attCopy := make(map[string]any, len(att))
	for k, v := range att {
		attCopy[k] = v
	}
	regCopy["attestations"] = attCopy

	return computeSoulUpdateRegistrationDigest(regCopy, attCopy)
}

func validateSoulBoundaryAppendRegistrationBuild(base map[string]any, baseVersion string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) *apptheory.AppError {
	if identity == nil || base == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	baseVersion = strings.TrimSpace(baseVersion)
	if baseVersion != "2" && baseVersion != "3" {
		return &apptheory.AppError{Code: "app.conflict", Message: "registration version is unsupported; update registration first"}
	}
	if input.ExpectedPrev < 0 || input.NextVersion <= 0 || input.NextVersion != input.ExpectedPrev+1 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid expected_version"}
	}
	return nil
}

func collectSoulRegistrationBoundaryIDs(boundariesAny []any) map[string]struct{} {
	existingIDs := map[string]struct{}{}
	for _, item := range boundariesAny {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		id = strings.TrimSpace(id)
		if id != "" {
			existingIDs[id] = struct{}{}
		}
	}
	return existingIDs
}

func (s *Server) mergeSoulBoundaryRegistrationBoundaries(ctx context.Context, agentIDHex string, boundariesAny []any, existingIDs map[string]struct{}, boundaryID string) ([]any, *apptheory.AppError) {
	dbBounds, listErr := s.listSoulAgentBoundariesNoTruncation(ctx, agentIDHex)
	if listErr != nil {
		return nil, listErr
	}
	missing := make([]*models.SoulAgentBoundary, 0, len(dbBounds))
	for _, boundary := range dbBounds {
		if boundary == nil {
			continue
		}
		if strings.TrimSpace(boundary.BoundaryID) == boundaryID {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists"}
		}
		if _, ok := existingIDs[strings.TrimSpace(boundary.BoundaryID)]; ok {
			continue
		}
		missing = append(missing, boundary)
	}
	sortSoulBoundariesByAddedAt(missing)
	for _, boundary := range missing {
		boundariesAny = append(boundariesAny, soulBoundaryV2MapFromModel(boundary))
		existingIDs[strings.TrimSpace(boundary.BoundaryID)] = struct{}{}
	}
	return boundariesAny, nil
}

func buildSoulBoundaryAppendEntry(input soulBoundaryAppendBuildInput, issuedAt string) map[string]any {
	entry := map[string]any{
		"id":             input.BoundaryID,
		"category":       input.Category,
		"statement":      input.Statement,
		"addedAt":        issuedAt,
		"addedInVersion": strconv.Itoa(input.NextVersion),
		"signature":      input.Signature,
	}
	if strings.TrimSpace(input.Rationale) != "" {
		entry["rationale"] = strings.TrimSpace(input.Rationale)
	}
	if strings.TrimSpace(input.Supersedes) != "" {
		entry["supersedes"] = strings.TrimSpace(input.Supersedes)
	}
	return entry
}

func (s *Server) finalizeSoulBoundaryAppendRegistration(reg map[string]any, baseVersion string) (*soul.RegistrationFileV2, *soul.RegistrationFileV3, []byte, []string, map[string]string, *apptheory.AppError) {
	regV2, regV3, parseErr := parseSoulRegistrationByVersion(baseVersion, reg)
	if parseErr != nil {
		return nil, nil, nil, nil, nil, parseErr
	}
	digest, appErr := computeSoulRegistrationSelfAttestationDigest(reg)
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}
	capsNorm, appErr := normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, extractCapabilityNames(reg))
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}
	return regV2, regV3, digest, capsNorm, extractCapabilityClaimLevels(reg), nil
}

func (s *Server) buildSoulBoundaryAppendRegistration(ctx context.Context, base map[string]any, baseVersion string, agentIDHex string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) (reg map[string]any, regV2 *soul.RegistrationFileV2, regV3 *soul.RegistrationFileV3, digest []byte, capsNorm []string, claimLevels map[string]string, appErr *apptheory.AppError) {
	if s == nil {
		return nil, nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	appErr = validateSoulBoundaryAppendRegistrationBuild(base, baseVersion, identity, input)
	if appErr != nil {
		return nil, nil, nil, nil, nil, nil, appErr
	}

	reg = cloneSoulRegistrationMap(base)

	setSoulRegistrationPreviousVersionURI(reg, s.cfg.SoulPackBucketName, agentIDHex, input.NextVersion)

	issuedAt := input.IssuedAt.UTC().Format(time.RFC3339Nano)
	reg["updated"] = issuedAt
	reg["changeSummary"] = fmt.Sprintf("Append boundary %s", input.BoundaryID)

	att := ensureSoulRegistrationAttestations(reg)
	att["selfAttestation"] = strings.TrimSpace(input.SelfAttestation)

	boundariesAny, _ := reg["boundaries"].([]any)
	if boundariesAny == nil {
		boundariesAny = []any{}
	}
	existingIDs := collectSoulRegistrationBoundaryIDs(boundariesAny)
	if _, ok := existingIDs[input.BoundaryID]; ok {
		return nil, nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists in registration"}
	}

	boundariesAny, appErr = s.mergeSoulBoundaryRegistrationBoundaries(ctx, agentIDHex, boundariesAny, existingIDs, input.BoundaryID)
	if appErr != nil {
		return nil, nil, nil, nil, nil, nil, appErr
	}

	boundariesAny = append(boundariesAny, buildSoulBoundaryAppendEntry(input, issuedAt))
	reg["boundaries"] = boundariesAny

	regV2, regV3, digest, capsNorm, claimLevels, appErr = s.finalizeSoulBoundaryAppendRegistration(reg, baseVersion)
	if appErr != nil {
		return nil, nil, nil, nil, nil, nil, appErr
	}

	return reg, regV2, regV3, digest, capsNorm, claimLevels, nil
}

func (s *Server) publishSoulBoundaryAppend(ctx *apptheory.Context, agentIDHex string, identity *models.SoulAgentIdentity, baseReg map[string]any, baseVersion string, input soulBoundaryAppendBuildInput) (*models.SoulAgentBoundary, *apptheory.AppError) {
	regMap, regV2, regV3, digest, capsNorm, claimLevels, appErr := s.buildSoulBoundaryAppendRegistration(ctx.Context(), baseReg, baseVersion, agentIDHex, identity, input)
	if appErr != nil {
		return nil, appErr
	}
	if verifyErr := verifyEthereumSignatureBytes(identity.Wallet, digest, input.SelfAttestation); verifyErr != nil {
		log.Printf("controlplane: soul_integrity invalid_registration_signature agent=%s request_id=%s", agentIDHex, strings.TrimSpace(ctx.RequestID))
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	regBytes, marshalErr := json.Marshal(regMap)
	if marshalErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	sum := sha256.Sum256(regBytes)
	regSHA256 := hex.EncodeToString(sum[:])
	changeSummary := extractStringField(regMap, "changeSummary")
	now := time.Now().UTC()

	boundary := &models.SoulAgentBoundary{
		AgentID:        agentIDHex,
		BoundaryID:     input.BoundaryID,
		Category:       input.Category,
		Statement:      input.Statement,
		Rationale:      input.Rationale,
		AddedInVersion: input.NextVersion,
		Supersedes:     input.Supersedes,
		Signature:      input.Signature,
		AddedAt:        input.IssuedAt.UTC(),
	}
	_ = boundary.UpdateKeys()

	if strings.TrimSpace(baseVersion) == "3" {
		_, pubErr := s.publishSoulAgentRegistrationV3WithExtraWrites(ctx.Context(), agentIDHex, identity, regV3, regBytes, regSHA256, input.SelfAttestation, changeSummary, capsNorm, claimLevels, &input.ExpectedPrev, now, func(tx soulRegistrationV3TxHook) error {
			tx.Create(boundary)
			return nil
		})
		if pubErr != nil {
			return nil, pubErr
		}
		_ = s.syncSoulV3StateFromRegistration(ctx.Context(), agentIDHex, identity, regV3, now)
	} else {
		_, pubErr := s.publishSoulAgentRegistrationV2WithExtraWrites(ctx.Context(), agentIDHex, identity, regV2, regBytes, regSHA256, input.SelfAttestation, changeSummary, capsNorm, claimLevels, &input.ExpectedPrev, now, func(tx soulRegistrationV2TxHook) error {
			tx.Create(boundary)
			return nil
		})
		if pubErr != nil {
			return nil, pubErr
		}
	}

	log.Printf("controlplane: soul_integrity boundary_append_published agent=%s boundary=%s request_id=%s", agentIDHex, input.BoundaryID, strings.TrimSpace(ctx.RequestID))
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.boundary.append",
		Target:    fmt.Sprintf("soul_agent_boundary:%s:%s", agentIDHex, input.BoundaryID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})
	s.tryWriteSoulBoundaryKeywordIndexForBoundary(ctx.Context(), identity, boundary)
	return boundary, nil
}

func (s *Server) listSoulAgentBoundariesNoTruncation(ctx context.Context, agentIDHex string) ([]*models.SoulAgentBoundary, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if agentIDHex == "" {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var items []*models.SoulAgentBoundary
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentBoundary{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "BOUNDARY#").
		All(&items); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list boundaries"}
	}
	return items, nil
}

func sortSoulBoundariesByAddedAt(items []*models.SoulAgentBoundary) {
	if len(items) < 2 {
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		a := items[i]
		b := items[j]
		if a == nil && b == nil {
			return false
		}
		if a == nil {
			return true
		}
		if b == nil {
			return false
		}
		if !a.AddedAt.Equal(b.AddedAt) {
			return a.AddedAt.Before(b.AddedAt)
		}
		return strings.TrimSpace(a.BoundaryID) < strings.TrimSpace(b.BoundaryID)
	})
}

func soulBoundaryV2MapFromModel(b *models.SoulAgentBoundary) map[string]any {
	if b == nil {
		return map[string]any{}
	}
	addedAt := ""
	if !b.AddedAt.IsZero() {
		addedAt = b.AddedAt.UTC().Format(time.RFC3339Nano)
	}
	addedIn := ""
	if b.AddedInVersion > 0 {
		addedIn = strconv.Itoa(b.AddedInVersion)
	}
	out := map[string]any{
		"id":             strings.TrimSpace(b.BoundaryID),
		"category":       strings.ToLower(strings.TrimSpace(b.Category)),
		"statement":      strings.TrimSpace(b.Statement),
		"addedAt":        addedAt,
		"addedInVersion": addedIn,
		"signature":      strings.TrimSpace(b.Signature),
	}
	if strings.TrimSpace(b.Rationale) != "" {
		out["rationale"] = strings.TrimSpace(b.Rationale)
	}
	if strings.TrimSpace(b.Supersedes) != "" {
		out["supersedes"] = strings.TrimSpace(b.Supersedes)
	}
	return out
}
