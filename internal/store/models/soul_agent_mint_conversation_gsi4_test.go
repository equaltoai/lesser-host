package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSoulAgentMintConversationGSI4_UpdateKeys(t *testing.T) {
	t.Parallel()

	t.Run("computes agent-scoped time-ordered keys", func(t *testing.T) {
		t.Parallel()

		m := &SoulAgentMintConversation{
			AgentID:        " 0xABC ",
			ConversationID: " conv-1 ",
			CreatedAt:      time.Date(2026, 3, 7, 12, 34, 56, 123456789, time.UTC),
		}
		require.NoError(t, m.UpdateKeys())
		require.Equal(t, "SOUL#AGENT#0xabc", m.GSI4PK)
		require.Equal(t, "2026-03-07T12:34:56.123456789Z#conv-1", m.GSI4SK)
	})

	t.Run("gsi4SK orders lexicographically exactly like createdAt", func(t *testing.T) {
		t.Parallel()

		older := &SoulAgentMintConversation{
			AgentID: "0xabc", ConversationID: "conv-old",
			CreatedAt: time.Date(2026, 3, 7, 12, 34, 56, 900000000, time.UTC),
		}
		newer := &SoulAgentMintConversation{
			AgentID: "0xabc", ConversationID: "conv-new",
			CreatedAt: time.Date(2026, 3, 7, 12, 34, 57, 0, time.UTC),
		}
		require.NoError(t, older.UpdateKeys())
		require.NoError(t, newer.UpdateKeys())
		// DESC (newest first) must place the newer conversation first; the
		// fixed-width nanosecond key format guarantees string order == time
		// order even across differing fraction lengths (RFC3339Nano does not).
		require.Greater(t, newer.GSI4SK, older.GSI4SK)
		require.True(t, newer.CreatedAt.After(older.CreatedAt))
	})

	t.Run("preserves gsi4 keys when createdAt absent (partial update models)", func(t *testing.T) {
		t.Parallel()

		m := &SoulAgentMintConversation{
			AgentID:        "0xabc",
			ConversationID: "conv-1",
			CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		}
		require.NoError(t, m.UpdateKeys())
		require.NotEmpty(t, m.GSI4PK)
		require.NotEmpty(t, m.GSI4SK)

		// A field-scoped update model may omit CreatedAt; UpdateKeys must not
		// corrupt the gsi4 keys with a zero-time prefix.
		partial := &SoulAgentMintConversation{AgentID: "0xabc", ConversationID: "conv-1", GSI4PK: m.GSI4PK, GSI4SK: m.GSI4SK}
		require.NoError(t, partial.UpdateKeys())
		require.Equal(t, m.GSI4PK, partial.GSI4PK)
		require.Equal(t, m.GSI4SK, partial.GSI4SK)
	})
}

func TestSoulAgentMintConversationGSI4BackfillMarker_UpdateKeys(t *testing.T) {
	t.Parallel()

	m := &SoulAgentMintConversationGSI4BackfillMarker{Scanned: 10, Updated: 8, Repaired: 2, AlreadyCorrect: 1, Errors: 1}
	require.NoError(t, m.UpdateKeys())
	require.Equal(t, SoulAgentMintConversationGSI4BackfillMarkerPK, m.PK)
	require.Equal(t, SoulAgentMintConversationGSI4BackfillMarkerSK, m.SK)

	s := m.String()
	require.Contains(t, s, "scanned=10")
	require.Contains(t, s, "updated=8")
	require.Contains(t, s, "repaired=2")
	require.Contains(t, s, "already_correct=1")
	require.Contains(t, s, "errors=1")
}
