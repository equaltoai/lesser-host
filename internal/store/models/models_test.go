package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestModels_UpdateKeysAndDefaults(t *testing.T) {
	t.Parallel()

	t.Run("User_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		u := &User{Username: " alice ", Role: ""}
		require.NoError(t, u.BeforeCreate())
		require.NotEmpty(t, u.PK)
		require.NotEmpty(t, u.SK)
		require.Equal(t, RoleOperator, u.Role)
		require.False(t, u.CreatedAt.IsZero())
		require.Equal(t, "alice", u.Username)
	})

	t.Run("OperatorSession_UpdateKeys", func(t *testing.T) {
		t.Parallel()

		s := &OperatorSession{
			ID:        " tok ",
			ExpiresAt: time.Unix(10, 0).UTC(),
		}
		require.NoError(t, s.UpdateKeys())
		require.Equal(t, "tok", s.ID)
		require.NotEmpty(t, s.PK)
		require.NotEmpty(t, s.SK)
		require.Equal(t, s.ExpiresAt.Unix(), s.TTL)
	})

	t.Run("Instance_BeforeCreate_Defaults", func(t *testing.T) {
		t.Parallel()

		inst := &Instance{Slug: "slug", Owner: "owner"}
		require.NoError(t, inst.BeforeCreate())
		require.NotEmpty(t, inst.PK)
		require.NotEmpty(t, inst.SK)
		require.NotEmpty(t, inst.GSI1PK)
		require.NotEmpty(t, inst.GSI1SK)
		require.False(t, inst.CreatedAt.IsZero())
		require.Equal(t, InstanceStatusActive, inst.Status)
		require.NotNil(t, inst.HostedPreviewsEnabled)
		require.NotNil(t, inst.LinkSafetyEnabled)
		require.NotNil(t, inst.RendersEnabled)
		require.NotNil(t, inst.ModerationEnabled)
		require.NotNil(t, inst.AIEnabled)
		require.NotNil(t, inst.AIPricingMultiplierBps)
		require.NotNil(t, inst.AIMaxInflightJobs)
	})

	t.Run("Domain_BeforeCreateAndUpdate", func(t *testing.T) {
		t.Parallel()

		d := &Domain{Domain: " Example.COM ", InstanceSlug: " slug "}
		require.NoError(t, d.BeforeCreate())
		require.NotEmpty(t, d.PK)
		require.NotEmpty(t, d.SK)
		require.NotEmpty(t, d.GSI1PK)
		require.NotEmpty(t, d.GSI1SK)
		require.Equal(t, DomainTypeVanity, d.Type)
		require.Equal(t, DomainStatusPending, d.Status)
		require.False(t, d.CreatedAt.IsZero())
		require.False(t, d.UpdatedAt.IsZero())

		before := d.UpdatedAt
		require.NoError(t, d.BeforeUpdate())
		require.False(t, d.UpdatedAt.Before(before))
	})

	t.Run("WalletModels_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		now := time.Unix(100, 0).UTC()

		ch := &WalletChallenge{ID: "c1", Username: "u", Address: "0xabc", ChainID: 1, ExpiresAt: now.Add(time.Minute)}
		require.NoError(t, ch.BeforeCreate())
		require.NotEmpty(t, ch.PK)
		require.NotEmpty(t, ch.SK)

		cred := &WalletCredential{Username: "u", Address: "0xabc", ChainID: 1, Type: "ethereum", LinkedAt: now, LastUsed: now}
		require.NoError(t, cred.BeforeCreate())
		require.NotEmpty(t, cred.PK)
		require.NotEmpty(t, cred.SK)

		idx := &WalletIndex{}
		idx.UpdateKeys("ethereum", "0xabc", "u")
		require.NotEmpty(t, idx.PK)
		require.NotEmpty(t, idx.SK)
	})

	t.Run("ControlPlaneConfig_Keys", func(t *testing.T) {
		t.Parallel()

		c := &ControlPlaneConfig{}
		require.NoError(t, c.BeforeCreate())
		require.Equal(t, "CONTROL_PLANE", c.PK)
		require.Equal(t, "CONFIG", c.SK)
	})

	t.Run("SetupSession_Keys", func(t *testing.T) {
		t.Parallel()

		s := &SetupSession{ID: " s ", ExpiresAt: time.Unix(123, 0).UTC()}
		require.NoError(t, s.UpdateKeys())
		require.NotEmpty(t, s.PK)
		require.NotEmpty(t, s.SK)
		require.NotZero(t, s.TTL)
	})

	t.Run("InstanceKey_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		k := &InstanceKey{ID: " key ", InstanceSlug: "slug"}
		require.NoError(t, k.BeforeCreate())
		require.NotEmpty(t, k.PK)
		require.NotEmpty(t, k.SK)
		require.False(t, k.CreatedAt.IsZero())
	})

	t.Run("InstanceBudgetMonth_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		b := &InstanceBudgetMonth{InstanceSlug: " slug ", Month: "2026-02"}
		require.NoError(t, b.BeforeCreate())
		require.NotEmpty(t, b.PK)
		require.NotEmpty(t, b.SK)
		require.False(t, b.UpdatedAt.IsZero())
	})

	t.Run("BillingProfile_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		p := &BillingProfile{Username: " u ", Provider: ""}
		require.NoError(t, p.BeforeCreate())
		require.NotEmpty(t, p.PK)
		require.NotEmpty(t, p.SK)
		require.False(t, p.CreatedAt.IsZero())
		require.False(t, p.UpdatedAt.IsZero())
		require.Equal(t, "u", p.Username)
		require.Equal(t, BillingProviderNone, p.Provider)
	})

	t.Run("CreditPurchase_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		p := &CreditPurchase{ID: "p1", Username: "u", InstanceSlug: "slug", Credits: 100, AmountCents: 250}
		require.NoError(t, p.BeforeCreate())
		require.NotEmpty(t, p.PK)
		require.NotEmpty(t, p.SK)
		require.False(t, p.CreatedAt.IsZero())
		require.False(t, p.UpdatedAt.IsZero())
		require.NotEmpty(t, p.Status)
	})

	t.Run("BillingPaymentMethod_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		m := &BillingPaymentMethod{ID: "pm1", Username: "u", Provider: "stripe"}
		require.NoError(t, m.BeforeCreate())
		require.NotEmpty(t, m.PK)
		require.NotEmpty(t, m.SK)
		require.False(t, m.CreatedAt.IsZero())
		require.False(t, m.UpdatedAt.IsZero())
	})

	t.Run("AIJob_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		j := &AIJob{ID: "j1", InstanceSlug: "slug", Module: "MOD", PolicyVersion: "1", ModelSet: "set", InputsHash: "hash"}
		require.NoError(t, j.BeforeCreate())
		require.NotEmpty(t, j.PK)
		require.NotEmpty(t, j.SK)
		require.NotZero(t, j.TTL)
		require.Equal(t, AIJobStatusQueued, j.Status)
		require.Greater(t, j.MaxAttempts, int64(0))
	})

	t.Run("AIResult_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &AIResult{ID: "r1", InstanceSlug: "slug", Module: "m", PolicyVersion: "1", ModelSet: "set", InputsHash: "hash", ResultJSON: "{}"}
		require.NoError(t, r.BeforeCreate())
		require.NotEmpty(t, r.PK)
		require.NotEmpty(t, r.SK)
		require.NotZero(t, r.TTL)
	})

	t.Run("RenderArtifact_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &RenderArtifact{ID: "r1", NormalizedURL: "https://example.com", PolicyVersion: "1"}
		require.NoError(t, r.BeforeCreate())
		require.NotEmpty(t, r.PK)
		require.NotEmpty(t, r.SK)
		require.False(t, r.CreatedAt.IsZero())
		require.False(t, r.ExpiresAt.IsZero())
		require.NotZero(t, r.TTL)
	})

	t.Run("LinkPreview_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		p := &LinkPreview{ID: "p1", NormalizedURL: "https://example.com", PolicyVersion: "1"}
		require.NoError(t, p.BeforeCreate())
		require.NotEmpty(t, p.PK)
		require.NotEmpty(t, p.SK)
		require.False(t, p.StoredAt.IsZero())
		require.False(t, p.FetchedAt.IsZero())
		require.False(t, p.ExpiresAt.IsZero())
		require.NotZero(t, p.TTL)
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
		require.NoError(t, e.BeforeCreate())
		require.NotEmpty(t, e.PK)
		require.NotEmpty(t, e.SK)
		require.False(t, e.CreatedAt.IsZero())
		require.Equal(t, BillingTypeNone, e.BillingType)
	})

	t.Run("ProvisionJob_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		j := &ProvisionJob{ID: "pj1", InstanceSlug: "slug"}
		require.NoError(t, j.BeforeCreate())
		require.NotEmpty(t, j.PK)
		require.NotEmpty(t, j.SK)
		require.False(t, j.CreatedAt.IsZero())
		require.False(t, j.UpdatedAt.IsZero())
		require.Equal(t, ProvisionJobStatusQueued, j.Status)
		require.Greater(t, j.MaxAttempts, int64(0))
	})

	t.Run("AuditLogEntry_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		a := &AuditLogEntry{Actor: "alice", Action: "do", Target: "t"}
		require.NoError(t, a.BeforeCreate())
		require.NotEmpty(t, a.PK)
		require.NotEmpty(t, a.SK)
		require.False(t, a.CreatedAt.IsZero())
	})

	t.Run("VanityDomainRequest_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &VanityDomainRequest{Domain: "example.com", RequestedBy: "u", InstanceSlug: "slug"}
		require.NoError(t, r.BeforeCreate())
		require.NotEmpty(t, r.PK)
		require.NotEmpty(t, r.SK)
		require.NotEmpty(t, r.GSI1PK)
		require.NotEmpty(t, r.GSI1SK)
		require.Equal(t, VanityDomainRequestStatusPending, r.Status)
	})

	t.Run("ExternalInstanceRegistration_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &ExternalInstanceRegistration{ID: "id", Username: "u", Slug: "slug"}
		require.NoError(t, r.BeforeCreate())
		require.NotEmpty(t, r.PK)
		require.NotEmpty(t, r.SK)
		require.False(t, r.CreatedAt.IsZero())
		require.False(t, r.UpdatedAt.IsZero())
		require.NotEmpty(t, r.Status)
	})

	t.Run("TipHostState_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		s := &TipHostState{HostIDHex: "abc", DomainNormalized: "example.com", ContractAddress: "0x0"}
		require.NoError(t, s.BeforeCreate())
		require.NotEmpty(t, s.PK)
		require.NotEmpty(t, s.SK)
	})

	t.Run("TipHostRegistration_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		r := &TipHostRegistration{ID: "id", DomainNormalized: "example.com", HostIDHex: "abc", WalletAddr: "0xabc", ChainID: 1}
		require.NoError(t, r.BeforeCreate())
		require.NotEmpty(t, r.PK)
		require.NotEmpty(t, r.SK)
		require.NotEmpty(t, r.GSI1PK)
		require.NotEmpty(t, r.GSI1SK)
		require.False(t, r.CreatedAt.IsZero())
		require.False(t, r.UpdatedAt.IsZero())
	})

	t.Run("TipRegistryOperation_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		o := &TipRegistryOperation{ID: "id", Kind: TipRegistryOperationKindRegisterHost, DomainNormalized: "example.com", HostIDHex: "abc"}
		require.NoError(t, o.BeforeCreate())
		require.NotEmpty(t, o.PK)
		require.NotEmpty(t, o.SK)
		require.NotEmpty(t, o.GSI1PK)
		require.NotEmpty(t, o.GSI1SK)
		require.False(t, o.CreatedAt.IsZero())
		require.False(t, o.UpdatedAt.IsZero())
		require.NotEmpty(t, o.Status)
	})

	t.Run("WebAuthnChallenge_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		c := &WebAuthnChallenge{Challenge: "challenge", UserID: "u", ExpiresAt: time.Now().Add(1 * time.Minute)}
		require.NoError(t, c.BeforeCreate())
		require.NotEmpty(t, c.PK)
		require.NotEmpty(t, c.SK)
		require.NotZero(t, c.TTL)
	})

	t.Run("WebAuthnCredential_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		c := &WebAuthnCredential{ID: "cred", UserID: "u", PublicKey: []byte("pk"), AttestationType: "none"}
		require.NoError(t, c.BeforeCreate())
		require.NotEmpty(t, c.PK)
		require.NotEmpty(t, c.SK)
		require.False(t, c.CreatedAt.IsZero())
		require.False(t, c.LastUsedAt.IsZero())
	})

	t.Run("Attestation_BeforeCreate", func(t *testing.T) {
		t.Parallel()

		a := &Attestation{ID: "id", Module: "mod", PolicyVersion: "1", JWS: "jws"}
		require.NoError(t, a.BeforeCreate())
		require.NotEmpty(t, a.PK)
		require.NotEmpty(t, a.SK)
	})
}
