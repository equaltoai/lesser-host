package controlplane

import (
	"net/http"
	"sort"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

// soulInstanceMintConversationListSummary is the compact conversation summary
// projected for the instance-key list endpoint. It excludes messages,
// produced declarations, and all private/internal fields.
type soulInstanceMintConversationListSummary struct {
	ConversationID string    `json:"conversation_id"`
	RegistrationID string    `json:"registration_id"`
	Status         string    `json:"status"`
	MessageCount   int       `json:"message_count"`
	LatestTurnID   string    `json:"latest_turn_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

type soulInstanceMintConversationListResponse struct {
	Conversations []soulInstanceMintConversationListSummary `json:"conversations"`
}

// handleSoulInstanceListMintConversationSummaries returns a bounded list of
// conversation summaries for an agent within the authenticated instance
// boundary. It queries HostedGenesisSession (the durable source of truth) via
// the agent-scoped GSI2 index, projects metadata-only summaries, sorts by
// updated_at descending, and bounds to max 50 results.
//
// This handler is instance-key authenticated and reuses the same auth context
// as the single-conversation read handler. No messages, produced declarations,
// or other private transcript material are included in the response.
func (s *Server) handleSoulInstanceListMintConversationSummaries(ctx *apptheory.Context) (*apptheory.Response, error) {
	start := time.Now()
	reqCtx, appErr := s.requireMintConversationInstanceReadContext(ctx)
	if appErr != nil {
		s.logSoulMintInstanceReadAccess(ctx, nil, ctxParam(ctx, "agentId"), "", soulMintInstanceReadRouteList, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}

	limit, appErr := parseSoulMintInstanceReadLimit(ctx)
	if appErr != nil {
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, "", soulMintInstanceReadRouteList, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}

	sessions, err := s.store.ListHostedGenesisSessionsByAgent(ctx.Context(), reqCtx.key.InstanceSlug, reqCtx.agentIDHex)
	if err != nil {
		appErr := soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "failed to list mint conversations", http.StatusInternalServerError, nil)
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, "", soulMintInstanceReadRouteList, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}

	summaries := make([]soulInstanceMintConversationListSummary, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		summaries = append(summaries, soulInstanceMintConversationListSummary{
			ConversationID: strings.TrimSpace(session.ConversationID),
			RegistrationID: strings.TrimSpace(session.RegistrationID),
			Status:         strings.TrimSpace(session.Status),
			MessageCount:   session.MessageCount,
			LatestTurnID:   strings.TrimSpace(session.LatestTurnID),
			CreatedAt:      session.CreatedAt.UTC(),
			UpdatedAt:      session.UpdatedAt.UTC(),
		})
	}

	// GSI2 sorts by createdAt; sort by updated_at descending in-memory as
	// required by the list contract. Tie-break by conversation_id for stability.
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].UpdatedAt.Equal(summaries[j].UpdatedAt) {
			return summaries[i].ConversationID > summaries[j].ConversationID
		}
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	// Bound to the requested limit (max 50, enforced by parseSoulMintInstanceReadLimit).
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}

	resp, jsonErr := soulMintInstanceReadJSON(http.StatusOK, soulInstanceMintConversationListResponse{
		Conversations: summaries,
	}, soulMintInstanceReadListMaxBytes)
	if jsonErr != nil {
		appErr := soulMintInstanceReadResponseError(jsonErr)
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, "", soulMintInstanceReadRouteList, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}
	s.recordSoulMintInstanceReadAudit(ctx, reqCtx.key, reqCtx.agentIDHex, "", soulMintInstanceReadRouteList, "success", resp.Status, len(resp.Body), start)
	return resp, nil
}
