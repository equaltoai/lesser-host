package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulRecoveryInventoryPath = "/api/v1/soul/instance/recovery/agents"

	soulRecoveryClassificationPublished = "published_artifact_verified"
	soulRecoveryClassificationLegacy    = "legacy_declarations_only"

	soulRecoveryRouteInventory = "recovery_inventory"
	soulRecoveryRouteDetail    = "recovery_detail"

	soulRecoveryDefaultLimit    = 20
	soulRecoveryMaxLimit        = 50
	soulRecoveryMaxScanPages    = 20
	soulRecoveryMaxSessions     = 50
	soulRecoveryMaxVersions     = 100
	soulRecoveryMaxArtifactSize = int64(1024 * 1024)
	soulRecoveryMaxResponseSize = 2 * 1024 * 1024

	soulRecoveryCodeInvalidRequest    = "soul_recovery.invalid_request"
	soulRecoveryCodeUnauthorized      = "soul_recovery.unauthorized"
	soulRecoveryCodeBoundaryViolation = "soul_recovery.boundary_violation"
	soulRecoveryCodeNotFound          = "soul_recovery.not_found"
	soulRecoveryCodeIntegrityConflict = "soul_recovery.integrity_conflict"
	soulRecoveryCodeResponseTooLarge  = "soul_recovery.response_too_large"
	soulRecoveryCodeRateLimited       = "soul_recovery.rate_limited"
	soulRecoveryCodeInternal          = "soul_recovery.internal"
)

type soulRecoveryVersion struct {
	VersionNumber              int       `json:"version_number"`
	RegistrationURI            string    `json:"registration_uri"`
	RegistrationSHA256         string    `json:"registration_sha256"`
	PreviousRegistrationSHA256 string    `json:"previous_registration_sha256,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
	ChecksumVerified           bool      `json:"checksum_verified"`
}

type soulRecoverySource struct {
	RegistrationID string `json:"registration_id"`
	ConversationID string `json:"conversation_id"`
	ProducedAt     string `json:"produced_at,omitempty"`
}

type soulRecoveryProvenance struct {
	Source                   string `json:"source"`
	DigestSemantics          string `json:"digest_semantics"`
	HistoricalPublicationSHA bool   `json:"historical_publication_sha"`
}

type soulRecoveryAgentDetail struct {
	Version                string                      `json:"version"`
	AgentID                string                      `json:"agent_id"`
	Domain                 string                      `json:"domain"`
	LocalID                string                      `json:"local_id"`
	Status                 string                      `json:"status"`
	Classification         string                      `json:"classification"`
	SelfDescriptionVersion int                         `json:"self_description_version"`
	Source                 soulRecoverySource          `json:"source"`
	MigrationReadSHA256    string                      `json:"migration_read_sha256"`
	Provenance             soulRecoveryProvenance      `json:"provenance"`
	Versions               []soulRecoveryVersion       `json:"versions"`
	Declarations           json.RawMessage             `json:"declarations"`
	PublishedRegistration  *soulRecoveryPublishedState `json:"published_registration,omitempty"`
}

type soulRecoveryPublishedState struct {
	CurrentRegistrationSHA256 string `json:"current_registration_sha256"`
	CurrentChecksumVerified   bool   `json:"current_checksum_verified"`
}

type soulRecoveryAgentSummary struct {
	AgentID                string                `json:"agent_id"`
	Domain                 string                `json:"domain"`
	LocalID                string                `json:"local_id"`
	Status                 string                `json:"status"`
	Classification         string                `json:"classification"`
	SelfDescriptionVersion int                   `json:"self_description_version"`
	Source                 soulRecoverySource    `json:"source"`
	MigrationReadSHA256    string                `json:"migration_read_sha256"`
	Versions               []soulRecoveryVersion `json:"versions"`
}

type soulRecoveryAgentListResponse struct {
	Version    string                     `json:"version"`
	Agents     []soulRecoveryAgentSummary `json:"agents"`
	Count      int                        `json:"count"`
	Limit      int                        `json:"limit"`
	HasMore    bool                       `json:"has_more"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type soulRecoveryInventoryCursor struct {
	DomainIndex int    `json:"domain_index"`
	Inner       string `json:"inner,omitempty"`
}

type soulRecoveryArtifactState struct {
	Versions       []soulRecoveryVersion
	CurrentSHA256  string
	CurrentPresent bool
}

type soulRecoveryInventoryScan struct {
	domainIndex int
	inner       string
	pageCount   int
	out         []soulRecoveryAgentSummary
	seen        map[string]struct{}
}

func (s *Server) handleSoulInstanceRecoveryAgent(ctx *apptheory.Context) (*apptheory.Response, error) {
	started := time.Now()
	key, appErr := s.requireSoulRecoveryKey(ctx)
	if appErr != nil {
		s.logSoulRecoveryAccess(ctx, nil, ctxParam(ctx, "agentId"), soulRecoveryRouteDetail, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}
	agentID, _, parseErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if parseErr != nil {
		appErr = soulRecoveryError(soulRecoveryCodeInvalidRequest, "agentId is invalid", http.StatusBadRequest, map[string]any{"field": "agentId"})
		s.logSoulRecoveryAccess(ctx, key, ctxParam(ctx, "agentId"), soulRecoveryRouteDetail, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}

	detail, appErr := s.loadSoulRecoveryAgent(ctx.Context(), key.InstanceSlug, agentID)
	if appErr != nil {
		s.recordSoulRecoveryAudit(ctx, key, agentID, soulRecoveryRouteDetail, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}
	resp, err := soulMintInstanceReadJSON(http.StatusOK, detail, soulRecoveryMaxResponseSize)
	if err != nil {
		appErr = soulRecoveryResponseError(err)
		s.recordSoulRecoveryAudit(ctx, key, agentID, soulRecoveryRouteDetail, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}
	s.recordSoulRecoveryAudit(ctx, key, agentID, soulRecoveryRouteDetail, "success", resp.Status, len(resp.Body), started)
	return resp, nil
}

func (s *Server) handleSoulInstanceRecoveryAgents(ctx *apptheory.Context) (*apptheory.Response, error) {
	started := time.Now()
	key, appErr := s.requireSoulRecoveryKey(ctx)
	if appErr != nil {
		s.logSoulRecoveryAccess(ctx, nil, "", soulRecoveryRouteInventory, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}
	limit, appErr := parseSoulRecoveryLimit(ctx)
	if appErr != nil {
		s.recordSoulRecoveryAudit(ctx, key, "", soulRecoveryRouteInventory, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}
	cursor, appErr := decodeSoulRecoveryInventoryCursor(queryFirst(ctx, "cursor"))
	if appErr != nil {
		s.recordSoulRecoveryAudit(ctx, key, "", soulRecoveryRouteInventory, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}

	agents, hasMore, nextCursor, appErr := s.listSoulRecoveryAgents(ctx, key.InstanceSlug, cursor, limit)
	if appErr != nil {
		s.recordSoulRecoveryAudit(ctx, key, "", soulRecoveryRouteInventory, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}
	resp, err := soulMintInstanceReadJSON(http.StatusOK, soulRecoveryAgentListResponse{
		Version: "1", Agents: agents, Count: len(agents), Limit: limit, HasMore: hasMore, NextCursor: nextCursor,
	}, soulMintInstanceReadListMaxBytes)
	if err != nil {
		appErr = soulRecoveryResponseError(err)
		s.recordSoulRecoveryAudit(ctx, key, "", soulRecoveryRouteInventory, "error", appErr.StatusCode, 0, started)
		return nil, appErr
	}
	s.recordSoulRecoveryAudit(ctx, key, "", soulRecoveryRouteInventory, "success", resp.Status, len(resp.Body), started)
	return resp, nil
}

func (s *Server) requireSoulRecoveryKey(ctx *apptheory.Context) (*models.InstanceKey, *apptheory.AppTheoryError) {
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, soulRecoveryError(soulRecoveryCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if ctx == nil {
		return nil, soulRecoveryError(soulRecoveryCodeInvalidRequest, "invalid request", http.StatusBadRequest, nil)
	}
	if len(bytes.TrimSpace(ctx.Request.Body)) > 0 {
		return nil, soulRecoveryError(soulRecoveryCodeInvalidRequest, "request body is not allowed for this read", http.StatusBadRequest, map[string]any{"field": "body"})
	}
	if !s.cfg.SoulEnabled {
		return nil, soulRecoveryError(soulRecoveryCodeNotFound, "not found", http.StatusNotFound, nil)
	}
	key, appErr := s.requireSoulMintInstanceReadKey(ctx)
	if appErr == nil {
		return key, nil
	}
	if appErr.Code == soulMintInstanceReadCodeUnauthorized {
		return nil, soulRecoveryError(soulRecoveryCodeUnauthorized, "unauthorized", http.StatusUnauthorized, nil)
	}
	return nil, soulRecoveryError(soulRecoveryCodeInternal, "internal error", http.StatusInternalServerError, nil)
}

func (s *Server) loadSoulRecoveryAgent(ctx context.Context, instanceSlug string, agentID string) (*soulRecoveryAgentDetail, *apptheory.AppTheoryError) {
	identity, effectiveStatus, appErr := s.loadSoulRecoveryIdentity(ctx, instanceSlug, agentID)
	if appErr != nil {
		return nil, appErr
	}
	return s.loadSoulRecoveryAgentFromIdentity(ctx, instanceSlug, agentID, identity, effectiveStatus)
}

func (s *Server) loadSoulRecoveryAgentFromIdentity(ctx context.Context, instanceSlug string, agentID string, identity *models.SoulAgentIdentity, effectiveStatus string) (*soulRecoveryAgentDetail, *apptheory.AppTheoryError) {
	promotion, appErr := s.loadSoulRecoveryPromotion(ctx, agentID)
	if appErr != nil {
		return nil, appErr
	}
	session, rawDeclarations, producedAt, appErr := s.loadSoulRecoveryDeclarationSource(ctx, instanceSlug, agentID, identity, promotion)
	if appErr != nil {
		return nil, appErr
	}
	artifacts, appErr := s.loadSoulRecoveryArtifacts(ctx, identity)
	if appErr != nil {
		return nil, appErr
	}
	classification, appErr := classifySoulRecoveryArtifacts(identity.SelfDescriptionVersion, artifacts)
	if appErr != nil {
		return nil, appErr
	}
	if !hostedGenesisPromotionConfirmsPublication(session, identity, promotion) {
		return nil, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "promotion binding is invalid", http.StatusConflict, nil)
	}
	return buildSoulRecoveryAgentDetail(identity, session, rawDeclarations, producedAt, effectiveStatus, classification, artifacts), nil
}

func (s *Server) loadSoulRecoveryIdentity(ctx context.Context, instanceSlug string, agentID string) (*models.SoulAgentIdentity, string, *apptheory.AppTheoryError) {
	identity, effectiveStatus, appErr := s.loadSoulRecoveryIdentityRecord(ctx, instanceSlug, agentID)
	if appErr != nil {
		return nil, "", appErr
	}
	if effectiveStatus != models.SoulAgentStatusActive {
		return nil, "", soulRecoveryError(soulRecoveryCodeIntegrityConflict, "agent is not active recovery state", http.StatusConflict, nil)
	}
	return identity, effectiveStatus, nil
}

func (s *Server) loadSoulRecoveryIdentityRecord(ctx context.Context, instanceSlug string, agentID string) (*models.SoulAgentIdentity, string, *apptheory.AppTheoryError) {
	identity, err := s.getSoulAgentIdentity(ctx, agentID)
	if theoryErrors.IsNotFound(err) {
		return nil, "", soulRecoveryError(soulRecoveryCodeNotFound, "agent not found", http.StatusNotFound, nil)
	}
	if err != nil || identity == nil {
		return nil, "", soulRecoveryError(soulRecoveryCodeInternal, "failed to load agent identity", http.StatusInternalServerError, nil)
	}
	if !strings.EqualFold(strings.TrimSpace(identity.AgentID), strings.TrimSpace(agentID)) {
		return nil, "", soulRecoveryError(soulRecoveryCodeIntegrityConflict, "agent identity binding is invalid", http.StatusConflict, nil)
	}
	if accessErr := s.requireSoulAgentInstanceAccess(ctx, instanceSlug, identity); accessErr != nil {
		return nil, "", soulRecoveryAccessError(accessErr)
	}
	effectiveStatus := strings.ToLower(strings.TrimSpace(identity.LifecycleStatus))
	if effectiveStatus == "" {
		effectiveStatus = strings.ToLower(strings.TrimSpace(identity.Status))
	}
	return identity, effectiveStatus, nil
}

func (s *Server) loadSoulRecoveryPromotion(ctx context.Context, agentID string) (*models.SoulAgentPromotion, *apptheory.AppTheoryError) {
	promotion, err := s.getSoulAgentPromotion(ctx, agentID)
	if theoryErrors.IsNotFound(err) {
		return nil, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "graduated promotion evidence is missing", http.StatusConflict, nil)
	}
	if err != nil {
		return nil, soulRecoveryError(soulRecoveryCodeInternal, "failed to load promotion evidence", http.StatusInternalServerError, nil)
	}
	if promotion == nil {
		return nil, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "graduated promotion evidence is missing", http.StatusConflict, nil)
	}
	return promotion, nil
}

func (s *Server) loadSoulRecoveryDeclarationSource(ctx context.Context, instanceSlug string, agentID string, identity *models.SoulAgentIdentity, promotion *models.SoulAgentPromotion) (*models.HostedGenesisSession, string, time.Time, *apptheory.AppTheoryError) {
	sessions, err := s.store.ListHostedGenesisSessionsByAgent(ctx, instanceSlug, agentID)
	if err != nil {
		return nil, "", time.Time{}, soulRecoveryError(soulRecoveryCodeInternal, "failed to load hosted genesis sources", http.StatusInternalServerError, nil)
	}
	if len(sessions) > soulRecoveryMaxSessions {
		return nil, "", time.Time{}, soulRecoveryError(soulRecoveryCodeResponseTooLarge, "too many hosted genesis sources", http.StatusRequestEntityTooLarge, nil)
	}
	session, appErr := selectSoulRecoverySession(instanceSlug, agentID, identity, promotion, sessions)
	if appErr != nil {
		return nil, "", time.Time{}, appErr
	}

	conversation, err := s.store.GetSoulAgentMintConversation(ctx, agentID, session.ConversationID)
	if theoryErrors.IsNotFound(err) {
		return nil, "", time.Time{}, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "declaration source is missing", http.StatusConflict, nil)
	}
	if err != nil {
		return nil, "", time.Time{}, soulRecoveryError(soulRecoveryCodeInternal, "failed to load declaration source", http.StatusInternalServerError, nil)
	}
	if conversation == nil {
		return nil, "", time.Time{}, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "declaration source is missing", http.StatusConflict, nil)
	}
	if !strings.EqualFold(strings.TrimSpace(conversation.AgentID), agentID) || strings.TrimSpace(conversation.ConversationID) != strings.TrimSpace(session.ConversationID) {
		return nil, "", time.Time{}, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "declaration source binding is invalid", http.StatusConflict, nil)
	}
	rawDeclarations := strings.TrimSpace(models.DecodeSoulMintConversationBlob(conversation.ProducedDeclarations))
	if _, parseErr := parseAndValidateMintConversationDeclarations(rawDeclarations); parseErr != nil {
		return nil, "", time.Time{}, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "declaration source is invalid", http.StatusConflict, nil)
	}
	producedAt := conversation.CompletedAt
	if producedAt.IsZero() {
		producedAt = firstTime(conversation.UpdatedAt, conversation.CreatedAt)
	}
	if producedAt.IsZero() {
		return nil, "", time.Time{}, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "declaration source timestamp is missing", http.StatusConflict, nil)
	}
	return session, rawDeclarations, producedAt, nil
}

func buildSoulRecoveryAgentDetail(identity *models.SoulAgentIdentity, session *models.HostedGenesisSession, rawDeclarations string, producedAt time.Time, effectiveStatus string, classification string, artifacts soulRecoveryArtifactState) *soulRecoveryAgentDetail {
	declarationJSON, err := json.Marshal(json.RawMessage(rawDeclarations))
	if err != nil {
		// The declaration source has already passed strict JSON validation. Keep
		// this helper total without reinterpreting the stored value.
		declarationJSON = []byte(rawDeclarations)
	}
	digest := sha256.Sum256(declarationJSON)
	detail := &soulRecoveryAgentDetail{
		Version:                "1",
		AgentID:                strings.ToLower(strings.TrimSpace(identity.AgentID)),
		Domain:                 strings.ToLower(strings.TrimSpace(identity.Domain)),
		LocalID:                strings.TrimSpace(identity.LocalID),
		Status:                 effectiveStatus,
		Classification:         classification,
		SelfDescriptionVersion: identity.SelfDescriptionVersion,
		Source: soulRecoverySource{
			RegistrationID: strings.TrimSpace(session.RegistrationID),
			ConversationID: strings.TrimSpace(session.ConversationID),
			ProducedAt:     producedAt.UTC().Format(time.RFC3339Nano),
		},
		MigrationReadSHA256: "sha256:" + hex.EncodeToString(digest[:]),
		Provenance: soulRecoveryProvenance{
			Source:                   "hosted_genesis_exact_declarations",
			DigestSemantics:          "migration_read_sha256",
			HistoricalPublicationSHA: false,
		},
		Versions:     artifacts.Versions,
		Declarations: json.RawMessage(declarationJSON),
	}
	if classification == soulRecoveryClassificationPublished {
		detail.PublishedRegistration = &soulRecoveryPublishedState{
			CurrentRegistrationSHA256: artifacts.CurrentSHA256,
			CurrentChecksumVerified:   true,
		}
	}
	return detail
}

func selectSoulRecoverySession(instanceSlug string, agentID string, identity *models.SoulAgentIdentity, promotion *models.SoulAgentPromotion, sessions []*models.HostedGenesisSession) (*models.HostedGenesisSession, *apptheory.AppTheoryError) {
	if appErr := validateSoulRecoveryPromotionIdentity(identity, promotion); appErr != nil {
		return nil, appErr
	}
	var selected *models.HostedGenesisSession
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if !soulRecoverySessionWithinBoundary(session, instanceSlug, agentID) {
			return nil, soulRecoveryError(soulRecoveryCodeBoundaryViolation, "hosted genesis source is outside the authenticated instance boundary", http.StatusForbidden, nil)
		}
		if !soulRecoverySessionMatchesPromotion(session, promotion) {
			continue
		}
		if !soulRecoverySessionStatusAllowed(session.Status) {
			return nil, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "hosted genesis source status is invalid", http.StatusConflict, nil)
		}
		if selected != nil {
			return nil, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "hosted genesis source is ambiguous", http.StatusConflict, nil)
		}
		selected = session
	}
	if selected == nil {
		return nil, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "hosted genesis source is missing", http.StatusConflict, nil)
	}
	return selected, nil
}

func validateSoulRecoveryPromotionIdentity(identity *models.SoulAgentIdentity, promotion *models.SoulAgentPromotion) *apptheory.AppTheoryError {
	if identity == nil || promotion == nil || strings.TrimSpace(promotion.RegistrationID) == "" || strings.TrimSpace(promotion.LatestConversationID) == "" {
		return soulRecoveryError(soulRecoveryCodeIntegrityConflict, "promotion binding is incomplete", http.StatusConflict, nil)
	}
	if !strings.EqualFold(strings.TrimSpace(promotion.Domain), strings.TrimSpace(identity.Domain)) ||
		!strings.EqualFold(strings.TrimSpace(promotion.LocalID), strings.TrimSpace(identity.LocalID)) {
		return soulRecoveryError(soulRecoveryCodeIntegrityConflict, "promotion identity binding is invalid", http.StatusConflict, nil)
	}
	return nil
}

func soulRecoverySessionWithinBoundary(session *models.HostedGenesisSession, instanceSlug string, agentID string) bool {
	return session != nil && strings.EqualFold(strings.TrimSpace(session.InstanceSlug), strings.TrimSpace(instanceSlug)) &&
		strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(agentID))
}

func soulRecoverySessionMatchesPromotion(session *models.HostedGenesisSession, promotion *models.SoulAgentPromotion) bool {
	return session != nil && promotion != nil && strings.TrimSpace(session.RegistrationID) == strings.TrimSpace(promotion.RegistrationID) &&
		strings.TrimSpace(session.ConversationID) == strings.TrimSpace(promotion.LatestConversationID)
}

func soulRecoverySessionStatusAllowed(raw string) bool {
	status := strings.ToLower(strings.TrimSpace(raw))
	return status == models.SoulMintConversationStatusCompleted || status == models.SoulMintConversationStatusDeclarationReady || status == models.SoulMintConversationStatusPublished
}

func (s *Server) loadSoulRecoveryArtifacts(ctx context.Context, identity *models.SoulAgentIdentity) (soulRecoveryArtifactState, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || s.soulPacks == nil || identity == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return soulRecoveryArtifactState{}, soulRecoveryError(soulRecoveryCodeInternal, "recovery artifact store is not configured", http.StatusInternalServerError, nil)
	}
	agentID := strings.ToLower(strings.TrimSpace(identity.AgentID))
	if identity.SelfDescriptionVersion > soulRecoveryMaxVersions {
		return soulRecoveryArtifactState{}, soulRecoveryError(soulRecoveryCodeResponseTooLarge, "registration version history is too large", http.StatusRequestEntityTooLarge, nil)
	}
	state := soulRecoveryArtifactState{Versions: make([]soulRecoveryVersion, 0, max(identity.SelfDescriptionVersion, 0))}
	missingVersions := 0
	for version := 1; version <= identity.SelfDescriptionVersion; version++ {
		view, present, appErr := s.loadSoulRecoveryVersionArtifact(ctx, agentID, version)
		if appErr != nil {
			return soulRecoveryArtifactState{}, appErr
		}
		if !present {
			missingVersions++
			continue
		}
		state.Versions = append(state.Versions, view)
	}

	currentBody, currentPresent, appErr := s.getSoulRecoveryObject(ctx, soulRegistrationS3Key(agentID))
	if appErr != nil {
		return soulRecoveryArtifactState{}, appErr
	}
	state.CurrentPresent = currentPresent
	if currentPresent {
		sum := sha256.Sum256(currentBody)
		state.CurrentSHA256 = hex.EncodeToString(sum[:])
	}
	if missingVersions > 0 && missingVersions != identity.SelfDescriptionVersion {
		return soulRecoveryArtifactState{}, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration version history is incomplete", http.StatusConflict, nil)
	}
	return state, nil
}

func (s *Server) loadSoulRecoveryVersionArtifact(ctx context.Context, agentID string, version int) (soulRecoveryVersion, bool, *apptheory.AppTheoryError) {
	record, err := s.getSoulAgentVersionRecord(ctx, agentID, version)
	if theoryErrors.IsNotFound(err) {
		_, objectPresent, appErr := s.getSoulRecoveryObject(ctx, soulRegistrationVersionedS3Key(agentID, version))
		if appErr != nil {
			return soulRecoveryVersion{}, false, appErr
		}
		if objectPresent {
			return soulRecoveryVersion{}, false, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration version object has no bound version row", http.StatusConflict, nil)
		}
		return soulRecoveryVersion{}, false, nil
	}
	if err != nil || record == nil {
		return soulRecoveryVersion{}, false, soulRecoveryError(soulRecoveryCodeInternal, "failed to load registration version evidence", http.StatusInternalServerError, nil)
	}
	if !strings.EqualFold(record.AgentID, agentID) || record.VersionNumber != version || record.CreatedAt.IsZero() {
		return soulRecoveryVersion{}, false, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration version binding is invalid", http.StatusConflict, nil)
	}
	expectedKey := soulRegistrationVersionedS3Key(agentID, version)
	expectedURI := fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), expectedKey)
	if strings.TrimSpace(record.RegistrationURI) != expectedURI {
		return soulRecoveryVersion{}, false, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration version URI is invalid", http.StatusConflict, nil)
	}
	body, present, appErr := s.getSoulRecoveryObject(ctx, expectedKey)
	if appErr != nil {
		return soulRecoveryVersion{}, false, appErr
	}
	if !present || !soulRecoverySHA256Matches(body, record.RegistrationSHA256) {
		return soulRecoveryVersion{}, false, soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration version checksum is invalid", http.StatusConflict, nil)
	}
	return soulRecoveryVersion{
		VersionNumber:              version,
		RegistrationURI:            expectedURI,
		RegistrationSHA256:         strings.ToLower(strings.TrimSpace(record.RegistrationSHA256)),
		PreviousRegistrationSHA256: strings.ToLower(strings.TrimSpace(record.PreviousRegistrationSHA256)),
		CreatedAt:                  record.CreatedAt.UTC(),
		ChecksumVerified:           true,
	}, true, nil
}

func classifySoulRecoveryArtifacts(identityVersion int, state soulRecoveryArtifactState) (string, *apptheory.AppTheoryError) {
	if identityVersion <= 0 {
		return "", soulRecoveryError(soulRecoveryCodeIntegrityConflict, "identity has no published version claim", http.StatusConflict, nil)
	}
	if len(state.Versions) == 0 && !state.CurrentPresent {
		return soulRecoveryClassificationLegacy, nil
	}
	if len(state.Versions) != identityVersion || !state.CurrentPresent {
		return "", soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration artifact history is incomplete", http.StatusConflict, nil)
	}
	for i := range state.Versions {
		version := state.Versions[i]
		if version.VersionNumber != i+1 || !version.ChecksumVerified {
			return "", soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration version history is invalid", http.StatusConflict, nil)
		}
		if i == 0 {
			if strings.TrimSpace(version.PreviousRegistrationSHA256) != "" {
				return "", soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration version chain is invalid", http.StatusConflict, nil)
			}
			continue
		}
		if !strings.EqualFold(version.PreviousRegistrationSHA256, state.Versions[i-1].RegistrationSHA256) {
			return "", soulRecoveryError(soulRecoveryCodeIntegrityConflict, "registration version chain is invalid", http.StatusConflict, nil)
		}
	}
	latest := state.Versions[len(state.Versions)-1]
	if !strings.EqualFold(state.CurrentSHA256, latest.RegistrationSHA256) {
		return "", soulRecoveryError(soulRecoveryCodeIntegrityConflict, "current registration checksum is invalid", http.StatusConflict, nil)
	}
	return soulRecoveryClassificationPublished, nil
}

func (s *Server) getSoulRecoveryObject(ctx context.Context, key string) ([]byte, bool, *apptheory.AppTheoryError) {
	body, _, _, err := s.soulPacks.GetObject(ctx, key, soulRecoveryMaxArtifactSize)
	if err == nil {
		return body, true, nil
	}
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return nil, false, nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "too large") {
		return nil, false, soulRecoveryError(soulRecoveryCodeResponseTooLarge, "registration artifact is too large", http.StatusRequestEntityTooLarge, nil)
	}
	return nil, false, soulRecoveryError(soulRecoveryCodeInternal, "failed to read registration artifact", http.StatusInternalServerError, nil)
}

func soulRecoverySHA256Matches(body []byte, expected string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if len(expected) != sha256.Size*2 {
		return false
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]) == expected
}

func (s *Server) listSoulRecoveryAgents(ctx *apptheory.Context, instanceSlug string, cursor soulRecoveryInventoryCursor, limit int) ([]soulRecoveryAgentSummary, bool, string, *apptheory.AppTheoryError) {
	domains, appErr := s.loadSoulRecoveryDomains(ctx, instanceSlug)
	if appErr != nil {
		return nil, false, "", appErr
	}
	if cursor.DomainIndex < 0 || cursor.DomainIndex > len(domains) {
		return nil, false, "", soulRecoveryError(soulRecoveryCodeInvalidRequest, "cursor is invalid", http.StatusBadRequest, map[string]any{"field": "cursor"})
	}
	scan := &soulRecoveryInventoryScan{
		domainIndex: cursor.DomainIndex,
		inner:       cursor.Inner,
		out:         make([]soulRecoveryAgentSummary, 0, limit),
		seen:        map[string]struct{}{},
	}
	for {
		done, hasMore, nextCursor, scanErr := s.scanSoulRecoveryInventoryPage(ctx, instanceSlug, domains, limit, scan)
		if scanErr != nil {
			return nil, false, "", scanErr
		}
		if done {
			return scan.out, hasMore, nextCursor, nil
		}
	}
}

func (s *Server) loadSoulRecoveryDomains(ctx *apptheory.Context, instanceSlug string) ([]string, *apptheory.AppTheoryError) {
	instance, err := s.store.GetInstance(ctx.Context(), instanceSlug)
	if theoryErrors.IsNotFound(err) {
		return nil, soulRecoveryError(soulRecoveryCodeUnauthorized, "unauthorized", http.StatusUnauthorized, nil)
	}
	if err != nil {
		return nil, soulRecoveryError(soulRecoveryCodeInternal, "failed to load instance", http.StatusInternalServerError, nil)
	}
	if instance == nil || !strings.EqualFold(instance.Slug, instanceSlug) {
		return nil, soulRecoveryError(soulRecoveryCodeUnauthorized, "unauthorized", http.StatusUnauthorized, nil)
	}
	domainOwners, appErr := s.listSoulRosterDomainOwners(ctx.Context(), []*models.Instance{instance})
	if appErr != nil {
		return nil, soulRecoveryError(soulRecoveryCodeInternal, "failed to load instance domains", http.StatusInternalServerError, nil)
	}
	domains := make([]string, 0, len(domainOwners))
	for domain := range domainOwners {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains, nil
}

func (s *Server) scanSoulRecoveryInventoryPage(ctx *apptheory.Context, instanceSlug string, domains []string, limit int, scan *soulRecoveryInventoryScan) (bool, bool, string, *apptheory.AppTheoryError) {
	if scan.domainIndex >= len(domains) {
		return true, false, "", nil
	}
	if scan.pageCount >= soulRecoveryMaxScanPages {
		return true, true, encodeSoulRecoveryInventoryCursor(soulRecoveryInventoryCursor{DomainIndex: scan.domainIndex, Inner: scan.inner}), nil
	}
	scan.pageCount++
	items, pageHasMore, next, appErr := s.querySoulRecoveryDomainAgents(ctx.Context(), domains[scan.domainIndex], scan.inner, limit-len(scan.out))
	if appErr != nil {
		return true, false, "", appErr
	}
	if appErr := s.appendSoulRecoveryInventoryItems(ctx, instanceSlug, domains[scan.domainIndex], items, scan); appErr != nil {
		return true, false, "", appErr
	}
	if pageHasMore && strings.TrimSpace(next) != "" {
		scan.inner = strings.TrimSpace(next)
	} else {
		scan.domainIndex++
		scan.inner = ""
	}
	if len(scan.out) < limit {
		return false, false, "", nil
	}
	if scan.domainIndex < len(domains) {
		return true, true, encodeSoulRecoveryInventoryCursor(soulRecoveryInventoryCursor{DomainIndex: scan.domainIndex, Inner: scan.inner}), nil
	}
	return true, false, "", nil
}

func (s *Server) appendSoulRecoveryInventoryItems(ctx *apptheory.Context, instanceSlug string, domain string, items []*models.SoulDomainAgentIndex, scan *soulRecoveryInventoryScan) *apptheory.AppTheoryError {
	for _, item := range items {
		if item == nil || !strings.EqualFold(item.Domain, domain) {
			return soulRecoveryError(soulRecoveryCodeIntegrityConflict, "domain index binding is invalid", http.StatusConflict, nil)
		}
		agentID := strings.ToLower(strings.TrimSpace(item.AgentID))
		if agentID == "" {
			return soulRecoveryError(soulRecoveryCodeIntegrityConflict, "domain index agent binding is invalid", http.StatusConflict, nil)
		}
		identity, effectiveStatus, appErr := s.loadSoulRecoveryIdentityRecord(ctx.Context(), instanceSlug, agentID)
		if appErr != nil {
			if appErr.Code == soulRecoveryCodeNotFound {
				continue
			}
			return appErr
		}
		if !soulRecoveryInventoryIndexMatchesIdentity(item, identity) {
			return soulRecoveryError(soulRecoveryCodeIntegrityConflict, "domain index identity binding is invalid", http.StatusConflict, nil)
		}
		if _, ok := scan.seen[agentID]; ok {
			continue
		}
		if effectiveStatus != models.SoulAgentStatusActive {
			continue
		}
		detail, appErr := s.loadSoulRecoveryAgentFromIdentity(ctx.Context(), instanceSlug, agentID, identity, effectiveStatus)
		if appErr != nil {
			return appErr
		}
		scan.seen[agentID] = struct{}{}
		scan.out = append(scan.out, soulRecoverySummaryFromDetail(detail))
	}
	return nil
}

func soulRecoveryInventoryIndexMatchesIdentity(item *models.SoulDomainAgentIndex, identity *models.SoulAgentIdentity) bool {
	if item == nil || identity == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(item.AgentID), strings.TrimSpace(identity.AgentID)) &&
		strings.EqualFold(strings.TrimSpace(item.Domain), strings.TrimSpace(identity.Domain)) &&
		strings.EqualFold(strings.TrimSpace(item.LocalID), strings.TrimSpace(identity.LocalID))
}

func (s *Server) querySoulRecoveryDomainAgents(ctx context.Context, domain string, cursor string, limit int) ([]*models.SoulDomainAgentIndex, bool, string, *apptheory.AppTheoryError) {
	if limit <= 0 {
		limit = soulRecoveryDefaultLimit
	}
	var items []*models.SoulDomainAgentIndex
	query := s.store.DB.WithContext(ctx).
		Model(&models.SoulDomainAgentIndex{}).
		Where("PK", "=", fmt.Sprintf("SOUL#DOMAIN#%s", strings.ToLower(strings.TrimSpace(domain)))).
		OrderBy("SK", "ASC").
		Limit(limit)
	if strings.TrimSpace(cursor) != "" {
		query = query.Cursor(strings.TrimSpace(cursor))
	}
	paged, err := query.AllPaginated(&items)
	if err != nil {
		return nil, false, "", soulRecoveryError(soulRecoveryCodeInternal, "failed to list recovery agents", http.StatusInternalServerError, nil)
	}
	if paged == nil {
		return items, false, "", nil
	}
	return items, paged.HasMore, strings.TrimSpace(paged.NextCursor), nil
}

func soulRecoverySummaryFromDetail(detail *soulRecoveryAgentDetail) soulRecoveryAgentSummary {
	if detail == nil {
		return soulRecoveryAgentSummary{}
	}
	return soulRecoveryAgentSummary{
		AgentID:                detail.AgentID,
		Domain:                 detail.Domain,
		LocalID:                detail.LocalID,
		Status:                 detail.Status,
		Classification:         detail.Classification,
		SelfDescriptionVersion: detail.SelfDescriptionVersion,
		Source:                 detail.Source,
		MigrationReadSHA256:    detail.MigrationReadSHA256,
		Versions:               detail.Versions,
	}
}

func parseSoulRecoveryLimit(ctx *apptheory.Context) (int, *apptheory.AppTheoryError) {
	raw := strings.TrimSpace(queryFirst(ctx, "limit"))
	if raw == "" {
		return soulRecoveryDefaultLimit, nil
	}
	limit := envIntPositiveClampedFromString(raw, 0, soulRecoveryMaxLimit)
	if limit <= 0 {
		return 0, soulRecoveryError(soulRecoveryCodeInvalidRequest, "limit is invalid", http.StatusBadRequest, map[string]any{"field": "limit"})
	}
	return limit, nil
}

func encodeSoulRecoveryInventoryCursor(cursor soulRecoveryInventoryCursor) string {
	body, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeSoulRecoveryInventoryCursor(raw string) (soulRecoveryInventoryCursor, *apptheory.AppTheoryError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return soulRecoveryInventoryCursor{}, nil
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return soulRecoveryInventoryCursor{}, soulRecoveryError(soulRecoveryCodeInvalidRequest, "cursor is invalid", http.StatusBadRequest, map[string]any{"field": "cursor"})
	}
	var cursor soulRecoveryInventoryCursor
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.DomainIndex < 0 {
		return soulRecoveryInventoryCursor{}, soulRecoveryError(soulRecoveryCodeInvalidRequest, "cursor is invalid", http.StatusBadRequest, map[string]any{"field": "cursor"})
	}
	return cursor, nil
}

func soulRecoveryAccessError(appErr *apptheory.AppTheoryError) *apptheory.AppTheoryError {
	if appErr == nil {
		return nil
	}
	if appErr.Code == soulMintAppErrCodeInternal {
		return soulRecoveryError(soulRecoveryCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	return soulRecoveryError(soulRecoveryCodeBoundaryViolation, "agent is outside the authenticated instance boundary", http.StatusForbidden, nil)
}

func soulRecoveryError(code string, message string, status int, details map[string]any) *apptheory.AppTheoryError {
	err := apptheory.NewAppTheoryError(code, message).WithStatusCode(status)
	if len(details) > 0 {
		err = err.WithDetails(details)
	}
	return err
}

func soulRecoveryResponseError(err error) *apptheory.AppTheoryError {
	if appErr, ok := err.(*apptheory.AppTheoryError); ok && appErr != nil {
		if appErr.Code == soulMintInstanceReadCodeResponseTooLarge {
			return soulRecoveryError(soulRecoveryCodeResponseTooLarge, "recovery response is too large", http.StatusRequestEntityTooLarge, nil)
		}
		return appErr
	}
	return soulRecoveryError(soulRecoveryCodeInternal, "internal error", http.StatusInternalServerError, nil)
}

func (s *Server) recordSoulRecoveryAudit(ctx *apptheory.Context, key *models.InstanceKey, agentID string, routeClass string, outcome string, status int, responseBytes int, started time.Time) {
	s.logSoulRecoveryAccess(ctx, key, agentID, routeClass, outcome, status, responseBytes, started)
	if s == nil || key == nil || ctx == nil {
		return
	}
	target := fmt.Sprintf("soul_recovery:%s:instance=%s:agent=%s:status=%d:bytes=%d",
		strings.TrimSpace(routeClass), soulMintInstanceReadAuditHash(key.InstanceSlug), soulMintInstanceReadAuditHash(agentID), status, responseBytes)
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:  "instance_key:" + soulMintInstanceReadAuditHash(key.ID),
		Action: "soul.recovery.instance_read." + strings.TrimSpace(routeClass) + "." + strings.TrimSpace(outcome),
		Target: target,
	})
}

func (s *Server) logSoulRecoveryAccess(ctx *apptheory.Context, key *models.InstanceKey, agentID string, routeClass string, outcome string, status int, responseBytes int, started time.Time) {
	requestID := ""
	instanceSlug := ""
	instanceKeyID := ""
	if ctx != nil {
		requestID = strings.TrimSpace(ctx.RequestID)
		instanceKeyID = sha256HexTrimmed(httpx.BearerToken(ctx.Request.Headers))
	}
	if key != nil {
		instanceSlug = key.InstanceSlug
		instanceKeyID = key.ID
	}
	durationMS := int64(0)
	if !started.IsZero() {
		durationMS = time.Since(started).Milliseconds()
	}
	log.Printf("controlplane: soul_recovery auth_scheme=instance_key instance_slug_hash=%s instance_key_hash=%s agent_id_hash=%s route_class=%s outcome=%s status=%d request_id=%s duration_ms=%d response_bytes=%d",
		soulMintInstanceReadAuditHash(instanceSlug), soulMintInstanceReadAuditHash(instanceKeyID), soulMintInstanceReadAuditHash(agentID),
		strings.TrimSpace(routeClass), strings.TrimSpace(outcome), status, requestID, durationMS, responseBytes)
}
