package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testStateTableName = "tbl_test"

func TestMainTableName_DefaultAndOverride(t *testing.T) {
	t.Setenv("STATE_TABLE_NAME", "")
	require.Equal(t, "lesser-host-state", MainTableName())

	t.Setenv("STATE_TABLE_NAME", "  custom  ")
	require.Equal(t, "custom", MainTableName())
}

func TestModelContracts_TableNameAndKeyAccessors(t *testing.T) {
	t.Setenv("STATE_TABLE_NAME", testStateTableName)

	type tableNamer interface {
		TableName() string
	}
	for _, model := range []tableNamer{
		AIJob{},
		AIResult{},
		Attestation{},
		AuditLogEntry{},
		BillingPaymentMethod{},
		BillingProfile{},
		ControlPlaneConfig{},
		CreditPurchase{},
		Domain{},
		ExternalInstanceRegistration{},
		Instance{},
		InstanceBudgetMonth{},
		InstanceKey{},
		LinkPreview{},
		LinkSafetyBasicResult{},
		OperatorSession{},
		User{},
		ProvisionJob{},
		RenderArtifact{},
		SetupSession{},
		SoulCommSendIdempotency{},
		TipHostRegistration{},
		TipHostState{},
		TipRegistryOperation{},
		WebAuthnChallenge{},
		WebAuthnCredential{},
		VanityDomainRequest{},
		WalletChallenge{},
		WalletCredential{},
		WalletIndex{},
	} {
		require.Equal(t, testStateTableName, model.TableName())
	}

	aiJob := &AIJob{ID: "id", InstanceSlug: "slug", Status: AIJobStatusQueued}
	require.NoError(t, aiJob.BeforeCreate())
	require.NotEmpty(t, aiJob.GetPK())
	require.NotEmpty(t, aiJob.GetSK())

	aiRes := &AIResult{ID: "id", InstanceSlug: "slug"}
	require.NoError(t, aiRes.BeforeCreate())
	require.NotEmpty(t, aiRes.GetPK())
	require.NotEmpty(t, aiRes.GetSK())

	a := &Attestation{ID: "id"}
	require.NoError(t, a.BeforeCreate())
	require.NotEmpty(t, a.GetPK())
	require.NotEmpty(t, a.GetSK())

	bl := &BillingProfile{Username: "u"}
	require.NoError(t, bl.BeforeCreate())
	require.NotEmpty(t, bl.GetPK())
	require.NotEmpty(t, bl.GetSK())

	pm := &BillingPaymentMethod{ID: "pm", Username: "u", Provider: BillingProviderStripe}
	require.NoError(t, pm.BeforeCreate())
	require.NotEmpty(t, pm.GetPK())
	require.NotEmpty(t, pm.GetSK())

	p := &CreditPurchase{ID: "p", Username: "u", InstanceSlug: "slug", Month: "2026-02"}
	require.NoError(t, p.BeforeCreate())
	require.NotEmpty(t, p.GetPK())
	require.NotEmpty(t, p.GetSK())

	er := &ExternalInstanceRegistration{ID: "id", Username: "u", Slug: "slug"}
	require.NoError(t, er.BeforeCreate())
	require.NotEmpty(t, er.GetPK())
	require.NotEmpty(t, er.GetSK())

	bm := &InstanceBudgetMonth{InstanceSlug: "slug", Month: "2026-02"}
	require.NoError(t, bm.BeforeCreate())
	require.NotEmpty(t, bm.GetPK())
	require.NotEmpty(t, bm.GetSK())

	pj := &ProvisionJob{ID: "pj", InstanceSlug: "slug"}
	require.NoError(t, pj.BeforeCreate())
	require.NotEmpty(t, pj.GetPK())
	require.NotEmpty(t, pj.GetSK())

	ra := &RenderArtifact{ID: "r", NormalizedURL: "https://example.com", PolicyVersion: "1"}
	require.NoError(t, ra.BeforeCreate())
	require.NotEmpty(t, ra.GetPK())
	require.NotEmpty(t, ra.GetSK())

	th := &TipHostState{HostIDHex: "abc", DomainNormalized: "example.com", ContractAddress: "0x0"}
	require.NoError(t, th.BeforeCreate())
	require.NotEmpty(t, th.GetPK())
	require.NotEmpty(t, th.GetSK())

	idem := &SoulCommSendIdempotency{
		InstanceSlug:   "slug",
		AgentID:        "0xabc",
		IdempotencyKey: "retry-1",
		RequestHash:    "hash-1",
		MessageID:      "comm-msg-1",
		ChannelType:    "email",
		To:             "alice@example.com",
	}
	require.NoError(t, idem.BeforeCreate())
	require.NotEmpty(t, idem.GetPK())
	require.NotEmpty(t, idem.GetSK())
}

func TestModelContracts_BeforeUpdate_MaintainsKeys(t *testing.T) {
	now := time.Unix(10, 0).UTC()

	job := &AIJob{ID: "j", InstanceSlug: "slug", Status: AIJobStatusQueued, ExpiresAt: now.Add(1 * time.Hour), CreatedAt: now, UpdatedAt: now}
	_ = job.UpdateKeys()
	before := job.UpdatedAt
	require.NoError(t, job.BeforeUpdate())
	require.True(t, job.UpdatedAt.After(before))
	require.Equal(t, job.ExpiresAt.Unix(), job.TTL)
	require.NotEmpty(t, job.GetPK())
	require.NotEmpty(t, job.GetSK())

	profile := &BillingProfile{Username: "u", Provider: BillingProviderNone, CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = profile.UpdateKeys()
	before = profile.UpdatedAt
	require.NoError(t, profile.BeforeUpdate())
	require.True(t, profile.UpdatedAt.After(before))

	method := &BillingPaymentMethod{ID: "pm", Username: "u", Provider: BillingProviderStripe, Status: BillingPaymentMethodStatusActive, CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = method.UpdateKeys()
	before = method.UpdatedAt
	require.NoError(t, method.BeforeUpdate())
	require.True(t, method.UpdatedAt.After(before))

	purchase := &CreditPurchase{ID: "p", Username: "u", InstanceSlug: "slug", Month: "2026-02", CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = purchase.UpdateKeys()
	before = purchase.UpdatedAt
	require.NoError(t, purchase.BeforeUpdate())
	require.True(t, purchase.UpdatedAt.After(before))

	reg := &ExternalInstanceRegistration{ID: "id", Username: "u", Slug: "slug", CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = reg.UpdateKeys()
	before = reg.UpdatedAt
	require.NoError(t, reg.BeforeUpdate())
	require.True(t, reg.UpdatedAt.After(before))

	budget := &InstanceBudgetMonth{InstanceSlug: "slug", Month: "2026-02", UpdatedAt: time.Unix(1, 0).UTC()}
	_ = budget.UpdateKeys()
	before = budget.UpdatedAt
	require.NoError(t, budget.BeforeUpdate())
	require.True(t, budget.UpdatedAt.After(before))

	prov := &ProvisionJob{ID: "pj", InstanceSlug: "slug", CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = prov.UpdateKeys()
	before = prov.UpdatedAt
	require.NoError(t, prov.BeforeUpdate())
	require.True(t, prov.UpdatedAt.After(before))

	artifact := &RenderArtifact{ID: "r", NormalizedURL: "https://example.com", PolicyVersion: "1", CreatedAt: now, ExpiresAt: now.Add(1 * time.Hour)}
	_ = artifact.UpdateKeys()
	beforeTTL := artifact.TTL
	artifact.ExpiresAt = now.Add(2 * time.Hour)
	require.NoError(t, artifact.BeforeUpdate())
	require.NotEqual(t, beforeTTL, artifact.TTL)
	require.NotZero(t, artifact.TTL)
	require.NotEmpty(t, artifact.GSI1PK)
	require.NotEmpty(t, artifact.GSI1SK)

	host := &TipHostState{HostIDHex: "abc", DomainNormalized: "example.com", ContractAddress: "0x0", UpdatedAt: time.Unix(1, 0).UTC()}
	_ = host.UpdateKeys()
	before = host.UpdatedAt
	require.NoError(t, host.BeforeUpdate())
	require.True(t, host.UpdatedAt.After(before))
}
