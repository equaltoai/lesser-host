package controlplane

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestCreditsAmountCents(t *testing.T) {
	t.Parallel()

	if _, err := creditsAmountCents(0, 100); err == nil {
		t.Fatalf("expected error for credits <= 0")
	}
	if _, err := creditsAmountCents(1, 0); err == nil {
		t.Fatalf("expected error for pricing <= 0")
	}
	if _, err := creditsAmountCents(1_000_000_001, 100); err == nil {
		t.Fatalf("expected error for huge credits")
	}

	// Ceil behavior: 1001 credits at 100 cents per 1000 => 101 cents.
	got, err := creditsAmountCents(1001, 100)
	if err != nil {
		t.Fatalf("creditsAmountCents: %v", err)
	}
	if got != 101 {
		t.Fatalf("expected ceil to 101, got %d", got)
	}
}

func TestPortalBillingParsers(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	if got, appErr := normalizeCreditsCheckoutMonth("", now); appErr != nil || got != "1970-01" {
		t.Fatalf("expected default month 1970-01, got %q err=%v", got, appErr)
	}
	if _, appErr := normalizeCreditsCheckoutMonth("nope", now); appErr == nil {
		t.Fatalf("expected error for invalid month")
	}

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, appErr := parsePortalCreditsCheckoutRequest(ctx); appErr == nil {
		t.Fatalf("expected error for missing fields")
	}

	ctx.Request.Body = []byte(`{"instance_slug":" Slug ","credits":10,"month":"2026-02"}`)
	req, appErr := parsePortalCreditsCheckoutRequest(ctx)
	if appErr != nil {
		t.Fatalf("parsePortalCreditsCheckoutRequest: %v", appErr)
	}
	if req.InstanceSlug != "slug" || req.Credits != 10 || req.Month != "2026-02" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestPortalWalletHelpers(t *testing.T) {
	t.Parallel()

	if got := portalUsernameForWalletAddress(" 0xAbC "); got != "wallet-abc" {
		t.Fatalf("unexpected username: %q", got)
	}

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, err := parsePortalWalletLogin(ctx); err == nil {
		t.Fatalf("expected error")
	}

	ctx.Request.Body = []byte(`{"challengeId":"c","address":"a","signature":"s","message":"m","email":" e ","display_name":" n "}`)
	req, err := parsePortalWalletLogin(ctx)
	if err != nil {
		t.Fatalf("parsePortalWalletLogin: %v", err)
	}
	if req.Email != "e" || req.DisplayName != "n" {
		t.Fatalf("expected trimmed optional fields, got %#v", req)
	}
}

func TestOperatorProvisioningHelpers(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{}
	if got := queryFirst(ctx, "x"); got != "" {
		t.Fatalf("expected empty query")
	}
	ctx.Request.Query = map[string][]string{"limit": {"2"}, "x": {"a", "b"}}
	if got := queryFirst(ctx, "x"); got != "a" {
		t.Fatalf("expected first query value, got %q", got)
	}

	if got := parseLimit("", 50, 1, 200); got != 50 {
		t.Fatalf("expected default, got %d", got)
	}
	if got := parseLimit("0", 50, 1, 200); got != 1 {
		t.Fatalf("expected clamp to min, got %d", got)
	}
	if got := parseLimit("999", 50, 1, 200); got != 200 {
		t.Fatalf("expected clamp to max, got %d", got)
	}

	j := &models.ProvisionJob{ID: " id ", InstanceSlug: " slug ", Status: " ok ", ReceiptJSON: " {} "}
	item := operatorProvisionJobListItemFromModel(j)
	if item.ID != "id" || item.InstanceSlug != "slug" || !item.HasReceipt {
		t.Fatalf("unexpected list item: %#v", item)
	}

	detail := operatorProvisionJobDetailFromModel(j)
	if detail.ID != "id" || detail.InstanceSlug != "slug" || !detail.HasReceipt {
		t.Fatalf("unexpected detail: %#v", detail)
	}
}

func TestInstanceAndDomainResponsesApplyDefaults(t *testing.T) {
	t.Parallel()

	resp := instanceResponseFromModel(nil)
	if resp.Slug != "" || resp.HostedPreviewsEnabled != false {
		// HostedPreviewsEnabled defaults to true only when instance exists; zero-value response is false.
		t.Fatalf("unexpected nil response: %#v", resp)
	}

	inst := &models.Instance{Slug: " slug ", Owner: " owner ", AIModelSet: ""}
	resp = instanceResponseFromModel(inst)
	if resp.Slug != "slug" || resp.Owner != "owner" {
		t.Fatalf("expected trimmed fields, got %#v", resp)
	}
	if resp.RenderPolicy == "" || resp.OveragePolicy == "" {
		t.Fatalf("expected default policies, got %#v", resp)
	}
	if resp.AIModelSet != defaultAIModelSet {
		t.Fatalf("expected default model set, got %q", resp.AIModelSet)
	}

	dresp := domainResponseFromModel(&models.Domain{Domain: " d ", InstanceSlug: " s "})
	if dresp.Domain != "d" || dresp.InstanceSlug != "s" {
		t.Fatalf("expected trimmed domain fields, got %#v", dresp)
	}
}

func TestTipRegistryOpID_DeterministicAndSensitiveToFlags(t *testing.T) {
	t.Parallel()

	active := true
	allowed := true

	id1 := tipRegistryOpID("register_host", 1, "0x1", "0x2", "0x3", 250, "", nil, nil)
	id2 := tipRegistryOpID("register_host", 1, "0x1", "0x2", "0x3", 250, "", nil, nil)
	if id1 == "" || id1 != id2 {
		t.Fatalf("expected stable op id, got %q vs %q", id1, id2)
	}

	id3 := tipRegistryOpID("register_host", 1, "0x1", "0x2", "0x3", 250, "", &active, nil)
	if id3 == id1 {
		t.Fatalf("expected different id when active is set")
	}

	id4 := tipRegistryOpID("register_host", 1, "0x1", "0x2", "0x3", 250, "", &active, &allowed)
	if id4 == id3 {
		t.Fatalf("expected different id when tokenAllowed is set")
	}
}

func TestTipRegistryProofParsingAndWalletVerification(t *testing.T) {
	t.Parallel()

	proofs, err := parseTipRegistryProofs(nil)
	if err != nil || !proofs.requireDNS || proofs.requireHTTPS {
		t.Fatalf("expected dns required by default, got %#v err=%v", proofs, err)
	}

	if _, err := parseTipRegistryProofs([]string{"nope"}); err == nil {
		t.Fatalf("expected error for invalid proof")
	}
	if _, err := parseTipRegistryProofs([]string{" "}); err == nil {
		t.Fatalf("expected error for no proofs selected")
	}

	now := time.Unix(100, 0).UTC()
	msg := buildTipRegistryWalletMessage("example.com", "0xAbC", 1, 250, "proof", "nonce", now, now.Add(time.Minute))
	if !strings.Contains(msg, "example.com") {
		t.Fatalf("expected domain in message")
	}

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()

	hash := accounts.TextHash([]byte(msg))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	sigHex := "0x" + hex.EncodeToString(sig)

	appErr := verifyTipHostRegistrationWallet(&models.TipHostRegistration{
		WalletAddr:    addr,
		WalletMessage: msg,
	}, sigHex)
	if appErr != nil {
		t.Fatalf("expected signature valid, got %v", appErr)
	}
}

func TestParseTipHostRegistrationVerifyInput(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, _, appErr := parseTipHostRegistrationVerifyInput(ctx); appErr == nil {
		t.Fatalf("expected error")
	}

	ctx.Request.Body = []byte(`{"signature":"s","proofs":["dns_txt"]}`)
	sig, proofs, appErr := parseTipHostRegistrationVerifyInput(ctx)
	if appErr != nil {
		t.Fatalf("parseTipHostRegistrationVerifyInput: %v", appErr)
	}
	if sig != "s" || !proofs.requireDNS {
		t.Fatalf("unexpected parsed input: sig=%q proofs=%#v", sig, proofs)
	}
}
