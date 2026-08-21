package controlplane

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulAppendContinuity_VerifiesSignedEntry(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentIDHex,
			Domain:    "example.com",
			LocalID:   "agent-alice",
			Wallet:    wallet,
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()

	entryType := models.SoulContinuityEntryTypeModelChange
	timestamp := canonicalSoulSignedTimestamp(time.Now().UTC())
	summary := "Updated underlying model to claude-opus-4-6."
	recovery := ""
	references := []string{"boundary-001"}

	digest, appErr := computeSoulContinuityEntryDigest(entryType, timestamp, summary, recovery, references, "")
	if appErr != nil {
		t.Fatalf("digest: %v", appErr)
	}
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sigHex := "0x" + hex.EncodeToString(sig)

	reqBody, _ := json.Marshal(map[string]any{
		"type":       entryType,
		"timestamp":  timestamp,
		"summary":    summary,
		"references": references,
		"signature":  sigHex,
	})

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: reqBody},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulAppendContinuity(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulAppendContinuityResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Entry.Type != entryType {
		t.Fatalf("expected type %q, got %q", entryType, out.Entry.Type)
	}
	if out.Entry.Timestamp.IsZero() || canonicalSoulSignedTimestamp(out.Entry.Timestamp) != timestamp {
		t.Fatalf("expected timestamp %q, got %#v", timestamp, out.Entry.Timestamp)
	}
	if out.Entry.Signature != strings.ToLower(sigHex) {
		t.Fatalf("expected signature %q, got %q", strings.ToLower(sigHex), out.Entry.Signature)
	}
}

func TestHandleSoulAppendContinuity_DuplicateSignedEntryIsIdempotent(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Twice()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Twice()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Twice()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentIDHex,
			Domain:    "example.com",
			LocalID:   "agent-alice",
			Wallet:    wallet,
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: time.Now().Add(-time.Minute).UTC(),
		}
	}).Twice()

	entryType := models.SoulContinuityEntryTypeModelChange
	timestamp := canonicalSoulSignedTimestamp(time.Now().UTC())
	summary := "Updated underlying model to claude-opus-4-6."
	references := []string{"boundary-001"}
	digest, appErr := computeSoulContinuityEntryDigest(entryType, timestamp, summary, "", references, "")
	if appErr != nil {
		t.Fatalf("digest: %v", appErr)
	}
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	reqBody := mustMarshalJSON(t, map[string]any{
		"type":       entryType,
		"timestamp":  timestamp,
		"summary":    summary,
		"references": references,
		"signature":  "0x" + hex.EncodeToString(sig),
	})

	tdb.qContinuity.On("Create").Unset()
	tdb.qContinuity.On("Create").Return(nil).Once()
	tdb.qContinuity.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	var stored models.SoulAgentContinuity
	tdb.qContinuity.On("First", mock.AnythingOfType("*models.SoulAgentContinuity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentContinuity](t, args, 0)
		*dest = stored
	}).Once()

	appendEntry := func(requestID string) *apptheory.Response {
		ctx := &apptheory.Context{
			RequestID:    requestID,
			AuthIdentity: "admin",
			Params:       map[string]string{"agentId": agentIDHex},
			Request:      apptheory.Request{Body: reqBody},
		}
		ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
		resp, handleErr := s.handleSoulAppendContinuity(ctx)
		if handleErr != nil {
			t.Fatalf("append %s: %v", requestID, handleErr)
		}
		return resp
	}

	first := appendEntry("r-continuity-1")
	if first.Status != http.StatusCreated {
		t.Fatalf("first append expected 201, got %d (body=%q)", first.Status, string(first.Body))
	}
	stored = mustUnmarshalJSON[soulAppendContinuityResponse](t, first.Body).Entry
	if stored.PK == "" || stored.SK == "" {
		_ = stored.UpdateKeys()
	}

	retry := appendEntry("r-continuity-2")
	if retry.Status != http.StatusOK {
		t.Fatalf("duplicate append expected idempotent 200, got %d (body=%q)", retry.Status, string(retry.Body))
	}
	replayed := mustUnmarshalJSON[soulAppendContinuityResponse](t, retry.Body).Entry
	if replayed.Type != stored.Type || replayed.Summary != stored.Summary || !replayed.Timestamp.Equal(stored.Timestamp) {
		t.Fatalf("duplicate append returned a different entry: got %#v want %#v", replayed, stored)
	}
}

func TestParseSoulSignedTimestamp_CanonicalizesLikeJavaScriptISOString(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 29, 3, 15, 0, 0, time.UTC)
	parsed, canonical, appErr := parseSoulSignedTimestamp("2026-04-29T03:14:59.120987654Z", now, "timestamp")
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
	if canonical != "2026-04-29T03:14:59.120Z" {
		t.Fatalf("unexpected canonical timestamp: %q", canonical)
	}
	if parsed.Nanosecond() != 120_000_000 {
		t.Fatalf("expected millisecond-truncated timestamp, got %s", parsed.Format(time.RFC3339Nano))
	}

	_, _, appErr = parseSoulSignedTimestamp("2026-04-29T02:59:59.999Z", now, "timestamp")
	if appErr == nil || appErr.Message != "timestamp is too far in the past" {
		t.Fatalf("expected stale timestamp rejection, got %#v", appErr)
	}
}
