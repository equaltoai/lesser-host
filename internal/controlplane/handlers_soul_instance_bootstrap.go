package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulInstanceBootstrapCodeUnauthorized       = "soul_instance.unauthorized"
	soulInstanceBootstrapCodeInvalidRequest     = "soul_instance.invalid_request"
	soulInstanceBootstrapCodeBoundaryViolation  = "soul_instance.boundary_violation"
	soulInstanceBootstrapCodeNotFound           = "soul_instance.not_found"
	soulInstanceBootstrapCodeNotImplemented     = "soul_instance.not_implemented"
	soulInstanceBootstrapCodeInternal           = "soul_instance.internal"
	soulInstanceBootstrapMessageUnauthorized    = "unauthorized"
	soulInstanceBootstrapMessageBoundary        = "resource is outside the authenticated instance boundary"
	soulInstanceBootstrapBoundaryInstanceDomain = "instance_domain"

	soulInstanceBootstrapRouteRegisterBegin        = "register_begin"
	soulInstanceBootstrapRoutePrincipalPreflight   = "principal_declaration_preflight"
	soulInstanceBootstrapRouteRegisterVerify       = "register_verify"
	soulInstanceBootstrapRouteConversation         = "mint_conversation"
	soulInstanceBootstrapRouteConversationGet      = "mint_conversation_get"
	soulInstanceBootstrapRouteConversationComplete = "mint_conversation_complete"
	soulInstanceBootstrapRouteFinalizePreflight    = "finalize_preflight"
	soulInstanceBootstrapRouteFinalizeBegin        = "finalize_begin"
	soulInstanceBootstrapRouteFinalize             = "finalize"
)

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
		return nil, err
	}
	domainNormalized, appErr := normalizeSoulInstanceBootstrapDomain(req.Domain)
	if appErr != nil {
		return nil, appErr
	}
	if _, _, appErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, instCtx.instanceSlug, domainNormalized); appErr != nil {
		return nil, appErr
	}

	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRouteRegisterBegin)
}

func (s *Server) handleSoulInstanceAgentRegistrationPrincipalDeclarationPreflight(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.requireSoulInstanceBootstrapRegistrationContext(ctx); appErr != nil {
		return nil, appErr
	}
	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRoutePrincipalPreflight)
}

func (s *Server) handleSoulInstanceAgentRegistrationVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.requireSoulInstanceBootstrapRegistrationContext(ctx); appErr != nil {
		return nil, appErr
	}
	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRouteRegisterVerify)
}

func (s *Server) handleSoulInstanceMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.requireSoulInstanceBootstrapRegistrationContext(ctx); appErr != nil {
		return nil, appErr
	}
	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRouteConversation)
}

func (s *Server) handleSoulInstanceGetRegistrationMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx); appErr != nil {
		return nil, appErr
	}
	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRouteConversationGet)
}

func (s *Server) handleSoulInstanceCompleteMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx); appErr != nil {
		return nil, appErr
	}
	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRouteConversationComplete)
}

func (s *Server) handleSoulInstanceFinalizeMintConversationPreflight(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx); appErr != nil {
		return nil, appErr
	}
	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRouteFinalizePreflight)
}

func (s *Server) handleSoulInstanceBeginFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx); appErr != nil {
		return nil, appErr
	}
	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRouteFinalizeBegin)
}

func (s *Server) handleSoulInstanceFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx); appErr != nil {
		return nil, appErr
	}
	return nil, soulInstanceBootstrapScaffoldError(soulInstanceBootstrapRouteFinalize)
}

func (s *Server) requireSoulInstanceBootstrapContext(ctx *apptheory.Context) (soulInstanceBootstrapContext, *apptheory.AppTheoryError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return soulInstanceBootstrapContext{}, soulInstanceBootstrapErrorFromAppError(appErr)
	}
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
	if err != nil {
		return soulInstanceBootstrapConversationContext{}, soulInstanceBootstrapError(soulInstanceBootstrapCodeNotFound, "conversation not found", http.StatusNotFound, nil)
	}
	return soulInstanceBootstrapConversationContext{
		soulInstanceBootstrapRegistrationContext: regCtx,
		conv:                                     conv,
		conversationID:                           conversationID,
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

func soulInstanceBootstrapScaffoldError(route string) *apptheory.AppTheoryError {
	return soulInstanceBootstrapError(
		soulInstanceBootstrapCodeNotImplemented,
		"instance-key soul bootstrap route is scaffolded but not implemented",
		http.StatusNotImplemented,
		map[string]any{"route": strings.TrimSpace(route)},
	)
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

func soulInstanceBootstrapErrorFromAppError(appErr *apptheory.AppError) *apptheory.AppTheoryError {
	if appErr == nil {
		return nil
	}
	switch appErr.Code {
	case appErrCodeUnauthorized:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeUnauthorized, soulInstanceBootstrapMessageUnauthorized, http.StatusUnauthorized, nil)
	case appErrCodeBadRequest:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, appErr.Message, http.StatusBadRequest, nil)
	case soulMintAppErrCodeConflict:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeBoundaryViolation, appErr.Message, http.StatusForbidden, nil)
	default:
		return soulInstanceBootstrapError(soulInstanceBootstrapCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
}

func soulInstanceBootstrapError(code string, message string, status int, details map[string]any) *apptheory.AppTheoryError {
	err := apptheory.NewAppTheoryError(code, message).WithStatusCode(status)
	if len(details) > 0 {
		err = err.WithDetails(details)
	}
	return err
}
