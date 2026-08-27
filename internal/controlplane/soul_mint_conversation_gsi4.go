package controlplane

import (
	"github.com/theory-cloud/tabletheory/v3/pkg/core"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// soulMintConversationGSI4DescOrder is the gsi4 sort direction for the
// operator mint-conversation list (newest first). Kept as a constant so the
// OrderBy calls and their test assertions cannot drift apart.
const soulMintConversationGSI4DescOrder = "DESC"

// setSoulMintConversationGSI4Keys writes the gsi4 agent-scoped time-ordered
// index keys onto a conversation UpdateWithBuilder write (issue #1067, part C2
// of #1061). The keys are immutable (agentId + createdAt); every field-scoped
// conversation write re-writes them so healed/backfilled items can never
// silently drop out of the index. The write is guarded: a legacy item without a
// stored CreatedAt (so the update model carries no computed keys) keeps its
// existing index keys instead of being moved to a zero-time partition.
func setSoulMintConversationGSI4Keys(ub core.UpdateBuilder, conv *models.SoulAgentMintConversation) {
	if conv == nil || ub == nil {
		return
	}
	if conv.GSI4PK == "" || conv.GSI4SK == "" {
		return
	}
	ub.Set("GSI4PK", conv.GSI4PK)
	ub.Set("GSI4SK", conv.GSI4SK)
}
