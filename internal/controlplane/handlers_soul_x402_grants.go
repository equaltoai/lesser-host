package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	x402CodeInvalidRequest        = "x402.invalid_request"
	x402CodeUnauthorized          = "x402.unauthorized"
	x402CodeGrantUnavailable      = "x402.grant_unavailable"
	x402CodeGrantRejected         = "x402.grant_rejected"
	x402CodeIdempotencyConflict   = "x402.idempotency_conflict"
	x402CodeInternal              = "x402.internal"
	x402GrantMaximumLifetime      = 24 * time.Hour
	x402GrantMaximumUsage         = 100
	x402HashPrefix                = "sha256:"
	x402DefaultGrantPaymentScheme = models.SoulX402InvocationGrantPaymentSchemeX402
)

type soulX402GrantIssueRequest struct {
	AgentID        string                      `json:"agentId"`
	Capability     string                      `json:"capability"`
	Tool           string                      `json:"tool"`
	Resource       string                      `json:"resource"`
	RequestHash    string                      `json:"requestHash"`
	Caller         soulX402GrantCallerRequest  `json:"caller"`
	Payment        soulX402GrantPaymentRequest `json:"payment"`
	Nonce          string                      `json:"nonce"`
	IdempotencyKey string                      `json:"idempotencyKey"`
	ExpiresAt      string                      `json:"expiresAt"`
	MaxUsage       int                         `json:"maxUsage,omitempty"`
}

type soulX402GrantCallerRequest struct {
	Subject     string `json:"subject,omitempty"`
	SubjectHash string `json:"subjectHash,omitempty"`
}

type soulX402GrantPaymentRequest struct {
	Scheme        string `json:"scheme,omitempty"`
	Network       string `json:"network"`
	Asset         string `json:"asset,omitempty"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency,omitempty"`
	Facilitator   string `json:"facilitator,omitempty"`
	Evidence      string `json:"evidence,omitempty"`
	EvidenceHash  string `json:"evidenceHash,omitempty"`
	PaymentID     string `json:"paymentId,omitempty"`
	PaymentIDHash string `json:"paymentIdHash,omitempty"`
}

type soulX402GrantConsumeRequest struct {
	GrantToken          string `json:"grantToken"`
	AgentID             string `json:"agentId"`
	Capability          string `json:"capability"`
	Tool                string `json:"tool"`
	Resource            string `json:"resource"`
	RequestHash         string `json:"requestHash"`
	IdempotencyKey      string `json:"idempotencyKey"`
	PaymentEvidenceHash string `json:"paymentEvidenceHash"`
}

type validatedSoulX402GrantIssue struct {
	agentIDHex        string
	capability        string
	tool              string
	resource          string
	requestHash       string
	callerSubjectHash string
	payment           soulX402GrantPaymentBinding
	nonce             string
	idempotencyHash   string
	expiresAt         time.Time
	maxUsage          int
	issueRequestHash  string
}

type validatedSoulX402GrantConsume struct {
	grantTokenHash      string
	agentIDHex          string
	capability          string
	tool                string
	resource            string
	requestHash         string
	idempotencyHash     string
	paymentEvidenceHash string
	consumeRequestHash  string
}

type soulX402GrantIssueResponse struct {
	Grant         soulX402InvocationGrantView `json:"grant"`
	Replayed      bool                        `json:"replayed"`
	TokenReturned bool                        `json:"tokenReturned"`
}

type soulX402GrantConsumeResponse struct {
	Accepted bool                             `json:"accepted"`
	Replayed bool                             `json:"replayed"`
	Grant    soulX402InvocationGrantView      `json:"grant"`
	Usage    soulX402InvocationGrantUsageView `json:"usage"`
}

type soulX402InvocationGrantView struct {
	GrantID            string                      `json:"grantId"`
	GrantToken         string                      `json:"grantToken,omitempty"`
	AgentID            string                      `json:"agentId"`
	Capability         string                      `json:"capability"`
	Tool               string                      `json:"tool"`
	Resource           string                      `json:"resource"`
	RequestHash        string                      `json:"requestHash"`
	CallerSubjectHash  string                      `json:"callerSubjectHash"`
	Payment            soulX402GrantPaymentBinding `json:"payment"`
	Nonce              string                      `json:"nonce"`
	IdempotencyKeyHash string                      `json:"idempotencyKeyHash"`
	PolicyVersion      string                      `json:"policyVersion"`
	Authority          string                      `json:"authority"`
	Status             string                      `json:"status"`
	MaxUsage           int                         `json:"maxUsage"`
	UsedCount          int                         `json:"usedCount"`
	IssuedAt           string                      `json:"issuedAt"`
	ExpiresAt          string                      `json:"expiresAt"`
}

type soulX402GrantPaymentBinding struct {
	Scheme                   string `json:"scheme"`
	Network                  string `json:"network"`
	Asset                    string `json:"asset,omitempty"`
	Amount                   string `json:"amount"`
	Currency                 string `json:"currency,omitempty"`
	Facilitator              string `json:"facilitator,omitempty"`
	FacilitatorTrustBoundary string `json:"facilitatorTrustBoundary"`
	EvidenceHash             string `json:"evidenceHash"`
	PaymentIDHash            string `json:"paymentIdHash,omitempty"`
}

type soulX402InvocationGrantUsageView struct {
	Slot               int    `json:"slot"`
	IdempotencyKeyHash string `json:"idempotencyKeyHash"`
	ConsumeRequestHash string `json:"consumeRequestHash"`
	ConsumedAt         string `json:"consumedAt"`
	UsedCount          int    `json:"usedCount"`
	MaxUsage           int    `json:"maxUsage"`
}

func (s *Server) handleSoulX402IssueInvocationGrant(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	if !s.cfg.SoulEnabled {
		return nil, newX402Error(x402CodeGrantUnavailable, "grant unavailable", http.StatusNotFound)
	}

	// Require instance key authentication for grant issuance.
	// Grants represent a payment offer that binds the issuing instance as the payer's
	// representative. Requiring an authenticated instance key prevents unauthenticated
	// callers from issuing unverified grants and provides audit traceability.
	key, authErr := s.requireCommInstanceKey(ctx)
	if authErr != nil {
		return nil, newX402Error(x402CodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	}
	_ = key // key is validated by requireCommInstanceKey; instance slug available for future audit binding.

	now := time.Now().UTC()
	req, appErr := parseSoulX402GrantIssueRequest(ctx, now)
	if appErr != nil {
		return nil, appErr
	}
	policyVersion, appErr := s.soulX402GrantablePolicyVersion(ctx.Context(), req.agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	return s.issueOrReplaySoulX402InvocationGrant(ctx.Context(), req, policyVersion, now)
}

func (s *Server) handleSoulX402ConsumeInvocationGrant(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	if !s.cfg.SoulEnabled {
		return nil, newX402Error(x402CodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	}
	key, authErr := s.requireCommInstanceKey(ctx)
	if authErr != nil {
		return nil, newX402Error(x402CodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	}

	grantID := strings.TrimSpace(ctx.Param("grantId"))
	if grantID == "" || len(grantID) > 128 {
		return nil, newX402Error(x402CodeInvalidRequest, "grantId is invalid", http.StatusBadRequest)
	}
	req, appErr := parseSoulX402GrantConsumeRequest(ctx)
	if appErr != nil {
		return nil, appErr
	}
	grant, appErr := s.loadSoulX402GrantForConsume(ctx.Context(), grantID, req)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.authorizeSoulX402GrantConsumeInstance(ctx.Context(), key, grant); appErr != nil {
		return nil, appErr
	}

	usage, usedCount, replayed, claimErr := s.claimSoulX402InvocationGrantUsage(ctx.Context(), key, grant, req, time.Now().UTC())
	if claimErr != nil {
		return nil, claimErr
	}
	return apptheory.JSON(http.StatusOK, soulX402GrantConsumeResponse{
		Accepted: true,
		Replayed: replayed,
		Grant:    soulX402GrantView(grant, "", usedCount),
		Usage:    soulX402UsageView(usage, usedCount, grant.MaxUsage),
	})
}

func (s *Server) soulX402GrantablePolicyVersion(ctx context.Context, agentID string) (string, *apptheory.AppTheoryError) {
	identity, err := s.getSoulAgentIdentity(ctx, agentID)
	if err != nil {
		if theoryErrors.IsNotFound(err) {
			return "", x402GrantUnavailableError()
		}
		return "", newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	if !soulX402IdentityGrantable(identity) {
		return "", x402GrantUnavailableError()
	}
	policy := effectiveSoulAgentPolicy(identity)
	if strings.TrimSpace(policy.CallerAccessPayment.PublicPaidCaller.Access) != models.SoulPublicPaidCallerAccessGrantable {
		return "", x402GrantUnavailableError()
	}
	return policy.CallerAccessPaymentPolicyVersion, nil
}

func (s *Server) issueOrReplaySoulX402InvocationGrant(ctx context.Context, req validatedSoulX402GrantIssue, policyVersion string, now time.Time) (*apptheory.Response, error) {
	grantToken, tokenErr := generateRandomSecret(32)
	if tokenErr != nil {
		return nil, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	grant := soulX402GrantFromIssue(req, grantToken, policyVersion, now)
	createErr := s.store.DB.WithContext(ctx).Model(grant).IfNotExists().Create()
	if createErr == nil {
		return apptheory.JSON(http.StatusOK, soulX402GrantIssueResponse{
			Grant:         soulX402GrantView(grant, grantToken, 0),
			Replayed:      false,
			TokenReturned: true,
		})
	}
	if !theoryErrors.IsConditionFailed(createErr) {
		return nil, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	return s.replaySoulX402InvocationGrant(ctx, grant)
}

func (s *Server) replaySoulX402InvocationGrant(ctx context.Context, grant *models.SoulX402InvocationGrant) (*apptheory.Response, error) {
	existing, err := s.getSoulX402InvocationGrant(ctx, grant.GrantID)
	if err != nil {
		return nil, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	if strings.TrimSpace(existing.IssueRequestHash) != strings.TrimSpace(grant.IssueRequestHash) {
		return nil, newX402Error(x402CodeIdempotencyConflict, "idempotency key already used for a different grant request", http.StatusConflict)
	}
	usedCount, countErr := s.countSoulX402InvocationGrantUsages(ctx, existing)
	if countErr != nil {
		return nil, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	return apptheory.JSON(http.StatusOK, soulX402GrantIssueResponse{
		Grant:         soulX402GrantView(existing, "", usedCount),
		Replayed:      true,
		TokenReturned: false,
	})
}

func (s *Server) loadSoulX402GrantForConsume(ctx context.Context, grantID string, req validatedSoulX402GrantConsume) (*models.SoulX402InvocationGrant, *apptheory.AppTheoryError) {
	grant, err := s.getSoulX402InvocationGrant(ctx, grantID)
	if theoryErrors.IsNotFound(err) {
		return nil, newX402Error(x402CodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	}
	if err != nil {
		return nil, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	if appErr := validateSoulX402GrantForConsume(grant, req, time.Now().UTC()); appErr != nil {
		return nil, appErr
	}
	return grant, nil
}

func (s *Server) authorizeSoulX402GrantConsumeInstance(ctx context.Context, key *models.InstanceKey, grant *models.SoulX402InvocationGrant) *apptheory.AppTheoryError {
	identity, err := s.getSoulAgentIdentity(ctx, grant.AgentID)
	if theoryErrors.IsNotFound(err) {
		return newX402Error(x402CodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	}
	if err != nil {
		return newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	if !soulX402IdentityActive(identity) {
		return newX402Error(x402CodeGrantRejected, "grant rejected", http.StatusForbidden)
	}
	if accessErr := s.requireCommAgentInstanceAccess(ctx, key, identity); accessErr != nil {
		return newX402Error(x402CodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	}
	return nil
}

func parseSoulX402GrantIssueRequest(ctx *apptheory.Context, now time.Time) (validatedSoulX402GrantIssue, *apptheory.AppTheoryError) {
	var raw soulX402GrantIssueRequest
	if err := httpx.ParseJSON(ctx, &raw); err != nil {
		return validatedSoulX402GrantIssue{}, newX402Error(x402CodeInvalidRequest, "invalid request", http.StatusBadRequest)
	}
	agentID, appErr := normalizeX402AgentID(raw.AgentID)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	capability, appErr := normalizeX402TokenField("capability", raw.Capability, 128)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	tool, appErr := normalizeX402TokenField("tool", raw.Tool, 128)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	resource, appErr := normalizeX402StringField("resource", raw.Resource, 512, true)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	requestHash, appErr := normalizeX402Hash("requestHash", raw.RequestHash, true)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	callerSubjectHash, appErr := normalizeX402SensitiveHash("caller.subject", raw.Caller.Subject, raw.Caller.SubjectHash, true)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	payment, appErr := normalizeX402PaymentBinding(raw.Payment)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	nonce, appErr := normalizeX402StringField("nonce", raw.Nonce, 256, true)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	idempotencyKey, appErr := normalizeX402StringField("idempotencyKey", raw.IdempotencyKey, 256, true)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	expiresAt, appErr := normalizeX402ExpiresAt(raw.ExpiresAt, now)
	if appErr != nil {
		return validatedSoulX402GrantIssue{}, appErr
	}
	maxUsage := raw.MaxUsage
	if maxUsage == 0 {
		maxUsage = 1
	}
	if maxUsage < 1 || maxUsage > x402GrantMaximumUsage {
		return validatedSoulX402GrantIssue{}, newX402Error(x402CodeInvalidRequest, "maxUsage is invalid", http.StatusBadRequest)
	}

	out := validatedSoulX402GrantIssue{
		agentIDHex:        agentID,
		capability:        capability,
		tool:              tool,
		resource:          resource,
		requestHash:       requestHash,
		callerSubjectHash: callerSubjectHash,
		payment:           payment,
		nonce:             nonce,
		idempotencyHash:   sha256HexTrimmed(idempotencyKey),
		expiresAt:         expiresAt,
		maxUsage:          maxUsage,
	}
	out.issueRequestHash = soulX402IssueRequestHash(out)
	return out, nil
}

func parseSoulX402GrantConsumeRequest(ctx *apptheory.Context) (validatedSoulX402GrantConsume, *apptheory.AppTheoryError) {
	var raw soulX402GrantConsumeRequest
	if err := httpx.ParseJSON(ctx, &raw); err != nil {
		return validatedSoulX402GrantConsume{}, newX402Error(x402CodeInvalidRequest, "invalid request", http.StatusBadRequest)
	}
	token, appErr := normalizeX402StringField("grantToken", raw.GrantToken, 512, true)
	if appErr != nil {
		return validatedSoulX402GrantConsume{}, appErr
	}
	agentID, appErr := normalizeX402AgentID(raw.AgentID)
	if appErr != nil {
		return validatedSoulX402GrantConsume{}, appErr
	}
	capability, appErr := normalizeX402TokenField("capability", raw.Capability, 128)
	if appErr != nil {
		return validatedSoulX402GrantConsume{}, appErr
	}
	tool, appErr := normalizeX402TokenField("tool", raw.Tool, 128)
	if appErr != nil {
		return validatedSoulX402GrantConsume{}, appErr
	}
	resource, appErr := normalizeX402StringField("resource", raw.Resource, 512, true)
	if appErr != nil {
		return validatedSoulX402GrantConsume{}, appErr
	}
	requestHash, appErr := normalizeX402Hash("requestHash", raw.RequestHash, true)
	if appErr != nil {
		return validatedSoulX402GrantConsume{}, appErr
	}
	idempotencyKey, appErr := normalizeX402StringField("idempotencyKey", raw.IdempotencyKey, 256, true)
	if appErr != nil {
		return validatedSoulX402GrantConsume{}, appErr
	}
	paymentEvidenceHash, appErr := normalizeX402Hash("paymentEvidenceHash", raw.PaymentEvidenceHash, true)
	if appErr != nil {
		return validatedSoulX402GrantConsume{}, appErr
	}
	out := validatedSoulX402GrantConsume{
		grantTokenHash:      sha256HexTrimmed(token),
		agentIDHex:          agentID,
		capability:          capability,
		tool:                tool,
		resource:            resource,
		requestHash:         requestHash,
		idempotencyHash:     sha256HexTrimmed(idempotencyKey),
		paymentEvidenceHash: paymentEvidenceHash,
	}
	out.consumeRequestHash = soulX402ConsumeRequestHash(out)
	return out, nil
}

func normalizeX402PaymentBinding(raw soulX402GrantPaymentRequest) (soulX402GrantPaymentBinding, *apptheory.AppTheoryError) {
	scheme := strings.ToLower(strings.TrimSpace(raw.Scheme))
	if scheme == "" {
		scheme = x402DefaultGrantPaymentScheme
	}
	if scheme != models.SoulX402InvocationGrantPaymentSchemeX402 {
		return soulX402GrantPaymentBinding{}, newX402Error(x402CodeInvalidRequest, "payment.scheme is invalid", http.StatusBadRequest)
	}
	network, appErr := normalizeX402TokenField("payment.network", raw.Network, 128)
	if appErr != nil {
		return soulX402GrantPaymentBinding{}, appErr
	}
	asset, appErr := normalizeX402OptionalTokenField("payment.asset", raw.Asset, 128)
	if appErr != nil {
		return soulX402GrantPaymentBinding{}, appErr
	}
	amount, appErr := normalizeX402StringField("payment.amount", raw.Amount, 128, true)
	if appErr != nil {
		return soulX402GrantPaymentBinding{}, appErr
	}
	currency, appErr := normalizeX402OptionalCurrency("payment.currency", raw.Currency)
	if appErr != nil {
		return soulX402GrantPaymentBinding{}, appErr
	}
	facilitator, appErr := normalizeX402StringField("payment.facilitator", raw.Facilitator, 512, false)
	if appErr != nil {
		return soulX402GrantPaymentBinding{}, appErr
	}
	evidenceHash, appErr := normalizeX402SensitiveHash("payment.evidence", raw.Evidence, raw.EvidenceHash, true)
	if appErr != nil {
		return soulX402GrantPaymentBinding{}, appErr
	}
	paymentIDHash, appErr := normalizeX402SensitiveHash("payment.paymentId", raw.PaymentID, raw.PaymentIDHash, false)
	if appErr != nil {
		return soulX402GrantPaymentBinding{}, appErr
	}
	return soulX402GrantPaymentBinding{
		Scheme:                   scheme,
		Network:                  network,
		Asset:                    asset,
		Amount:                   amount,
		Currency:                 currency,
		Facilitator:              facilitator,
		FacilitatorTrustBoundary: models.SoulX402InvocationGrantFacilitatorTrustCallerProvided,
		EvidenceHash:             evidenceHash,
		PaymentIDHash:            paymentIDHash,
	}, nil
}

func normalizeX402AgentID(raw string) (string, *apptheory.AppTheoryError) {
	agentID, _, appErr := parseSoulAgentIDHex(raw)
	if appErr != nil {
		return "", newX402Error(x402CodeInvalidRequest, "agentId is invalid", http.StatusBadRequest)
	}
	return agentID, nil
}

func normalizeX402TokenField(field string, raw string, maxLen int) (string, *apptheory.AppTheoryError) {
	value, appErr := normalizeX402StringField(field, raw, maxLen, true)
	if appErr != nil {
		return "", appErr
	}
	return strings.ToLower(value), nil
}

func normalizeX402OptionalTokenField(field string, raw string, maxLen int) (string, *apptheory.AppTheoryError) {
	value, appErr := normalizeX402StringField(field, raw, maxLen, false)
	if appErr != nil {
		return "", appErr
	}
	return strings.ToLower(value), nil
}

func normalizeX402OptionalCurrency(field string, raw string) (string, *apptheory.AppTheoryError) {
	value, appErr := normalizeX402StringField(field, raw, 32, false)
	if appErr != nil {
		return "", appErr
	}
	return strings.ToUpper(value), nil
}

func normalizeX402StringField(field string, raw string, maxLen int, required bool) (string, *apptheory.AppTheoryError) {
	value := strings.TrimSpace(raw)
	if value == "" {
		if required {
			return "", newX402Error(x402CodeInvalidRequest, field+" is required", http.StatusBadRequest)
		}
		return "", nil
	}
	if maxLen > 0 && len(value) > maxLen {
		return "", newX402Error(x402CodeInvalidRequest, field+" is invalid", http.StatusBadRequest)
	}
	return value, nil
}

func normalizeX402Hash(field string, raw string, required bool) (string, *apptheory.AppTheoryError) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, x402HashPrefix)
	if value == "" {
		if required {
			return "", newX402Error(x402CodeInvalidRequest, field+" is required", http.StatusBadRequest)
		}
		return "", nil
	}
	if len(value) != 64 {
		return "", newX402Error(x402CodeInvalidRequest, field+" is invalid", http.StatusBadRequest)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", newX402Error(x402CodeInvalidRequest, field+" is invalid", http.StatusBadRequest)
	}
	return value, nil
}

func normalizeX402SensitiveHash(field string, raw string, rawHash string, required bool) (string, *apptheory.AppTheoryError) {
	computed := sha256HexTrimmed(raw)
	normalized, appErr := normalizeX402Hash(field+"Hash", rawHash, false)
	if appErr != nil {
		return "", appErr
	}
	if computed == "" && normalized == "" {
		if required {
			return "", newX402Error(x402CodeInvalidRequest, field+" is required", http.StatusBadRequest)
		}
		return "", nil
	}
	if computed != "" && normalized != "" && computed != normalized {
		return "", newX402Error(x402CodeInvalidRequest, field+"Hash does not match", http.StatusBadRequest)
	}
	if normalized != "" {
		return normalized, nil
	}
	return computed, nil
}

func normalizeX402ExpiresAt(raw string, now time.Time) (time.Time, *apptheory.AppTheoryError) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, newX402Error(x402CodeInvalidRequest, "expiresAt is required", http.StatusBadRequest)
	}
	expiresAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, newX402Error(x402CodeInvalidRequest, "expiresAt is invalid", http.StatusBadRequest)
	}
	expiresAt = expiresAt.UTC()
	if !expiresAt.After(now) {
		return time.Time{}, newX402Error(x402CodeInvalidRequest, "expiresAt must be in the future", http.StatusBadRequest)
	}
	if expiresAt.After(now.Add(x402GrantMaximumLifetime)) {
		return time.Time{}, newX402Error(x402CodeInvalidRequest, "expiresAt exceeds maximum lifetime", http.StatusBadRequest)
	}
	return expiresAt, nil
}

func soulX402IdentityActive(identity *models.SoulAgentIdentity) bool {
	if identity == nil {
		return false
	}
	status := strings.TrimSpace(identity.LifecycleStatus)
	if status == "" {
		status = strings.TrimSpace(identity.Status)
	}
	return status == models.SoulAgentStatusActive
}

func soulX402IdentityGrantable(identity *models.SoulAgentIdentity) bool {
	return soulX402IdentityActive(identity)
}

func soulX402GrantFromIssue(req validatedSoulX402GrantIssue, grantToken string, policyVersion string, now time.Time) *models.SoulX402InvocationGrant {
	grant := &models.SoulX402InvocationGrant{
		GrantID:                         models.SoulX402InvocationGrantID(req.agentIDHex, req.idempotencyHash),
		AgentID:                         req.agentIDHex,
		Capability:                      req.capability,
		Tool:                            req.tool,
		Resource:                        req.resource,
		RequestHash:                     req.requestHash,
		CallerSubjectHash:               req.callerSubjectHash,
		PaymentScheme:                   req.payment.Scheme,
		PaymentNetwork:                  req.payment.Network,
		PaymentAsset:                    req.payment.Asset,
		PaymentAmount:                   req.payment.Amount,
		PaymentCurrency:                 req.payment.Currency,
		PaymentFacilitator:              req.payment.Facilitator,
		PaymentFacilitatorTrustBoundary: req.payment.FacilitatorTrustBoundary,
		PaymentEvidenceHash:             req.payment.EvidenceHash,
		PaymentIDHash:                   req.payment.PaymentIDHash,
		Nonce:                           req.nonce,
		IdempotencyKeyHash:              req.idempotencyHash,
		IssueRequestHash:                req.issueRequestHash,
		GrantTokenHash:                  sha256HexTrimmed(grantToken),
		PolicyVersion:                   strings.TrimSpace(policyVersion),
		Authority:                       models.SoulX402InvocationGrantAuthorityScopedInvocation,
		Status:                          models.SoulX402InvocationGrantStatusIssued,
		MaxUsage:                        req.maxUsage,
		IssuedAt:                        now,
		ExpiresAt:                       req.expiresAt,
		UpdatedAt:                       now,
	}
	_ = grant.UpdateKeys()
	return grant
}

func (s *Server) getSoulX402InvocationGrant(ctx context.Context, grantID string) (*models.SoulX402InvocationGrant, error) {
	rec := &models.SoulX402InvocationGrant{GrantID: grantID}
	_ = rec.UpdateKeys()
	var item models.SoulX402InvocationGrant
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulX402InvocationGrant{}).
		Where("PK", "=", rec.PK).
		Where("SK", "=", rec.SK).
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Server) listSoulX402InvocationGrantUsages(ctx context.Context, grantID string, limit int) ([]*models.SoulX402InvocationGrantUsage, error) {
	if limit <= 0 || limit > x402GrantMaximumUsage {
		limit = x402GrantMaximumUsage
	}
	var items []*models.SoulX402InvocationGrantUsage
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulX402InvocationGrantUsage{}).
		Where("PK", "=", models.SoulX402InvocationGrantPK(grantID)).
		Where("SK", "BEGINS_WITH", "USAGE#").
		OrderBy("SK", "ASC").
		Limit(limit).
		ConsistentRead().
		All(&items)
	return items, err
}

func (s *Server) countSoulX402InvocationGrantUsages(ctx context.Context, grant *models.SoulX402InvocationGrant) (int, error) {
	if grant == nil {
		return 0, nil
	}
	items, err := s.listSoulX402InvocationGrantUsages(ctx, grant.GrantID, grant.MaxUsage)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func validateSoulX402GrantForConsume(grant *models.SoulX402InvocationGrant, req validatedSoulX402GrantConsume, now time.Time) *apptheory.AppTheoryError {
	if grant == nil {
		return newX402Error(x402CodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	}
	if strings.TrimSpace(grant.GrantTokenHash) == "" || !strings.EqualFold(strings.TrimSpace(grant.GrantTokenHash), req.grantTokenHash) {
		return newX402Error(x402CodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	}
	if strings.TrimSpace(grant.Status) != models.SoulX402InvocationGrantStatusIssued {
		return newX402Error(x402CodeGrantRejected, "grant rejected", http.StatusForbidden)
	}
	if grant.ExpiresAt.IsZero() || !grant.ExpiresAt.After(now) {
		return newX402Error(x402CodeGrantRejected, "grant expired", http.StatusForbidden)
	}
	if !strings.EqualFold(strings.TrimSpace(grant.AgentID), req.agentIDHex) ||
		!strings.EqualFold(strings.TrimSpace(grant.Capability), req.capability) ||
		!strings.EqualFold(strings.TrimSpace(grant.Tool), req.tool) ||
		strings.TrimSpace(grant.Resource) != strings.TrimSpace(req.resource) ||
		!strings.EqualFold(strings.TrimSpace(grant.RequestHash), req.requestHash) {
		return newX402Error(x402CodeGrantRejected, "grant scope mismatch", http.StatusForbidden)
	}
	if grant.MaxUsage < 1 || grant.MaxUsage > x402GrantMaximumUsage {
		return newX402Error(x402CodeGrantRejected, "grant rejected", http.StatusForbidden)
	}
	if strings.TrimSpace(grant.PaymentEvidenceHash) != req.paymentEvidenceHash {
		return newX402Error(x402CodeGrantRejected, "payment evidence hash mismatch", http.StatusForbidden)
	}
	return nil
}

func (s *Server) claimSoulX402InvocationGrantUsage(ctx context.Context, key *models.InstanceKey, grant *models.SoulX402InvocationGrant, req validatedSoulX402GrantConsume, now time.Time) (*models.SoulX402InvocationGrantUsage, int, bool, *apptheory.AppTheoryError) {
	maxAttempts := grant.MaxUsage + 1
	for attempt := 0; attempt < maxAttempts; attempt++ {
		usage, usedCount, replayed, retry, appErr := s.tryClaimSoulX402InvocationGrantUsage(ctx, key, grant, req, now)
		if appErr != nil || !retry {
			return usage, usedCount, replayed, appErr
		}
	}
	return nil, grant.MaxUsage, false, newX402Error(x402CodeGrantRejected, "grant usage exhausted", http.StatusForbidden)
}

func (s *Server) tryClaimSoulX402InvocationGrantUsage(ctx context.Context, key *models.InstanceKey, grant *models.SoulX402InvocationGrant, req validatedSoulX402GrantConsume, now time.Time) (*models.SoulX402InvocationGrantUsage, int, bool, bool, *apptheory.AppTheoryError) {
	items, occupied, replay, appErr := s.loadSoulX402InvocationGrantUsageSnapshot(ctx, grant, req)
	if appErr != nil {
		return nil, 0, false, false, appErr
	}
	if replay != nil {
		return replay, len(items), true, false, nil
	}
	if len(occupied) >= grant.MaxUsage {
		return nil, len(occupied), false, false, newX402Error(x402CodeGrantRejected, "grant usage exhausted", http.StatusForbidden)
	}

	for _, slot := range availableSoulX402InvocationGrantUsageSlots(grant.MaxUsage, occupied) {
		usage := &models.SoulX402InvocationGrantUsage{
			GrantID:                   grant.GrantID,
			UsageSlot:                 slot,
			AgentID:                   grant.AgentID,
			InstanceSlug:              key.InstanceSlug,
			ConsumeIdempotencyKeyHash: req.idempotencyHash,
			ConsumeRequestHash:        req.consumeRequestHash,
			ConsumedAt:                now,
		}
		if createErr := s.store.DB.WithContext(ctx).Model(usage).IfNotExists().Create(); createErr == nil {
			usedCount := len(occupied) + 1
			_ = s.recordSoulX402InvocationGrantConsumption(ctx, grant, usage, usedCount, now)
			return usage, usedCount, false, false, nil
		} else if theoryErrors.IsConditionFailed(createErr) {
			return nil, len(occupied), false, true, nil
		} else {
			return nil, 0, false, false, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
		}
	}
	return nil, len(occupied), false, false, newX402Error(x402CodeGrantRejected, "grant usage exhausted", http.StatusForbidden)
}

func (s *Server) loadSoulX402InvocationGrantUsageSnapshot(ctx context.Context, grant *models.SoulX402InvocationGrant, req validatedSoulX402GrantConsume) ([]*models.SoulX402InvocationGrantUsage, map[int]struct{}, *models.SoulX402InvocationGrantUsage, *apptheory.AppTheoryError) {
	items, err := s.listSoulX402InvocationGrantUsages(ctx, grant.GrantID, grant.MaxUsage)
	if err != nil {
		return nil, nil, nil, newX402Error(x402CodeInternal, "internal error", http.StatusInternalServerError)
	}
	occupied := map[int]struct{}{}
	for _, item := range items {
		if item == nil {
			continue
		}
		occupied[item.UsageSlot] = struct{}{}
		if strings.EqualFold(strings.TrimSpace(item.ConsumeIdempotencyKeyHash), req.idempotencyHash) {
			if !strings.EqualFold(strings.TrimSpace(item.ConsumeRequestHash), req.consumeRequestHash) {
				return nil, nil, nil, newX402Error(x402CodeIdempotencyConflict, "idempotency key already used for a different consume request", http.StatusConflict)
			}
			return items, occupied, item, nil
		}
	}
	return items, occupied, nil, nil
}

func availableSoulX402InvocationGrantUsageSlots(maxUsage int, occupied map[int]struct{}) []int {
	slots := make([]int, 0, maxUsage)
	for slot := 0; slot < maxUsage; slot++ {
		if _, ok := occupied[slot]; !ok {
			slots = append(slots, slot)
		}
	}
	sort.Ints(slots)
	return slots
}

func (s *Server) recordSoulX402InvocationGrantConsumption(ctx context.Context, grant *models.SoulX402InvocationGrant, usage *models.SoulX402InvocationGrantUsage, usedCount int, now time.Time) error {
	if s == nil || s.store == nil || s.store.DB == nil || grant == nil || usage == nil {
		return nil
	}
	update := &models.SoulX402InvocationGrant{
		GrantID:                         grant.GrantID,
		AgentID:                         grant.AgentID,
		Capability:                      grant.Capability,
		Tool:                            grant.Tool,
		Resource:                        grant.Resource,
		RequestHash:                     grant.RequestHash,
		CallerSubjectHash:               grant.CallerSubjectHash,
		PaymentScheme:                   grant.PaymentScheme,
		PaymentNetwork:                  grant.PaymentNetwork,
		PaymentAsset:                    grant.PaymentAsset,
		PaymentAmount:                   grant.PaymentAmount,
		PaymentCurrency:                 grant.PaymentCurrency,
		PaymentFacilitator:              grant.PaymentFacilitator,
		PaymentFacilitatorTrustBoundary: grant.PaymentFacilitatorTrustBoundary,
		PaymentEvidenceHash:             grant.PaymentEvidenceHash,
		PaymentIDHash:                   grant.PaymentIDHash,
		Nonce:                           grant.Nonce,
		IdempotencyKeyHash:              grant.IdempotencyKeyHash,
		IssueRequestHash:                grant.IssueRequestHash,
		GrantTokenHash:                  grant.GrantTokenHash,
		PolicyVersion:                   grant.PolicyVersion,
		Authority:                       grant.Authority,
		Status:                          grant.Status,
		MaxUsage:                        grant.MaxUsage,
		ConsumedUsageCount:              usedCount,
		LatestConsumeSlot:               usage.UsageSlot,
		LatestConsumeRequest:            usage.ConsumeRequestHash,
		IssuedAt:                        grant.IssuedAt,
		ExpiresAt:                       grant.ExpiresAt,
		UpdatedAt:                       now,
		LastConsumedAt:                  now,
	}
	_ = update.UpdateKeys()
	return s.store.DB.WithContext(ctx).Model(update).IfExists().Update("ConsumedUsageCount", "LatestConsumeSlot", "LatestConsumeRequest", "LastConsumedAt", "UpdatedAt")
}

func soulX402GrantView(grant *models.SoulX402InvocationGrant, grantToken string, usedCount int) soulX402InvocationGrantView {
	if grant == nil {
		return soulX402InvocationGrantView{}
	}
	if usedCount < 0 {
		usedCount = 0
	}
	if grant.ConsumedUsageCount > usedCount {
		usedCount = grant.ConsumedUsageCount
	}
	return soulX402InvocationGrantView{
		GrantID:           strings.TrimSpace(grant.GrantID),
		GrantToken:        strings.TrimSpace(grantToken),
		AgentID:           strings.ToLower(strings.TrimSpace(grant.AgentID)),
		Capability:        strings.TrimSpace(grant.Capability),
		Tool:              strings.TrimSpace(grant.Tool),
		Resource:          strings.TrimSpace(grant.Resource),
		RequestHash:       strings.TrimSpace(grant.RequestHash),
		CallerSubjectHash: strings.TrimSpace(grant.CallerSubjectHash),
		Payment: soulX402GrantPaymentBinding{
			Scheme:                   strings.TrimSpace(grant.PaymentScheme),
			Network:                  strings.TrimSpace(grant.PaymentNetwork),
			Asset:                    strings.TrimSpace(grant.PaymentAsset),
			Amount:                   strings.TrimSpace(grant.PaymentAmount),
			Currency:                 strings.TrimSpace(grant.PaymentCurrency),
			Facilitator:              strings.TrimSpace(grant.PaymentFacilitator),
			FacilitatorTrustBoundary: strings.TrimSpace(grant.PaymentFacilitatorTrustBoundary),
			EvidenceHash:             strings.TrimSpace(grant.PaymentEvidenceHash),
			PaymentIDHash:            strings.TrimSpace(grant.PaymentIDHash),
		},
		Nonce:              strings.TrimSpace(grant.Nonce),
		IdempotencyKeyHash: strings.TrimSpace(grant.IdempotencyKeyHash),
		PolicyVersion:      strings.TrimSpace(grant.PolicyVersion),
		Authority:          strings.TrimSpace(grant.Authority),
		Status:             strings.TrimSpace(grant.Status),
		MaxUsage:           grant.MaxUsage,
		UsedCount:          usedCount,
		IssuedAt:           grant.IssuedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:          grant.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
}

func soulX402UsageView(usage *models.SoulX402InvocationGrantUsage, usedCount int, maxUsage int) soulX402InvocationGrantUsageView {
	if usage == nil {
		return soulX402InvocationGrantUsageView{UsedCount: usedCount, MaxUsage: maxUsage}
	}
	return soulX402InvocationGrantUsageView{
		Slot:               usage.UsageSlot,
		IdempotencyKeyHash: strings.TrimSpace(usage.ConsumeIdempotencyKeyHash),
		ConsumeRequestHash: strings.TrimSpace(usage.ConsumeRequestHash),
		ConsumedAt:         usage.ConsumedAt.UTC().Format(time.RFC3339Nano),
		UsedCount:          usedCount,
		MaxUsage:           maxUsage,
	}
}

func soulX402IssueRequestHash(req validatedSoulX402GrantIssue) string {
	payload := struct {
		AgentID           string                      `json:"agentId"`
		Capability        string                      `json:"capability"`
		Tool              string                      `json:"tool"`
		Resource          string                      `json:"resource"`
		RequestHash       string                      `json:"requestHash"`
		CallerSubjectHash string                      `json:"callerSubjectHash"`
		Payment           soulX402GrantPaymentBinding `json:"payment"`
		Nonce             string                      `json:"nonce"`
		IdempotencyHash   string                      `json:"idempotencyKeyHash"`
		ExpiresAt         string                      `json:"expiresAt"`
		MaxUsage          int                         `json:"maxUsage"`
	}{
		AgentID:           req.agentIDHex,
		Capability:        req.capability,
		Tool:              req.tool,
		Resource:          req.resource,
		RequestHash:       req.requestHash,
		CallerSubjectHash: req.callerSubjectHash,
		Payment:           req.payment,
		Nonce:             req.nonce,
		IdempotencyHash:   req.idempotencyHash,
		ExpiresAt:         req.expiresAt.UTC().Format(time.RFC3339Nano),
		MaxUsage:          req.maxUsage,
	}
	return hashX402JSON(payload)
}

func soulX402ConsumeRequestHash(req validatedSoulX402GrantConsume) string {
	payload := struct {
		AgentID             string `json:"agentId"`
		Capability          string `json:"capability"`
		Tool                string `json:"tool"`
		Resource            string `json:"resource"`
		RequestHash         string `json:"requestHash"`
		GrantTokenHash      string `json:"grantTokenHash"`
		IdempotencyHash     string `json:"idempotencyKeyHash"`
		PaymentEvidenceHash string `json:"paymentEvidenceHash"`
	}{
		AgentID:             req.agentIDHex,
		Capability:          req.capability,
		Tool:                req.tool,
		Resource:            req.resource,
		RequestHash:         req.requestHash,
		GrantTokenHash:      req.grantTokenHash,
		IdempotencyHash:     req.idempotencyHash,
		PaymentEvidenceHash: req.paymentEvidenceHash,
	}
	return hashX402JSON(payload)
}

func hashX402JSON(payload any) string {
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func newX402Error(code string, message string, status int) *apptheory.AppTheoryError {
	return apptheory.NewAppTheoryError(code, message).WithStatusCode(status)
}

func x402GrantUnavailableError() *apptheory.AppTheoryError {
	return newX402Error(x402CodeGrantUnavailable, "grant unavailable", http.StatusNotFound)
}
