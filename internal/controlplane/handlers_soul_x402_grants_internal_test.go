package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	x402GrantTestRequestHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	x402GrantTestToken       = "raw-grant-token"
)

type soulX402GrantTestDB struct {
	db        *ttmocks.MockExtendedDB
	qKey      *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qIdentity *ttmocks.MockQuery
	qGrant    *ttmocks.MockQuery
	qUsage    *ttmocks.MockQuery
}

func newSoulX402GrantTestDB() soulX402GrantTestDB {
	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qGrant := new(ttmocks.MockQuery)
	qUsage := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulX402InvocationGrant")).Return(qGrant).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulX402InvocationGrantUsage")).Return(qUsage).Maybe()

	for _, q := range []*ttmocks.MockQuery{qKey, qDomain, qIdentity, qGrant, qUsage} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}
	return soulX402GrantTestDB{db: db, qKey: qKey, qDomain: qDomain, qIdentity: qIdentity, qGrant: qGrant, qUsage: qUsage}
}

func x402IssueBody(t *testing.T, overrides map[string]any) []byte {
	t.Helper()
	body := map[string]any{
		"agentId":           soulLifecycleTestAgentIDHex,
		"capabilityVersion": models.SoulX402InvocationGrantCapabilityVocabularyScopedV1,
		"capability":        "tools.invoke",
		"tool":              "summarize",
		"resource":          "mcp://agent/summarize",
		"scope":             "write",
		"requestHash":       x402GrantTestRequestHash,
		"caller":            map[string]any{"subject": "did:example:caller-1"},
		"payment":           map[string]any{"network": "base-sepolia", "asset": "usdc", "amount": "1000", "currency": "usd", "facilitator": "https://facilitator.example", "evidence": "raw-payment-evidence", "paymentId": "payment-1"},
		"nonce":             "nonce-1",
		"idempotencyKey":    "issue-idem-1",
		"expiresAt":         time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"maxUsage":          1,
	}
	for key, value := range overrides {
		if value == nil {
			delete(body, key)
			continue
		}
		body[key] = value
	}
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func x402ConsumeBody(t *testing.T, grantToken string, overrides map[string]any) []byte {
	t.Helper()
	body := map[string]any{
		"grantToken":          grantToken,
		"agentId":             soulLifecycleTestAgentIDHex,
		"capabilityVersion":   models.SoulX402InvocationGrantCapabilityVocabularyScopedV1,
		"capability":          "tools.invoke",
		"tool":                "summarize",
		"resource":            "mcp://agent/summarize",
		"requestHash":         x402GrantTestRequestHash,
		"idempotencyKey":      "consume-idem-1",
		"paymentEvidenceHash": "sha256:" + sha256HexTrimmed("raw-payment-evidence"),
	}
	for key, value := range overrides {
		body[key] = value
	}
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func x402StoredGrant(grantToken string) models.SoulX402InvocationGrant {
	grant := models.SoulX402InvocationGrant{
		GrantID:                         "x402-grant-1",
		AgentID:                         soulLifecycleTestAgentIDHex,
		CapabilityVersion:               models.SoulX402InvocationGrantCapabilityVocabularyScopedV1,
		Capability:                      "tools.invoke",
		Tool:                            "summarize",
		Resource:                        "mcp://agent/summarize",
		Scope:                           models.SoulX402InvocationGrantScopeWrite,
		RequestHash:                     x402GrantTestRequestHash,
		CallerSubjectHash:               sha256HexTrimmed("did:example:caller-1"),
		PaymentScheme:                   models.SoulX402InvocationGrantPaymentSchemeX402,
		PaymentNetwork:                  "base-sepolia",
		PaymentAsset:                    "usdc",
		PaymentAmount:                   "1000",
		PaymentFacilitatorTrustBoundary: models.SoulX402InvocationGrantFacilitatorTrustCallerProvided,
		PaymentEvidenceHash:             sha256HexTrimmed("raw-payment-evidence"),
		Nonce:                           "nonce-1",
		IdempotencyKeyHash:              sha256HexTrimmed("issue-idem-1"),
		IssueRequestHash:                strings.Repeat("b", 64),
		GrantTokenHash:                  sha256HexTrimmed(grantToken),
		PolicyVersion:                   models.SoulCallerAccessPaymentPolicyVersionV1,
		Authority:                       models.SoulX402InvocationGrantAuthorityScopedInvocation,
		Status:                          models.SoulX402InvocationGrantStatusIssued,
		MaxUsage:                        1,
		IssuedAt:                        time.Now().Add(-time.Minute).UTC(),
		ExpiresAt:                       time.Now().Add(time.Hour).UTC(),
	}
	_ = grant.UpdateKeys()
	return grant
}

func expectX402Identity(t *testing.T, q *ttmocks.MockQuery, access string) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:                          soulLifecycleTestAgentIDHex,
			Domain:                           "example.com",
			LocalID:                          "agent-alice",
			Status:                           models.SoulAgentStatusActive,
			LifecycleStatus:                  models.SoulAgentStatusActive,
			PolicyVersion:                    models.SoulPolicyVersionHostedBoundSoulV1,
			AnchorState:                      models.SoulAnchorStateHostedOffchain,
			CallerAccessPaymentPolicyVersion: models.SoulCallerAccessPaymentPolicyVersionV1,
			PublicPaidCallerAccess:           access,
			UpdatedAt:                        time.Now().UTC(),
		}
	}).Once()
}

func expectX402Grant(t *testing.T, q *ttmocks.MockQuery, grant models.SoulX402InvocationGrant) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulX402InvocationGrant")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulX402InvocationGrant](t, args, 0)
		*dest = grant
	}).Once()
}

func expectX402GrantCreateCapture(t *testing.T, tdb soulX402GrantTestDB) **models.SoulX402InvocationGrant {
	t.Helper()
	var captured *models.SoulX402InvocationGrant
	tdb.db.ExpectedCalls = nil
	tdb.db.On("WithContext", mock.Anything).Return(tdb.db).Maybe()
	tdb.db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(tdb.qKey).Maybe()
	tdb.db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(tdb.qIdentity).Maybe()
	tdb.db.On("Model", mock.AnythingOfType("*models.SoulX402InvocationGrant")).Return(tdb.qGrant).Run(func(args mock.Arguments) {
		grant, ok := args.Get(0).(*models.SoulX402InvocationGrant)
		if ok && strings.TrimSpace(grant.GrantID) != "" {
			captured = grant
		}
	}).Maybe()
	tdb.db.On("Model", mock.AnythingOfType("*models.SoulX402InvocationGrantUsage")).Return(tdb.qUsage).Maybe()
	tdb.qGrant.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qGrant).Maybe()
	tdb.qGrant.On("IfNotExists").Return(tdb.qGrant).Maybe()
	tdb.qGrant.On("Create").Return(nil).Once()
	return &captured
}

func requireX402IssueResponse(t *testing.T, resp *apptheory.Response, err error) soulX402GrantIssueResponse {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Status, string(resp.Body))
	}
	var out soulX402GrantIssueResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func requireX402ConsumeResponse(t *testing.T, resp *apptheory.Response, err error) soulX402GrantConsumeResponse {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Status, string(resp.Body))
	}
	var out soulX402GrantConsumeResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func assertX402IssueMinimized(t *testing.T, resp *apptheory.Response, out soulX402GrantIssueResponse, captured *models.SoulX402InvocationGrant) {
	t.Helper()
	if !out.TokenReturned || out.Replayed || strings.TrimSpace(out.Grant.GrantToken) == "" {
		t.Fatalf("expected first response to return one grant token, got %#v", out)
	}
	assertX402IssueResponseDoesNotLeakRawMaterial(t, resp)
	if captured == nil {
		t.Fatalf("expected captured grant")
		return
	}
	assertX402CapturedGrantHashes(t, out, captured)
	assertX402GrantAuthorityAndScope(t, out.Grant, *captured)
	if captured.PolicyVersion != models.SoulCallerAccessPaymentPolicyVersionV1 {
		t.Fatalf("unexpected policy version: %q", captured.PolicyVersion)
	}
}

func assertX402IssueResponseDoesNotLeakRawMaterial(t *testing.T, resp *apptheory.Response) {
	t.Helper()
	for _, forbidden := range []string{"raw-payment-evidence", "payment-1", "did:example:caller-1"} {
		if strings.Contains(string(resp.Body), forbidden) {
			t.Fatalf("response leaked raw caller/payment material %q: %s", forbidden, string(resp.Body))
		}
	}
}

func assertX402CapturedGrantHashes(t *testing.T, out soulX402GrantIssueResponse, captured *models.SoulX402InvocationGrant) {
	t.Helper()
	if captured.PaymentEvidenceHash != sha256HexTrimmed("raw-payment-evidence") {
		t.Fatalf("grant did not store payment evidence hash: %#v", captured)
	}
	if captured.PaymentIDHash != sha256HexTrimmed("payment-1") {
		t.Fatalf("grant did not store payment id hash: %#v", captured)
	}
	if captured.CallerSubjectHash != sha256HexTrimmed("did:example:caller-1") {
		t.Fatalf("grant did not store caller subject hash: %#v", captured)
	}
	if captured.GrantTokenHash != sha256HexTrimmed(out.Grant.GrantToken) {
		t.Fatalf("grant token hash mismatch")
	}
}

func assertX402GrantAuthorityAndScope(t *testing.T, view soulX402InvocationGrantView, captured models.SoulX402InvocationGrant) {
	t.Helper()
	if captured.Authority != models.SoulX402InvocationGrantAuthorityScopedInvocation || view.Authority != models.SoulX402InvocationGrantAuthorityScopedInvocation {
		t.Fatalf("grant authority must stay scoped invocation only: %#v", view)
	}
	if captured.Scope != models.SoulX402InvocationGrantScopeWrite || view.Scope != models.SoulX402InvocationGrantScopeWrite {
		t.Fatalf("grant scope must be stored and returned separately from authority: captured=%q response=%q", captured.Scope, view.Scope)
	}
	if captured.CapabilityVersion != models.SoulX402InvocationGrantCapabilityVocabularyScopedV1 || view.CapabilityVersion != models.SoulX402InvocationGrantCapabilityVocabularyScopedV1 {
		t.Fatalf("grant must carry scoped capability vocabulary version separately from authority: captured=%q response=%q", captured.CapabilityVersion, view.CapabilityVersion)
	}
}

func TestP0_HandleSoulX402IssueInvocationGrant_SuccessMinimizesPaymentEvidence(t *testing.T) {
	t.Parallel()

	tdb := newSoulX402GrantTestDB()
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: sha256HexTrimmed("raw-instance-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)})
	expectX402Identity(t, tdb.qIdentity, models.SoulPublicPaidCallerAccessGrantable)
	captured := expectX402GrantCreateCapture(t, tdb)

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	resp, err := s.handleSoulX402IssueInvocationGrant(&apptheory.Context{Request: apptheory.Request{Body: x402IssueBody(t, nil), Headers: map[string][]string{"authorization": {"Bearer raw-instance-key"}}}})
	out := requireX402IssueResponse(t, resp, err)
	assertX402IssueMinimized(t, resp, out, *captured)
}

func TestHandleSoulX402IssueInvocationGrant_HostedAnchorIsNotCapabilityGate(t *testing.T) {
	t.Parallel()

	tdb := newSoulX402GrantTestDB()
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: sha256HexTrimmed("raw-instance-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)})
	expectX402Identity(t, tdb.qIdentity, models.SoulPublicPaidCallerAccessGrantable)
	captured := expectX402GrantCreateCapture(t, tdb)

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	resp, err := s.handleSoulX402IssueInvocationGrant(&apptheory.Context{Request: apptheory.Request{Body: x402IssueBody(t, nil), Headers: map[string][]string{"authorization": {"Bearer raw-instance-key"}}}})
	out := requireX402IssueResponse(t, resp, err)
	if !out.TokenReturned || out.Grant.PolicyVersion != models.SoulCallerAccessPaymentPolicyVersionV1 {
		t.Fatalf("expected hosted/offchain grantable policy to issue scoped grant, got %#v", out)
	}
	if *captured == nil || (*captured).PolicyVersion != models.SoulCallerAccessPaymentPolicyVersionV1 {
		t.Fatalf("expected hosted/offchain grant persisted without immutable anchor requirement, got %#v", *captured)
	}
}

func TestHandleSoulX402IssueInvocationGrant_DeniedPolicyReturnsGenericUnavailable(t *testing.T) {
	t.Parallel()

	tdb := newSoulX402GrantTestDB()
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: sha256HexTrimmed("raw-instance-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)})
	expectX402Identity(t, tdb.qIdentity, models.SoulPublicPaidCallerAccessDenied)
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	_, err := s.handleSoulX402IssueInvocationGrant(&apptheory.Context{Request: apptheory.Request{Body: x402IssueBody(t, nil), Headers: map[string][]string{"authorization": {"Bearer raw-instance-key"}}}})
	appErr := requireCommTheoryError(t, err)
	if appErr.Code != x402CodeGrantUnavailable || appErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected generic unavailable/404, got %q/%d", appErr.Code, appErr.StatusCode)
	}
	tdb.qGrant.AssertNotCalled(t, "Create")
}

func TestHandleSoulX402ConsumeInvocationGrant_ConsumesAndReplaysByIdempotency(t *testing.T) {
	t.Parallel()

	grantToken := x402GrantTestToken
	grant := x402StoredGrant(grantToken)

	tdb := newSoulX402GrantTestDB()
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: sha256HexTrimmed("raw-instance-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)})
	expectX402Grant(t, tdb.qGrant, grant)
	expectX402Identity(t, tdb.qIdentity, models.SoulPublicPaidCallerAccessGrantable)
	expectCommDomain(t, tdb.qDomain, models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified})
	tdb.qUsage.On("All", mock.AnythingOfType("*[]*models.SoulX402InvocationGrantUsage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulX402InvocationGrantUsage](t, args, 0)
		*dest = nil
	}).Once()
	tdb.qUsage.On("Create").Return(nil).Once()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	resp, err := s.handleSoulX402ConsumeInvocationGrant(&apptheory.Context{
		Params: map[string]string{"grantId": "x402-grant-1"},
		Request: apptheory.Request{
			Body:    x402ConsumeBody(t, grantToken, nil),
			Headers: map[string][]string{"authorization": {"Bearer raw-instance-key"}},
		},
	})
	out := requireX402ConsumeResponse(t, resp, err)
	assertX402ConsumeAcceptedOnce(t, out)

	// Direct replay path: same consume idempotency and request hash returns accepted without another slot.
	tdbReplay := newSoulX402GrantTestDB()
	existing := models.SoulX402InvocationGrantUsage{
		GrantID:                   grant.GrantID,
		UsageSlot:                 out.Usage.Slot,
		AgentID:                   grant.AgentID,
		InstanceSlug:              "inst1",
		ConsumeIdempotencyKeyHash: out.Usage.IdempotencyKeyHash,
		ConsumeRequestHash:        out.Usage.ConsumeRequestHash,
		ConsumedAt:                time.Now().UTC(),
	}
	tdbReplay.qUsage.On("All", mock.AnythingOfType("*[]*models.SoulX402InvocationGrantUsage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulX402InvocationGrantUsage](t, args, 0)
		*dest = []*models.SoulX402InvocationGrantUsage{&existing}
	}).Once()
	sReplay := &Server{store: store.New(tdbReplay.db)}
	usage, usedCount, replayed, appErr := sReplay.claimSoulX402InvocationGrantUsage(t.Context(), &models.InstanceKey{InstanceSlug: "inst1"}, &grant, validatedSoulX402GrantConsume{
		idempotencyHash:    existing.ConsumeIdempotencyKeyHash,
		consumeRequestHash: existing.ConsumeRequestHash,
	}, time.Now().UTC())
	if appErr != nil || !replayed || usedCount != 1 || usage == nil {
		t.Fatalf("expected idempotent replay, usage=%#v used=%d replay=%v err=%v", usage, usedCount, replayed, appErr)
	}
}

func TestHandleSoulX402ConsumeInvocationGrant_AcceptsInstanceCapabilityVocabulary(t *testing.T) {
	t.Parallel()

	grantToken := x402GrantTestToken
	grant := x402StoredGrant(grantToken)
	grant.CapabilityVersion = models.SoulX402InvocationGrantCapabilityVocabularyInstanceV1
	grant.Capability = models.SoulX402InvocationGrantCapabilityInstanceAgentCreate
	grant.Tool = "agent_create"
	grant.Resource = "instance://tools/agent_create"
	_ = grant.UpdateKeys()

	tdb := newSoulX402GrantTestDB()
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: sha256HexTrimmed("raw-instance-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)})
	expectX402Grant(t, tdb.qGrant, grant)
	expectX402Identity(t, tdb.qIdentity, models.SoulPublicPaidCallerAccessGrantable)
	expectCommDomain(t, tdb.qDomain, models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified})
	tdb.qUsage.On("All", mock.AnythingOfType("*[]*models.SoulX402InvocationGrantUsage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulX402InvocationGrantUsage](t, args, 0)
		*dest = nil
	}).Once()
	tdb.qUsage.On("Create").Return(nil).Once()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	resp, err := s.handleSoulX402ConsumeInvocationGrant(&apptheory.Context{
		Params: map[string]string{"grantId": "x402-grant-1"},
		Request: apptheory.Request{
			Body: x402ConsumeBody(t, grantToken, map[string]any{
				"capabilityVersion": models.SoulX402InvocationGrantCapabilityVocabularyInstanceV1,
				"capability":        models.SoulX402InvocationGrantCapabilityInstanceAgentCreate,
				"tool":              "agent_create",
				"resource":          "instance://tools/agent_create",
			}),
			Headers: map[string][]string{"authorization": {"Bearer raw-instance-key"}},
		},
	})
	out := requireX402ConsumeResponse(t, resp, err)
	assertX402ConsumeAcceptedOnce(t, out)
	if out.Grant.CapabilityVersion != models.SoulX402InvocationGrantCapabilityVocabularyInstanceV1 ||
		out.Grant.Capability != models.SoulX402InvocationGrantCapabilityInstanceAgentCreate {
		t.Fatalf("expected instance capability vocabulary in response: %#v", out.Grant)
	}
}

func TestHandleSoulX402ConsumeInvocationGrant_RejectsPaymentEvidenceBeforeUsage(t *testing.T) {
	t.Parallel()

	grantToken := x402GrantTestToken
	grant := x402StoredGrant(grantToken)
	tdb := newSoulX402GrantTestDB()
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: sha256HexTrimmed("raw-instance-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)})
	expectX402Grant(t, tdb.qGrant, grant)

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	_, err := s.handleSoulX402ConsumeInvocationGrant(&apptheory.Context{
		Params: map[string]string{"grantId": "x402-grant-1"},
		Request: apptheory.Request{
			Body: x402ConsumeBody(t, grantToken, map[string]any{
				"paymentEvidenceHash": sha256HexTrimmed("different-payment-evidence"),
			}),
			Headers: map[string][]string{"authorization": {"Bearer raw-instance-key"}},
		},
	})
	appErr := requireCommTheoryError(t, err)
	if appErr.Code != x402CodeGrantRejected || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected payment evidence rejection before usage, got %q/%d", appErr.Code, appErr.StatusCode)
	}
	tdb.qUsage.AssertNotCalled(t, "All", mock.Anything)
	tdb.qUsage.AssertNotCalled(t, "Create")
}

func assertX402ConsumeAcceptedOnce(t *testing.T, out soulX402GrantConsumeResponse) {
	t.Helper()
	if !out.Accepted || out.Replayed || out.Usage.UsedCount != 1 || out.Grant.UsedCount != 1 {
		t.Fatalf("unexpected consume response: %#v", out)
	}
	if out.Grant.Scope != models.SoulX402InvocationGrantScopeWrite || out.Grant.Authority != models.SoulX402InvocationGrantAuthorityScopedInvocation {
		t.Fatalf("consume response must expose access scope without changing authority: %#v", out.Grant)
	}
	if out.Usage.IdempotencyKeyHash != sha256HexTrimmed("consume-idem-1") || strings.TrimSpace(out.Usage.ConsumeRequestHash) == "" {
		t.Fatalf("usage did not return minimized consume idempotency hash: %#v", out.Usage)
	}
}

func TestClaimSoulX402InvocationGrantUsage_ExhaustsMaxUsage(t *testing.T) {
	t.Parallel()

	tdb := newSoulX402GrantTestDB()
	grant := &models.SoulX402InvocationGrant{GrantID: "x402-grant-1", AgentID: soulLifecycleTestAgentIDHex, MaxUsage: 1}
	existing := &models.SoulX402InvocationGrantUsage{GrantID: grant.GrantID, UsageSlot: 0, AgentID: grant.AgentID, InstanceSlug: "inst1", ConsumeIdempotencyKeyHash: sha256HexTrimmed("consume-idem-1"), ConsumeRequestHash: strings.Repeat("c", 64), ConsumedAt: time.Now().UTC()}
	tdb.qUsage.On("All", mock.AnythingOfType("*[]*models.SoulX402InvocationGrantUsage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulX402InvocationGrantUsage](t, args, 0)
		*dest = []*models.SoulX402InvocationGrantUsage{existing}
	}).Once()
	s := &Server{store: store.New(tdb.db)}
	_, usedCount, replayed, appErr := s.claimSoulX402InvocationGrantUsage(t.Context(), &models.InstanceKey{InstanceSlug: "inst1"}, grant, validatedSoulX402GrantConsume{
		idempotencyHash:    sha256HexTrimmed("consume-idem-2"),
		consumeRequestHash: strings.Repeat("d", 64),
	}, time.Now().UTC())
	if appErr == nil || appErr.Code != x402CodeGrantRejected || appErr.StatusCode != http.StatusForbidden || replayed || usedCount != 1 {
		t.Fatalf("expected max-usage rejection, used=%d replay=%v err=%#v", usedCount, replayed, appErr)
	}
}

func TestClaimSoulX402InvocationGrantUsage_RefreshesAfterSlotContention(t *testing.T) {
	t.Parallel()

	tdb := newSoulX402GrantTestDB()
	grant := &models.SoulX402InvocationGrant{GrantID: "x402-grant-1", AgentID: soulLifecycleTestAgentIDHex, MaxUsage: 2}
	req := validatedSoulX402GrantConsume{
		idempotencyHash:    sha256HexTrimmed("consume-idem-1"),
		consumeRequestHash: strings.Repeat("c", 64),
	}
	existing := &models.SoulX402InvocationGrantUsage{
		GrantID:                   grant.GrantID,
		UsageSlot:                 0,
		AgentID:                   grant.AgentID,
		InstanceSlug:              "inst1",
		ConsumeIdempotencyKeyHash: req.idempotencyHash,
		ConsumeRequestHash:        req.consumeRequestHash,
		ConsumedAt:                time.Now().UTC(),
	}
	tdb.qUsage.On("All", mock.AnythingOfType("*[]*models.SoulX402InvocationGrantUsage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulX402InvocationGrantUsage](t, args, 0)
		*dest = nil
	}).Once()
	tdb.qUsage.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	tdb.qUsage.On("All", mock.AnythingOfType("*[]*models.SoulX402InvocationGrantUsage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulX402InvocationGrantUsage](t, args, 0)
		*dest = []*models.SoulX402InvocationGrantUsage{existing}
	}).Once()

	s := &Server{store: store.New(tdb.db)}
	usage, usedCount, replayed, appErr := s.claimSoulX402InvocationGrantUsage(t.Context(), &models.InstanceKey{InstanceSlug: "inst1"}, grant, req, time.Now().UTC())
	if appErr != nil || !replayed || usedCount != 1 || usage == nil || usage.UsageSlot != 0 {
		t.Fatalf("expected contention refresh to return existing replay, usage=%#v used=%d replay=%v err=%v", usage, usedCount, replayed, appErr)
	}
	tdb.qUsage.AssertNumberOfCalls(t, "Create", 1)
}

func TestHandleSoulX402IssueInvocationGrant_IdempotencyConflict(t *testing.T) {
	t.Parallel()

	tdb := newSoulX402GrantTestDB()
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: sha256HexTrimmed("raw-instance-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)})
	expectX402Identity(t, tdb.qIdentity, models.SoulPublicPaidCallerAccessGrantable)
	tdb.qGrant.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	existing := models.SoulX402InvocationGrant{
		GrantID:          models.SoulX402InvocationGrantID(soulLifecycleTestAgentIDHex, sha256HexTrimmed("issue-idem-1")),
		AgentID:          soulLifecycleTestAgentIDHex,
		IssueRequestHash: strings.Repeat("e", 64),
		MaxUsage:         1,
		IssuedAt:         time.Now().UTC(),
		ExpiresAt:        time.Now().Add(time.Hour).UTC(),
	}
	expectX402Grant(t, tdb.qGrant, existing)
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	_, err := s.handleSoulX402IssueInvocationGrant(&apptheory.Context{Request: apptheory.Request{Body: x402IssueBody(t, nil), Headers: map[string][]string{"authorization": {"Bearer raw-instance-key"}}}})
	appErr := requireCommTheoryError(t, err)
	if appErr.Code != x402CodeIdempotencyConflict || appErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected idempotency conflict, got %q/%d", appErr.Code, appErr.StatusCode)
	}
}

func TestParseSoulX402GrantIssueRequest_NormalizesHashesAndRejectsBadInputs(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	caller := "did:example:caller-1"
	evidence := "x402-evidence"
	paymentID := "payment-id-1"
	body := x402IssueBody(t, map[string]any{
		"scope": "WRITE",
		"caller": map[string]any{
			"subject":     caller,
			"subjectHash": "sha256:" + sha256HexTrimmed(caller),
		},
		"payment": map[string]any{
			"network":       "BASE-SEPOLIA",
			"asset":         "USDC",
			"amount":        "1000",
			"evidence":      evidence,
			"evidenceHash":  "sha256:" + sha256HexTrimmed(evidence),
			"paymentId":     paymentID,
			"paymentIdHash": "sha256:" + sha256HexTrimmed(paymentID),
		},
	})
	req, appErr := parseSoulX402GrantIssueRequest(&apptheory.Context{Request: apptheory.Request{Body: body}}, now)
	if appErr != nil {
		t.Fatalf("unexpected parse err: %v", appErr)
	}
	if req.callerSubjectHash != sha256HexTrimmed(caller) || req.payment.EvidenceHash != sha256HexTrimmed(evidence) || req.payment.PaymentIDHash != sha256HexTrimmed(paymentID) {
		t.Fatalf("unexpected minimized hashes: %#v", req)
	}
	if req.payment.Network != "base-sepolia" || req.payment.Asset != "usdc" || req.payment.Scheme != models.SoulX402InvocationGrantPaymentSchemeX402 {
		t.Fatalf("unexpected normalized payment binding: %#v", req.payment)
	}
	if req.scope != models.SoulX402InvocationGrantScopeWrite {
		t.Fatalf("expected scope normalized to write, got %q", req.scope)
	}
	if req.capabilityVersion != models.SoulX402InvocationGrantCapabilityVocabularyScopedV1 {
		t.Fatalf("expected scoped capability vocabulary version, got %q", req.capabilityVersion)
	}

	instanceBody := x402IssueBody(t, map[string]any{
		"capabilityVersion": models.SoulX402InvocationGrantCapabilityVocabularyInstanceV1,
		"capability":        models.SoulX402InvocationGrantCapabilityInstanceInstallPlan,
		"tool":              "agent_local_install_plan",
		"resource":          "instance://tools/agent_local_install_plan",
	})
	instanceReq, appErr := parseSoulX402GrantIssueRequest(&apptheory.Context{Request: apptheory.Request{Body: instanceBody}}, now)
	if appErr != nil {
		t.Fatalf("expected instance capability parse: %v", appErr)
	}
	if instanceReq.capabilityVersion != models.SoulX402InvocationGrantCapabilityVocabularyInstanceV1 || instanceReq.capability != models.SoulX402InvocationGrantCapabilityInstanceInstallPlan {
		t.Fatalf("unexpected instance capability binding: %#v", instanceReq)
	}

	cases := []struct {
		name string
		body []byte
	}{
		{name: "missing capability version", body: x402IssueBody(t, map[string]any{"capabilityVersion": nil})},
		{name: "unknown capability version", body: x402IssueBody(t, map[string]any{"capabilityVersion": "actor/v0"})},
		{name: "scoped vocabulary cannot bind instance tool", body: x402IssueBody(t, map[string]any{"tool": "agent_create"})},
		{name: "scoped vocabulary cannot bind instance capability", body: x402IssueBody(t, map[string]any{"capability": models.SoulX402InvocationGrantCapabilityInstanceAgentCreate})},
		{name: "instance vocabulary rejects actor capability", body: x402IssueBody(t, map[string]any{"capabilityVersion": models.SoulX402InvocationGrantCapabilityVocabularyInstanceV1})},
		{name: "instance vocabulary rejects mismatched tool", body: x402IssueBody(t, map[string]any{"capabilityVersion": models.SoulX402InvocationGrantCapabilityVocabularyInstanceV1, "capability": models.SoulX402InvocationGrantCapabilityInstanceAgentCreate, "tool": "agent_local_install_plan"})},
		{name: "missing scope", body: x402IssueBody(t, map[string]any{"scope": nil})},
		{name: "empty scope", body: x402IssueBody(t, map[string]any{"scope": ""})},
		{name: "unknown scope", body: x402IssueBody(t, map[string]any{"scope": "owner"})},
		{name: "bad request hash", body: x402IssueBody(t, map[string]any{"requestHash": "not-a-hash"})},
		{name: "subject hash mismatch", body: x402IssueBody(t, map[string]any{"caller": map[string]any{"subject": caller, "subjectHash": strings.Repeat("0", 64)}})},
		{name: "evidence hash mismatch", body: x402IssueBody(t, map[string]any{"payment": map[string]any{"network": "base", "amount": "1", "evidence": evidence, "evidenceHash": strings.Repeat("0", 64)}})},
		{name: "expiry too far", body: x402IssueBody(t, map[string]any{"expiresAt": now.Add(25 * time.Hour).Format(time.RFC3339)})},
		{name: "bad max usage", body: x402IssueBody(t, map[string]any{"maxUsage": 101})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, appErr := parseSoulX402GrantIssueRequest(&apptheory.Context{Request: apptheory.Request{Body: tc.body}}, now); appErr == nil {
				t.Fatalf("expected parse error")
			}
		})
	}
}

func TestValidateSoulX402GrantForConsume_RejectsInvalidScopeAndLifecycle(t *testing.T) {
	t.Parallel()

	token := "grant-token"
	grant := &models.SoulX402InvocationGrant{
		GrantID:             "x402-grant-1",
		AgentID:             soulLifecycleTestAgentIDHex,
		CapabilityVersion:   models.SoulX402InvocationGrantCapabilityVocabularyScopedV1,
		Capability:          "tools.invoke",
		Tool:                "summarize",
		Resource:            "mcp://agent/summarize",
		Scope:               models.SoulX402InvocationGrantScopeWrite,
		RequestHash:         x402GrantTestRequestHash,
		PaymentEvidenceHash: sha256HexTrimmed("raw-payment-evidence"),
		GrantTokenHash:      sha256HexTrimmed(token),
		Status:              models.SoulX402InvocationGrantStatusIssued,
		MaxUsage:            1,
		IssuedAt:            time.Now().Add(-time.Minute).UTC(),
		ExpiresAt:           time.Now().Add(time.Hour).UTC(),
	}
	valid := validatedSoulX402GrantConsume{
		grantTokenHash:      sha256HexTrimmed(token),
		agentIDHex:          soulLifecycleTestAgentIDHex,
		capabilityVersion:   models.SoulX402InvocationGrantCapabilityVocabularyScopedV1,
		capability:          "tools.invoke",
		tool:                "summarize",
		resource:            "mcp://agent/summarize",
		requestHash:         x402GrantTestRequestHash,
		paymentEvidenceHash: sha256HexTrimmed("raw-payment-evidence"),
	}
	if appErr := validateSoulX402GrantForConsume(grant, valid, time.Now().UTC()); appErr != nil {
		t.Fatalf("expected valid grant: %v", appErr)
	}

	cases := []struct {
		name   string
		mutate func(*models.SoulX402InvocationGrant, *validatedSoulX402GrantConsume)
		code   string
	}{
		{name: "bad token", code: x402CodeUnauthorized, mutate: func(_ *models.SoulX402InvocationGrant, req *validatedSoulX402GrantConsume) {
			req.grantTokenHash = sha256HexTrimmed("other")
		}},
		{name: "revoked", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, _ *validatedSoulX402GrantConsume) {
			g.Status = models.SoulX402InvocationGrantStatusRevoked
		}},
		{name: "expired", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, _ *validatedSoulX402GrantConsume) {
			g.ExpiresAt = time.Now().Add(-time.Minute).UTC()
		}},
		{name: "missing capability version", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, _ *validatedSoulX402GrantConsume) {
			g.CapabilityVersion = ""
		}},
		{name: "unknown capability version", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, _ *validatedSoulX402GrantConsume) {
			g.CapabilityVersion = "actor/v0"
		}},
		{name: "capability version mismatch", code: x402CodeGrantRejected, mutate: func(_ *models.SoulX402InvocationGrant, req *validatedSoulX402GrantConsume) {
			req.capabilityVersion = models.SoulX402InvocationGrantCapabilityVocabularyInstanceV1
		}},
		{name: "scoped vocabulary cannot bind instance tool", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, _ *validatedSoulX402GrantConsume) {
			g.Tool = "agent_create"
		}},
		{name: "scope mismatch", code: x402CodeGrantRejected, mutate: func(_ *models.SoulX402InvocationGrant, req *validatedSoulX402GrantConsume) { req.tool = "other" }},
		{name: "missing access scope", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, _ *validatedSoulX402GrantConsume) {
			g.Scope = ""
		}},
		{name: "unknown access scope", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, _ *validatedSoulX402GrantConsume) {
			g.Scope = "owner"
		}},
		{name: "bad max usage", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, _ *validatedSoulX402GrantConsume) { g.MaxUsage = 101 }},
		{name: "payment evidence hash mismatch", code: x402CodeGrantRejected, mutate: func(g *models.SoulX402InvocationGrant, req *validatedSoulX402GrantConsume) {
			g.PaymentEvidenceHash = sha256HexTrimmed("stored-evidence")
			req.paymentEvidenceHash = sha256HexTrimmed("different-evidence")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := *grant
			req := valid
			tc.mutate(&g, &req)
			appErr := validateSoulX402GrantForConsume(&g, req, time.Now().UTC())
			if appErr == nil || appErr.Code != tc.code {
				t.Fatalf("expected %s, got %#v", tc.code, appErr)
			}
		})
	}
}

func TestIssueOrReplaySoulX402InvocationGrant_ReplaysWithoutToken(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	req, appErr := parseSoulX402GrantIssueRequest(&apptheory.Context{Request: apptheory.Request{Body: x402IssueBody(t, nil)}}, now)
	if appErr != nil {
		t.Fatalf("parse: %v", appErr)
	}
	probe := soulX402GrantFromIssue(req, "original-token", models.SoulCallerAccessPaymentPolicyVersionV1, now)

	tdb := newSoulX402GrantTestDB()
	tdb.qGrant.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	expectX402Grant(t, tdb.qGrant, *probe)
	tdb.qUsage.On("All", mock.AnythingOfType("*[]*models.SoulX402InvocationGrantUsage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulX402InvocationGrantUsage](t, args, 0)
		*dest = nil
	}).Once()
	s := &Server{store: store.New(tdb.db)}
	resp, err := s.issueOrReplaySoulX402InvocationGrant(t.Context(), req, models.SoulCallerAccessPaymentPolicyVersionV1, now)
	out := requireX402IssueResponse(t, resp, err)
	if !out.Replayed || out.TokenReturned || strings.TrimSpace(out.Grant.GrantToken) != "" {
		t.Fatalf("expected replay without raw token: %#v", out)
	}
}
