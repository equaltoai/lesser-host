package models

import (
	"testing"
	"time"
)

func TestModels_UpdateKeysAndDefaults(t *testing.T) {
	t.Parallel()

	t.Run("User_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		u := &User{Username: " alice ", Role: ""}
		if err := u.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if u.PK == "" || u.SK == "" {
			t.Fatalf("expected keys set")
		}
		if u.Role != RoleOperator {
			t.Fatalf("expected default role %q, got %q", RoleOperator, u.Role)
		}
		if u.CreatedAt.IsZero() {
			t.Fatalf("expected CreatedAt set")
		}
		if u.Username != "alice" {
			t.Fatalf("expected trimmed username, got %q", u.Username)
		}
	})

	t.Run("OperatorSession_UpdateKeys", func(t *testing.T) {
		t.Parallel()

		s := &OperatorSession{
			ID:        " tok ",
			ExpiresAt: time.Unix(10, 0).UTC(),
		}
		if err := s.UpdateKeys(); err != nil {
			t.Fatalf("UpdateKeys: %v", err)
		}
		if s.ID != "tok" {
			t.Fatalf("expected trimmed id, got %q", s.ID)
		}
		if s.PK == "" || s.SK == "" {
			t.Fatalf("expected keys set")
		}
		if s.TTL != s.ExpiresAt.Unix() {
			t.Fatalf("expected TTL set from ExpiresAt")
		}
	})

	t.Run("Instance_BeforeCreate_Defaults", func(t *testing.T) {
		t.Parallel()

		inst := &Instance{Slug: "slug", Owner: "owner"}
		if err := inst.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if inst.PK == "" || inst.SK == "" || inst.GSI1PK == "" || inst.GSI1SK == "" {
			t.Fatalf("expected keys set")
		}
		if inst.CreatedAt.IsZero() {
			t.Fatalf("expected CreatedAt set")
		}
		if inst.Status != InstanceStatusActive {
			t.Fatalf("expected default status %q, got %q", InstanceStatusActive, inst.Status)
		}
		if inst.HostedPreviewsEnabled == nil || inst.LinkSafetyEnabled == nil || inst.RendersEnabled == nil {
			t.Fatalf("expected trust defaults set")
		}
		if inst.ModerationEnabled == nil {
			t.Fatalf("expected moderation defaults set")
		}
		if inst.AIEnabled == nil || inst.AIPricingMultiplierBps == nil || inst.AIMaxInflightJobs == nil {
			t.Fatalf("expected ai defaults set")
		}
	})

	t.Run("Domain_BeforeCreateAndUpdate", func(t *testing.T) {
		t.Parallel()

		d := &Domain{Domain: " Example.COM ", InstanceSlug: " slug "}
		if err := d.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if d.PK == "" || d.SK == "" || d.GSI1PK == "" || d.GSI1SK == "" {
			t.Fatalf("expected keys set")
		}
		if d.Type != DomainTypeVanity {
			t.Fatalf("expected default type %q, got %q", DomainTypeVanity, d.Type)
		}
		if d.Status != DomainStatusPending {
			t.Fatalf("expected default status %q, got %q", DomainStatusPending, d.Status)
		}
		if d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}

		before := d.UpdatedAt
		if err := d.BeforeUpdate(); err != nil {
			t.Fatalf("BeforeUpdate: %v", err)
		}
		if !d.UpdatedAt.After(before) && d.UpdatedAt != before {
			t.Fatalf("expected UpdatedAt updated")
		}
	})

	t.Run("WalletModels_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		now := time.Unix(100, 0).UTC()

		ch := &WalletChallenge{ID: "c1", Username: "u", Address: "0xabc", ChainID: 1, ExpiresAt: now.Add(time.Minute)}
		if err := ch.BeforeCreate(); err != nil {
			t.Fatalf("WalletChallenge.BeforeCreate: %v", err)
		}
		if ch.PK == "" || ch.SK == "" {
			t.Fatalf("expected keys set")
		}

		cred := &WalletCredential{Username: "u", Address: "0xabc", ChainID: 1, Type: "ethereum", LinkedAt: now, LastUsed: now}
		if err := cred.BeforeCreate(); err != nil {
			t.Fatalf("WalletCredential.BeforeCreate: %v", err)
		}
		if cred.PK == "" || cred.SK == "" {
			t.Fatalf("expected keys set")
		}

		idx := &WalletIndex{}
		idx.UpdateKeys("ethereum", "0xabc", "u")
		if idx.PK == "" || idx.SK == "" {
			t.Fatalf("expected keys set")
		}
	})

	t.Run("ControlPlaneConfig_Keys", func(t *testing.T) {
		t.Parallel()

		c := &ControlPlaneConfig{}
		if err := c.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if c.PK != "CONTROL_PLANE" || c.SK != "CONFIG" {
			t.Fatalf("unexpected keys: %q %q", c.PK, c.SK)
		}
	})

	t.Run("SetupSession_Keys", func(t *testing.T) {
		t.Parallel()

		s := &SetupSession{ID: " s ", ExpiresAt: time.Unix(123, 0).UTC()}
		if err := s.UpdateKeys(); err != nil {
			t.Fatalf("UpdateKeys: %v", err)
		}
		if s.PK == "" || s.SK == "" || s.TTL == 0 {
			t.Fatalf("expected keys + ttl set")
		}
	})

	t.Run("InstanceKey_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		k := &InstanceKey{ID: " key ", InstanceSlug: "slug"}
		if err := k.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if k.PK == "" || k.SK == "" {
			t.Fatalf("expected keys set")
		}
		if k.CreatedAt.IsZero() {
			t.Fatalf("expected CreatedAt set")
		}
	})

	t.Run("InstanceBudgetMonth_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		b := &InstanceBudgetMonth{InstanceSlug: " slug ", Month: "2026-02"}
		if err := b.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if b.PK == "" || b.SK == "" {
			t.Fatalf("expected keys set")
		}
		if b.UpdatedAt.IsZero() {
			t.Fatalf("expected UpdatedAt set")
		}
	})

	t.Run("BillingProfile_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		p := &BillingProfile{Username: " u ", Provider: ""}
		if err := p.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if p.PK == "" || p.SK == "" {
			t.Fatalf("expected keys set")
		}
		if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}
		if p.Username != "u" {
			t.Fatalf("expected trimmed username, got %q", p.Username)
		}
		if p.Provider != BillingProviderNone {
			t.Fatalf("expected default provider %q, got %q", BillingProviderNone, p.Provider)
		}
	})

	t.Run("CreditPurchase_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		p := &CreditPurchase{ID: "p1", Username: "u", InstanceSlug: "slug", Credits: 100, AmountCents: 250}
		if err := p.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if p.PK == "" || p.SK == "" {
			t.Fatalf("expected keys set")
		}
		if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}
		if p.Status == "" {
			t.Fatalf("expected default status set")
		}
	})

	t.Run("BillingPaymentMethod_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		m := &BillingPaymentMethod{ID: "pm1", Username: "u", Provider: "stripe"}
		if err := m.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if m.PK == "" || m.SK == "" {
			t.Fatalf("expected keys set")
		}
		if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}
	})

	t.Run("AIJob_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		j := &AIJob{ID: "j1", InstanceSlug: "slug", Module: "MOD", PolicyVersion: "1", ModelSet: "set", InputsHash: "hash"}
		if err := j.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if j.PK == "" || j.SK == "" || j.TTL == 0 {
			t.Fatalf("expected keys + ttl set")
		}
		if j.Status != AIJobStatusQueued {
			t.Fatalf("expected default status %q, got %q", AIJobStatusQueued, j.Status)
		}
		if j.MaxAttempts <= 0 {
			t.Fatalf("expected MaxAttempts set")
		}
	})

	t.Run("AIResult_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &AIResult{ID: "r1", InstanceSlug: "slug", Module: "m", PolicyVersion: "1", ModelSet: "set", InputsHash: "hash", ResultJSON: "{}"}
		if err := r.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if r.PK == "" || r.SK == "" || r.TTL == 0 {
			t.Fatalf("expected keys + ttl set")
		}
	})

	t.Run("RenderArtifact_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &RenderArtifact{ID: "r1", NormalizedURL: "https://example.com", PolicyVersion: "1"}
		if err := r.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if r.PK == "" || r.SK == "" {
			t.Fatalf("expected keys set")
		}
		if r.CreatedAt.IsZero() || r.ExpiresAt.IsZero() || r.TTL == 0 {
			t.Fatalf("expected timestamps + ttl set")
		}
	})

	t.Run("LinkPreview_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		p := &LinkPreview{ID: "p1", NormalizedURL: "https://example.com", PolicyVersion: "1"}
		if err := p.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if p.PK == "" || p.SK == "" {
			t.Fatalf("expected keys set")
		}
		if p.StoredAt.IsZero() || p.FetchedAt.IsZero() || p.ExpiresAt.IsZero() || p.TTL == 0 {
			t.Fatalf("expected timestamps + ttl set")
		}
	})

	t.Run("LinkSafetyBasicResult_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &LinkSafetyBasicResult{
			ID:        "r1",
			LinksHash: "hash",
			Links: []LinkSafetyBasicLinkResult{
				{URL: "https://example.com", Risk: "low"},
			},
			Summary: LinkSafetyBasicSummary{OverallRisk: "low"},
		}
		if err := r.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if r.PK == "" || r.SK == "" {
			t.Fatalf("expected keys set")
		}
		if r.CreatedAt.IsZero() || r.ExpiresAt.IsZero() || r.TTL == 0 {
			t.Fatalf("expected timestamps + ttl set")
		}
	})

	t.Run("UsageLedgerEntry_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		e := &UsageLedgerEntry{ID: "e1", InstanceSlug: "slug", Month: "2026-02", RequestedCredits: 10}
		if err := e.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if e.PK == "" || e.SK == "" {
			t.Fatalf("expected keys set")
		}
		if e.CreatedAt.IsZero() {
			t.Fatalf("expected CreatedAt set")
		}
		if e.BillingType != BillingTypeNone {
			t.Fatalf("expected default billing type %q, got %q", BillingTypeNone, e.BillingType)
		}
	})

	t.Run("ProvisionJob_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		j := &ProvisionJob{ID: "pj1", InstanceSlug: "slug"}
		if err := j.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if j.PK == "" || j.SK == "" {
			t.Fatalf("expected keys set")
		}
		if j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}
		if j.Status != ProvisionJobStatusQueued {
			t.Fatalf("expected default status %q, got %q", ProvisionJobStatusQueued, j.Status)
		}
		if j.MaxAttempts <= 0 {
			t.Fatalf("expected max attempts set")
		}
	})

	t.Run("AuditLogEntry_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		a := &AuditLogEntry{Actor: "alice", Action: "do", Target: "t"}
		if err := a.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if a.PK == "" || a.SK == "" {
			t.Fatalf("expected keys set")
		}
		if a.CreatedAt.IsZero() {
			t.Fatalf("expected CreatedAt set")
		}
	})

	t.Run("VanityDomainRequest_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &VanityDomainRequest{Domain: "example.com", RequestedBy: "u", InstanceSlug: "slug"}
		if err := r.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if r.PK == "" || r.SK == "" || r.GSI1PK == "" || r.GSI1SK == "" {
			t.Fatalf("expected keys set")
		}
		if r.Status != VanityDomainRequestStatusPending {
			t.Fatalf("expected default status %q, got %q", VanityDomainRequestStatusPending, r.Status)
		}
	})

	t.Run("ExternalInstanceRegistration_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &ExternalInstanceRegistration{ID: "id", Username: "u", Slug: "slug"}
		if err := r.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if r.PK == "" || r.SK == "" {
			t.Fatalf("expected keys set")
		}
		if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}
		if r.Status == "" {
			t.Fatalf("expected default status set")
		}
	})

	t.Run("TipHostState_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		s := &TipHostState{HostIDHex: "abc", DomainNormalized: "example.com", ContractAddress: "0x0"}
		if err := s.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if s.PK == "" || s.SK == "" {
			t.Fatalf("expected keys set")
		}
	})

	t.Run("TipHostRegistration_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &TipHostRegistration{ID: "id", DomainNormalized: "example.com", HostIDHex: "abc", WalletAddr: "0xabc", ChainID: 1}
		if err := r.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if r.PK == "" || r.SK == "" || r.GSI1PK == "" || r.GSI1SK == "" {
			t.Fatalf("expected keys set")
		}
		if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}
	})

	t.Run("TipRegistryOperation_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		o := &TipRegistryOperation{ID: "id", Kind: TipRegistryOperationKindRegisterHost, DomainNormalized: "example.com", HostIDHex: "abc"}
		if err := o.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if o.PK == "" || o.SK == "" || o.GSI1PK == "" || o.GSI1SK == "" {
			t.Fatalf("expected keys set")
		}
		if o.CreatedAt.IsZero() || o.UpdatedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}
		if o.Status == "" {
			t.Fatalf("expected default status set")
		}
	})

	t.Run("WebAuthnChallenge_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		c := &WebAuthnChallenge{Challenge: "challenge", UserID: "u", ExpiresAt: time.Now().Add(1 * time.Minute)}
		if err := c.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if c.PK == "" || c.SK == "" || c.TTL == 0 {
			t.Fatalf("expected keys + ttl set")
		}
	})

	t.Run("WebAuthnCredential_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		c := &WebAuthnCredential{ID: "cred", UserID: "u", PublicKey: []byte("pk"), AttestationType: "none"}
		if err := c.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if c.PK == "" || c.SK == "" {
			t.Fatalf("expected keys set")
		}
		if c.CreatedAt.IsZero() || c.LastUsedAt.IsZero() {
			t.Fatalf("expected timestamps set")
		}
	})

	t.Run("Attestation_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		a := &Attestation{ID: "id", Module: "mod", PolicyVersion: "1", JWS: "jws"}
		if err := a.BeforeCreate(); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if a.PK == "" || a.SK == "" {
			t.Fatalf("expected keys set")
		}
	})
}
