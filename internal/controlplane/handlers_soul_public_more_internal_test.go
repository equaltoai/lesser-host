package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestSetSoulPublicHeaders_CORSAllowlistAndVary(t *testing.T) {
	t.Parallel()

	t.Run("allow_star_ignores_origin", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{SoulPublicCORSOrigins: []string{"*"}}}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"origin": {"https://evil.example"}}}}
		resp := &apptheory.Response{}

		s.setSoulPublicHeaders(ctx, resp, " public, max-age=12 ")
		if got := strings.TrimSpace(resp.Headers["access-control-allow-origin"][0]); got != "*" {
			t.Fatalf("expected allow-origin *, got %q", got)
		}
		if got := strings.TrimSpace(resp.Headers["cache-control"][0]); got != "public, max-age=12" {
			t.Fatalf("unexpected cache-control: %q", got)
		}
	})

	t.Run("allow_specific_origin_sets_vary", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{SoulPublicCORSOrigins: []string{"https://app.example"}}}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"origin": {"https://app.example"}}}}
		resp := &apptheory.Response{Headers: map[string][]string{"vary": {"accept-encoding"}}}

		s.setSoulPublicHeaders(ctx, resp, "")
		if got := strings.TrimSpace(resp.Headers["access-control-allow-origin"][0]); got != "https://app.example" {
			t.Fatalf("expected allow-origin to echo request origin, got %q", got)
		}
		vary := resp.Headers["vary"]
		hasOrigin := false
		for _, v := range vary {
			if strings.EqualFold(strings.TrimSpace(v), "origin") {
				hasOrigin = true
				break
			}
		}
		if !hasOrigin {
			t.Fatalf("expected vary to include origin, got %#v", vary)
		}
	})

	t.Run("origin_not_allowed_sets_no_header", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{SoulPublicCORSOrigins: []string{"https://app.example"}}}
		ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"origin": {"https://other.example"}}}}
		resp := &apptheory.Response{}

		s.setSoulPublicHeaders(ctx, resp, "")
		if got := resp.Headers["access-control-allow-origin"]; len(got) != 0 {
			t.Fatalf("expected no allow-origin header, got %#v", got)
		}
	})
}

func TestSearchSoulAgentsByENS_RequiresENSName(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	_, _, _, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{})
	if appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request ens required, got %#v", appErr)
	}
}

func TestSearchSoulAgentsByENS_ResolutionNotFoundReturnsEmpty(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()
	assertSoulPublicENSSearchResults(t, s, soulPublicSearchParams{ENSName: "agent.lessersoul.eth"}, "")
}

func TestSearchSoulAgentsByENS_StatusDefaultsToActiveOnly(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	mockSoulPublicENSResolution(t, &tdb, agentID)
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-a", Status: models.SoulAgentStatusSuspended}
	}).Once()
	assertSoulPublicENSSearchResults(t, s, soulPublicSearchParams{ENSName: "agent.lessersoul.eth"}, "")
}

func TestSearchSoulAgentsByENS_BoundaryFilterExcludesAgent(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	mockSoulPublicENSResolution(t, &tdb, agentID)
	mockSoulPublicENSIdentity(t, &tdb, agentID)
	tdb.qBoundIdx.On("First", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	assertSoulPublicENSSearchResults(t, s, soulPublicSearchParams{
		ENSName:    "agent.lessersoul.eth",
		Boundary:   "finance",
		Channels:   []string{"email"},
		Limit:      1,
		LocalExact: false,
	}, "")
}

func TestSearchSoulAgentsByENS_ChannelFilterExcludesAgent(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	mockSoulPublicENSResolution(t, &tdb, agentID)
	mockSoulPublicENSIdentity(t, &tdb, agentID)
	tdb.qBoundIdx.On("First", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulBoundaryKeywordAgentIndex](t, args, 0)
		*dest = models.SoulBoundaryKeywordAgentIndex{AgentID: agentID, Domain: "example.com", LocalID: "agent-b", Keyword: "finance"}
	}).Once()
	tdb.qChanIdx.On("First", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	assertSoulPublicENSSearchResults(t, s, soulPublicSearchParams{
		ENSName:  "agent.lessersoul.eth",
		Boundary: "finance",
		Channels: []string{"email"},
	}, "")
}

func TestSearchSoulAgentsByENS_BoundaryAndChannelFiltersIncludeAgent(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	mockSoulPublicENSResolution(t, &tdb, agentID)
	mockSoulPublicENSIdentity(t, &tdb, agentID)
	tdb.qBoundIdx.On("First", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulBoundaryKeywordAgentIndex](t, args, 0)
		*dest = models.SoulBoundaryKeywordAgentIndex{AgentID: agentID, Domain: "example.com", LocalID: "agent-b", Keyword: "finance"}
	}).Once()
	tdb.qChanIdx.On("First", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulChannelAgentIndex](t, args, 0)
		*dest = models.SoulChannelAgentIndex{AgentID: agentID, Domain: "example.com", LocalID: "agent-b", ChannelType: "email"}
	}).Once()
	assertSoulPublicENSSearchResults(t, s, soulPublicSearchParams{
		ENSName:  "agent.lessersoul.eth",
		Boundary: "finance",
		Channels: []string{"email"},
	}, agentID)
}

func TestQuerySoulSearchByChannels_ChannelRequired(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	if _, _, _, appErr := s.querySoulSearchByChannels(context.Background(), nil, "", "", false, "", 10); appErr == nil {
		t.Fatalf("expected channel required error")
	}
}

func TestQuerySoulSearchByChannels_InvalidCursor(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	if _, _, _, appErr := s.querySoulSearchByChannels(context.Background(), []string{"email"}, "", "", false, "bad", 10); appErr == nil {
		t.Fatalf("expected invalid cursor error")
	}
}

func TestQuerySoulSearchByChannels_ReturnsCursorForSameChannel(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qChanIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{HasMore: true, NextCursor: " next1 "}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulChannelAgentIndex](t, args, 0)
		*dest = []*models.SoulChannelAgentIndex{{AgentID: agentID, Domain: "example.com", LocalID: "agent-a", ChannelType: "email"}}
	}).Once()

	entries, hasMore, nextCursor, appErr := s.querySoulSearchByChannels(context.Background(), []string{"email", "phone"}, "", "", false, "", 1)
	if appErr != nil || len(entries) != 1 || !hasMore || strings.TrimSpace(nextCursor) == "" {
		t.Fatalf("unexpected: entries=%#v hasMore=%v nextCursor=%q err=%v", entries, hasMore, nextCursor, appErr)
	}
	chIndex, inner, ok := decodeSoulChannelSearchCursor(nextCursor)
	if !ok || chIndex != 0 || inner != "next1" {
		t.Fatalf("unexpected cursor: index=%d inner=%q ok=%v raw=%q", chIndex, inner, ok, nextCursor)
	}
}

func TestQuerySoulSearchByChannels_AdvancesChannelIndex(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qChanIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulChannelAgentIndex](t, args, 0)
		*dest = []*models.SoulChannelAgentIndex{{AgentID: agentID, Domain: "example.com", LocalID: "agent-a", ChannelType: "email"}}
	}).Once()

	entries, hasMore, nextCursor, appErr := s.querySoulSearchByChannels(context.Background(), []string{"email", "phone"}, "", "", false, "", 1)
	if appErr != nil || len(entries) != 1 || !hasMore {
		t.Fatalf("unexpected: entries=%#v hasMore=%v nextCursor=%q err=%v", entries, hasMore, nextCursor, appErr)
	}
	chIndex, inner, ok := decodeSoulChannelSearchCursor(nextCursor)
	if !ok || chIndex != 1 || inner != "" {
		t.Fatalf("unexpected channel-advance cursor: index=%d inner=%q ok=%v raw=%q", chIndex, inner, ok, nextCursor)
	}
}

func mockSoulPublicENSResolution(t *testing.T, tdb *soulPublicTestDB, agentID string) {
	t.Helper()
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
}

func mockSoulPublicENSIdentity(t *testing.T, tdb *soulPublicTestDB, agentID string) {
	t.Helper()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-b", Status: models.SoulAgentStatusActive}
	}).Once()
}

func assertSoulPublicENSSearchResults(t *testing.T, s *Server, params soulPublicSearchParams, expectedAgentID string) {
	t.Helper()
	results, hasMore, nextCursor, appErr := s.searchSoulAgentsByENS(context.Background(), params)
	if appErr != nil || hasMore || nextCursor != "" {
		t.Fatalf("unexpected: results=%#v hasMore=%v nextCursor=%q err=%v", results, hasMore, nextCursor, appErr)
	}
	if expectedAgentID == "" {
		if len(results) != 0 {
			t.Fatalf("expected no results, got %#v", results)
		}
		return
	}
	if len(results) != 1 || results[0].AgentID != expectedAgentID {
		t.Fatalf("expected agent %q, got %#v", expectedAgentID, results)
	}
}

func TestQuerySoulSearchByChannelType_ErrorBranches(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, _, _, appErr := s.querySoulSearchByChannelType(context.Background(), "sms", "", "", false, "", 10); appErr == nil {
		t.Fatalf("expected invalid channel error")
	}

	tdb.qChanIdx.On("AllPaginated", mock.Anything).Return(nil, errors.New("boom")).Once()
	if _, _, _, appErr := s.querySoulSearchByChannelType(context.Background(), "email", "", "", false, "", 10); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
}

func TestSoulSearchEntryPassesFilters_ChannelFilterBranch(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("cc", 32)
	entry := soulSearchIndexEntry{AgentID: agentID, Domain: "example.com", LocalID: "agent-c"}

	t.Run("excludes_agent_without_any_matching_channel", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-c", Status: models.SoulAgentStatusActive}
		}).Once()
		tdb.qChanIdx.On("First", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

		pass, err := s.soulSearchEntryPassesFilters(context.Background(), entry, soulPublicSearchParams{Channels: []string{"email"}}, soulSearchPrimaryDomain)
		if err != nil || pass {
			t.Fatalf("expected channel filter exclusion, got pass=%v err=%v", pass, err)
		}
	})

	t.Run("includes_agent_when_any_channel_matches", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-c", Status: models.SoulAgentStatusActive}
		}).Once()
		tdb.qChanIdx.On("First", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulChannelAgentIndex](t, args, 0)
			*dest = models.SoulChannelAgentIndex{AgentID: agentID, Domain: "example.com", LocalID: "agent-c", ChannelType: "email"}
		}).Once()

		pass, err := s.soulSearchEntryPassesFilters(context.Background(), entry, soulPublicSearchParams{Channels: []string{"email"}}, soulSearchPrimaryDomain)
		if err != nil || !pass {
			t.Fatalf("expected channel filter to pass, got pass=%v err=%v", pass, err)
		}
	})

	t.Run("propagates_channel_index_errors", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-c", Status: models.SoulAgentStatusActive}
		}).Once()
		tdb.qChanIdx.On("First", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(errors.New("boom")).Once()

		pass, err := s.soulSearchEntryPassesFilters(context.Background(), entry, soulPublicSearchParams{Channels: []string{"email"}}, soulSearchPrimaryDomain)
		if err == nil || pass {
			t.Fatalf("expected error propagation, got pass=%v err=%v", pass, err)
		}
	})

	t.Run("rejects_claim_level_mismatch_without_db", func(t *testing.T) {
		t.Parallel()

		s := &Server{}
		pass, err := s.soulSearchEntryPassesFilters(context.Background(), soulSearchIndexEntry{ClaimLevel: "self-declared"}, soulPublicSearchParams{Capability: "social", ClaimLevel: "challenge-passed"}, soulSearchPrimaryCapability)
		if err != nil || pass {
			t.Fatalf("expected claim level mismatch to fail closed, got pass=%v err=%v", pass, err)
		}
	})
}

func TestHandleSoulPublicSearch_BadBoundaryAndChannelParams(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	t.Run("invalid_boundary", func(t *testing.T) {
		t.Parallel()

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
			"boundary": {"two words"},
		}}}
		if _, err := s.handleSoulPublicSearch(ctx); err == nil {
			t.Fatalf("expected bad_request for invalid boundary keyword")
		}
	})

	t.Run("invalid_channel", func(t *testing.T) {
		t.Parallel()

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
			"channel": {"sms"},
		}}}
		if _, err := s.handleSoulPublicSearch(ctx); err == nil {
			t.Fatalf("expected bad_request for invalid channel")
		}
	})
}

func TestHandleSoulPublicGetAgent_NotFound(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("11", 32)
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	if _, err := s.handleSoulPublicGetAgent(ctx); err == nil {
		t.Fatalf("expected not_found")
	}
}

func TestHandleSoulPublicGetReputation_InternalError(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("22", 32)
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(errors.New("boom")).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	if _, err := s.handleSoulPublicGetReputation(ctx); err == nil {
		t.Fatalf("expected internal error")
	}
}

func TestHandleSoulPublicGetRegistration_ContentTypePassthrough(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	packs := &fakeSoulPublicPacks{body: []byte(`{"ok":true}`), contentType: " application/activity+json "}
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}

	agentID := "0x" + strings.Repeat("33", 32)
	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetRegistration(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
	if got := strings.TrimSpace(resp.Headers["content-type"][0]); got != "application/activity+json" {
		t.Fatalf("expected content-type passthrough, got %q", got)
	}
}

func TestHandleSoulPublicGetValidations_DefaultLimit(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("44", 32)
	tdb.qVal.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentValidationRecord](t, args, 0)
		*dest = []*models.SoulAgentValidationRecord{}
	}).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetValidations(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulPublicValidationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 0 || len(out.Validations) != 0 || out.HasMore {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestAgentHasBoundaryKeywordIndex_ErrorBranch(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("aa", 32)
	tdb.qBoundIdx.On("First", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(errors.New("boom")).Once()
	ok, err := s.agentHasBoundaryKeywordIndex(context.Background(), agentID, "example.com", "agent-a", "finance")
	if ok || err == nil {
		t.Fatalf("expected error branch, got ok=%v err=%v", ok, err)
	}
}

func TestParseSoulPublicSearchParams_ClaimLevelLegacyKey(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":           {"example.com"},
		"capability":  {"social"},
		"claim_level": {"challenge-passed"},
		"limit":       {"1"},
	}}}
	params, appErr := parseSoulPublicSearchParams(ctx)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
	if params.ClaimLevel != "challenge-passed" {
		t.Fatalf("expected claim_level to be honored, got %#v", params)
	}
}

func TestParseSoulSearchDomainAndLocal_MismatchErrors(t *testing.T) {
	t.Parallel()

	if _, _, _, appErr := parseSoulSearchDomainAndLocal("example.com/agent-a", "other.com"); appErr == nil {
		t.Fatalf("expected domain mismatch error")
	}
	if _, _, _, appErr := parseSoulSearchDomainAndLocal("example.com", "other.com"); appErr == nil {
		t.Fatalf("expected domain mismatch error")
	}
}

func TestGetSoulAgentReputation_ValidatesInput(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, err := s.getSoulAgentReputation(context.Background(), " "); err == nil {
		t.Fatalf("expected agent id required error")
	}
}

func TestHandleSoulPublicSearch_InvalidPrimaryIndex(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	_, _, _, appErr := s.querySoulSearchIndexEntries(context.Background(), "nope", soulPublicSearchParams{Domain: "example.com"}, "", 1)
	if appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal invalid search index, got %#v", appErr)
	}
}

func TestQuerySoulSearchByDomain_RequiresDomain(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, _, _, appErr := s.querySoulSearchByDomain(context.Background(), " ", "", false, "", 10); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request domain required, got %#v", appErr)
	}
}

func TestSearchSoulAgentsByENS_SupportsRFC3339StatusOverride(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("dd", 32)
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-d", Status: models.SoulAgentStatusActive, LifecycleStatus: ""}
	}).Once()

	results, _, _, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{ENSName: "agent.lessersoul.eth", Status: models.SoulAgentStatusActive})
	if appErr != nil || len(results) != 1 {
		t.Fatalf("expected status override to match identity status, got results=%#v err=%v", results, appErr)
	}
}

func TestSetSoulPublicHeaders_NoOriginHeaderDoesNotSetAllowOrigin(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{SoulPublicCORSOrigins: []string{"https://app.example"}}}
	resp := &apptheory.Response{}
	s.setSoulPublicHeaders(&apptheory.Context{}, resp, "")
	if got := resp.Headers["access-control-allow-origin"]; len(got) != 0 {
		t.Fatalf("expected allow-origin absent without origin header, got %#v", got)
	}
}

func TestSetSoulPublicHeaders_DedupesOriginVary(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{SoulPublicCORSOrigins: []string{"https://app.example"}}}
	ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"origin": {"https://app.example"}}}}
	resp := &apptheory.Response{Headers: map[string][]string{"vary": {"Origin"}}}
	s.setSoulPublicHeaders(ctx, resp, "")
	if got := resp.Headers["vary"]; len(got) != 1 || got[0] != "Origin" {
		t.Fatalf("expected origin vary not duplicated, got %#v", got)
	}
}

func TestHandleSoulPublicSearch_ChannelCursorInvalid(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"channel": {"email", "phone"},
		"cursor":  {"ch:9:"},
	}}}
	if _, err := s.handleSoulPublicSearch(ctx); err == nil {
		t.Fatalf("expected invalid cursor")
	}
}

func TestHandleSoulPublicGetAgent_NilGuards(t *testing.T) {
	t.Parallel()

	var s *Server
	if _, err := s.handleSoulPublicGetAgent(&apptheory.Context{}); err == nil {
		t.Fatalf("expected internal error for nil server")
	}
	s = &Server{}
	if _, err := s.handleSoulPublicGetAgent(nil); err == nil {
		t.Fatalf("expected internal error for nil ctx")
	}
}

func TestHandleSoulPublicGetRegistration_SoulDisabledAndNoPacks(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: false}}

	agentID := "0x" + strings.Repeat("33", 32)
	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	if _, err := s.handleSoulPublicGetRegistration(ctx); err == nil {
		t.Fatalf("expected not_found when soul disabled")
	}

	s = &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	if _, err := s.handleSoulPublicGetRegistration(ctx); err == nil {
		t.Fatalf("expected not_found when packs missing")
	}
}

func TestParseSoulSearchQuery_ErrorBranches(t *testing.T) {
	t.Parallel()

	if _, _, err := parseSoulSearchQuery("bad domain"); err == nil {
		t.Fatalf("expected invalid domain to error")
	}
	if _, _, err := parseSoulSearchQuery("example.com/@@"); err == nil {
		t.Fatalf("expected invalid local id to error")
	}
}

func TestSearchSoulAgentsByENS_ReturnsInternalOnIdentityNil(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("ee", 32)
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{}
	}).Once()

	results, hasMore, nextCursor, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{ENSName: "agent.lessersoul.eth"})
	if appErr != nil || hasMore || nextCursor != "" || len(results) != 0 {
		t.Fatalf("expected empty non-active result, got results=%#v hasMore=%v nextCursor=%q err=%v", results, hasMore, nextCursor, appErr)
	}
}

func TestHandleSoulPublicSearch_LimitClamps(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	// Return empty results from domain index.
	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":     {"example.com"},
		"limit": {"999"},
	}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
}

func TestSetSoulPublicHeaders_UsesWildcardWhenEmptyAllowlist(t *testing.T) {
	t.Parallel()

	s := &Server{}
	resp := &apptheory.Response{}
	s.setSoulPublicHeaders(nil, resp, "")
	if got := resp.Headers["access-control-allow-origin"]; len(got) != 1 || got[0] != "*" {
		t.Fatalf("expected wildcard allow-origin by default, got %#v", got)
	}
}

func TestSoulAllQueryValues_CaseInsensitiveScanPath(t *testing.T) {
	t.Parallel()

	query := map[string][]string{
		"ChaNnEl": {"email"},
	}
	got := soulAllQueryValues(query, "channel")
	if len(got) != 1 || got[0] != "email" {
		t.Fatalf("unexpected scan lookup: %#v", got)
	}
}

func TestFilterSoulSearchEntries_SkipsIdentityErrors(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("ff", 32)
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(errors.New("boom")).Once()

	entries := []soulSearchIndexEntry{{AgentID: agentID, Domain: "example.com", LocalID: "agent-f"}}
	got := s.filterActiveSoulSearchEntries(context.Background(), entries, 10)
	if len(got) != 0 {
		t.Fatalf("expected identity error to be skipped, got %#v", got)
	}
}

func TestGetSoulAgentReputation_StoreNotConfigured(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if _, err := s.getSoulAgentReputation(context.Background(), "0x"+strings.Repeat("11", 32)); err == nil {
		t.Fatalf("expected store not configured error")
	}
}

func TestHandleSoulPublicGetAgent_RepLookupErrorIsNonFatal(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("12", 32)
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(errors.New("boom")).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgent(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulPublicAgentResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Reputation != nil {
		t.Fatalf("expected reputation omitted on rep lookup error, got %#v", out.Reputation)
	}
}

func TestHandleSoulPublicGetValidations_AllPaginatedError(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("45", 32)
	tdb.qVal.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), errors.New("boom")).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	if _, err := s.handleSoulPublicGetValidations(ctx); err == nil {
		t.Fatalf("expected internal error")
	}
}

func TestQuerySoulSearchByChannels_InvalidCursorIndexRange(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, _, _, appErr := s.querySoulSearchByChannels(context.Background(), []string{"email", "phone"}, "", "", false, "ch:9:", 10); appErr == nil {
		t.Fatalf("expected invalid cursor error")
	}
}

func TestQuerySoulSearchByCapability_InvalidCapability(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, _, _, appErr := s.querySoulSearchByCapability(context.Background(), "bad cap", "", "", false, "", 10); appErr == nil {
		t.Fatalf("expected invalid capability")
	}
}

func TestQuerySoulSearchByBoundaryKeyword_RequiresKeyword(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, _, _, appErr := s.querySoulSearchByBoundaryKeyword(context.Background(), " ", "", "", false, "", 10); appErr == nil {
		t.Fatalf("expected boundary required error")
	}
}

func TestQuerySoulSearchByChannelType_FiltersNilItems(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qChanIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulChannelAgentIndex](t, args, 0)
		*dest = []*models.SoulChannelAgentIndex{
			nil,
			{AgentID: "0x" + strings.Repeat("aa", 32), Domain: "example.com", LocalID: "agent-a", ChannelType: "email"},
		}
	}).Once()

	out, _, _, appErr := s.querySoulSearchByChannelType(context.Background(), "email", "", "", false, "", 10)
	if appErr != nil || len(out) != 1 {
		t.Fatalf("unexpected: out=%#v appErr=%v", out, appErr)
	}
}

func TestSearchSoulAgentsByENS_IdentityNotFoundReturnsEmpty(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("ab", 32)
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	results, _, _, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{ENSName: "agent.lessersoul.eth"})
	if appErr != nil || len(results) != 0 {
		t.Fatalf("expected identity not found to return empty, got results=%#v err=%v", results, appErr)
	}
}

func TestSearchSoulAgentsByENS_ResolutionError(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(errors.New("boom")).Once()
	_, _, _, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{ENSName: "agent.lessersoul.eth"})
	if appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
}

func TestSearchSoulAgentsByENS_ChannelIndexError(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("bc", 32)
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-bc", Status: models.SoulAgentStatusActive}
	}).Once()
	tdb.qChanIdx.On("First", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(errors.New("boom")).Once()

	_, _, _, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{ENSName: "agent.lessersoul.eth", Channels: []string{"email"}})
	if appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error for channel index, got %#v", appErr)
	}
}

func TestSearchSoulAgentsByENS_BoundaryIndexError(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("cd", 32)
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-cd", Status: models.SoulAgentStatusActive}
	}).Once()
	tdb.qBoundIdx.On("First", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(errors.New("boom")).Once()

	_, _, _, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{ENSName: "agent.lessersoul.eth", Boundary: "finance"})
	if appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error for boundary index, got %#v", appErr)
	}
}

func TestSearchSoulAgentsByENS_StatusMismatchExplicit(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("de", 32)
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-de", Status: models.SoulAgentStatusActive}
	}).Once()

	results, _, _, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{ENSName: "agent.lessersoul.eth", Status: models.SoulAgentStatusSuspended})
	if appErr != nil || len(results) != 0 {
		t.Fatalf("expected explicit status mismatch to return empty, got results=%#v err=%v", results, appErr)
	}
}

func TestSearchSoulAgentsByENS_ResolutionEmptyAgentID(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: " "}
	}).Once()

	results, _, _, appErr := s.searchSoulAgentsByENS(context.Background(), soulPublicSearchParams{ENSName: "agent.lessersoul.eth"})
	if appErr != nil || len(results) != 0 {
		t.Fatalf("expected empty agent id resolution to return empty, got results=%#v err=%v", results, appErr)
	}
}

func TestQuerySoulSearchByChannelType_CursorAndPrefixBranches(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Ensure Cursor and SK prefix paths don't error.
	tdb.qChanIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulChannelAgentIndex](t, args, 0)
		*dest = []*models.SoulChannelAgentIndex{}
	}).Once()

	_, _, _, appErr := s.querySoulSearchByChannelType(context.Background(), "email", "example.com", "agent-a", true, "c1", 10)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
}

func TestQuerySoulSearchByChannels_InnerCursorPropagates(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("aa", 32)
	tdb.qChanIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulChannelAgentIndex](t, args, 0)
		*dest = []*models.SoulChannelAgentIndex{{AgentID: agentID, Domain: "example.com", LocalID: "agent-a", ChannelType: "email"}}
	}).Once()

	rawCursor := encodeSoulChannelSearchCursor(0, "inner")
	out, _, _, appErr := s.querySoulSearchByChannels(context.Background(), []string{"email"}, "", "", false, rawCursor, 10)
	if appErr != nil || len(out) != 1 {
		t.Fatalf("unexpected: out=%#v appErr=%v", out, appErr)
	}
}

func TestHandleSoulPublicSearch_UsesENSBranch(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("aa", 32)
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent.lessersoul.eth", AgentID: agentID}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-a", Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"ens": {"agent.lessersoul.eth"}}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
}

func TestHandleSoulPublicSearch_StatusFilterExcludesInactive(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("aa", 32)
	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{AgentID: agentID, Domain: "example.com", LocalID: "agent-a"},
		}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusSuspended}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"q": {"example.com"}, "status": {"active"}}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
	var out soulSearchResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 0 {
		t.Fatalf("expected inactive agent to be filtered, got %#v", out)
	}
}

func TestHandleSoulPublicGetAgent_IdentityError(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("11", 32)
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(errors.New("boom")).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	if _, err := s.handleSoulPublicGetAgent(ctx); err == nil {
		t.Fatalf("expected internal error")
	}
}

func TestHandleSoulPublicGetReputation_SuccessHeaders(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("22", 32)
	now := time.Unix(123, 0).UTC()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Composite: 0.2, UpdatedAt: now}
	}).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetReputation(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
	if got := strings.TrimSpace(resp.Headers["cache-control"][0]); got != "public, max-age=60" {
		t.Fatalf("unexpected cache-control: %q", got)
	}
}
