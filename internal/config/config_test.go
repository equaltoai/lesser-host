package config

import "testing"

func TestEnvHelpers(t *testing.T) {
	t.Setenv("X", "  hi ")
	if got := envString("X"); got != "hi" {
		t.Fatalf("envString: expected %q, got %q", "hi", got)
	}

	t.Setenv("BOOL", "YES")
	if !envBoolOn("BOOL") {
		t.Fatalf("envBoolOn: expected true")
	}
	t.Setenv("BOOL", "0")
	if envBoolOn("BOOL") {
		t.Fatalf("envBoolOn: expected false")
	}

	t.Setenv("N", "100")
	if got := envInt64Bounded("N", 5, 1, 10); got != 5 {
		t.Fatalf("envInt64Bounded: expected fallback, got %d", got)
	}
	t.Setenv("N", "7")
	if got := envInt64Bounded("N", 5, 1, 10); got != 7 {
		t.Fatalf("envInt64Bounded: expected 7, got %d", got)
	}

	t.Setenv("P", "-1")
	if got := envInt64Positive("P", 9); got != 9 {
		t.Fatalf("envInt64Positive: expected fallback, got %d", got)
	}
	t.Setenv("P", "2")
	if got := envInt64Positive("P", 9); got != 2 {
		t.Fatalf("envInt64Positive: expected 2, got %d", got)
	}

	t.Setenv("U16", "999")
	if got := envUint16Max("U16", 3, 500); got != 3 {
		t.Fatalf("envUint16Max: expected fallback, got %d", got)
	}

	if got := parseCSV(" a, ,b "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("parseCSV unexpected: %#v", got)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("STAGE", "")
	t.Setenv("WEBAUTHN_ORIGINS", " https://a ,https://b ")
	t.Setenv("ATTESTATION_PUBLIC_KEY_IDS", "k1,k2")
	t.Setenv("TIP_ENABLED", "true")
	t.Setenv("TIP_DEFAULT_HOST_FEE_BPS", "250")

	cfg := Load()

	if cfg.Stage != "lab" {
		t.Fatalf("expected default stage lab, got %q", cfg.Stage)
	}
	if cfg.AppName != "lesser-host" {
		t.Fatalf("expected app name lesser-host, got %q", cfg.AppName)
	}
	if len(cfg.WebAuthnOrigins) != 2 {
		t.Fatalf("expected origins parsed, got %#v", cfg.WebAuthnOrigins)
	}
	if len(cfg.AttestationPublicKeyIDs) != 2 {
		t.Fatalf("expected public key ids parsed, got %#v", cfg.AttestationPublicKeyIDs)
	}
	if !cfg.TipEnabled {
		t.Fatalf("expected tip enabled")
	}
	if cfg.TipDefaultHostFeeBps != 250 {
		t.Fatalf("expected tip fee bps 250, got %d", cfg.TipDefaultHostFeeBps)
	}
	if cfg.ManagedParentDomain == "" || cfg.ManagedInstanceRoleName == "" || cfg.ManagedDefaultRegion == "" {
		t.Fatalf("expected managed defaults set")
	}
	if cfg.ManagedLesserBodyGitHubOwner == "" || cfg.ManagedLesserBodyGitHubRepo == "" {
		t.Fatalf("expected managed lesser-body defaults set")
	}
	if cfg.PaymentsCentsPer1000Credits <= 0 {
		t.Fatalf("expected payments pricing default set")
	}
	if cfg.SoulV2StrictIntegrity {
		t.Fatalf("expected strict integrity default off")
	}
	if cfg.SoulCommMailboxRetentionDays != 90 {
		t.Fatalf("expected mailbox retention default 90, got %d", cfg.SoulCommMailboxRetentionDays)
	}
}

func TestLoad_SoulV2StrictIntegrity(t *testing.T) {
	t.Setenv("SOUL_V2_STRICT_INTEGRITY", "true")
	cfg := Load()
	if !cfg.SoulV2StrictIntegrity {
		t.Fatalf("expected strict integrity enabled")
	}
}

func TestLoad_SoulCommMailboxConfig(t *testing.T) {
	t.Setenv("SOUL_COMM_MAILBOX_BUCKET_NAME", " mailbox-bucket ")
	t.Setenv("SOUL_COMM_MAILBOX_RETENTION_DAYS", "120")
	cfg := Load()
	if cfg.SoulCommMailboxBucketName != "mailbox-bucket" {
		t.Fatalf("unexpected mailbox bucket: %q", cfg.SoulCommMailboxBucketName)
	}
	if cfg.SoulCommMailboxRetentionDays != 120 {
		t.Fatalf("unexpected mailbox retention: %d", cfg.SoulCommMailboxRetentionDays)
	}

	t.Setenv("SOUL_COMM_MAILBOX_RETENTION_DAYS", "999")
	cfg = Load()
	if cfg.SoulCommMailboxRetentionDays != 90 {
		t.Fatalf("expected invalid retention fallback, got %d", cfg.SoulCommMailboxRetentionDays)
	}
}
