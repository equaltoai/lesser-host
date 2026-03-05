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
	summary := "Archived"
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

	var req soulArchiveRequest
	_ = httpx.ParseJSON(ctx, &req)
	reason := strings.TrimSpace(req.Reason)
	tsRaw := strings.TrimSpace(req.Timestamp)
	if tsRaw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "timestamp is required"}
	}
	parsedTS, timestampCanonical, appErr := parseAndValidateSoulContinuityTimestamp(tsRaw)
	if appErr != nil {
		return nil, appErr
	}
	sig := strings.TrimSpace(req.Signature)
	if sig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}

	now := time.Now().UTC()

	// Verify archive continuity signature (EIP-191 over keccak256(JCS(unsignedEntry))).
	continuitySummary := "Archived"
	continuityRefs := []string{fmt.Sprintf("agent:%s", agentIDHex)}
	contDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeArchived, timestampCanonical, continuitySummary, "", continuityRefs)
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytesNonMalleable(identity.Wallet, contDigest, sig); err != nil {
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

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}

	currentStatus := strings.TrimSpace(identity.Status)
	if currentStatus != models.SoulAgentStatusActive && currentStatus != models.SoulAgentStatusSelfSuspended {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "only active or self-suspended agents can designate a successor"}
	}

	var req struct {
		SuccessorAgentID string `json:"successor_agent_id"`
	}
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	successorIDHex := strings.ToLower(strings.TrimSpace(req.SuccessorAgentID))
	if successorIDHex == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "successor_agent_id is required"}
	}
	if successorIDHex == agentIDHex {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "agent cannot succeed itself"}
	}

	// Verify successor exists.
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

	// If successor is in a different domain, ensure the actor has access to it.
	if !strings.EqualFold(strings.TrimSpace(successorIdentity.Domain), strings.TrimSpace(identity.Domain)) {
		if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(successorIdentity.Domain)); accessErr != nil {
			return nil, accessErr
		}
	}

	if strings.TrimSpace(successorIdentity.PredecessorAgentId) != "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "successor already has a predecessor"}
	}

	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339Nano)

	declaredSummary := "Succession declared"
	declaredRefs := []string{fmt.Sprintf("agent:%s", agentIDHex), fmt.Sprintf("successor:%s", successorIDHex)}
	declaredDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionDeclared, timestamp, declaredSummary, "", declaredRefs)
	if appErr != nil {
		return nil, appErr
	}

	receivedSummary := "Succession received"
	receivedRefs := []string{fmt.Sprintf("agent:%s", successorIDHex), fmt.Sprintf("predecessor:%s", agentIDHex)}
	receivedDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionReceived, timestamp, receivedSummary, "", receivedRefs)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulDesignateSuccessorBeginResponse{
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
	})
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

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}

	// Only active or self_suspended agents can designate a successor.
	currentStatus := strings.TrimSpace(identity.Status)
	if currentStatus != models.SoulAgentStatusActive && currentStatus != models.SoulAgentStatusSelfSuspended {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "only active or self-suspended agents can designate a successor"}
	}

	var req soulDesignateSuccessorRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	successorIDHex := strings.ToLower(strings.TrimSpace(req.SuccessorAgentID))
	if successorIDHex == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "successor_agent_id is required"}
	}
	if successorIDHex == agentIDHex {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "agent cannot succeed itself"}
	}

	// Verify the successor agent exists.
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
	// If successor is in a different domain, ensure the actor has access to it (since we will update its identity).
	if !strings.EqualFold(strings.TrimSpace(successorIdentity.Domain), strings.TrimSpace(identity.Domain)) {
		if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(successorIdentity.Domain)); accessErr != nil {
			return nil, accessErr
		}
	}
	if strings.TrimSpace(successorIdentity.PredecessorAgentId) != "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "successor already has a predecessor"}
	}

	reason := strings.TrimSpace(req.Reason)
	tsRaw := strings.TrimSpace(req.Timestamp)
	if tsRaw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "timestamp is required"}
	}
	parsedTS, timestampCanonical, appErr := parseAndValidateSoulContinuityTimestamp(tsRaw)
	if appErr != nil {
		return nil, appErr
	}
	predSig := strings.TrimSpace(req.PredecessorSig)
	succSig := strings.TrimSpace(req.SuccessorSig)
	if predSig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "predecessor_signature is required"}
	}
	if succSig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "successor_signature is required"}
	}

	now := time.Now().UTC()

	declaredSummary := "Succession declared"
	declaredRefs := []string{fmt.Sprintf("agent:%s", agentIDHex), fmt.Sprintf("successor:%s", successorIDHex)}
	declaredDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionDeclared, timestampCanonical, declaredSummary, "", declaredRefs)
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytesNonMalleable(identity.Wallet, declaredDigest, predSig); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid predecessor continuity signature"}
	}

	receivedSummary := "Succession received"
	receivedRefs := []string{fmt.Sprintf("agent:%s", successorIDHex), fmt.Sprintf("predecessor:%s", agentIDHex)}
	receivedDigest, appErr := computeSoulContinuityEntryDigest(models.SoulContinuityEntryTypeSuccessionReceived, timestampCanonical, receivedSummary, "", receivedRefs)
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytesNonMalleable(successorIdentity.Wallet, receivedDigest, succSig); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid successor continuity signature"}
	}

	identity.Status = models.SoulAgentStatusSucceeded
	identity.LifecycleStatus = models.SoulAgentStatusSucceeded
	identity.LifecycleReason = reason
	identity.SuccessorAgentId = successorIDHex
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	successorIdentity.PredecessorAgentId = agentIDHex
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
		tx.Update(identity, []string{"Status", "LifecycleStatus", "LifecycleReason", "SuccessorAgentId", "UpdatedAt"}, tabletheory.IfExists())
		tx.Update(successorIdentity, []string{"PredecessorAgentId", "UpdatedAt"}, tabletheory.IfExists())
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
