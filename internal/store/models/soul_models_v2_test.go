package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- SoulAgentIdentity v2 extensions ---

func TestSoulAgentIdentity_V2Fields(t *testing.T) {
	a := &SoulAgentIdentity{
		AgentID:                " 0xABC ",
		Domain:                 " Example.COM ",
		LocalID:                " @Alice/ ",
		Wallet:                 " 0xDEF ",
		PrincipalAddress:       " 0x1234ABCD ",
		PrincipalSignature:     " 0xSIG123 ",
		LifecycleStatus:        " ACTIVE ",
		LifecycleReason:        " some reason ",
		SuccessorAgentId:       " 0xSUCC ",
		SelfDescriptionVersion: 2,
	}
	require.NoError(t, a.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", a.PK)
	require.Equal(t, "IDENTITY", a.SK)
	require.Equal(t, "0xabc", a.AgentID)
	require.Equal(t, "example.com", a.Domain)
	require.Equal(t, "alice", a.LocalID)
	require.Equal(t, "0x1234abcd", a.PrincipalAddress)
	require.Equal(t, "0xsig123", a.PrincipalSignature)
	require.Equal(t, "active", a.LifecycleStatus)
	require.Equal(t, "some reason", a.LifecycleReason)
	require.Equal(t, "0xsucc", a.SuccessorAgentId)
	require.Equal(t, 2, a.SelfDescriptionVersion)
}

func TestSoulAgentIdentity_LifecycleStatusConstants(t *testing.T) {
	require.Equal(t, "self_suspended", SoulAgentStatusSelfSuspended)
	require.Equal(t, "archived", SoulAgentStatusArchived)
	require.Equal(t, "succeeded", SoulAgentStatusSucceeded)
}

// --- SoulAgentReputation v2 extensions ---

func TestSoulAgentReputation_V2Fields(t *testing.T) {
	r := &SoulAgentReputation{
		AgentID:              " 0xABC ",
		Integrity:            0.85,
		DelegationsCompleted: 10,
		BoundaryViolations:   2,
		FailureRecoveries:    3,
	}
	require.NoError(t, r.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", r.PK)
	require.Equal(t, "REPUTATION", r.SK)
	require.Equal(t, 0.85, r.Integrity)
	require.Equal(t, int64(10), r.DelegationsCompleted)
	require.Equal(t, int64(2), r.BoundaryViolations)
	require.Equal(t, int64(3), r.FailureRecoveries)
}

// --- SoulAgentValidationRecord v2 extensions ---

func TestSoulAgentValidationRecord_OptInStatus(t *testing.T) {
	v := &SoulAgentValidationRecord{
		AgentID:       " 0xABC ",
		ChallengeID:   "chal-1",
		ChallengeType: "capability",
		ValidatorID:   "validator-1",
		Result:        SoulValidationResultPass,
		OptInStatus:   " ACCEPTED ",
	}
	require.NoError(t, v.BeforeCreate())

	require.Equal(t, "accepted", v.OptInStatus)
}

func TestSoulAgentValidationRecord_OptInStatusConstants(t *testing.T) {
	require.Equal(t, "accepted", SoulValidationOptInStatusAccepted)
	require.Equal(t, "declined", SoulValidationOptInStatusDeclined)
	require.Equal(t, "pending", SoulValidationOptInStatusPending)
}

// --- SoulOperation v2 Kind constants ---

func TestSoulOperation_V2KindConstants(t *testing.T) {
	require.Equal(t, "archive", SoulOperationKindArchive)
	require.Equal(t, "self_suspend", SoulOperationKindSelfSuspend)
	require.Equal(t, "self_reinstate", SoulOperationKindSelfReinstate)
	require.Equal(t, "designate_successor", SoulOperationKindDesignateSuccessor)
	require.Equal(t, "dispute", SoulOperationKindDispute)
}

// --- SoulAgentBoundary ---

func TestSoulAgentBoundary_Keys(t *testing.T) {
	b := &SoulAgentBoundary{
		AgentID:    " 0xABC ",
		BoundaryID: " boundary-001 ",
		Category:   " REFUSAL ",
		Statement:  " I will not do X. ",
		Rationale:  " Because Y. ",
		Supersedes: " boundary-000 ",
		Signature:  " 0xSIG ",
	}
	require.NoError(t, b.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", b.PK)
	require.Equal(t, "BOUNDARY#boundary-001", b.SK)
	require.Equal(t, "0xabc", b.AgentID)
	require.Equal(t, "boundary-001", b.BoundaryID)
	require.Equal(t, "refusal", b.Category)
	require.Equal(t, "I will not do X.", b.Statement)
	require.Equal(t, "Because Y.", b.Rationale)
	require.Equal(t, "boundary-000", b.Supersedes)
	require.Equal(t, "0xsig", b.Signature)
	require.False(t, b.AddedAt.IsZero())
}

func TestSoulAgentBoundary_CategoryConstants(t *testing.T) {
	require.Equal(t, "refusal", SoulBoundaryCategoryRefusal)
	require.Equal(t, "scope_limit", SoulBoundaryCategoryScopeLimit)
	require.Equal(t, "ethical_commitment", SoulBoundaryCategoryEthicalCommitment)
	require.Equal(t, "circuit_breaker", SoulBoundaryCategoryCircuitBreaker)
}

// --- SoulAgentContinuity ---

func TestSoulAgentContinuity_Keys(t *testing.T) {
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	c := &SoulAgentContinuity{
		AgentID:   " 0xABC ",
		Type:      " SIGNIFICANT_FAILURE ",
		Summary:   " Something failed. ",
		Recovery:  " Fixed it. ",
		Signature: " 0xSIG ",
		Timestamp: ts,
	}
	require.NoError(t, c.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", c.PK)
	require.Contains(t, c.SK, "CONTINUITY#")
	require.Contains(t, c.SK, "#significant_failure")
	require.Equal(t, "significant_failure", c.Type)
	require.Equal(t, "Something failed.", c.Summary)
	require.Equal(t, "Fixed it.", c.Recovery)
	require.Equal(t, "0xsig", c.Signature)
}

func TestSoulAgentContinuity_DefaultTimestamp(t *testing.T) {
	c := &SoulAgentContinuity{
		AgentID: "0xabc",
		Type:    "recovery",
	}
	require.NoError(t, c.BeforeCreate())
	require.False(t, c.Timestamp.IsZero())
}

func TestSoulAgentContinuity_EntryTypeConstants(t *testing.T) {
	require.Equal(t, "capability_acquired", SoulContinuityEntryTypeCapabilityAcquired)
	require.Equal(t, "capability_deprecated", SoulContinuityEntryTypeCapabilityDeprecated)
	require.Equal(t, "significant_failure", SoulContinuityEntryTypeSignificantFailure)
	require.Equal(t, "recovery", SoulContinuityEntryTypeRecovery)
	require.Equal(t, "boundary_added", SoulContinuityEntryTypeBoundaryAdded)
	require.Equal(t, "migration", SoulContinuityEntryTypeMigration)
	require.Equal(t, "model_change", SoulContinuityEntryTypeModelChange)
	require.Equal(t, "relationship_formed", SoulContinuityEntryTypeRelationshipFormed)
	require.Equal(t, "relationship_ended", SoulContinuityEntryTypeRelationshipEnded)
	require.Equal(t, "self_suspension", SoulContinuityEntryTypeSelfSuspension)
	require.Equal(t, "archived", SoulContinuityEntryTypeArchived)
	require.Equal(t, "succession_declared", SoulContinuityEntryTypeSuccessionDeclared)
	require.Equal(t, "succession_received", SoulContinuityEntryTypeSuccessionReceived)
}

// --- SoulAgentRelationship ---

func TestSoulAgentRelationship_Keys(t *testing.T) {
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	r := &SoulAgentRelationship{
		FromAgentID: " 0xFROM ",
		ToAgentID:   " 0xTO ",
		Type:        " DELEGATION ",
		Context:     ` {"taskType":"summarization"} `,
		Message:     " Great work. ",
		Signature:   " 0xSIG ",
		CreatedAt:   ts,
	}
	require.NoError(t, r.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xto", r.PK)
	require.Contains(t, r.SK, "RELATIONSHIP#0xfrom#")
	require.Equal(t, "0xfrom", r.FromAgentID)
	require.Equal(t, "0xto", r.ToAgentID)
	require.Equal(t, "delegation", r.Type)
	require.Equal(t, `{"taskType":"summarization"}`, r.Context)
	require.Equal(t, "Great work.", r.Message)
	require.Equal(t, "0xsig", r.Signature)
}

func TestSoulAgentRelationship_TypeConstants(t *testing.T) {
	require.Equal(t, "endorsement", SoulRelationshipTypeEndorsement)
	require.Equal(t, "delegation", SoulRelationshipTypeDelegation)
	require.Equal(t, "collaboration", SoulRelationshipTypeCollaboration)
	require.Equal(t, "trust_grant", SoulRelationshipTypeTrustGrant)
	require.Equal(t, "trust_revocation", SoulRelationshipTypeTrustRevocation)
}

// --- SoulAgentVersion ---

func TestSoulAgentVersion_Keys(t *testing.T) {
	v := &SoulAgentVersion{
		AgentID:         " 0xABC ",
		VersionNumber:   3,
		RegistrationUri: " s3://bucket/path ",
		ChangeSummary:   " Added new capabilities. ",
		SelfAttestation: " 0xATTEST ",
	}
	require.NoError(t, v.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", v.PK)
	require.Equal(t, "VERSION#3", v.SK)
	require.Equal(t, "0xabc", v.AgentID)
	require.Equal(t, 3, v.VersionNumber)
	require.Equal(t, "s3://bucket/path", v.RegistrationUri)
	require.Equal(t, "Added new capabilities.", v.ChangeSummary)
	require.False(t, v.CreatedAt.IsZero())
}

// --- SoulAgentMintConversation ---

func TestSoulAgentMintConversation_Keys(t *testing.T) {
	m := &SoulAgentMintConversation{
		AgentID:        " 0xABC ",
		ConversationID: " conv-001 ",
		Model:          " claude-opus-4-6 ",
	}
	require.NoError(t, m.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", m.PK)
	require.Equal(t, "MINT_CONVERSATION#conv-001", m.SK)
	require.Equal(t, "claude-opus-4-6", m.Model)
	require.Equal(t, SoulMintConversationStatusInProgress, m.Status)
	require.False(t, m.CreatedAt.IsZero())
}

func TestSoulAgentMintConversation_StatusConstants(t *testing.T) {
	require.Equal(t, "in_progress", SoulMintConversationStatusInProgress)
	require.Equal(t, "completed", SoulMintConversationStatusCompleted)
	require.Equal(t, "failed", SoulMintConversationStatusFailed)
}

// --- SoulAgentDispute ---

func TestSoulAgentDispute_Keys(t *testing.T) {
	d := &SoulAgentDispute{
		AgentID:   " 0xABC ",
		DisputeID: " dispute-001 ",
		SignalRef: " validation-123 ",
		Evidence:  " Here is evidence. ",
		Statement: " This is wrong. ",
	}
	require.NoError(t, d.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", d.PK)
	require.Equal(t, "DISPUTE#dispute-001", d.SK)
	require.Equal(t, "0xabc", d.AgentID)
	require.Equal(t, SoulDisputeStatusOpen, d.Status)
	require.False(t, d.CreatedAt.IsZero())
}

func TestSoulAgentDispute_StatusConstants(t *testing.T) {
	require.Equal(t, "open", SoulDisputeStatusOpen)
	require.Equal(t, "resolved", SoulDisputeStatusResolved)
	require.Equal(t, "dismissed", SoulDisputeStatusDismissed)
}

// --- SoulAgentFailure ---

func TestSoulAgentFailure_Keys(t *testing.T) {
	ts := time.Date(2026, 3, 10, 14, 30, 0, 0, time.UTC)
	f := &SoulAgentFailure{
		AgentID:     " 0xABC ",
		FailureID:   " fail-001 ",
		FailureType: " CONTENT_QUALITY ",
		Description: " Bad output. ",
		Impact:      " Low ",
		RecoveryRef: " continuity-entry-123 ",
		Timestamp:   ts,
	}
	require.NoError(t, f.BeforeCreate())

	require.Equal(t, "SOUL#AGENT#0xabc", f.PK)
	require.Contains(t, f.SK, "FAILURE#")
	require.Contains(t, f.SK, "#fail-001")
	require.Equal(t, "content_quality", f.FailureType)
	require.Equal(t, "Bad output.", f.Description)
	require.Equal(t, "Low", f.Impact)
	require.Equal(t, "continuity-entry-123", f.RecoveryRef)
}

func TestSoulAgentFailure_DefaultTimestamp(t *testing.T) {
	f := &SoulAgentFailure{
		AgentID:     "0xabc",
		FailureID:   "fail-001",
		FailureType: "content_quality",
	}
	require.NoError(t, f.BeforeCreate())
	require.False(t, f.Timestamp.IsZero())
}

// --- SoulRelationshipFromIndex ---

func TestSoulRelationshipFromIndex_Keys(t *testing.T) {
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	i := &SoulRelationshipFromIndex{
		FromAgentID: " 0xFROM ",
		ToAgentID:   " 0xTO ",
		Type:        " DELEGATION ",
		CreatedAt:   ts,
	}
	require.NoError(t, i.BeforeCreate())

	require.Equal(t, "SOUL#RELATIONSHIPS_FROM#0xfrom", i.PK)
	require.Contains(t, i.SK, "TO#0xto#")
	require.Equal(t, "delegation", i.Type)
}

func TestSoulRelationshipFromIndex_DefaultCreatedAt(t *testing.T) {
	i := &SoulRelationshipFromIndex{
		FromAgentID: "0xfrom",
		ToAgentID:   "0xto",
		Type:        "endorsement",
	}
	require.NoError(t, i.BeforeCreate())
	require.False(t, i.CreatedAt.IsZero())
}
