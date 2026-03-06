package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// --- Request types ---

type soulArchiveRequest struct {
	Reason    string `json:"reason,omitempty"`
	Timestamp string `json:"timestamp"`
	Signature string `json:"signature"`
}

type soulDesignateSuccessorRequest struct {
	SuccessorAgentID string `json:"successor_agent_id"`
	Reason           string `json:"reason,omitempty"`
	Timestamp        string `json:"timestamp"`
	PredecessorSig   string `json:"predecessor_signature"`
	SuccessorSig     string `json:"successor_signature"`
}

type soulArchiveBeginResponse struct {
	Version string               `json:"version"`
	Entry   soulContinuityToSign `json:"entry"`
}

type soulDesignateSuccessorBeginResponse struct {
	Version          string               `json:"version"`
	PredecessorEntry soulContinuityToSign `json:"predecessor_entry"`
	SuccessorEntry   soulContinuityToSign `json:"successor_entry"`
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
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}

	// Only active or self_suspended agents can be archived.
	currentStatus := strings.TrimSpace(identity.Status)
	if currentStatus != models.SoulAgentStatusActive && currentStatus != models.SoulAgentStatusSelfSuspended {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "only active or self-suspended agents can be archived"}
	}

	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339Nano)
	summary := soulContinuitySummaryArchived
	references := []string{fmt.Sprintf("agent:%s", agentIDHex)}

	digest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeArchived, timestamp, summary, "", references)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulArchiveBeginResponse{
		Version: "1",
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

	reason, parsedTS, timestampCanonical, sig, appErr := parseSoulArchiveRequestBody(ctx)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()

	// Verify archive continuity signature (EIP-191 over keccak256(JCS(unsignedEntry))).
	continuitySummary, continuityRefs := soulArchiveContinuityPayload(agentIDHex)
	contDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeArchived, timestampCanonical, continuitySummary, "", continuityRefs)
	if appErr != nil {
		return nil, appErr
	}
	if sigErr := verifyEthereumSignatureBytesNonMalleable(identity.Wallet, contDigest, sig); sigErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid continuity signature"}
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
		tx.Update(identity, []string{"Status", "LifecycleStatus", "LifecycleReason", "UpdatedAt"}, tabletheory.IfExists())
		tx.Create(continuity)
		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to archive agent"}
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

	beginResp, appErr := buildSoulDesignateSuccessorBeginResponse(agentIDHex, successorIDHex, time.Now().UTC().Format(time.RFC3339Nano))
	if appErr != nil {
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

	successorIDHex, reason, parsedTS, timestampCanonical, predSig, succSig, appErr := parseSoulDesignateSuccessorRequestBody(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	successorIdentity, appErr := s.loadSoulActiveSuccessorIdentity(ctx, identity, agentIDHex, successorIDHex)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()

	declaredSummary, declaredRefs, receivedSummary, receivedRefs := soulSuccessionContinuityPayloads(agentIDHex, successorIDHex)
	declaredDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionDeclared, timestampCanonical, declaredSummary, "", declaredRefs)
	if appErr != nil {
		return nil, appErr
	}
	if sigErr := verifyEthereumSignatureBytesNonMalleable(identity.Wallet, declaredDigest, predSig); sigErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid predecessor continuity signature"}
	}

	receivedDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionReceived, timestampCanonical, receivedSummary, "", receivedRefs)
	if appErr != nil {
		return nil, appErr
	}
	if sigErr := verifyEthereumSignatureBytesNonMalleable(successorIdentity.Wallet, receivedDigest, succSig); sigErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid successor continuity signature"}
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
		tx.Update(identity, []string{"Status", "LifecycleStatus", "LifecycleReason", "SuccessorAgentID", "UpdatedAt"}, tabletheory.IfExists())
		tx.Update(successorIdentity, []string{"PredecessorAgentID", "UpdatedAt"}, tabletheory.IfExists())
		tx.Create(predContinuity)
		tx.Create(succContinuity)
		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to designate successor"}
	}

	return apptheory.JSON(http.StatusOK, identity)
}

func parseAndValidateSoulContinuityTimestamp(tsRaw string) (time.Time, string, *apptheory.AppError) {
	tsRaw = strings.TrimSpace(tsRaw)
	parsedTS, parseErr := time.Parse(time.RFC3339, tsRaw)
	if parseErr != nil {
		if parsedTS, parseErr = time.Parse(time.RFC3339Nano, tsRaw); parseErr != nil {
			return time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "timestamp must be RFC3339"}
		}
	}

	now := time.Now().UTC()
	if parsedTS.After(now.Add(5 * time.Minute)) {
		return time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "timestamp cannot be in the future"}
	}
	if parsedTS.Before(now.Add(-10 * 365 * 24 * time.Hour)) {
		return time.Time{}, "", &apptheory.AppError{Code: "app.bad_request", Message: "timestamp is too far in the past"}
	}

	return parsedTS.UTC(), parsedTS.UTC().Format(time.RFC3339Nano), nil
}

func (s *Server) loadSoulLifecycleIdentity(ctx *apptheory.Context, agentIDHex string) (*models.SoulAgentIdentity, *apptheory.AppError) {
	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}
	return identity, nil
}

func validateSoulLifecycleMutableStatus(identity *models.SoulAgentIdentity, action string) *apptheory.AppError {
	status := ""
	if identity != nil {
		status = strings.TrimSpace(identity.Status)
	}
	if status == models.SoulAgentStatusActive || status == models.SoulAgentStatusSelfSuspended {
		return nil
	}
	return &apptheory.AppError{Code: "app.conflict", Message: fmt.Sprintf("only active or self-suspended agents can %s", strings.TrimSpace(action))}
}

func parseSoulArchiveRequestBody(ctx *apptheory.Context) (reason string, parsedTS time.Time, timestampCanonical string, sig string, appErr *apptheory.AppError) {
	var req soulArchiveRequest
	_ = httpx.ParseJSON(ctx, &req)

	reason = strings.TrimSpace(req.Reason)
	tsRaw := strings.TrimSpace(req.Timestamp)
	if tsRaw == "" {
		return "", time.Time{}, "", "", &apptheory.AppError{Code: "app.bad_request", Message: "timestamp is required"}
	}
	parsedTS, timestampCanonical, appErr = parseAndValidateSoulContinuityTimestamp(tsRaw)
	if appErr != nil {
		return "", time.Time{}, "", "", appErr
	}

	sig = strings.TrimSpace(req.Signature)
	if sig == "" {
		return "", time.Time{}, "", "", &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}
	return reason, parsedTS, timestampCanonical, sig, nil
}

func parseSoulSuccessorAgentID(ctx *apptheory.Context, agentIDHex string) (string, *apptheory.AppError) {
	var req struct {
		SuccessorAgentID string `json:"successor_agent_id"`
	}
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		appErr, ok := parseErr.(*apptheory.AppError)
		if !ok {
			return "", &apptheory.AppError{Code: "app.bad_request", Message: parseErr.Error()}
		}
		return "", appErr
	}
	return normalizeSoulSuccessorAgentID(req.SuccessorAgentID, agentIDHex)
}

func parseSoulDesignateSuccessorRequestBody(ctx *apptheory.Context, agentIDHex string) (successorIDHex string, reason string, parsedTS time.Time, timestampCanonical string, predSig string, succSig string, appErr *apptheory.AppError) {
	var req soulDesignateSuccessorRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		parsedAppErr, ok := parseErr.(*apptheory.AppError)
		if !ok {
			return "", "", time.Time{}, "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: parseErr.Error()}
		}
		return "", "", time.Time{}, "", "", "", parsedAppErr
	}

	successorIDHex, appErr = normalizeSoulSuccessorAgentID(req.SuccessorAgentID, agentIDHex)
	if appErr != nil {
		return "", "", time.Time{}, "", "", "", appErr
	}

	reason = strings.TrimSpace(req.Reason)
	tsRaw := strings.TrimSpace(req.Timestamp)
	if tsRaw == "" {
		return "", "", time.Time{}, "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "timestamp is required"}
	}
	parsedTS, timestampCanonical, appErr = parseAndValidateSoulContinuityTimestamp(tsRaw)
	if appErr != nil {
		return "", "", time.Time{}, "", "", "", appErr
	}

	predSig = strings.TrimSpace(req.PredecessorSig)
	if predSig == "" {
		return "", "", time.Time{}, "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "predecessor_signature is required"}
	}

	succSig = strings.TrimSpace(req.SuccessorSig)
	if succSig == "" {
		return "", "", time.Time{}, "", "", "", &apptheory.AppError{Code: "app.bad_request", Message: "successor_signature is required"}
	}

	return successorIDHex, reason, parsedTS, timestampCanonical, predSig, succSig, nil
}

func normalizeSoulSuccessorAgentID(successorAgentID string, agentIDHex string) (string, *apptheory.AppError) {
	successorIDHex := strings.ToLower(strings.TrimSpace(successorAgentID))
	if successorIDHex == "" {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "successor_agent_id is required"}
	}
	if successorIDHex == agentIDHex {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "agent cannot succeed itself"}
	}
	return successorIDHex, nil
}

func (s *Server) loadSoulActiveSuccessorIdentity(ctx *apptheory.Context, identity *models.SoulAgentIdentity, agentIDHex string, successorIDHex string) (*models.SoulAgentIdentity, *apptheory.AppError) {
	successorIdentity, err := s.getSoulAgentIdentity(ctx.Context(), successorIDHex)
	if err != nil || successorIdentity == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "successor agent not found"}
	}

	successorStatus := strings.TrimSpace(successorIdentity.LifecycleStatus)
	if successorStatus == "" {
		successorStatus = strings.TrimSpace(successorIdentity.Status)
	}
	if successorStatus != models.SoulAgentStatusActive {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "successor agent is not active"}
	}

	if identity != nil && !strings.EqualFold(strings.TrimSpace(successorIdentity.Domain), strings.TrimSpace(identity.Domain)) {
		if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(successorIdentity.Domain)); accessErr != nil {
			return nil, accessErr
		}
	}
	if strings.TrimSpace(successorIdentity.PredecessorAgentID) != "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "successor already has a predecessor"}
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

func buildSoulDesignateSuccessorBeginResponse(agentIDHex string, successorIDHex string, timestamp string) (soulDesignateSuccessorBeginResponse, *apptheory.AppError) {
	declaredSummary, declaredRefs, receivedSummary, receivedRefs := soulSuccessionContinuityPayloads(agentIDHex, successorIDHex)

	declaredDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionDeclared, timestamp, declaredSummary, "", declaredRefs)
	if appErr != nil {
		return soulDesignateSuccessorBeginResponse{}, appErr
	}
	receivedDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionReceived, timestamp, receivedSummary, "", receivedRefs)
	if appErr != nil {
		return soulDesignateSuccessorBeginResponse{}, appErr
	}

	return soulDesignateSuccessorBeginResponse{
		Version: "1",
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
