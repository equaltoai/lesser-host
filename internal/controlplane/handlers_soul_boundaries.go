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
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if s == nil || s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if identity.SelfDescriptionVersion <= 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet published; update registration first"}
	}

	var req soulAppendBoundaryRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
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

	// Verify EIP-191 boundary signature over keccak256(bytes(statement)).
	statementDigest := crypto.Keccak256([]byte(statement))
	if err := verifyEthereumSignatureBytes(identity.Wallet, statementDigest, signature); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid boundary signature"}
	}

	// If supersedes is set, verify the referenced boundary exists.
	if supersedes != "" {
		_, err := getSoulAgentItemBySK[models.SoulAgentBoundary](s, ctx.Context(), agentIDHex, fmt.Sprintf("BOUNDARY#%s", supersedes))
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "superseded boundary not found"}
		}
	}

	// Avoid prompting a signature for an already-existing boundary.
	if _, err := getSoulAgentItemBySK[models.SoulAgentBoundary](s, ctx.Context(), agentIDHex, fmt.Sprintf("BOUNDARY#%s", boundaryID)); err == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists"}
	} else if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to check boundary"}
	}

	// Load the current v2 registration as the base document.
	baseReg, appErr := s.loadSoulAgentV2RegistrationMap(ctx.Context(), agentIDHex, identity)
	if appErr != nil {
		return nil, appErr
	}
	if soulRegistrationMapHasBoundaryID(baseReg, boundaryID) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists in registration"}
	}

	now := time.Now().UTC()
	expectedVersion := identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1

	regMap, _, digest, _, _, appErr := s.buildSoulBoundaryAppendV2Registration(ctx.Context(), baseReg, agentIDHex, identity, soulBoundaryAppendBuildInput{
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
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if s == nil || s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	var req soulConfirmAppendBoundaryRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
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

	issuedAtRaw := strings.TrimSpace(req.IssuedAt)
	if issuedAtRaw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at is required"}
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if err != nil {
		issuedAt, err = time.Parse(time.RFC3339, issuedAtRaw)
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at must be an RFC3339 timestamp"}
	}

	expectedVersion := req.ExpectedVersion
	if expectedVersion == nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is required"}
	}
	if *expectedVersion < 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is invalid"}
	}

	selfSig := strings.TrimSpace(req.SelfAttestation)
	if selfSig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "self_attestation is required"}
	}

	// Retry-friendly: if the agent has already advanced and the boundary exists, treat as idempotent success.
	if *expectedVersion < identity.SelfDescriptionVersion {
		if existing, err := getSoulAgentItemBySK[models.SoulAgentBoundary](s, ctx.Context(), agentIDHex, fmt.Sprintf("BOUNDARY#%s", boundaryID)); err == nil && existing != nil {
			return apptheory.JSON(http.StatusOK, soulAppendBoundaryResponse{Boundary: *existing})
		}
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}
	if *expectedVersion != identity.SelfDescriptionVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	// Verify EIP-191 signature over keccak256(bytes(statement)).
	statementDigest := crypto.Keccak256([]byte(statement))
	if err := verifyEthereumSignatureBytes(identity.Wallet, statementDigest, signature); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid boundary signature"}
	}

	// If supersedes is set, verify the referenced boundary exists.
	if supersedes != "" {
		_, err := getSoulAgentItemBySK[models.SoulAgentBoundary](s, ctx.Context(), agentIDHex, fmt.Sprintf("BOUNDARY#%s", supersedes))
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "superseded boundary not found"}
		}
	}

	// Load the current v2 registration as the base document.
	baseReg, appErr := s.loadSoulAgentV2RegistrationMap(ctx.Context(), agentIDHex, identity)
	if appErr != nil {
		return nil, appErr
	}
	if soulRegistrationMapHasBoundaryID(baseReg, boundaryID) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists in registration"}
	}

	nextVersion := *expectedVersion + 1
	regMap, regV2, digest, capsNorm, claimLevels, appErr := s.buildSoulBoundaryAppendV2Registration(ctx.Context(), baseReg, agentIDHex, identity, soulBoundaryAppendBuildInput{
		BoundaryID:      boundaryID,
		Category:        category,
		Statement:       statement,
		Rationale:       rationale,
		Supersedes:      supersedes,
		Signature:       signature,
		IssuedAt:        issuedAt.UTC(),
		ExpectedPrev:    *expectedVersion,
		NextVersion:     nextVersion,
		SelfAttestation: selfSig,
	})
	if appErr != nil {
		return nil, appErr
	}

	if err := verifyEthereumSignatureBytes(identity.Wallet, digest, selfSig); err != nil {
		log.Printf("controlplane: soul_integrity invalid_registration_signature agent=%s request_id=%s", agentIDHex, strings.TrimSpace(ctx.RequestID))
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	regBytes, err := json.Marshal(regMap)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	regSHA256 := func() string {
		sum := sha256.Sum256(regBytes)
		return hex.EncodeToString(sum[:])
	}()

	changeSummary := extractStringField(regMap, "changeSummary")

	now := time.Now().UTC()
	boundary := &models.SoulAgentBoundary{
		AgentID:        agentIDHex,
		BoundaryID:     boundaryID,
		Category:       category,
		Statement:      statement,
		Rationale:      rationale,
		AddedInVersion: nextVersion,
		Supersedes:     supersedes,
		Signature:      signature,
		AddedAt:        issuedAt.UTC(),
	}
	_ = boundary.UpdateKeys()

	// Publish a new v2 registration version and append the boundary record atomically in DynamoDB.
	_, pubErr := s.publishSoulAgentRegistrationV2WithExtraWrites(ctx.Context(), agentIDHex, identity, regV2, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, expectedVersion, now, func(tx soulRegistrationV2TxHook) error {
		tx.Create(boundary)
		return nil
	})
	if pubErr != nil {
		return nil, pubErr
	}

	log.Printf("controlplane: soul_integrity boundary_append_published agent=%s boundary=%s request_id=%s", agentIDHex, boundaryID, strings.TrimSpace(ctx.RequestID))

	// Audit log.
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.boundary.append",
		Target:    fmt.Sprintf("soul_agent_boundary:%s:%s", agentIDHex, boundaryID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	// Best-effort: maintain boundary keyword index for search.
	s.tryWriteSoulBoundaryKeywordIndexForBoundary(ctx.Context(), identity, boundary)

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

func (s *Server) loadSoulAgentV2RegistrationMap(ctx context.Context, agentIDHex string, identity *models.SoulAgentIdentity) (map[string]any, *apptheory.AppError) {
	if s == nil || identity == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	if strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	key := soulRegistrationS3Key(agentIDHex)
	body, _, _, err := s.soulPacks.GetObject(ctx, key, 1024*1024)
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet published; update registration first"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to fetch registration"}
	}

	var reg map[string]any
	if err := json.Unmarshal(body, &reg); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to parse registration"}
	}
	if strings.TrimSpace(extractStringField(reg, "version")) != "2" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not v2; update registration first"}
	}

	if validateErr := validateSoulUpdateRegistrationIdentityFields(reg, agentIDHex, identity); validateErr != nil {
		return nil, validateErr
	}
	if wallet := extractStringField(reg, "wallet"); wallet != "" && !strings.EqualFold(wallet, strings.TrimSpace(identity.Wallet)) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent wallet is out of sync; update registration first"}
	}

	return reg, nil
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

func (s *Server) buildSoulBoundaryAppendV2Registration(ctx context.Context, base map[string]any, agentIDHex string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) (reg map[string]any, regV2 *soul.RegistrationFileV2, digest []byte, capsNorm []string, claimLevels map[string]string, appErr *apptheory.AppError) {
	if s == nil || identity == nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if base == nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(extractStringField(base, "version")) != "2" {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not v2; update registration first"}
	}
	if input.ExpectedPrev < 0 || input.NextVersion <= 0 || input.NextVersion != input.ExpectedPrev+1 {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid expected_version"}
	}

	// Shallow copy base map so we can mutate fields for the new version.
	reg = make(map[string]any, len(base))
	for k, v := range base {
		reg[k] = v
	}

	// Update previousVersionUri for the new version.
	if input.NextVersion <= 1 {
		delete(reg, "previousVersionUri")
	} else {
		prevKey := soulRegistrationVersionedS3Key(agentIDHex, input.NextVersion-1)
		reg["previousVersionUri"] = fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), prevKey)
	}

	// Update timestamps + summary.
	issuedAt := input.IssuedAt.UTC().Format(time.RFC3339Nano)
	reg["updated"] = issuedAt
	reg["changeSummary"] = fmt.Sprintf("Append boundary %s", input.BoundaryID)

	// Ensure attestations object exists and set selfAttestation placeholder/signature.
	attAny, ok := reg["attestations"]
	att, ok2 := attAny.(map[string]any)
	if !ok || !ok2 || att == nil {
		att = map[string]any{}
	}
	att["selfAttestation"] = strings.TrimSpace(input.SelfAttestation)
	reg["attestations"] = att

	// Build updated boundaries: preserve existing array, then append any missing DB boundaries, then append the new boundary.
	boundariesAny, _ := reg["boundaries"].([]any)
	if boundariesAny == nil {
		boundariesAny = []any{}
	}
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
	if _, ok := existingIDs[input.BoundaryID]; ok {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists in registration"}
	}

	// If DB has additional boundaries (e.g., from old append flows), append them before the new one.
	dbBounds, listErr := s.listSoulAgentBoundariesNoTruncation(ctx, agentIDHex)
	if listErr != nil {
		return nil, nil, nil, nil, nil, listErr
	}
	missing := make([]*models.SoulAgentBoundary, 0, len(dbBounds))
	for _, b := range dbBounds {
		if b == nil {
			continue
		}
		if strings.TrimSpace(b.BoundaryID) == input.BoundaryID {
			return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "boundary with this ID already exists"}
		}
		if _, ok := existingIDs[strings.TrimSpace(b.BoundaryID)]; ok {
			continue
		}
		missing = append(missing, b)
	}
	sortSoulBoundariesByAddedAt(missing)
	for _, b := range missing {
		boundariesAny = append(boundariesAny, soulBoundaryV2MapFromModel(b))
		existingIDs[strings.TrimSpace(b.BoundaryID)] = struct{}{}
	}

	newBoundary := map[string]any{
		"id":             input.BoundaryID,
		"category":       input.Category,
		"statement":      input.Statement,
		"addedAt":        issuedAt,
		"addedInVersion": strconv.Itoa(input.NextVersion),
		"signature":      input.Signature,
	}
	if strings.TrimSpace(input.Rationale) != "" {
		newBoundary["rationale"] = strings.TrimSpace(input.Rationale)
	}
	if strings.TrimSpace(input.Supersedes) != "" {
		newBoundary["supersedes"] = strings.TrimSpace(input.Supersedes)
	}
	boundariesAny = append(boundariesAny, newBoundary)
	reg["boundaries"] = boundariesAny

	regBytes, err := json.Marshal(reg)
	if err != nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	parsed, err := soul.ParseRegistrationFileV2(regBytes)
	if err != nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v2 registration schema"}
	}
	if err := parsed.Validate(); err != nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	digest, appErr = computeSoulRegistrationSelfAttestationDigest(reg)
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}

	// Capability indexing inputs.
	caps := extractCapabilityNames(reg)
	capsNorm, appErr = normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, caps)
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}
	claimLevels = extractCapabilityClaimLevels(reg)

	return reg, parsed, digest, capsNorm, claimLevels, nil
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
