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

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	boundaryTestCacheControl         = "public, max-age=60"
	boundaryTestCategoryRefusal      = "refusal"
	boundaryTestStatementNoDo        = "I will not do that."
	boundaryTestPrincipalDeclaration = "I accept responsibility for this agent's behavior."
	boundaryTestID001                = "boundary-001"
	boundaryTestID1                  = "boundary-1"
)

func requireBoundaryMap(t testing.TB, raw any) map[string]any {
	t.Helper()
	value, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %#v", raw)
	}
	return value
}

func requireBoundarySlice(t testing.TB, raw any) []any {
	t.Helper()
	value, ok := raw.([]any)
	if !ok {
		t.Fatalf("expected []any, got %#v", raw)
	}
	return value
}

func requireBoundaryString(t testing.TB, raw any) string {
	t.Helper()
	value, ok := raw.(string)
	if !ok {
		t.Fatalf("expected string, got %#v", raw)
	}
	return value
}

func TestHandleSoulPublicGetBoundaries_PaginatesAndSetsHeaders(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qBoundary.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(&core.PaginatedResult{
		NextCursor: " c2 ",
		HasMore:    true,
	}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{
			nil,
			{AgentID: soulLifecycleTestAgentIDHex, BoundaryID: "b-1", Category: models.SoulBoundaryCategoryRefusal, Statement: "x", AddedAt: time.Unix(100, 0).UTC()},
		}
	}).Once()

	ctx := &apptheory.Context{
		Params:  map[string]string{"agentId": soulLifecycleTestAgentIDHex},
		Request: apptheory.Request{Query: map[string][]string{"limit": {"2"}}},
	}
	resp, err := s.handleSoulPublicGetBoundaries(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if got := strings.TrimSpace(resp.Headers["cache-control"][0]); got != boundaryTestCacheControl {
		t.Fatalf("unexpected cache-control: %q", got)
	}

	var out soulListBoundariesResponse
	unmarshalErr := json.Unmarshal(resp.Body, &out)
	if unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if out.Count != 1 || len(out.Boundaries) != 1 || out.NextCursor != "c2" || !out.HasMore {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Boundaries[0].BoundaryID != "b-1" {
		t.Fatalf("unexpected boundary: %#v", out.Boundaries[0])
	}
}

func TestSortSoulBoundariesByAddedAt(t *testing.T) {
	t.Parallel()

	t1 := time.Unix(100, 0).UTC()
	t2 := time.Unix(200, 0).UTC()
	items := []*models.SoulAgentBoundary{
		{BoundaryID: "b", AddedAt: t2},
		nil,
		{BoundaryID: "c", AddedAt: t1},
		{BoundaryID: "a", AddedAt: t1},
	}

	sortSoulBoundariesByAddedAt(items)
	if items[0] != nil {
		t.Fatalf("expected nil boundary first, got %#v", items[0])
	}
	if items[1] == nil || strings.TrimSpace(items[1].BoundaryID) != "a" {
		t.Fatalf("expected boundary a second, got %#v", items[1])
	}
	if items[2] == nil || strings.TrimSpace(items[2].BoundaryID) != "c" {
		t.Fatalf("expected boundary c third, got %#v", items[2])
	}
	if items[3] == nil || strings.TrimSpace(items[3].BoundaryID) != "b" {
		t.Fatalf("expected boundary b last, got %#v", items[3])
	}
}

func TestSoulBoundaryV2MapFromModel(t *testing.T) {
	t.Parallel()

	if got := soulBoundaryV2MapFromModel(nil); len(got) != 0 {
		t.Fatalf("expected nil model to return empty map, got %#v", got)
	}

	addedAt := time.Unix(123, 0).UTC()
	m := soulBoundaryV2MapFromModel(&models.SoulAgentBoundary{
		BoundaryID:     " id ",
		Category:       "Refusal",
		Statement:      " s ",
		Rationale:      " r ",
		Supersedes:     " x ",
		Signature:      " sig ",
		AddedAt:        addedAt,
		AddedInVersion: 7,
	})
	if m["id"] != "id" || m["category"] != boundaryTestCategoryRefusal || m["statement"] != "s" || m["signature"] != "sig" {
		t.Fatalf("unexpected normalized map: %#v", m)
	}
	if m["rationale"] != "r" || m["supersedes"] != "x" {
		t.Fatalf("expected optional fields present: %#v", m)
	}
	if got := strings.TrimSpace(requireBoundaryString(t, m["addedAt"])); got != addedAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected addedAt: %q", got)
	}
	if got := strings.TrimSpace(requireBoundaryString(t, m["addedInVersion"])); got != "7" {
		t.Fatalf("unexpected addedInVersion: %q", got)
	}
}

func TestSoulRegistrationMapHasBoundaryID_Branches(t *testing.T) {
	t.Parallel()

	if soulRegistrationMapHasBoundaryID(nil, "b") {
		t.Fatalf("expected nil reg to return false")
	}
	if soulRegistrationMapHasBoundaryID(map[string]any{}, " ") {
		t.Fatalf("expected blank boundaryID to return false")
	}
	if soulRegistrationMapHasBoundaryID(map[string]any{}, "b") {
		t.Fatalf("expected missing boundaries to return false")
	}
	if soulRegistrationMapHasBoundaryID(map[string]any{"boundaries": "nope"}, "b") {
		t.Fatalf("expected wrong boundaries type to return false")
	}
	if soulRegistrationMapHasBoundaryID(map[string]any{"boundaries": []any{"nope"}}, "b") {
		t.Fatalf("expected non-map items to be ignored")
	}
	if soulRegistrationMapHasBoundaryID(map[string]any{"boundaries": []any{map[string]any{"id": "x"}}}, "b") {
		t.Fatalf("expected id mismatch to return false")
	}
	if !soulRegistrationMapHasBoundaryID(map[string]any{"boundaries": []any{map[string]any{"id": "b"}}}, "b") {
		t.Fatalf("expected id match to return true")
	}
}

func TestLoadSoulAgentRegistrationMap_Branches(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	identity := &models.SoulAgentIdentity{
		AgentID: agentID,
		Domain:  "example.com",
		LocalID: "agent-a",
		Wallet:  "0x000000000000000000000000000000000000beef",
	}

	t.Run("nil_guards", func(t *testing.T) { t.Parallel(); testLoadSoulAgentRegistrationMapNilGuards(t, agentID, identity) })
	t.Run("bucket_config_required", func(t *testing.T) {
		t.Parallel()
		testLoadSoulAgentRegistrationMapBucketConfigRequired(t, agentID, identity)
	})
	t.Run("no_such_key", func(t *testing.T) { t.Parallel(); testLoadSoulAgentRegistrationMapNoSuchKey(t, agentID, identity) })
	t.Run("fetch_error", func(t *testing.T) { t.Parallel(); testLoadSoulAgentRegistrationMapFetchError(t, agentID, identity) })
	t.Run("unmarshal_error", func(t *testing.T) { t.Parallel(); testLoadSoulAgentRegistrationMapUnmarshalError(t, agentID, identity) })
	t.Run("unsupported_version", func(t *testing.T) {
		t.Parallel()
		testLoadSoulAgentRegistrationMapUnsupportedVersion(t, agentID, identity)
	})
	t.Run("wallet_out_of_sync", func(t *testing.T) {
		t.Parallel()
		testLoadSoulAgentRegistrationMapWalletOutOfSync(t, agentID, identity)
	})
}

func testLoadSoulAgentRegistrationMapNilGuards(t *testing.T, agentID string, identity *models.SoulAgentIdentity) {
	t.Helper()
	var s *Server
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, identity); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
	s = &Server{}
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, nil); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
}

func testLoadSoulAgentRegistrationMapBucketConfigRequired(t *testing.T, agentID string, identity *models.SoulAgentIdentity) {
	t.Helper()
	s := &Server{soulPacks: &fakeSoulPackStore{}, cfg: config.Config{SoulPackBucketName: ""}}
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, identity); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for missing bucket, got %#v", appErr)
	}
	s = &Server{soulPacks: nil, cfg: config.Config{SoulPackBucketName: "bucket"}}
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, identity); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for missing packs, got %#v", appErr)
	}
}

func testLoadSoulAgentRegistrationMapNoSuchKey(t *testing.T, agentID string, identity *models.SoulAgentIdentity) {
	t.Helper()
	s := &Server{soulPacks: &fakeSoulPackStore{}, cfg: config.Config{SoulPackBucketName: "bucket"}}
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, identity); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for missing registration, got %#v", appErr)
	}
}

func testLoadSoulAgentRegistrationMapFetchError(t *testing.T, agentID string, identity *models.SoulAgentIdentity) {
	t.Helper()
	s := &Server{soulPacks: &fakeSoulPublicPacks{err: errors.New("boom")}, cfg: config.Config{SoulPackBucketName: "bucket"}}
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, identity); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal fetch error, got %#v", appErr)
	}
}

func testLoadSoulAgentRegistrationMapUnmarshalError(t *testing.T, agentID string, identity *models.SoulAgentIdentity) {
	t.Helper()
	s := &Server{soulPacks: &fakeSoulPublicPacks{body: []byte("{")}, cfg: config.Config{SoulPackBucketName: "bucket"}}
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, identity); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal parse error, got %#v", appErr)
	}
}

func testLoadSoulAgentRegistrationMapUnsupportedVersion(t *testing.T, agentID string, identity *models.SoulAgentIdentity) {
	t.Helper()
	s := &Server{soulPacks: &fakeSoulPublicPacks{body: []byte(`{"version":"9"}`)}, cfg: config.Config{SoulPackBucketName: "bucket"}}
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, identity); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict unsupported version, got %#v", appErr)
	}
}

func testLoadSoulAgentRegistrationMapWalletOutOfSync(t *testing.T, agentID string, identity *models.SoulAgentIdentity) {
	t.Helper()
	body := mustMarshalJSON(t, map[string]any{
		"version": "2",
		"agentId": agentID,
		"domain":  "example.com",
		"localId": "agent-a",
		"wallet":  "0x000000000000000000000000000000000000dEaD",
	})
	s := &Server{soulPacks: &fakeSoulPublicPacks{body: body}, cfg: config.Config{SoulPackBucketName: "bucket"}}
	if _, _, appErr := s.loadSoulAgentRegistrationMap(context.Background(), agentID, identity); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict wallet out of sync, got %#v", appErr)
	}
}
