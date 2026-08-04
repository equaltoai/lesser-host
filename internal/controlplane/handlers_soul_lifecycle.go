package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	"github.com/theory-cloud/tabletheory/v3"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// --- Request types ---

type soulArchiveRequest struct {
	Reason    string `json:"reason,omitempty"`
	Timestamp string `json:"timestamp"`
	Signature string `json:"signature"`
	Nonce     string `json:"continuity_nonce"`
}

type soulDesignateSuccessorRequest struct {
	SuccessorAgentID string `json:"successor_agent_id"`
	Reason           string `json:"reason,omitempty"`
	Timestamp        string `json:"timestamp"`
	PredecessorSig   string `json:"predecessor_signature"`
	SuccessorSig     string `json:"successor_signature"`
	Nonce            string `json:"continuity_nonce"`
}

type soulArchiveBeginResponse struct {
	Version         string               `json:"version"`
	Entry           soulContinuityToSign `json:"entry"`
	ContinuityNonce string               `json:"continuity_nonce"`
}

type soulDesignateSuccessorBeginResponse struct {
	Version          string               `json:"version"`
	PredecessorEntry soulContinuityToSign `json:"predecessor_entry"`
	SuccessorEntry   soulContinuityToSign `json:"successor_entry"`
	ContinuityNonce  string               `json:"continuity_nonce"`
}

type soulContinuityToSign struct {
	AgentID    string   `json:"agent_id"`
	Type       string   `json:"type"`
	Timestamp  string   `json:"timestamp"`
	Summary    string   `json:"summary"`
	References []string `json:"references,omitempty"`
	DigestHex  string   `json:"digest_hex"`
}

const (
	soulContinuitySummaryArchived           = "Archived"
	soulContinuitySummarySuccessionDeclared = "Succession declared"
	soulContinuitySummarySuccessionReceived = "Succession received"

	soulLifecycleChallengeTTL                       = 5 * time.Minute
	soulLifecycleChallengePurposeArchiveAgent       = "archive_agent"
	soulLifecycleChallengePurposeDesignateSuccessor = "designate_successor"
)

// --- Handlers ---

func (s *Server) handleSoulArchiveAgentBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if err != nil {
		return nil, newAppTheoryError("app.not_found", "agent not found")
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}

	// Only active or self_suspended agents can be archived.
	currentStatus := strings.TrimSpace(identity.Status)
	if currentStatus != models.SoulAgentStatusActive && currentStatus != models.SoulAgentStatusSelfSuspended {
		return nil, newAppTheoryError("app.conflict", "only active or self-suspended agents can be archived")
	}

	now := time.Now().UTC()
	timestamp := canonicalSoulSignedTimestamp(now)
	summary := soulContinuitySummaryArchived
	references := []string{fmt.Sprintf("agent:%s", agentIDHex)}
	continuityNonce, appErr := newSoulContinuityNonce()
	if appErr != nil {
		return nil, appErr
	}

	digest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeArchived, timestamp, summary, "", references, continuityNonce)
	if appErr != nil {
		return nil, appErr
	}

	challenge := newSoulLifecycleChallenge(agentIDHex, continuityNonce, soulLifecycleChallengePurposeArchiveAgent, "", now)
	if appErr := s.persistSoulLifecycleChallenge(ctx, challenge); appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulArchiveBeginResponse{
		Version:         "1",
		ContinuityNonce: continuityNonce,
		Entry: soulContinuityToSign{
			AgentID:    agentIDHex,
			Type:       models.SoulContinuityEntryTypeArchived,
			Timestamp:  timestamp,
			Summary:    summary,
			References: references,
			DigestHex:  "0x" + fmt.Sprintf("%x", digest),
		},
	})
}

// handleSoulArchiveAgent archives an agent, making it read-only.
// Only active or self_suspended agents can be archived. This is a one-way transition.
func (s *Server) handleSoulArchiveAgent(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	identity, appErr := s.loadSoulLifecycleIdentity(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if statusErr := validateSoulLifecycleMutableStatus(identity, "be archived"); statusErr != nil {
		return nil, statusErr
	}

	reason, parsedTS, timestampCanonical, sig, continuityNonce, appErr := parseSoulArchiveRequestBody(ctx)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	challenge, appErr := s.loadSoulLifecycleChallenge(ctx, agentIDHex, continuityNonce, soulLifecycleChallengePurposeArchiveAgent, "", now)
	if appErr != nil {
		return nil, appErr
	}

	// Verify archive continuity signature (EIP-191 over keccak256(JCS(unsignedEntry))).
	continuitySummary, continuityRefs := soulArchiveContinuityPayload(agentIDHex)
	contDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeArchived, timestampCanonical, continuitySummary, "", continuityRefs, continuityNonce)
	if appErr != nil {
		return nil, appErr
	}
	if sigErr := verifyEthereumSignatureBytesNonMalleable(identity.Wallet, contDigest, sig); sigErr != nil {
		return nil, newAppTheoryError("app.bad_request", "invalid continuity signature")
	}

	identity.Status = models.SoulAgentStatusArchived
	identity.LifecycleStatus = models.SoulAgentStatusArchived
	identity.LifecycleReason = reason
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	continuity := &models.SoulAgentContinuity{
		AgentID:      agentIDHex,
		Type:         models.SoulContinuityEntryTypeArchived,
		Summary:      continuitySummary,
		Recovery:     "",
		ReferencesV2: continuityRefs,
		Signature:    sig,
		Timestamp:    parsedTS.UTC(),
	}
	_ = continuity.UpdateKeys()

	// Audit log.
	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.archive",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Delete(challenge, tabletheory.IfExists(), tabletheory.Condition("TTL", ">", now.Unix()))
		tx.Update(identity, []string{"Status", "LifecycleStatus", "LifecycleReason", "UpdatedAt"}, tabletheory.IfExists())
		tx.Create(continuity)
		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, invalidSoulLifecycleChallengeError()
		}
		return nil, newAppTheoryError("app.internal", "failed to archive agent")
	}

	return apptheory.JSON(http.StatusOK, identity)
}

func (s *Server) handleSoulDesignateSuccessorBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	identity, appErr := s.loadSoulLifecycleIdentity(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if statusErr := validateSoulLifecycleMutableStatus(identity, "designate a successor"); statusErr != nil {
		return nil, statusErr
	}

	successorIDHex, appErr := parseSoulSuccessorAgentID(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if _, successorErr := s.loadSoulActiveSuccessorIdentity(ctx, identity, agentIDHex, successorIDHex); successorErr != nil {
		return nil, successorErr
	}

	continuityNonce, nonceErr := newSoulContinuityNonce()
	if nonceErr != nil {
		return nil, nonceErr
	}
	now := time.Now().UTC()
	beginResp, appErr := buildSoulDesignateSuccessorBeginResponse(agentIDHex, successorIDHex, canonicalSoulSignedTimestamp(now), continuityNonce)
	if appErr != nil {
		return nil, appErr
	}
	challenge := newSoulLifecycleChallenge(agentIDHex, continuityNonce, soulLifecycleChallengePurposeDesignateSuccessor, successorIDHex, now)
	if appErr := s.persistSoulLifecycleChallenge(ctx, challenge); appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, beginResp)
}

// handleSoulDesignateSuccessor designates a successor agent and transitions the
// current agent to "succeeded" status.
func (s *Server) handleSoulDesignateSuccessor(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	identity, appErr := s.loadSoulLifecycleIdentity(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if statusErr := validateSoulLifecycleMutableStatus(identity, "designate a successor"); statusErr != nil {
		return nil, statusErr
	}

	successorIDHex, reason, parsedTS, timestampCanonical, predSig, succSig, continuityNonce, appErr := parseSoulDesignateSuccessorRequestBody(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	challenge, appErr := s.loadSoulLifecycleChallenge(ctx, agentIDHex, continuityNonce, soulLifecycleChallengePurposeDesignateSuccessor, successorIDHex, now)
	if appErr != nil {
		return nil, appErr
	}

	successorIdentity, appErr := s.loadSoulActiveSuccessorIdentity(ctx, identity, agentIDHex, successorIDHex)
	if appErr != nil {
		return nil, appErr
	}

	declaredSummary, declaredRefs, receivedSummary, receivedRefs := soulSuccessionContinuityPayloads(agentIDHex, successorIDHex)
	if appErr := verifySoulDesignateSuccessorContinuitySignatures(identity.Wallet, successorIdentity.Wallet, timestampCanonical, continuityNonce, declaredSummary, declaredRefs, receivedSummary, receivedRefs, predSig, succSig); appErr != nil {
		return nil, appErr
	}

	identity.Status = models.SoulAgentStatusSucceeded
	identity.LifecycleStatus = models.SoulAgentStatusSucceeded
	identity.LifecycleReason = reason
	identity.SuccessorAgentID = successorIDHex
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	successorIdentity.PredecessorAgentID = agentIDHex
	successorIdentity.UpdatedAt = now
	_ = successorIdentity.UpdateKeys()

	predContinuity := &models.SoulAgentContinuity{
		AgentID:      agentIDHex,
		Type:         models.SoulContinuityEntryTypeSuccessionDeclared,
		Summary:      declaredSummary,
		ReferencesV2: declaredRefs,
		Signature:    predSig,
		Timestamp:    parsedTS.UTC(),
	}
	_ = predContinuity.UpdateKeys()

	succContinuity := &models.SoulAgentContinuity{
		AgentID:      successorIDHex,
		Type:         models.SoulContinuityEntryTypeSuccessionReceived,
		Summary:      receivedSummary,
		ReferencesV2: receivedRefs,
		Signature:    succSig,
		Timestamp:    parsedTS.UTC(),
	}
	_ = succContinuity.UpdateKeys()

	// Audit log.
	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.designate_successor",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Delete(challenge, tabletheory.IfExists(), tabletheory.Condition("TTL", ">", now.Unix()))
		tx.Update(identity, []string{"Status", "LifecycleStatus", "LifecycleReason", "SuccessorAgentID", "UpdatedAt"}, tabletheory.IfExists())
		tx.Update(successorIdentity, []string{"PredecessorAgentID", "UpdatedAt"}, tabletheory.IfExists())
		tx.Create(predContinuity)
		tx.Create(succContinuity)
		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, invalidSoulLifecycleChallengeError()
		}
		return nil, newAppTheoryError("app.internal", "failed to designate successor")
	}

	return apptheory.JSON(http.StatusOK, identity)
}

func parseAndValidateSoulContinuityTimestamp(tsRaw string) (time.Time, string, *apptheory.AppTheoryError) {
	return parseSoulSignedTimestamp(tsRaw, time.Now().UTC(), "timestamp")
}

func (s *Server) loadSoulLifecycleIdentity(ctx *apptheory.Context, agentIDHex string) (*models.SoulAgentIdentity, *apptheory.AppTheoryError) {
	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if err != nil {
		return nil, newAppTheoryError("app.not_found", "agent not found")
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}
	return identity, nil
}

func validateSoulLifecycleMutableStatus(identity *models.SoulAgentIdentity, action string) *apptheory.AppTheoryError {
	status := ""
	if identity != nil {
		status = strings.TrimSpace(identity.Status)
	}
	if status == models.SoulAgentStatusActive || status == models.SoulAgentStatusSelfSuspended {
		return nil
	}
	return newAppTheoryError("app.conflict", fmt.Sprintf("only active or self-suspended agents can %s", strings.TrimSpace(action)))
}

func verifySoulDesignateSuccessorContinuitySignatures(predecessorWallet string, successorWallet string, timestampCanonical string, continuityNonce string, declaredSummary string, declaredRefs []string, receivedSummary string, receivedRefs []string, predSig string, succSig string) *apptheory.AppTheoryError {
	declaredDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionDeclared, timestampCanonical, declaredSummary, "", declaredRefs, continuityNonce)
	if appErr != nil {
		return appErr
	}
	if sigErr := verifyEthereumSignatureBytesNonMalleable(predecessorWallet, declaredDigest, predSig); sigErr != nil {
		return newAppTheoryError("app.bad_request", "invalid predecessor continuity signature")
	}

	receivedDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionReceived, timestampCanonical, receivedSummary, "", receivedRefs, continuityNonce)
	if appErr != nil {
		return appErr
	}
	if sigErr := verifyEthereumSignatureBytesNonMalleable(successorWallet, receivedDigest, succSig); sigErr != nil {
		return newAppTheoryError("app.bad_request", "invalid successor continuity signature")
	}
	return nil
}

func newSoulLifecycleChallenge(agentIDHex string, nonce string, purpose string, successorIDHex string, now time.Time) *models.SoulLifecycleChallenge {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	challenge := &models.SoulLifecycleChallenge{
		AgentID:          agentIDHex,
		Nonce:            nonce,
		Purpose:          purpose,
		SuccessorAgentID: successorIDHex,
		IssuedAt:         now.UTC(),
		ExpiresAt:        now.UTC().Add(soulLifecycleChallengeTTL),
	}
	_ = challenge.UpdateKeys()
	return challenge
}

func (s *Server) persistSoulLifecycleChallenge(ctx *apptheory.Context, challenge *models.SoulLifecycleChallenge) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || challenge == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(challenge)
		return nil
	}); err != nil {
		return newAppTheoryError("app.internal", "failed to issue lifecycle challenge")
	}
	return nil
}

func (s *Server) loadSoulLifecycleChallenge(ctx *apptheory.Context, agentIDHex string, nonce string, purpose string, successorIDHex string, now time.Time) (*models.SoulLifecycleChallenge, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	nonce = strings.TrimSpace(nonce)
	if agentIDHex == "" || nonce == "" {
		return nil, invalidSoulLifecycleChallengeError()
	}

	key := &models.SoulLifecycleChallenge{
		AgentID: agentIDHex,
		Nonce:   nonce,
	}
	_ = key.UpdateKeys()

	var challenge models.SoulLifecycleChallenge
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulLifecycleChallenge{}).
		Where("PK", "=", key.PK).
		Where("SK", "=", key.SK).
		ConsistentRead().
		Limit(1).
		First(&challenge)
	if theoryErrors.IsNotFound(err) {
		return nil, invalidSoulLifecycleChallengeError()
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to load lifecycle challenge")
	}
	if appErr := validateSoulLifecycleChallenge(&challenge, agentIDHex, nonce, purpose, successorIDHex, now); appErr != nil {
		return nil, appErr
	}
	return &challenge, nil
}

func validateSoulLifecycleChallenge(challenge *models.SoulLifecycleChallenge, agentIDHex string, nonce string, purpose string, successorIDHex string, now time.Time) *apptheory.AppTheoryError {
	if challenge == nil {
		return invalidSoulLifecycleChallengeError()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	nonce = strings.TrimSpace(nonce)
	purpose = strings.ToLower(strings.TrimSpace(purpose))
	successorIDHex = strings.ToLower(strings.TrimSpace(successorIDHex))

	if strings.ToLower(strings.TrimSpace(challenge.AgentID)) != agentIDHex ||
		strings.TrimSpace(challenge.Nonce) != nonce ||
		strings.ToLower(strings.TrimSpace(challenge.Purpose)) != purpose {
		return invalidSoulLifecycleChallengeError()
	}
	if purpose == soulLifecycleChallengePurposeDesignateSuccessor &&
		strings.ToLower(strings.TrimSpace(challenge.SuccessorAgentID)) != successorIDHex {
		return invalidSoulLifecycleChallengeError()
	}
	if challenge.ExpiresAt.IsZero() || !now.Before(challenge.ExpiresAt.UTC()) {
		return newAppTheoryError("app.bad_request", "continuity_nonce expired")
	}
	_ = challenge.UpdateKeys()
	return nil
}

func invalidSoulLifecycleChallengeError() *apptheory.AppTheoryError {
	return newAppTheoryError("app.bad_request", "invalid continuity_nonce")
}

func parseSoulArchiveRequestBody(ctx *apptheory.Context) (reason string, parsedTS time.Time, timestampCanonical string, sig string, continuityNonce string, appErr *apptheory.AppTheoryError) {
	var req soulArchiveRequest
	_ = httpx.ParseJSON(ctx, &req)

	reason = strings.TrimSpace(req.Reason)
	tsRaw := strings.TrimSpace(req.Timestamp)
	if tsRaw == "" {
		return "", time.Time{}, "", "", "", newAppTheoryError("app.bad_request", "timestamp is required")
	}
	parsedTS, timestampCanonical, appErr = parseAndValidateSoulContinuityTimestamp(tsRaw)
	if appErr != nil {
		return "", time.Time{}, "", "", "", appErr
	}

	sig = strings.TrimSpace(req.Signature)
	if sig == "" {
		return "", time.Time{}, "", "", "", newAppTheoryError("app.bad_request", "signature is required")
	}

	continuityNonce = strings.TrimSpace(req.Nonce)
	if continuityNonce == "" {
		return "", time.Time{}, "", "", "", newAppTheoryError("app.bad_request", "continuity_nonce is required")
	}
	return reason, parsedTS, timestampCanonical, sig, continuityNonce, nil
}

func parseSoulSuccessorAgentID(ctx *apptheory.Context, agentIDHex string) (string, *apptheory.AppTheoryError) {
	var req struct {
		SuccessorAgentID string `json:"successor_agent_id"`
	}
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		appErr, ok := parseErr.(*apptheory.AppTheoryError)
		if !ok {
			return "", newAppTheoryError("app.bad_request", parseErr.Error())
		}
		return "", appErr
	}
	return normalizeSoulSuccessorAgentID(req.SuccessorAgentID, agentIDHex)
}

func parseSoulDesignateSuccessorRequestBody(ctx *apptheory.Context, agentIDHex string) (successorIDHex string, reason string, parsedTS time.Time, timestampCanonical string, predSig string, succSig string, continuityNonce string, appErr *apptheory.AppTheoryError) {
	var req soulDesignateSuccessorRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		parsedAppErr, ok := parseErr.(*apptheory.AppTheoryError)
		if !ok {
			return "", "", time.Time{}, "", "", "", "", newAppTheoryError("app.bad_request", parseErr.Error())
		}
		return "", "", time.Time{}, "", "", "", "", parsedAppErr
	}

	successorIDHex, appErr = normalizeSoulSuccessorAgentID(req.SuccessorAgentID, agentIDHex)
	if appErr != nil {
		return "", "", time.Time{}, "", "", "", "", appErr
	}

	reason = strings.TrimSpace(req.Reason)
	tsRaw := strings.TrimSpace(req.Timestamp)
	if tsRaw == "" {
		return "", "", time.Time{}, "", "", "", "", newAppTheoryError("app.bad_request", "timestamp is required")
	}
	parsedTS, timestampCanonical, appErr = parseAndValidateSoulContinuityTimestamp(tsRaw)
	if appErr != nil {
		return "", "", time.Time{}, "", "", "", "", appErr
	}

	predSig = strings.TrimSpace(req.PredecessorSig)
	if predSig == "" {
		return "", "", time.Time{}, "", "", "", "", newAppTheoryError("app.bad_request", "predecessor_signature is required")
	}

	succSig = strings.TrimSpace(req.SuccessorSig)
	if succSig == "" {
		return "", "", time.Time{}, "", "", "", "", newAppTheoryError("app.bad_request", "successor_signature is required")
	}

	continuityNonce = strings.TrimSpace(req.Nonce)
	if continuityNonce == "" {
		return "", "", time.Time{}, "", "", "", "", newAppTheoryError("app.bad_request", "continuity_nonce is required")
	}

	return successorIDHex, reason, parsedTS, timestampCanonical, predSig, succSig, continuityNonce, nil
}

func newSoulContinuityNonce() (string, *apptheory.AppTheoryError) {
	nonce, err := newToken(32)
	if err != nil {
		return "", newAppTheoryError("app.internal", "failed to generate nonce")
	}
	return nonce, nil
}

func normalizeSoulSuccessorAgentID(successorAgentID string, agentIDHex string) (string, *apptheory.AppTheoryError) {
	successorIDHex := strings.ToLower(strings.TrimSpace(successorAgentID))
	if successorIDHex == "" {
		return "", newAppTheoryError("app.bad_request", "successor_agent_id is required")
	}
	if successorIDHex == agentIDHex {
		return "", newAppTheoryError("app.bad_request", "agent cannot succeed itself")
	}
	return successorIDHex, nil
}

func (s *Server) loadSoulActiveSuccessorIdentity(ctx *apptheory.Context, identity *models.SoulAgentIdentity, agentIDHex string, successorIDHex string) (*models.SoulAgentIdentity, *apptheory.AppTheoryError) {
	successorIdentity, err := s.getSoulAgentIdentity(ctx.Context(), successorIDHex)
	if err != nil || successorIdentity == nil {
		return nil, newAppTheoryError("app.not_found", "successor agent not found")
	}

	successorStatus := strings.TrimSpace(successorIdentity.LifecycleStatus)
	if successorStatus == "" {
		successorStatus = strings.TrimSpace(successorIdentity.Status)
	}
	if successorStatus != models.SoulAgentStatusActive {
		return nil, newAppTheoryError("app.conflict", "successor agent is not active")
	}

	if identity != nil && !strings.EqualFold(strings.TrimSpace(successorIdentity.Domain), strings.TrimSpace(identity.Domain)) {
		if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(successorIdentity.Domain)); accessErr != nil {
			return nil, accessErr
		}
	}
	if strings.TrimSpace(successorIdentity.PredecessorAgentID) != "" {
		return nil, newAppTheoryError("app.conflict", "successor already has a predecessor")
	}

	_ = agentIDHex
	return successorIdentity, nil
}

func soulArchiveContinuityPayload(agentIDHex string) (string, []string) {
	return soulContinuitySummaryArchived, []string{fmt.Sprintf("agent:%s", agentIDHex)}
}

func soulSuccessionContinuityPayloads(agentIDHex string, successorIDHex string) (string, []string, string, []string) {
	return soulContinuitySummarySuccessionDeclared,
		[]string{fmt.Sprintf("agent:%s", agentIDHex), fmt.Sprintf("successor:%s", successorIDHex)},
		soulContinuitySummarySuccessionReceived,
		[]string{fmt.Sprintf("agent:%s", successorIDHex), fmt.Sprintf("predecessor:%s", agentIDHex)}
}

func buildSoulDesignateSuccessorBeginResponse(agentIDHex string, successorIDHex string, timestamp string, continuityNonce string) (soulDesignateSuccessorBeginResponse, *apptheory.AppTheoryError) {
	declaredSummary, declaredRefs, receivedSummary, receivedRefs := soulSuccessionContinuityPayloads(agentIDHex, successorIDHex)

	declaredDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionDeclared, timestamp, declaredSummary, "", declaredRefs, continuityNonce)
	if appErr != nil {
		return soulDesignateSuccessorBeginResponse{}, appErr
	}
	receivedDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionReceived, timestamp, receivedSummary, "", receivedRefs, continuityNonce)
	if appErr != nil {
		return soulDesignateSuccessorBeginResponse{}, appErr
	}

	return soulDesignateSuccessorBeginResponse{
		Version:         "1",
		ContinuityNonce: continuityNonce,
		PredecessorEntry: soulContinuityToSign{
			AgentID:    agentIDHex,
			Type:       models.SoulContinuityEntryTypeSuccessionDeclared,
			Timestamp:  timestamp,
			Summary:    declaredSummary,
			References: declaredRefs,
			DigestHex:  "0x" + fmt.Sprintf("%x", declaredDigest),
		},
		SuccessorEntry: soulContinuityToSign{
			AgentID:    successorIDHex,
			Type:       models.SoulContinuityEntryTypeSuccessionReceived,
			Timestamp:  timestamp,
			Summary:    receivedSummary,
			References: receivedRefs,
			DigestHex:  "0x" + fmt.Sprintf("%x", receivedDigest),
		},
	}, nil
}
