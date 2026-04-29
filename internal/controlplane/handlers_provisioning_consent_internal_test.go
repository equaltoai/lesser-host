package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	testProvisionConsentStageLab              = "lab"
	testProvisionConsentSlugDemo              = "demo"
	testProvisionConsentBaseDomainDemoGreater = "demo.greater.website"
	testProvisionConsentNonce16               = "0123456789abcdef"
	testProvisionConsentAdminAlice            = "Alice"
	testProvisionConsentCodeUnauthorized      = "app.unauthorized"
)

func TestBuildProvisionConsentMessage_EmitsLesserM9StructuredJSON(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 4, 29, 13, 30, 0, 0, time.UTC)
	msg := buildProvisionConsentMessage(testProvisionConsentStageLab, "Demo.Greater.Website.", testProvisionConsentAdminAlice, testProvisionConsentNonce16, expiresAt)

	if msg == "" || strings.TrimSpace(msg) != msg || strings.Contains(msg, "\n") {
		t.Fatalf("expected compact JSON with no surrounding whitespace, got %q", msg)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(msg), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if len(raw) != 5 {
		t.Fatalf("expected exactly 5 JSON fields, got %d: %s", len(raw), msg)
	}

	var payload provisionInitAdminConsentV1
	if err := json.Unmarshal([]byte(msg), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Kind != provisionInitAdminConsentKindV1 {
		t.Fatalf("unexpected kind: %q", payload.Kind)
	}
	if payload.Instance != "dev.demo.greater.website" {
		t.Fatalf("unexpected instance: %q", payload.Instance)
	}
	if payload.Username != testProvisionConsentAdminAlice {
		t.Fatalf("unexpected username: %q", payload.Username)
	}
	if payload.Nonce != testProvisionConsentNonce16 {
		t.Fatalf("unexpected nonce: %q", payload.Nonce)
	}
	if !payload.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("unexpected expires_at: %s", payload.ExpiresAt)
	}
}

func TestBuildProvisionConsentMessage_UsesManagedStageDomain(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 4, 29, 13, 30, 0, 0, time.UTC)
	cases := []struct {
		stage string
		want  string
	}{
		{stage: "lab", want: "dev.demo.greater.website"},
		{stage: "dev", want: "dev.demo.greater.website"},
		{stage: "staging", want: "staging.demo.greater.website"},
		{stage: "stage", want: "staging.demo.greater.website"},
		{stage: "live", want: "demo.greater.website"},
	}

	for _, tc := range cases {
		msg := buildProvisionConsentMessage(tc.stage, testProvisionConsentBaseDomainDemoGreater, testProvisionConsentSlugDemo, testProvisionConsentNonce16, expiresAt)
		var payload provisionInitAdminConsentV1
		if err := json.Unmarshal([]byte(msg), &payload); err != nil {
			t.Fatalf("unmarshal for %s: %v", tc.stage, err)
		}
		if payload.Instance != tc.want {
			t.Fatalf("stage %q: expected %q, got %q", tc.stage, tc.want, payload.Instance)
		}
	}
}

func TestManagedProvisionBaseDomain_NormalizesInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		slug         string
		parentDomain string
		want         string
	}{
		{name: "configured parent", slug: " Demo ", parentDomain: ".Greater.Website.", want: testProvisionConsentBaseDomainDemoGreater},
		{name: "default parent", slug: " Demo ", parentDomain: "", want: "demo.greater.website"},
		{name: "empty slug", slug: " ", parentDomain: "greater.website", want: ""},
		{name: "empty normalized parent", slug: testProvisionConsentSlugDemo, parentDomain: "...", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := managedProvisionBaseDomain(tc.slug, tc.parentDomain); got != tc.want {
				t.Fatalf("managedProvisionBaseDomain(%q, %q) = %q, want %q", tc.slug, tc.parentDomain, got, tc.want)
			}
		})
	}
}

func TestHandlePortalProvisionConsentChallenge_CreatesChallenge(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: testProvisionConsentStageLab, ManagedParentDomain: "greater.website"}}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testProvisionConsentSlugDemo, Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	now := time.Now().UTC()
	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WalletCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WalletCredential](t, args, 0)
		*dest = []*models.WalletCredential{{
			Username: "alice",
			Address:  "0x00000000000000000000000000000000000000aa",
			ChainID:  1,
			Type:     "ethereum",
			LinkedAt: now,
			LastUsed: now,
		}}
	}).Once()

	tdb.qConsent.On("Create").Return(nil).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testProvisionConsentSlugDemo},
		Request:      apptheory.Request{Body: []byte(`{"admin_username":"demo"}`)},
	}
	resp, err := s.handlePortalProvisionConsentChallenge(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var out provisionConsentChallengeResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.InstanceSlug != testProvisionConsentSlugDemo || out.Stage != testProvisionConsentStageLab || out.AdminUsername != testProvisionConsentSlugDemo {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Wallet.ID == "" || out.Wallet.Address == "" || out.Wallet.Message == "" {
		t.Fatalf("expected wallet challenge fields, got %#v", out.Wallet)
	}
	var payload provisionInitAdminConsentV1
	if err := json.Unmarshal([]byte(out.Wallet.Message), &payload); err != nil {
		t.Fatalf("unmarshal consent message: %v", err)
	}
	if payload.Kind != provisionInitAdminConsentKindV1 || payload.Instance != "dev.demo.greater.website" || payload.Username != testProvisionConsentSlugDemo {
		t.Fatalf("unexpected structured consent payload: %#v", payload)
	}
}

func TestHandlePortalProvisionConsentChallenge_RequiresApproval(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	tdb.stubUser.Approved = false
	s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: testProvisionConsentStageLab}}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testProvisionConsentSlugDemo, Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": testProvisionConsentSlugDemo}}
	if _, err := s.handlePortalProvisionConsentChallenge(ctx); err == nil {
		t.Fatalf("expected forbidden for unapproved user")
	}
}

func TestHandlePortalProvisionConsentChallenge_BlocksReservedWallet(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: testProvisionConsentStageLab}}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testProvisionConsentSlugDemo, Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qCred.On("All", mock.AnythingOfType("*[]*models.WalletCredential")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.WalletCredential](t, args, 0)
		*dest = []*models.WalletCredential{{
			Username: "alice",
			Address:  reservedWalletLesserHostAdmin,
			ChainID:  1,
			Type:     "ethereum",
			LinkedAt: time.Now().UTC(),
			LastUsed: time.Now().UTC(),
		}}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": testProvisionConsentSlugDemo}}
	if _, err := s.handlePortalProvisionConsentChallenge(ctx); err == nil {
		t.Fatalf("expected reserved wallet error")
	}
}

func TestGetProvisionConsentChallenge_NormalizesNotFoundToUnauthorized(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qConsent.On("First", mock.AnythingOfType("*models.ProvisionConsentChallenge")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	_, err := s.getProvisionConsentChallenge(ctx, "missing")
	if err == nil {
		t.Fatalf("expected error")
	}

	if appErr := normalizeNotFound(err); appErr == nil || appErr.Code != testProvisionConsentCodeUnauthorized {
		t.Fatalf("expected app.unauthorized, got %#v", appErr)
	}
}

func TestValidateProvisionConsentChallenge_RequiresExactMessageBytes(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	msg := buildProvisionConsentMessage(testProvisionConsentStageLab, testProvisionConsentBaseDomainDemoGreater, testProvisionConsentSlugDemo, testProvisionConsentNonce16, expiresAt)
	chall := &models.ProvisionConsentChallenge{
		Username:     "alice",
		InstanceSlug: testProvisionConsentSlugDemo,
		Stage:        testProvisionConsentStageLab,
		Message:      msg,
		ExpiresAt:    expiresAt,
	}
	ctx := &apptheory.Context{AuthIdentity: "alice"}

	if appErr := validateProvisionConsentChallenge(ctx, chall, testProvisionConsentSlugDemo, testProvisionConsentStageLab, msg); appErr != nil {
		t.Fatalf("expected exact message to validate, got %#v", appErr)
	}
	if appErr := validateProvisionConsentChallenge(ctx, chall, testProvisionConsentSlugDemo, testProvisionConsentStageLab, msg+" "); appErr == nil || appErr.Code != appErrCodeForbidden {
		t.Fatalf("expected message mismatch for added whitespace, got %#v", appErr)
	}
}

func TestValidateProvisionConsentChallenge_RejectsMismatchedOrExpiredChallenge(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	msg := buildProvisionConsentMessage(testProvisionConsentStageLab, testProvisionConsentBaseDomainDemoGreater, testProvisionConsentSlugDemo, testProvisionConsentNonce16, expiresAt)
	base := models.ProvisionConsentChallenge{
		Username:     "alice",
		InstanceSlug: testProvisionConsentSlugDemo,
		Stage:        testProvisionConsentStageLab,
		Message:      msg,
		ExpiresAt:    expiresAt,
	}

	cases := []struct {
		name  string
		ctx   *apptheory.Context
		mut   func(*models.ProvisionConsentChallenge)
		slug  string
		stage string
		code  string
	}{
		{
			name:  "nil context",
			ctx:   nil,
			slug:  testProvisionConsentSlugDemo,
			stage: testProvisionConsentStageLab,
			code:  "app.internal",
		},
		{
			name:  "actor mismatch",
			ctx:   &apptheory.Context{AuthIdentity: "bob"},
			slug:  testProvisionConsentSlugDemo,
			stage: testProvisionConsentStageLab,
			code:  appErrCodeForbidden,
		},
		{
			name:  "slug mismatch",
			ctx:   &apptheory.Context{AuthIdentity: "alice"},
			slug:  "other",
			stage: testProvisionConsentStageLab,
			code:  appErrCodeForbidden,
		},
		{
			name:  "stage mismatch",
			ctx:   &apptheory.Context{AuthIdentity: "alice"},
			slug:  testProvisionConsentSlugDemo,
			stage: "live",
			code:  appErrCodeForbidden,
		},
		{
			name: "expired",
			ctx:  &apptheory.Context{AuthIdentity: "alice"},
			mut: func(chall *models.ProvisionConsentChallenge) {
				chall.ExpiresAt = time.Now().UTC().Add(-time.Minute)
			},
			slug:  testProvisionConsentSlugDemo,
			stage: testProvisionConsentStageLab,
			code:  appErrCodeBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			chall := base
			if tc.mut != nil {
				tc.mut(&chall)
			}
			if appErr := validateProvisionConsentChallenge(tc.ctx, &chall, tc.slug, tc.stage, msg); appErr == nil || appErr.Code != tc.code {
				t.Fatalf("expected %s, got %#v", tc.code, appErr)
			}
		})
	}
}
