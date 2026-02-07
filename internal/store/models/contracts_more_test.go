package models

import (
	"testing"
	"time"
)

func TestMainTableName_DefaultAndOverride(t *testing.T) {
	t.Setenv("STATE_TABLE_NAME", "")
	if got := MainTableName(); got != "lesser-host-state" {
		t.Fatalf("expected default table name, got %q", got)
	}

	t.Setenv("STATE_TABLE_NAME", "  custom  ")
	if got := MainTableName(); got != "custom" {
		t.Fatalf("expected trimmed table name, got %q", got)
	}
}

func TestModelContracts_TableNameAndKeyAccessors(t *testing.T) {
	t.Setenv("STATE_TABLE_NAME", "tbl_test")

	if (AIJob{}).TableName() != "tbl_test" ||
		(AIResult{}).TableName() != "tbl_test" ||
		(Attestation{}).TableName() != "tbl_test" ||
		(AuditLogEntry{}).TableName() != "tbl_test" ||
		(BillingPaymentMethod{}).TableName() != "tbl_test" ||
		(BillingProfile{}).TableName() != "tbl_test" ||
		(ControlPlaneConfig{}).TableName() != "tbl_test" ||
		(CreditPurchase{}).TableName() != "tbl_test" ||
		(Domain{}).TableName() != "tbl_test" ||
		(ExternalInstanceRegistration{}).TableName() != "tbl_test" ||
		(Instance{}).TableName() != "tbl_test" ||
		(InstanceBudgetMonth{}).TableName() != "tbl_test" ||
		(InstanceKey{}).TableName() != "tbl_test" ||
		(LinkPreview{}).TableName() != "tbl_test" ||
		(LinkSafetyBasicResult{}).TableName() != "tbl_test" ||
		(OperatorSession{}).TableName() != "tbl_test" ||
		(User{}).TableName() != "tbl_test" ||
		(ProvisionJob{}).TableName() != "tbl_test" ||
		(RenderArtifact{}).TableName() != "tbl_test" ||
		(SetupSession{}).TableName() != "tbl_test" ||
		(TipHostRegistration{}).TableName() != "tbl_test" ||
		(TipHostState{}).TableName() != "tbl_test" ||
		(TipRegistryOperation{}).TableName() != "tbl_test" {
		t.Fatalf("expected TableName() to use MainTableName()")
	}

	aiJob := &AIJob{ID: "id", InstanceSlug: "slug", Status: AIJobStatusQueued}
	_ = aiJob.BeforeCreate()
	if aiJob.GetPK() == "" || aiJob.GetSK() == "" {
		t.Fatalf("expected ai job keys set")
	}

	aiRes := &AIResult{ID: "id", InstanceSlug: "slug"}
	_ = aiRes.BeforeCreate()
	if aiRes.GetPK() == "" || aiRes.GetSK() == "" {
		t.Fatalf("expected ai result keys set")
	}

	a := &Attestation{ID: "id"}
	_ = a.BeforeCreate()
	if a.GetPK() == "" || a.GetSK() == "" {
		t.Fatalf("expected attestation keys set")
	}

	bl := &BillingProfile{Username: "u"}
	_ = bl.BeforeCreate()
	if bl.GetPK() == "" || bl.GetSK() == "" {
		t.Fatalf("expected billing profile keys set")
	}

	pm := &BillingPaymentMethod{ID: "pm", Username: "u", Provider: BillingProviderStripe}
	_ = pm.BeforeCreate()
	if pm.GetPK() == "" || pm.GetSK() == "" {
		t.Fatalf("expected payment method keys set")
	}

	p := &CreditPurchase{ID: "p", Username: "u", InstanceSlug: "slug", Month: "2026-02"}
	_ = p.BeforeCreate()
	if p.GetPK() == "" || p.GetSK() == "" {
		t.Fatalf("expected credit purchase keys set")
	}

	er := &ExternalInstanceRegistration{ID: "id", Username: "u", Slug: "slug"}
	_ = er.BeforeCreate()
	if er.GetPK() == "" || er.GetSK() == "" {
		t.Fatalf("expected external registration keys set")
	}

	bm := &InstanceBudgetMonth{InstanceSlug: "slug", Month: "2026-02"}
	_ = bm.BeforeCreate()
	if bm.GetPK() == "" || bm.GetSK() == "" {
		t.Fatalf("expected budget month keys set")
	}

	pj := &ProvisionJob{ID: "pj", InstanceSlug: "slug"}
	_ = pj.BeforeCreate()
	if pj.GetPK() == "" || pj.GetSK() == "" {
		t.Fatalf("expected provision job keys set")
	}

	ra := &RenderArtifact{ID: "r", NormalizedURL: "https://example.com", PolicyVersion: "1"}
	_ = ra.BeforeCreate()
	if ra.GetPK() == "" || ra.GetSK() == "" {
		t.Fatalf("expected render artifact keys set")
	}

	th := &TipHostState{HostIDHex: "abc", DomainNormalized: "example.com", ContractAddress: "0x0"}
	_ = th.BeforeCreate()
	if th.GetPK() == "" || th.GetSK() == "" {
		t.Fatalf("expected tip host state keys set")
	}
}

func TestModelContracts_BeforeUpdate_MaintainsKeys(t *testing.T) {
	now := time.Unix(10, 0).UTC()

	job := &AIJob{ID: "j", InstanceSlug: "slug", Status: AIJobStatusQueued, ExpiresAt: now.Add(1 * time.Hour), CreatedAt: now, UpdatedAt: now}
	_ = job.UpdateKeys()
	before := job.UpdatedAt
	if err := job.BeforeUpdate(); err != nil {
		t.Fatalf("AIJob.BeforeUpdate: %v", err)
	}
	if !job.UpdatedAt.After(before) {
		t.Fatalf("expected UpdatedAt advanced")
	}
	if job.TTL != job.ExpiresAt.Unix() {
		t.Fatalf("expected TTL from ExpiresAt")
	}
	if job.GetPK() == "" || job.GetSK() == "" {
		t.Fatalf("expected keys still set")
	}

	profile := &BillingProfile{Username: "u", Provider: BillingProviderNone, CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = profile.UpdateKeys()
	before = profile.UpdatedAt
	if err := profile.BeforeUpdate(); err != nil {
		t.Fatalf("BillingProfile.BeforeUpdate: %v", err)
	}
	if !profile.UpdatedAt.After(before) {
		t.Fatalf("expected UpdatedAt advanced")
	}

	method := &BillingPaymentMethod{ID: "pm", Username: "u", Provider: BillingProviderStripe, Status: BillingPaymentMethodStatusActive, CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = method.UpdateKeys()
	before = method.UpdatedAt
	if err := method.BeforeUpdate(); err != nil {
		t.Fatalf("BillingPaymentMethod.BeforeUpdate: %v", err)
	}
	if !method.UpdatedAt.After(before) {
		t.Fatalf("expected UpdatedAt advanced")
	}

	purchase := &CreditPurchase{ID: "p", Username: "u", InstanceSlug: "slug", Month: "2026-02", CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = purchase.UpdateKeys()
	before = purchase.UpdatedAt
	if err := purchase.BeforeUpdate(); err != nil {
		t.Fatalf("CreditPurchase.BeforeUpdate: %v", err)
	}
	if !purchase.UpdatedAt.After(before) {
		t.Fatalf("expected UpdatedAt advanced")
	}

	reg := &ExternalInstanceRegistration{ID: "id", Username: "u", Slug: "slug", CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = reg.UpdateKeys()
	before = reg.UpdatedAt
	if err := reg.BeforeUpdate(); err != nil {
		t.Fatalf("ExternalInstanceRegistration.BeforeUpdate: %v", err)
	}
	if !reg.UpdatedAt.After(before) {
		t.Fatalf("expected UpdatedAt advanced")
	}

	budget := &InstanceBudgetMonth{InstanceSlug: "slug", Month: "2026-02", UpdatedAt: time.Unix(1, 0).UTC()}
	_ = budget.UpdateKeys()
	before = budget.UpdatedAt
	if err := budget.BeforeUpdate(); err != nil {
		t.Fatalf("InstanceBudgetMonth.BeforeUpdate: %v", err)
	}
	if !budget.UpdatedAt.After(before) {
		t.Fatalf("expected UpdatedAt advanced")
	}

	prov := &ProvisionJob{ID: "pj", InstanceSlug: "slug", CreatedAt: now, UpdatedAt: time.Unix(1, 0).UTC()}
	_ = prov.UpdateKeys()
	before = prov.UpdatedAt
	if err := prov.BeforeUpdate(); err != nil {
		t.Fatalf("ProvisionJob.BeforeUpdate: %v", err)
	}
	if !prov.UpdatedAt.After(before) {
		t.Fatalf("expected UpdatedAt advanced")
	}

	artifact := &RenderArtifact{ID: "r", NormalizedURL: "https://example.com", PolicyVersion: "1", CreatedAt: now, ExpiresAt: now.Add(1 * time.Hour)}
	_ = artifact.UpdateKeys()
	beforeTTL := artifact.TTL
	artifact.ExpiresAt = now.Add(2 * time.Hour)
	if err := artifact.BeforeUpdate(); err != nil {
		t.Fatalf("RenderArtifact.BeforeUpdate: %v", err)
	}
	if artifact.TTL == beforeTTL || artifact.TTL == 0 {
		t.Fatalf("expected TTL updated, got %d", artifact.TTL)
	}
	if artifact.GSI1PK == "" || artifact.GSI1SK == "" {
		t.Fatalf("expected render artifact gsi keys set")
	}

	host := &TipHostState{HostIDHex: "abc", DomainNormalized: "example.com", ContractAddress: "0x0", UpdatedAt: time.Unix(1, 0).UTC()}
	_ = host.UpdateKeys()
	before = host.UpdatedAt
	if err := host.BeforeUpdate(); err != nil {
		t.Fatalf("TipHostState.BeforeUpdate: %v", err)
	}
	if !host.UpdatedAt.After(before) {
		t.Fatalf("expected UpdatedAt advanced")
	}
}
