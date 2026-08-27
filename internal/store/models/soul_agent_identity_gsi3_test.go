package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSoulAgentIdentityGSI3_UpdateKeys(t *testing.T) {
	t.Parallel()

	t.Run("recomputes on status change", func(t *testing.T) {
		t.Parallel()

		a := &SoulAgentIdentity{
			AgentID: "0xabc",
			Status:  SoulAgentStatusActive,
		}
		require.NoError(t, a.UpdateKeys())
		require.Equal(t, "IDENTITY#active", a.GSI3PK)
		require.Equal(t, "0xabc", a.GSI3SK)

		a.Status = SoulAgentStatusSuspended
		require.NoError(t, a.UpdateKeys())
		require.Equal(t, "IDENTITY#suspended", a.GSI3PK)
		require.Equal(t, "0xabc", a.GSI3SK)
	})

	t.Run("preserves gsi3 keys when status absent (partial update models)", func(t *testing.T) {
		t.Parallel()

		a := &SoulAgentIdentity{
			AgentID: "0xabc",
			Status:  SoulAgentStatusActive,
		}
		require.NoError(t, a.UpdateKeys())
		require.Equal(t, "IDENTITY#active", a.GSI3PK)

		// A field-scoped update model may omit Status; UpdateKeys must not
		// corrupt the gsi3 keys with an empty status prefix.
		partial := &SoulAgentIdentity{AgentID: "0xabc", GSI3PK: a.GSI3PK, GSI3SK: a.GSI3SK}
		require.NoError(t, partial.UpdateKeys())
		require.Equal(t, "IDENTITY#active", partial.GSI3PK)
		require.Equal(t, "0xabc", partial.GSI3SK)
	})

	t.Run("normalizes status casing", func(t *testing.T) {
		t.Parallel()

		a := &SoulAgentIdentity{AgentID: "0xabc", Status: " ACTIVE "}
		require.NoError(t, a.UpdateKeys())
		require.Equal(t, "IDENTITY#active", a.GSI3PK)
	})
}

func TestSoulAgentIdentityStatuses_CompleteSet(t *testing.T) {
	t.Parallel()

	got := SoulAgentIdentityStatuses()
	require.ElementsMatch(t, []string{
		SoulAgentStatusPending,
		SoulAgentStatusActive,
		SoulAgentStatusSuspended,
		SoulAgentStatusSelfSuspended,
		SoulAgentStatusArchived,
		SoulAgentStatusSucceeded,
		SoulAgentStatusBurned,
	}, got)
	require.Len(t, got, 7)
}

func TestSoulAgentIdentityGSI3BackfillMarker_UpdateKeys(t *testing.T) {
	t.Parallel()

	m := &SoulAgentIdentityGSI3BackfillMarker{Scanned: 10, Updated: 8, AlreadyCorrect: 1, Errors: 1}
	require.NoError(t, m.UpdateKeys())
	require.Equal(t, SoulAgentIdentityGSI3BackfillMarkerPK, m.PK)
	require.Equal(t, SoulAgentIdentityGSI3BackfillMarkerSK, m.SK)

	s := m.String()
	require.Contains(t, s, "scanned=10")
	require.Contains(t, s, "updated=8")
	require.Contains(t, s, "already_correct=1")
	require.Contains(t, s, "errors=1")
}
