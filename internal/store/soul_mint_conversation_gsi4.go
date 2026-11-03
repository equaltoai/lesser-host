package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	// SoulMintConversationGSI4Name is the gsi4 agent-scoped time-ordered index
	// for SoulAgentMintConversation (issue #1067, part C2 of #1061):
	//
	//	gsi4PK = SOUL#AGENT#<agentId>
	//	gsi4SK = <createdAt>#<conversationId>   (fixed-width nanosecond UTC)
	//
	// The base SK is a crypto/rand token with no recency meaning, so the operator
	// mint-conversation list answers from this index instead of an SK-ordered
	// base-table query that silently selected an arbitrary page.
	SoulMintConversationGSI4Name = "gsi4"

	// soulMintConversationGSI4QueryPageSize caps the gsi4 read page. The operator
	// list route clamps caller limits into [1, soulMintConversationListMaxLimit],
	// so this is only the defensive floor for a non-positive limit.
	soulMintConversationGSI4QueryPageSize = 100

	// soulMintConversationGSI4DescOrder is the gsi4 sort direction (newest
	// first). The operator list queries gsi4SK descending; kept as a constant so
	// the query and its test assertions cannot drift apart.
	soulMintConversationGSI4DescOrder = "DESC"
)

// SoulMintConversationGSI4PK builds the gsi4 partition key for an agent. It
// matches the base PK (SOUL#AGENT#<agentId>) so the index groups every
// conversation item of the agent.
func SoulMintConversationGSI4PK(agentIDHex string) string {
	return models.SoulMintConversationGSI4PK(agentIDHex)
}

// ListSoulAgentMintConversationsByAgent returns the agent's mint conversations
// through the gsi4 time-ordered index, newest first, in one bounded page of at
// most limit items. Every read is a key-bounded GSI query with a capped page;
// there is no scan and no page can grow without bound. The returned items are
// ordered by gsi4SK descending, which encodes createdAt descending.
func (s *Store) ListSoulAgentMintConversationsByAgent(ctx context.Context, agentIDHex string, limit int) ([]*models.SoulAgentMintConversation, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("store not initialized")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if agentIDHex == "" {
		return nil, errors.New("agent id is required")
	}
	if limit <= 0 {
		limit = soulMintConversationGSI4QueryPageSize
	}

	var items []*models.SoulAgentMintConversation
	err := s.DB.WithContext(ctx).
		Model(&models.SoulAgentMintConversation{}).
		Index(SoulMintConversationGSI4Name).
		Where("gsi4PK", "=", SoulMintConversationGSI4PK(agentIDHex)).
		OrderBy("gsi4SK", soulMintConversationGSI4DescOrder).
		Limit(limit).
		All(&items)
	if err != nil && !IsNotFound(err) {
		return nil, err
	}
	return items, nil
}

// RequireSoulAgentMintConversationGSI4BackfillComplete fails closed until the
// operator has completed the gsi4 backfill for the SoulAgentMintConversation
// model in this table/stage.
//
// The stack update that creates gsi4 deploys before the backfill runs. During
// that window a gsi4 query would silently return an empty or partial
// conversation set as if it were complete, so the operator mint-conversation
// list consumer must call this gate first. The backfill tool writes the marker
// item only after a complete apply pass with zero errors (see
// scripts/soul-agent-identity-gsi3-backfill, which now covers both models).
func (s *Store) RequireSoulAgentMintConversationGSI4BackfillComplete(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return errors.New("store not initialized")
	}
	var marker models.SoulAgentMintConversationGSI4BackfillMarker
	err := s.DB.WithContext(ctx).
		Model(&models.SoulAgentMintConversationGSI4BackfillMarker{}).
		Where("PK", "=", models.SoulAgentMintConversationGSI4BackfillMarkerPK).
		Where("SK", "=", models.SoulAgentMintConversationGSI4BackfillMarkerSK).
		First(&marker)
	if err == nil {
		return nil
	}
	if IsNotFound(err) {
		return fmt.Errorf(
			"soul agent mint conversation gsi4 backfill not complete: run scripts/soul-agent-identity-gsi3-backfill --apply against this stage (marker %s/%s)",
			models.SoulAgentMintConversationGSI4BackfillMarkerPK,
			models.SoulAgentMintConversationGSI4BackfillMarkerSK,
		)
	}
	return fmt.Errorf("failed to read soul agent mint conversation gsi4 backfill marker: %w", err)
}
