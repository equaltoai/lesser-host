package controlplane

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulInstanceBootstrapCodeUnauthorized        = "soul_instance.unauthorized"
	soulInstanceBootstrapCodeInvalidRequest      = "soul_instance.invalid_request"
	soulInstanceBootstrapCodeBoundaryViolation   = "soul_instance.boundary_violation"
	soulInstanceBootstrapCodeConflict            = "soul_instance.conflict"
	soulInstanceBootstrapCodeNotFound            = "soul_instance.not_found"
	soulInstanceBootstrapCodeInternal            = "soul_instance.internal"
	soulInstanceBootstrapCodeMicroVMUnavailable  = "soul_instance.microvm_unavailable"
	soulInstanceBootstrapCodeAssistantTurnFailed = "soul_instance.assistant_turn_failed"
	soulInstanceBootstrapMessageUnauthorized     = "unauthorized"
	soulInstanceBootstrapMessageBoundary         = "resource is outside the authenticated instance boundary"
	soulInstanceBootstrapBoundaryInstanceDomain  = "instance_domain"
)

// appErrCodeMicroVMUnavailable is the control-plane AppTheoryError code emitted when
// the hosted genesis accept path cannot dispatch the M16 MicroVM controller
// run. It maps to a loud 5xx at every public surface so MicroVM-unavailable is
// never confused with a generic internal error or silently downgraded to a
// synchronous control-plane LLM call.
const appErrCodeMicroVMUnavailable = "app.microvm_unavailable"

// appErrCodeAssistantTurnFailed is the control-plane AppTheoryError code emitted when
// a hosted genesis assistant turn failed (the LLM provider errored or returned
// an empty response on the retained non-production sync path). H1.4 (kills G10a)
// makes a failed turn surface as a loud non-2xx typed failure instead of HTTP
// 200 with a failed body: the caller returns the error, so the public surface
// emits 502, never a silent 200-on-failure. The durable session is still
// persisted as a retryable failed turn before the error is returned.
const appErrCodeAssistantTurnFailed = "app.assistant_turn_failed"

type soulInstanceBootstrapContext struct {
	key          *models.InstanceKey
	instanceSlug string
}

type soulInstanceBootstrapRegistrationContext struct {
	soulInstanceBootstrapContext
	reg        *models.SoulAgentRegistration
	inst       *models.Instance
	agentIDHex string
}

type soulInstanceBootstrapConversationContext struct {
	soulInstanceBootstrapRegistrationContext
	session        *models.HostedGenesisSession
	conv           *models.SoulAgentMintConversation
	conversationID string
}

func (s *Server) handleSoulInstanceAgentRegistrationBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
	instCtx, appErr := s.requireSoulInstanceBootstrapContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	var req soulAgentRegistrationBeginRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, soulInstanceBootstrapErrorFromError(err)
	}
	domainNormalized, appErr := normalizeSoulInstanceBootstrapDomain(req.Domain)
	if appErr != nil {
		return nil, appErr
	}
	if _, _, appErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, instCtx.instanceSlug, domainNormalized); appErr != nil {
		return nil, appErr
	}

	out, beginErr := s.beginSoulAgentRegistration(ctx, req, domainNormalized, true, soulInstanceBootstrapActor(instCtx.instanceSlug), true)
	if beginErr != nil {
		return nil, soulInstanceBootstrapErrorFromAppError(beginErr)
	}
	return apptheory.JSON(http.StatusCreated, out)
}

func (s *Server) handleSoulInstanceAgentRegistrationPrincipalDeclarationPreflight(ctx *apptheory.Context) (*apptheory.Response, error) {
	regCtx, appErr := s.requireSoulInstanceBootstrapRegistrationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	_, principalAddrRaw, principalDeclarationRaw, declaredAtRaw, err := parseSoulAgentRegistrationPrincipalDeclarationPreflightInput(ctx)
	if err != nil {
		return nil, soulInstanceBootstrapErrorFromError(err)
	}

	if appErr := s.requireSoulInstanceRegistrationUsableForSigning(ctx, regCtx.reg); appErr != nil {
		return nil, appErr
	}

	principalAddr, principalDeclaration, declaredAt, normErr := s.normalizeSoulRegistrationPrincipalDeclarationInputs(
		ctx.Context(),
		principalAddrRaw,
		principalDeclarationRaw,
		declaredAtRaw,
	)
	if normErr != nil {
		return nil, soulInstanceBootstrapErrorFromAppError(normErr)
	}

	material, materialErr := s.computeSoulPrincipalDeclarationSigningMaterial(regCtx.reg, principalAddr, principalDeclaration, declaredAt)
	if materialErr != nil {
		return nil, soulInstanceBootstrapErrorFromAppError(materialErr)
	}
	digestHex := "0x" + hex.EncodeToString(material.digest)

	return apptheory.JSON(http.StatusOK, soulAgentRegistrationPrincipalDeclarationPreflightResponse{
		Version:          "1",
		PrincipalAddress: principalAddr,
		SignerAddress:    principalAddr,
		SigningMethod:    "eip191_personal_sign",
		MessageEncoding:  "hex_bytes",
		MessageHex:       digestHex,
		DigestHex:        digestHex,
		CanonicalJSON:    material.canonicalJSON,
		DeclaredAt:       declaredAt,
	})
}

func (s *Server) handleSoulInstanceAgentRegistrationVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	regCtx, appErr := s.requireSoulInstanceBootstrapRegistrationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	_, sig, principalAddrRaw, principalDeclarationRaw, principalSigRaw, declaredAtRaw, err := parseSoulAgentRegistrationVerifyInput(ctx)
	if err != nil {
		return nil, soulInstanceBootstrapErrorFromError(err)
	}

	resp, verifyErr := s.verifySoulInstanceAgentRegistration(
		ctx,
		regCtx,
		sig,
		principalAddrRaw,
		principalDeclarationRaw,
		principalSigRaw,
		declaredAtRaw,
	)
	if verifyErr != nil {
		return nil, verifyErr
	}
	return apptheory.JSON(http.StatusOK, resp)
}

func (s *Server) handleSoulInstanceMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	regCtx, appErr := s.requireSoulInstanceBootstrapRegistrationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	resp, err := s.handleSoulMintConversationForRegistrationAsync(ctx, mintConversationRegistrationContext{
		reg:        regCtx.reg,
		inst:       regCtx.inst,
		agentIDHex: regCtx.agentIDHex,
	}, regCtx.instanceSlug)
	if err != nil {
		return nil, soulInstanceBootstrapConversationErrorFromError(err)
	}
	return resp, nil
}

func (s *Server) handleSoulInstanceGetRegistrationMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	started := time.Now().UTC()
	convCtx, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	decodeMintConversationFields(convCtx.conv)
	if appErr := rejectOversizeSoulMintInstanceConversation(convCtx.conv); appErr != nil {
		return nil, appErr
	}
	observedSession, observedConversation, observeErr := s.observeHostedGenesisMicroVMOnRead(ctx, convCtx.session, convCtx.conv)
	if observeErr != nil {
		return nil, observeErr
	}
	convCtx.session, convCtx.conv = observedSession, observedConversation
	if appErr := s.repairSoulInstanceHostedGenesisPublishedPendingOnRead(ctx, convCtx); appErr != nil {
		return nil, appErr
	}
	resp, err := hostedGenesisConversationJSONFromSession(http.StatusOK, convCtx.session, convCtx.conv, hostedGenesisProjectionOptions{
		RegistrationID:  convCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
	})
	if err != nil {
		return nil, err
	}
	s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteSingle, "success", resp.Status, len(resp.Body), started)
	return resp, nil
}

func (s *Server) handleSoulInstanceCompleteMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	convCtx, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	publishGuardErr := s.ensureMintConversationAgentNotPublished(ctx.Context(), convCtx.agentIDHex)
	if replayReady, reason := hostedGenesisSessionCompletionReplayReady(convCtx.session); replayReady {
		return hostedGenesisConversationJSONFromSession(http.StatusOK, convCtx.session, convCtx.conv, hostedGenesisProjectionOptions{RegistrationID: convCtx.reg.ID, RequestID: strings.TrimSpace(ctx.RequestID), CollapseCreated: true})
	} else if reason != "" {
		return nil, soulInstanceMintConversationCompletionConflictFromSession(convCtx.session, reason)
	}
	if publishGuardErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(publishGuardErr)
	}
	status := hostedgenesis.NormalizeStatus(convCtx.session.Status)
	if status != hostedgenesis.StatusAssistantTurnReady &&
		status != hostedgenesis.StatusDeclarationExtractionPending {
		return hostedGenesisConversationJSONFromSession(http.StatusAccepted, convCtx.session, convCtx.conv, hostedGenesisProjectionOptions{RegistrationID: convCtx.reg.ID, RequestID: strings.TrimSpace(ctx.RequestID), CollapseCreated: true})
	}
	if status == hostedgenesis.StatusDeclarationExtractionPending && parseMintConversationCompleteDeclarations(ctx) == "" {
		return hostedGenesisConversationJSONFromSession(http.StatusAccepted, convCtx.session, convCtx.conv, hostedGenesisProjectionOptions{RegistrationID: convCtx.reg.ID, RequestID: strings.TrimSpace(ctx.RequestID), CollapseCreated: true})
	}
	// Project 48 M11: Hosted Genesis completion is no longer a Host-side
	// declaration-extraction trigger. The VM conversation actor owns the
	// extract/finalize decision after Host accepts the user's turn and dispatches
	// it through AppTheory Invoke. This endpoint remains a polling/finalize gate;
	// explicit declarations are handled only as a bounded compatibility seam for
	// older clients and are not the ordinary hosted path.
	if parseMintConversationCompleteDeclarations(ctx) == "" {
		return hostedGenesisConversationJSONFromSession(http.StatusAccepted, convCtx.session, convCtx.conv, hostedGenesisProjectionOptions{RegistrationID: convCtx.reg.ID, RequestID: strings.TrimSpace(ctx.RequestID), CollapseCreated: true})
	}
	resp, err := s.completeSoulMintConversationForRegistrationWithProjection(ctx, mintConversationRegistrationContext{
		reg:        convCtx.reg,
		inst:       convCtx.inst,
		agentIDHex: convCtx.agentIDHex,
	}, convCtx.conv, convCtx.conversationID, &hostedGenesisProjectionOptions{RegistrationID: convCtx.reg.ID, RequestID: strings.TrimSpace(ctx.RequestID), CollapseCreated: true})
	if err != nil {
		return nil, soulInstanceBootstrapConversationErrorFromError(err)
	}
	return resp, nil
}

func (s *Server) handleSoulInstanceFinalizeMintConversationPreflight(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleSoulInstanceBeginFinalizeMintConversation(ctx)
}

func (s *Server) handleSoulInstanceBeginFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	convCtx, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	finalizeCtx, appErr := s.loadSoulInstanceMintConversationFinalizeContext(ctx, convCtx)
	if appErr != nil {
		return nil, appErr
	}
	resp, err := s.beginFinalizeMintConversation(ctx, finalizeCtx)
	if err != nil {
		return nil, soulInstanceBootstrapConversationErrorFromError(err)
	}
	return resp, nil
}

func (s *Server) repairSoulInstanceHostedGenesisPublishedPendingOnRead(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) *apptheory.AppTheoryError {
	if ctx == nil || convCtx.reg == nil || convCtx.session == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(convCtx.reg.AuthorityModel), models.SoulAuthorityModelInstanceTrust) {
		return nil
	}
	if hostedgenesis.NormalizeStatus(convCtx.session.Status) != hostedgenesis.StatusDeclarationReady {
		return nil
	}
	identity, err := s.getSoulAgentIdentity(ctx.Context(), convCtx.agentIDHex)
	if err != nil {
		if errors.Is(err, theoryErrors.ErrItemNotFound) {
			return nil
		}
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if identity == nil || identity.SelfDescriptionVersion <= 0 {
		return nil
	}
	if appErr := s.ensureSoulAgentRegistrationPublishedIdentityActive(ctx.Context(), identity, time.Now().UTC()); appErr != nil {
		return soulInstanceBootstrapConversationErrorFromAppError(appErr)
	}
	return nil
}

func (s *Server) handleSoulInstanceFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	convCtx, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	finalizeCtx, appErr := s.loadSoulInstanceMintConversationFinalizeContext(ctx, convCtx)
	if appErr != nil {
		return nil, appErr
	}
	resp, err := s.finalizeMintConversation(ctx, finalizeCtx)
	if err != nil {
		return nil, soulInstanceBootstrapConversationErrorFromError(err)
	}
	return resp, nil
}

func (s *Server) requireSoulInstanceBootstrapContext(ctx *apptheory.Context) (soulInstanceBootstrapContext, *apptheory.AppTheoryError) {
	if appErr := requireStoreDB(s); appErr != nil {
		return soulInstanceBootstrapContext{}, soulInstanceBootstrapErrorFromAppError(appErr)
	}
	if ctx == nil {
		return soulInstanceBootstrapContext{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, "invalid request", http.StatusBadRequest, nil)
	}

	key, appErr := s.requireSoulInstanceBootstrapKey(ctx)
	if appErr != nil {
		return soulInstanceBootstrapContext{}, appErr
	}
	if !s.cfg.SoulEnabled {
		return soulInstanceBootstrapContext{}, soulInstanceBootstrapErrorFromAppError(newAppTheoryError(appErrCodeConflict, "soul registry is not configured"))
	}
	return soulInstanceBootstrapContext{key: key, instanceSlug: strings.TrimSpace(key.InstanceSlug)}, nil
}

func (s *Server) requireSoulInstanceBootstrapRegistrationContext(ctx *apptheory.Context) (soulInstanceBootstrapRegistrationContext, *apptheory.AppTheoryError) {
	instCtx, appErr := s.requireSoulInstanceBootstrapContext(ctx)
	if appErr != nil {
		return soulInstanceBootstrapRegistrationContext{}, appErr
	}
	regID := strings.TrimSpace(ctx.Param("id"))
	if regID == "" {
		return soulInstanceBootstrapRegistrationContext{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, "registration id is required", http.StatusBadRequest, map[string]any{"field": "id"})
	}
	reg, err := s.getSoulAgentRegistration(ctx.Context(), regID)
	if theoryErrors.IsNotFound(err) {
		return soulInstanceBootstrapRegistrationContext{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeNotFound, "registration not found", http.StatusNotFound, nil)
	}
	if err != nil {
		return soulInstanceBootstrapRegistrationContext{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	_, inst, accessErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, instCtx.instanceSlug, strings.TrimSpace(reg.DomainNormalized))
	if accessErr != nil {
		return soulInstanceBootstrapRegistrationContext{}, accessErr
	}
	return soulInstanceBootstrapRegistrationContext{
		soulInstanceBootstrapContext: instCtx,
		reg:                          reg,
		inst:                         inst,
		agentIDHex:                   strings.ToLower(strings.TrimSpace(reg.AgentID)),
	}, nil
}

func (s *Server) requireSoulInstanceBootstrapConversationContext(ctx *apptheory.Context) (soulInstanceBootstrapConversationContext, *apptheory.AppTheoryError) {
	regCtx, appErr := s.requireSoulInstanceBootstrapRegistrationContext(ctx)
	if appErr != nil {
		return soulInstanceBootstrapConversationContext{}, appErr
	}
	conversationID, convErr := requireSoulMintInstanceReadConversationID(ctx)
	if convErr != nil {
		return soulInstanceBootstrapConversationContext{}, convErr
	}
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), regCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil && !theoryErrors.IsNotFound(err) {
		return soulInstanceBootstrapConversationContext{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if conv != nil {
		decodeMintConversationFields(conv)
	}
	session, err := s.store.GetHostedGenesisSession(ctx.Context(), regCtx.instanceSlug, conversationID)
	if err != nil {
		return soulInstanceBootstrapConversationContext{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeNotFound, "conversation not found", http.StatusNotFound, nil)
	}
	if appErr := hydrateHostedGenesisSessionRouteBinding(session, mintConversationRegistrationContext{
		reg:        regCtx.reg,
		inst:       regCtx.inst,
		agentIDHex: regCtx.agentIDHex,
	}, regCtx.instanceSlug, conversationID); appErr != nil {
		return soulInstanceBootstrapConversationContext{}, soulInstanceBootstrapConversationErrorFromAppError(appErr)
	}
	return soulInstanceBootstrapConversationContext{
		soulInstanceBootstrapRegistrationContext: regCtx,
		session:                                  session,
		conv:                                     conv,
		conversationID:                           conversationID,
	}, nil
}

func (s *Server) loadSoulInstanceMintConversationFinalizeContext(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) (mintConversationFinalizeContext, *apptheory.AppTheoryError) {
	if s == nil || s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return mintConversationFinalizeContext{}, soulInstanceBootstrapConversationErrorFromAppError(newAppTheoryError("app.conflict", "soul registry bucket is not configured"))
	}
	if appErr := requireHostedGenesisSessionReadyForFinalize(convCtx.session, "conversation is not completed", "conversation has no produced declarations"); appErr != nil {
		return mintConversationFinalizeContext{}, soulInstanceBootstrapConversationErrorFromAppError(appErr)
	}
	identity, err := s.getSoulAgentIdentity(ctx.Context(), convCtx.agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return mintConversationFinalizeContext{}, soulInstanceBootstrapConversationErrorFromAppError(newAppTheoryError("app.conflict", "registration is not yet verified"))
	}
	if err != nil {
		return mintConversationFinalizeContext{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if isExplicitInstanceTrustAuthority(convCtx.reg, identity) {
		return mintConversationFinalizeContext{
			reg:            convCtx.reg,
			inst:           convCtx.inst,
			identity:       identity,
			conv:           convCtx.conv,
			session:        convCtx.session,
			agentIDHex:     convCtx.agentIDHex,
			conversationID: convCtx.conversationID,
			auditActor:     soulInstanceBootstrapActor(convCtx.instanceSlug),
		}, nil
	}
	if strings.TrimSpace(identity.PrincipalAddress) == "" ||
		strings.TrimSpace(identity.PrincipalSignature) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaredAt) == "" {
		return mintConversationFinalizeContext{}, soulInstanceBootstrapConversationErrorFromAppError(newAppTheoryError("app.conflict", "principal declaration is missing; re-verify registration"))
	}
	return mintConversationFinalizeContext{
		reg:            convCtx.reg,
		inst:           convCtx.inst,
		identity:       identity,
		conv:           convCtx.conv,
		session:        convCtx.session,
		agentIDHex:     convCtx.agentIDHex,
		conversationID: convCtx.conversationID,
		auditActor:     soulInstanceBootstrapActor(convCtx.instanceSlug),
	}, nil
}

func (s *Server) requireSoulInstanceBootstrapKey(ctx *apptheory.Context) (*models.InstanceKey, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	token := httpx.BearerToken(ctx.Request.Headers)
	if strings.TrimSpace(token) == "" {
		return nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeUnauthorized, soulInstanceBootstrapMessageUnauthorized, http.StatusUnauthorized, nil)
	}

	tokenHash := sha256HexTrimmed(token)
	key, err := s.store.GetInstanceKey(ctx.Context(), tokenHash)
	if theoryErrors.IsNotFound(err) {
		return nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeUnauthorized, soulInstanceBootstrapMessageUnauthorized, http.StatusUnauthorized, nil)
	}
	if err != nil {
		return nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if key == nil || strings.TrimSpace(key.InstanceSlug) == "" || !key.RevokedAt.IsZero() {
		return nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeUnauthorized, soulInstanceBootstrapMessageUnauthorized, http.StatusUnauthorized, nil)
	}

	s.updateSoulInstanceKeyLastUsed(ctx, key)
	return key, nil
}

func (s *Server) updateSoulInstanceKeyLastUsed(ctx *apptheory.Context, key *models.InstanceKey) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || key == nil {
		return
	}
	key.LastUsedAt = time.Now().UTC()
	_ = key.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(key).IfExists().Update("LastUsedAt")
}

func (s *Server) requireSoulInstanceRegistrationUsableForSigning(ctx *apptheory.Context, reg *models.SoulAgentRegistration) *apptheory.AppTheoryError {
	if reg == nil {
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	status := strings.ToLower(strings.TrimSpace(reg.Status))
	if status != models.SoulAgentRegistrationStatusCompleted && !reg.ExpiresAt.IsZero() && time.Now().After(reg.ExpiresAt) {
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, "registration expired", http.StatusBadRequest, nil)
	}
	if ensureErr := s.ensureSoulAgentNotActive(ctx.Context(), reg.AgentID); ensureErr != nil {
		return soulInstanceBootstrapErrorFromAppError(ensureErr)
	}
	return nil
}

func (s *Server) verifySoulInstanceAgentRegistration(
	ctx *apptheory.Context,
	regCtx soulInstanceBootstrapRegistrationContext,
	sig string,
	principalAddrRaw string,
	principalDeclarationRaw string,
	principalSigRaw string,
	declaredAtRaw string,
) (soulAgentRegistrationVerifyResponse, *apptheory.AppTheoryError) {
	reg := regCtx.reg
	if reg == nil {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if appErr := s.requireSoulInstanceRegistrationUsableForSigning(ctx, reg); appErr != nil {
		return soulAgentRegistrationVerifyResponse{}, appErr
	}

	if verifyErr := verifySoulAgentRegistrationWallet(reg, sig); verifyErr != nil {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapErrorFromAppError(verifyErr)
	}

	verifiedDNS, verifiedHTTPS, proofErr := verifySoulAgentRegistrationProofs(ctx.Context(), reg)
	if proofErr != nil {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapErrorFromAppError(proofErr)
	}

	principalAddr, principalDeclaration, principalSig, declaredAt, principalErr := s.validateSoulRegistrationVerifyPrincipalInputs(
		ctx.Context(),
		reg,
		principalAddrRaw,
		principalDeclarationRaw,
		principalSigRaw,
		declaredAtRaw,
	)
	if principalErr != nil {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapErrorFromAppError(principalErr)
	}

	completed := strings.EqualFold(strings.TrimSpace(reg.Status), models.SoulAgentRegistrationStatusCompleted)
	if completed {
		return s.replaySoulInstanceCompletedRegistration(ctx, reg, principalAddr, principalSig, principalDeclaration, declaredAt)
	}

	op, safeTx, _, opErr := s.createSoulMintOperation(ctx.Context(), reg, principalAddr, principalSig, principalDeclaration, declaredAt)
	if opErr != nil {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapErrorFromAppError(opErr)
	}

	now := time.Now().UTC()
	update, compErr := s.completeSoulAgentRegistration(ctx, reg, verifiedDNS, verifiedHTTPS, now)
	if compErr != nil {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapErrorFromAppError(compErr)
	}
	promotion, _ := s.getSoulAgentPromotion(ctx.Context(), reg.AgentID)
	promotion = updateSoulAgentPromotionForVerification(promotion, reg, op, principalAddr, now)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapErrorFromAppError(appErr)
	}
	if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:   models.SoulAgentPromotionEventTypeRequestApproved,
		RequestID:   strings.TrimSpace(ctx.RequestID),
		OperationID: op.OperationID,
		OccurredAt:  now,
	})); appErr != nil {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapErrorFromAppError(appErr)
	}

	audit := &models.AuditLogEntry{
		Actor:     soulInstanceBootstrapActor(regCtx.instanceSlug),
		Action:    "soul.registration.verify",
		Target:    fmt.Sprintf("soul_agent_registration:%s", reg.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	return soulAgentRegistrationVerifyResponse{
		Registration: *update,
		Operation:    *op,
		SafeTx:       safeTx,
		Promotion:    ptrTo(s.buildSoulAgentPromotionView(promotion)),
	}, nil
}

func (s *Server) replaySoulInstanceCompletedRegistration(
	ctx *apptheory.Context,
	reg *models.SoulAgentRegistration,
	principalAddr string,
	principalSig string,
	principalDeclaration string,
	declaredAt string,
) (soulAgentRegistrationVerifyResponse, *apptheory.AppTheoryError) {
	promotion, _ := s.getSoulAgentPromotion(ctx.Context(), reg.AgentID)
	if promotion != nil && strings.TrimSpace(promotion.PrincipalAddress) != "" && !strings.EqualFold(strings.TrimSpace(promotion.PrincipalAddress), principalAddr) {
		return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeBoundaryViolation, "principal_address mismatch for completed registration", http.StatusForbidden, nil)
	}

	op, appErr := s.loadSoulInstancePromotionOperation(ctx, promotion)
	if appErr != nil {
		return soulAgentRegistrationVerifyResponse{}, appErr
	}
	var safeTx *safeTxPayload
	if op != nil {
		safeTx = parseSafeTxPayload(op.SafePayloadJSON)
	} else {
		var opErr *apptheory.AppTheoryError
		op, safeTx, _, opErr = s.createSoulMintOperation(ctx.Context(), reg, principalAddr, principalSig, principalDeclaration, declaredAt)
		if opErr != nil {
			return soulAgentRegistrationVerifyResponse{}, soulInstanceBootstrapErrorFromAppError(opErr)
		}
	}
	if promotion == nil {
		now := firstNonZeroTime(reg.CompletedAt, reg.VerifiedAt, time.Now().UTC())
		if now.IsZero() {
			now = time.Now().UTC()
		}
		promotion = updateSoulAgentPromotionForVerification(buildSoulAgentPromotionFromRegistration(reg, now), reg, op, principalAddr, now)
	}

	return soulAgentRegistrationVerifyResponse{
		Registration: *reg,
		Operation:    *op,
		SafeTx:       safeTx,
		Promotion:    ptrTo(s.buildSoulAgentPromotionView(promotion)),
	}, nil
}

func (s *Server) loadSoulInstancePromotionOperation(ctx *apptheory.Context, promotion *models.SoulAgentPromotion) (*models.SoulOperation, *apptheory.AppTheoryError) {
	if promotion == nil || strings.TrimSpace(promotion.MintOperationID) == "" {
		return nil, nil
	}
	op, err := s.getSoulOperation(ctx.Context(), promotion.MintOperationID)
	if theoryErrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	return op, nil
}

func soulInstanceBootstrapActor(instanceSlug string) string {
	instanceSlug = strings.ToLower(strings.TrimSpace(instanceSlug))
	if instanceSlug == "" {
		return "instance:unknown"
	}
	return "instance:" + instanceSlug
}

func normalizeSoulInstanceBootstrapDomain(rawDomain string) (string, *apptheory.AppTheoryError) {
	domainNormalized, err := domains.NormalizeDomain(rawDomain)
	if err != nil {
		return "", soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, err.Error(), http.StatusBadRequest, map[string]any{"field": "domain"})
	}
	return domainNormalized, nil
}

func (s *Server) requireSoulInstanceBootstrapDomainAccess(ctx *apptheory.Context, instanceSlug string, normalizedDomain string) (*models.Domain, *models.Instance, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	instanceSlug = strings.TrimSpace(instanceSlug)
	normalizedDomain = strings.ToLower(strings.TrimSpace(normalizedDomain))
	if instanceSlug == "" {
		return nil, nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeUnauthorized, soulInstanceBootstrapMessageUnauthorized, http.StatusUnauthorized, nil)
	}
	if normalizedDomain == "" {
		return nil, nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, "domain is required", http.StatusBadRequest, map[string]any{"field": "domain"})
	}

	d, err := s.loadManagedStageAwareDomain(ctx.Context(), normalizedDomain)
	if theoryErrors.IsNotFound(err) {
		return nil, nil, soulInstanceBootstrapBoundaryError("domain", "domain_not_owned")
	}
	if err != nil {
		return nil, nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if d == nil || !domainIsVerifiedOrActive(d.Status) {
		return nil, nil, soulInstanceBootstrapBoundaryError("domain", "domain_not_verified")
	}
	if !strings.EqualFold(strings.TrimSpace(d.InstanceSlug), instanceSlug) {
		return nil, nil, soulInstanceBootstrapBoundaryError("domain", "tenant_domain_mismatch")
	}

	inst, err := s.loadInstanceMetadata(ctx.Context(), instanceSlug)
	if theoryErrors.IsNotFound(err) {
		return nil, nil, soulInstanceBootstrapBoundaryError("instance", "instance_not_found")
	}
	if err != nil {
		return nil, nil, soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	return d, inst, nil
}

func soulInstanceBootstrapBoundaryError(field string, reason string) *apptheory.AppTheoryError {
	return soulInstanceBootstrapError(
		soulInstanceBootstrapCodeBoundaryViolation,
		soulInstanceBootstrapMessageBoundary,
		http.StatusForbidden,
		map[string]any{
			"field":    strings.TrimSpace(field),
			"reason":   strings.TrimSpace(reason),
			"boundary": soulInstanceBootstrapBoundaryInstanceDomain,
		},
	)
}

func soulInstanceMintConversationCompletionConflictFromSession(session *models.HostedGenesisSession, reason string) *apptheory.AppTheoryError {
	status := stackDriftUnknown
	if session != nil {
		status = string(hostedgenesis.NormalizeStatus(session.Status))
	}
	return soulInstanceBootstrapError(
		soulInstanceBootstrapCodeConflict,
		soulMintConversationCompleteConflictMessage,
		http.StatusConflict,
		map[string]any{
			soulMintConversationCompleteDetailReason:              strings.TrimSpace(reason),
			soulMintConversationCompleteDetailStatus:              status,
			soulMintConversationCompleteDetailExpectedStatus:      string(hostedgenesis.StatusAssistantTurnReady),
			soulMintConversationCompleteDetailDeclarationsPresent: session != nil && session.DeclarationCheckpoint != nil,
			soulMintConversationCompleteDetailDeclarationsValid:   session != nil && hostedgenesis.CanPublish(hostedgenesis.PublishGateInput{Status: hostedgenesis.NormalizeStatus(session.Status), RegistrationID: session.RegistrationID, ConversationID: session.ConversationID, AgentID: session.AgentID, DeclarationCheckpoint: session.DeclarationCheckpoint}) == nil,
		},
	)
}

func soulInstanceBootstrapErrorFromAppError(appErr *apptheory.AppTheoryError) *apptheory.AppTheoryError {
	if appErr == nil {
		return nil
	}
	switch appErr.Code {
	case appErrCodeUnauthorized:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeUnauthorized, soulInstanceBootstrapMessageUnauthorized, http.StatusUnauthorized, nil)
	case appErrCodeBadRequest:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, appErr.Message, http.StatusBadRequest, nil)
	case appErrCodeForbidden:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeBoundaryViolation, appErr.Message, http.StatusForbidden, nil)
	case soulMintAppErrCodeConflict:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeBoundaryViolation, appErr.Message, http.StatusForbidden, nil)
	case soulMintAppErrCodeNotFound:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeNotFound, appErr.Message, http.StatusNotFound, nil)
	case appErrCodeMicroVMUnavailable:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeMicroVMUnavailable, appErr.Message, http.StatusServiceUnavailable, nil)
	case appErrCodeAssistantTurnFailed:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeAssistantTurnFailed, appErr.Message, http.StatusBadGateway, nil)
	default:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
}

func soulInstanceBootstrapErrorFromError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apptheory.AppTheoryError
	if errors.As(err, &appErr) {
		if isSoulInstanceBootstrapCode(appErr.Code) {
			return appErr
		}
		return soulInstanceBootstrapErrorFromAppError(appErr)
	}
	return err
}

func soulInstanceBootstrapConversationErrorFromAppError(appErr *apptheory.AppTheoryError) *apptheory.AppTheoryError {
	if appErr == nil {
		return nil
	}
	switch appErr.Code {
	case appErrCodeUnauthorized:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeUnauthorized, soulInstanceBootstrapMessageUnauthorized, http.StatusUnauthorized, nil)
	case appErrCodeBadRequest:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, appErr.Message, http.StatusBadRequest, nil)
	case appErrCodeForbidden:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeBoundaryViolation, appErr.Message, http.StatusForbidden, nil)
	case soulMintAppErrCodeConflict:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeConflict, appErr.Message, http.StatusConflict, nil)
	case soulMintAppErrCodeNotFound:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeNotFound, appErr.Message, http.StatusNotFound, nil)
	case appErrCodeMicroVMUnavailable:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeMicroVMUnavailable, appErr.Message, http.StatusServiceUnavailable, nil)
	case appErrCodeAssistantTurnFailed:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeAssistantTurnFailed, appErr.Message, http.StatusBadGateway, nil)
	default:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
}

func soulInstanceBootstrapConversationErrorFromError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apptheory.AppTheoryError
	if errors.As(err, &appErr) {
		if isSoulInstanceBootstrapCode(appErr.Code) {
			return appErr
		}
		return soulInstanceBootstrapConversationErrorFromAppError(appErr)
	}
	return err
}

func isSoulInstanceBootstrapCode(code string) bool {
	return strings.HasPrefix(strings.TrimSpace(code), "soul_instance.")
}

func soulInstanceBootstrapError(code string, message string, status int, details map[string]any) *apptheory.AppTheoryError {
	err := apptheory.NewAppTheoryError(code, message).WithStatusCode(status)
	if len(details) > 0 {
		err = err.WithDetails(details)
	}
	return err
}
