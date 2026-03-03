package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestModels_KeyAccessorsAndHooks_AreCallable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)

	t.Run("AuditLogEntry", func(t *testing.T) {
		t.Parallel()

		a := &AuditLogEntry{Target: "instance:demo", CreatedAt: now, ID: "1"}
		require.NoError(t, a.UpdateKeys())
		require.NotEmpty(t, a.GetPK())
		require.NotEmpty(t, a.GetSK())
	})

	t.Run("ControlPlaneConfig", func(t *testing.T) {
		t.Parallel()

		c := &ControlPlaneConfig{}
		require.NoError(t, c.UpdateKeys())
		require.NotEmpty(t, c.GetPK())
		require.NotEmpty(t, c.GetSK())
	})

	t.Run("Domain", func(t *testing.T) {
		t.Parallel()

		d := &Domain{Domain: "Example.COM", InstanceSlug: "demo", Type: DomainTypePrimary}
		require.NoError(t, d.UpdateKeys())
		require.NotEmpty(t, d.GetPK())
		require.NotEmpty(t, d.GetSK())
	})

	t.Run("Instance", func(t *testing.T) {
		t.Parallel()

		i := &Instance{Slug: "demo", Owner: "alice", CreatedAt: now}
		require.NoError(t, i.UpdateKeys())
		require.NotEmpty(t, i.GetPK())
		require.NotEmpty(t, i.GetSK())
	})

	t.Run("InstanceKey", func(t *testing.T) {
		t.Parallel()

		k := &InstanceKey{ID: "key1", InstanceSlug: "demo"}
		require.NoError(t, k.UpdateKeys())
		require.NotEmpty(t, k.GetPK())
		require.NotEmpty(t, k.GetSK())
	})

	t.Run("LinkPreview", func(t *testing.T) {
		t.Parallel()

		p := &LinkPreview{ID: "preview", ExpiresAt: now.Add(24 * time.Hour)}
		require.NoError(t, p.UpdateKeys())
		require.NotEmpty(t, p.GetPK())
		require.NotEmpty(t, p.GetSK())
	})

	t.Run("LinkSafetyBasicResult", func(t *testing.T) {
		t.Parallel()

		r := &LinkSafetyBasicResult{ID: "result", ExpiresAt: now.Add(24 * time.Hour)}
		require.NoError(t, r.UpdateKeys())
		require.NotEmpty(t, r.GetPK())
		require.NotEmpty(t, r.GetSK())
	})

	t.Run("OperatorSession", func(t *testing.T) {
		t.Parallel()

		s := &OperatorSession{ID: "sess1", Username: "alice"}
		require.NoError(t, s.UpdateKeys())
		require.NotEmpty(t, s.GetPK())
		require.NotEmpty(t, s.GetSK())
	})

	t.Run("OperatorUser", func(t *testing.T) {
		t.Parallel()

		u := &User{Username: "alice"}
		require.NoError(t, u.UpdateKeys())
		require.NotEmpty(t, u.GetPK())
		require.NotEmpty(t, u.GetSK())

	})

	t.Run("ProvisionConsentChallenge", func(t *testing.T) {
		t.Parallel()

		c := &ProvisionConsentChallenge{
			ID:           "challenge",
			Username:     "alice",
			InstanceSlug: "demo",
			Stage:        "lab",
			WalletAddr:   "0x00000000000000000000000000000000000000aa",
			ExpiresAt:    now.Add(10 * time.Minute),
		}
		require.Equal(t, MainTableName(), c.TableName())
		require.NoError(t, c.BeforeCreate())
		require.NotEmpty(t, c.GetPK())
		require.NotEmpty(t, c.GetSK())
		require.NotZero(t, c.TTL)
	})

	t.Run("SetupSession", func(t *testing.T) {
		t.Parallel()

		s := &SetupSession{ID: "setup", Purpose: "bootstrap"}
		require.NoError(t, s.UpdateKeys())
		require.NotEmpty(t, s.GetPK())
		require.NotEmpty(t, s.GetSK())
	})

	t.Run("TipHostRegistration", func(t *testing.T) {
		t.Parallel()

		r := &TipHostRegistration{ID: "reg", DomainNormalized: "example.com", HostIDHex: "0x00", CreatedAt: now}
		require.NoError(t, r.UpdateKeys())
		require.NoError(t, r.BeforeUpdate())
		require.NotEmpty(t, r.GetPK())
		require.NotEmpty(t, r.GetSK())
	})

	t.Run("TipRegistryOperation", func(t *testing.T) {
		t.Parallel()

		op := &TipRegistryOperation{ID: "op", HostIDHex: "0x00", CreatedAt: now}
		require.NoError(t, op.UpdateKeys())
		require.NoError(t, op.BeforeUpdate())
		require.NotEmpty(t, op.GetPK())
		require.NotEmpty(t, op.GetSK())
	})

	t.Run("UsageLedgerEntry", func(t *testing.T) {
		t.Parallel()

		e := &UsageLedgerEntry{ID: "1", InstanceSlug: "demo", Month: "2026-02", CreatedAt: now}
		require.Equal(t, MainTableName(), e.TableName())
		require.NoError(t, e.UpdateKeys())
		require.NotEmpty(t, e.GetPK())
		require.NotEmpty(t, e.GetSK())
	})

	t.Run("VanityDomainRequest", func(t *testing.T) {
		t.Parallel()

		r := &VanityDomainRequest{Domain: "example.com", InstanceSlug: "demo", CreatedAt: now}
		require.NoError(t, r.UpdateKeys())
		require.NoError(t, r.BeforeUpdate())
		require.NotEmpty(t, r.GetPK())
		require.NotEmpty(t, r.GetSK())
	})

	t.Run("WalletCredential", func(t *testing.T) {
		t.Parallel()

		cred := &WalletCredential{Username: "alice", Address: "0x00000000000000000000000000000000000000aa", Type: walletTypeEthereum}
		require.NoError(t, cred.UpdateKeys())
		require.NotEmpty(t, cred.GetPK())
		require.NotEmpty(t, cred.GetSK())
		require.NoError(t, cred.BeforeUpdate())
	})

	t.Run("WalletIndex", func(t *testing.T) {
		t.Parallel()

		idx := &WalletIndex{}
		idx.UpdateKeys(walletTypeEthereum, "0x00000000000000000000000000000000000000aa", "alice")
		require.NotEmpty(t, idx.GetPK())
		require.NotEmpty(t, idx.GetSK())
	})

	t.Run("WebAuthnChallenge", func(t *testing.T) {
		t.Parallel()

		ch := &WebAuthnChallenge{Challenge: "challenge", ExpiresAt: now.Add(time.Minute)}
		require.NoError(t, ch.UpdateKeys())
		require.NotEmpty(t, ch.GetPK())
		require.NotEmpty(t, ch.GetSK())
	})

	t.Run("WebAuthnCredential", func(t *testing.T) {
		t.Parallel()

		c := &WebAuthnCredential{ID: "cred", UserID: "alice"}
		require.NoError(t, c.UpdateKeys())
		require.NoError(t, c.BeforeUpdate())
		require.NotEmpty(t, c.GetPK())
		require.NotEmpty(t, c.GetSK())
	})

	t.Run("SoulWalletRotationRequest", func(t *testing.T) {
		t.Parallel()

		var nilReq *SoulWalletRotationRequest
		require.NoError(t, nilReq.UpdateKeys())

		expiresAt := now.Add(10 * time.Minute)
		req := &SoulWalletRotationRequest{
			AgentID:       " 0xAA ",
			Username:      " Alice ",
			CurrentWallet: " 0x00000000000000000000000000000000000000AA ",
			NewWallet:     " 0x00000000000000000000000000000000000000BB ",
			Nonce:         " 123 ",
			Deadline:      456,
			DigestHex:     " 0XABCD ",
			ExpiresAt:     expiresAt,
			ConfirmedAt:   now,
		}
		require.Equal(t, MainTableName(), req.TableName())
		require.NoError(t, req.BeforeCreate())
		require.Equal(t, "SOUL#AGENT#0xaa", req.GetPK())
		require.Equal(t, "ROTATION#Alice", req.GetSK())
		require.Equal(t, expiresAt.UTC().Unix(), req.TTL)
		require.Equal(t, "0xabcd", req.DigestHex)
		require.NoError(t, req.BeforeUpdate())
		require.NotZero(t, req.UpdatedAt)
	})

	t.Run("SoulAgentValidationChallenge", func(t *testing.T) {
		t.Parallel()

		c := &SoulAgentValidationChallenge{
			AgentID:       " 0xAA ",
			ChallengeID:   " chal ",
			ChallengeType: " DNS_TXT ",
			ValidatorID:   " VALIDATOR ",
			Request:       " req ",
			Response:      " resp ",
			Status:        "",
			Result:        " PASS ",
			Score:         0.9,
			IssuedAt:      time.Time{},
			RespondedAt:   now,
			EvaluatedAt:   now,
			UpdatedAt:     time.Time{},
		}
		require.Equal(t, MainTableName(), c.TableName())
		require.NoError(t, c.BeforeCreate())
		require.Equal(t, "SOUL#AGENT#0xaa", c.GetPK())
		require.Equal(t, "VALIDATIONCHAL#chal", c.GetSK())
		require.NotZero(t, c.TTL)
		require.Equal(t, SoulValidationChallengeStatusIssued, c.Status)
		require.Equal(t, "dns_txt", c.ChallengeType)
		require.Equal(t, "validator", c.ValidatorID)
		require.Equal(t, "pass", c.Result)

		c2 := &SoulAgentValidationChallenge{
			AgentID:       "0xaa",
			ChallengeID:   "c2",
			ChallengeType: "dns_txt",
			ValidatorID:   "validator",
			Status:        SoulValidationChallengeStatusIssued,
			OptInStatus:   SoulValidationOptInStatusPending,
		}
		require.NoError(t, c2.BeforeUpdate())
	})

	t.Run("SoulAgentRegistration", func(t *testing.T) {
		t.Parallel()

		r := &SoulAgentRegistration{
			ID:               " reg1 ",
			Username:         " Alice ",
			DomainRaw:        " Example.COM ",
			DomainNormalized: " Example.COM ",
			LocalIDRaw:       " @Alice/ ",
			LocalID:          " @Alice/ ",
			AgentID:          " 0xAA ",
			Wallet:           " 0x00000000000000000000000000000000000000AA ",
			Capabilities:     []string{"  CAP  "},
			WalletNonce:      " n ",
			WalletMessage:    " msg ",
			ProofToken:       " tok ",
			Status:           "",
		}
		require.Equal(t, MainTableName(), r.TableName())
		require.NoError(t, r.BeforeCreate())
		require.NotEmpty(t, r.GetPK())
		require.NotEmpty(t, r.GetSK())
		require.NotZero(t, r.TTL)
		require.NotZero(t, r.ExpiresAt)
		require.Equal(t, SoulAgentRegistrationStatusPending, r.Status)
		require.Equal(t, "example.com", r.DomainNormalized)
		require.Equal(t, "alice", r.LocalID)

		require.NoError(t, r.BeforeUpdate())
		require.NotZero(t, r.UpdatedAt)
	})

	t.Run("SoulAgentPeerEndorsement", func(t *testing.T) {
		t.Parallel()

		e := &SoulAgentPeerEndorsement{
			AgentID:         " 0xAA ",
			EndorserAgentID: " 0xBB ",
			Message:         " hello ",
			Signature:       " 0xSIG ",
		}
		require.Equal(t, MainTableName(), e.TableName())
		require.NoError(t, e.BeforeCreate())
		require.NotEmpty(t, e.GetPK())
		require.NotEmpty(t, e.GetSK())
	})

	t.Run("SoulAgentIdentity", func(t *testing.T) {
		t.Parallel()

		a := &SoulAgentIdentity{
			AgentID: " 0xAA ",
			Domain:  " Example.COM ",
			LocalID: " @Alice/ ",
			Wallet:  " 0xWALLET ",
			Status:  "",
		}
		require.Equal(t, MainTableName(), a.TableName())
		require.NoError(t, a.BeforeCreate())
		require.NoError(t, a.BeforeUpdate())
		require.NotEmpty(t, a.GetPK())
		require.NotEmpty(t, a.GetSK())
	})

	t.Run("SoulAgentReputation", func(t *testing.T) {
		t.Parallel()

		r := &SoulAgentReputation{AgentID: " 0xAA "}
		require.Equal(t, MainTableName(), r.TableName())
		require.NoError(t, r.BeforeCreate())
		require.NoError(t, r.BeforeUpdate())
		require.NotEmpty(t, r.GetPK())
		require.NotEmpty(t, r.GetSK())
	})

	t.Run("SoulAgentIndexes", func(t *testing.T) {
		t.Parallel()

		w := &SoulWalletAgentIndex{Wallet: " 0xWALLET ", AgentID: " 0xAA "}
		require.Equal(t, MainTableName(), w.TableName())
		require.NoError(t, w.BeforeCreate())
		require.NotEmpty(t, w.GetPK())
		require.NotEmpty(t, w.GetSK())

		d := &SoulDomainAgentIndex{Domain: " Example.COM ", LocalID: " @Alice/ ", AgentID: " 0xAA "}
		require.Equal(t, MainTableName(), d.TableName())
		require.NoError(t, d.BeforeCreate())
		require.NotEmpty(t, d.GetPK())
		require.NotEmpty(t, d.GetSK())

		c := &SoulCapabilityAgentIndex{Capability: " TTS ", Domain: " Example.COM ", LocalID: " @Alice/ ", AgentID: " 0xAA "}
		require.Equal(t, MainTableName(), c.TableName())
		require.NoError(t, c.BeforeCreate())
		require.NotEmpty(t, c.GetPK())
		require.NotEmpty(t, c.GetSK())
	})
}
