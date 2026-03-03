package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type fakeSoulPublicPacks struct {
	body        []byte
	contentType string
	etag        string
	err         error
}

func (f *fakeSoulPublicPacks) PutObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error {
	return nil
}

func (f *fakeSoulPublicPacks) GetObject(ctx context.Context, key string, maxBytes int64) ([]byte, string, string, error) {
	if f.err != nil {
		return nil, "", "", f.err
	}
	return f.body, f.contentType, f.etag, nil
}

type soulPublicTestDB struct {
	db        *ttmocks.MockExtendedDB
	qID       *ttmocks.MockQuery
	qRep      *ttmocks.MockQuery
	qVal      *ttmocks.MockQuery
	qDomIdx   *ttmocks.MockQuery
	qCapIdx   *ttmocks.MockQuery
	qBoundary *ttmocks.MockQuery
}

func newSoulPublicTestDB() soulPublicTestDB {
	db := ttmocks.NewMockExtendedDB()
	qID := new(ttmocks.MockQuery)
	qRep := new(ttmocks.MockQuery)
	qVal := new(ttmocks.MockQuery)
	qDomIdx := new(ttmocks.MockQuery)
	qCapIdx := new(ttmocks.MockQuery)
	qBoundary := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qRep).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentValidationRecord")).Return(qVal).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulDomainAgentIndex")).Return(qDomIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(qCapIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(qBoundary).Maybe()

	for _, q := range []*ttmocks.MockQuery{qID, qRep, qVal, qDomIdx, qCapIdx, qBoundary} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("Cursor", mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
	}

	return soulPublicTestDB{
		db:        db,
		qID:       qID,
		qRep:      qRep,
		qVal:      qVal,
		qDomIdx:   qDomIdx,
		qCapIdx:   qCapIdx,
		qBoundary: qBoundary,
	}
}

func TestEnvInt64PositiveFromString(t *testing.T) {
	t.Parallel()

	if got := envInt64PositiveFromString("", 50); got != 50 {
		t.Fatalf("unexpected default: %d", got)
	}
	if got := envInt64PositiveFromString("nope", 50); got != 50 {
		t.Fatalf("unexpected invalid: %d", got)
	}
	if got := envInt64PositiveFromString("-1", 50); got != 50 {
		t.Fatalf("unexpected negative: %d", got)
	}
	if got := envInt64PositiveFromString("0", 50); got != 50 {
		t.Fatalf("unexpected zero: %d", got)
	}
	if got := envInt64PositiveFromString(" 15 ", 50); got != 15 {
		t.Fatalf("unexpected positive: %d", got)
	}
}

func TestParseSoulSearchQuery(t *testing.T) {
	t.Parallel()

	if d, local, err := parseSoulSearchQuery(""); err != nil || d != "" || local != "" {
		t.Fatalf("unexpected empty: d=%q local=%q err=%v", d, local, err)
	}
	if d, local, err := parseSoulSearchQuery(testDomainExampleCom); err != nil || d != testDomainExampleCom || local != "" {
		t.Fatalf("unexpected domain: d=%q local=%q err=%v", d, local, err)
	}
	if d, local, err := parseSoulSearchQuery(testDomainExampleCom + "/agent-alice"); err != nil || d != testDomainExampleCom || local == "" {
		t.Fatalf("unexpected domain/local: d=%q local=%q err=%v", d, local, err)
	}
	if _, _, err := parseSoulSearchQuery("agent-only"); err == nil {
		t.Fatalf("expected local-only query to fail closed")
	}
}

func TestSetSoulPublicHeaders(t *testing.T) {
	t.Parallel()

	setSoulPublicHeaders(nil, "")
	resp := &apptheory.Response{}
	setSoulPublicHeaders(resp, "")
	if resp.Headers == nil || len(resp.Headers["cache-control"]) != 1 || resp.Headers["cache-control"][0] != "no-store" {
		t.Fatalf("unexpected cache header: %#v", resp.Headers)
	}
	if len(resp.Headers["access-control-allow-origin"]) != 1 || resp.Headers["access-control-allow-origin"][0] != "*" {
		t.Fatalf("unexpected allow-origin header: %#v", resp.Headers)
	}
}

func TestHandleSoulPublicGetAgent_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{SoulEnabled: true},
	}

	agentID := "0x" + strings.Repeat("11", 32)

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Composite: 0.1, UpdatedAt: time.Now().UTC()}
	}).Once()

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgent(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	if resp.Headers == nil || len(resp.Headers["cache-control"]) != 1 || resp.Headers["cache-control"][0] != "public, max-age=60" {
		t.Fatalf("unexpected headers: %#v", resp.Headers)
	}

	var out soulPublicAgentResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Version != "1" || out.Agent.AgentID != agentID || out.Reputation == nil || out.Reputation.AgentID != agentID {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandleSoulPublicGetReputation_NotFoundAndSuccess(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("22", 32)

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
		if _, err := s.handleSoulPublicGetReputation(ctx); err == nil {
			t.Fatalf("expected not_found error")
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
			*dest = models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Composite: 0.2, UpdatedAt: time.Now().UTC()}
		}).Once()

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
		resp, err := s.handleSoulPublicGetReputation(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}
	})
}

func TestHandleSoulPublicGetRegistration_SuccessAndNoSuchKey(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("33", 32)

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		packs := &fakeSoulPublicPacks{body: []byte(`{"ok":true}`), contentType: "", etag: "  etag "}
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
		resp, err := s.handleSoulPublicGetRegistration(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}
		if got := strings.TrimSpace(resp.Headers["content-type"][0]); got != "application/json" {
			t.Fatalf("expected default content-type, got %q", got)
		}
		if got := strings.TrimSpace(resp.Headers["etag"][0]); got != "etag" {
			t.Fatalf("unexpected etag: %q", got)
		}
		if got := strings.TrimSpace(resp.Headers["cache-control"][0]); got != "public, max-age=300" {
			t.Fatalf("unexpected cache-control: %q", got)
		}
	})

	t.Run("no_such_key", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		packs := &fakeSoulPublicPacks{err: &s3types.NoSuchKey{}}
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
		if _, err := s.handleSoulPublicGetRegistration(ctx); err == nil {
			t.Fatalf("expected not_found")
		}
	})
}

func TestHandleSoulPublicGetValidations_PaginatesAndFiltersNilItems(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	agentID := "0x" + strings.Repeat("44", 32)

	tdb.qVal.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{NextCursor: " c2 ", HasMore: true}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentValidationRecord](t, args, 0)
		*dest = []*models.SoulAgentValidationRecord{
			nil,
			{AgentID: agentID, ChallengeID: "c1", ChallengeType: "identity_verify", Result: "pass", EvaluatedAt: time.Now().UTC()},
		}
	}).Once()

	ctx := &apptheory.Context{
		Params:  map[string]string{"agentId": agentID},
		Request: apptheory.Request{Query: map[string][]string{"cursor": {"c1"}, "limit": {"500"}}},
	}
	resp, err := s.handleSoulPublicGetValidations(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulPublicValidationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Validations) != 1 || !out.HasMore || out.NextCursor != "c2" {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandleSoulPublicSearch_CapabilityAndDomainBranches(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)

	t.Run("capability", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qCapIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulCapabilityAgentIndex](t, args, 0)
			*dest = []*models.SoulCapabilityAgentIndex{
				{AgentID: agentA, Domain: "example.com", LocalID: "a"},
				{AgentID: agentB, Domain: "example.com", LocalID: "b"},
			}
		}).Once()

		firstCalls := 0
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			status := models.SoulAgentStatusActive
			if firstCalls > 0 {
				status = models.SoulAgentStatusSuspended
			}
			*dest = models.SoulAgentIdentity{AgentID: []string{agentA, agentB}[firstCalls], Status: status}
			firstCalls++
		}).Times(2)

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
			"capability": {"social"},
			"q":          {"example.com"},
			"cursor":     {"c1"},
			"limit":      {"10"},
		}}}

		resp, err := s.handleSoulPublicSearch(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}

		var out soulSearchResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != agentA || out.NextCursor != "" || out.HasMore {
			t.Fatalf("unexpected response: %#v", out)
		}
	})

	t.Run("domain", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
			*dest = []*models.SoulDomainAgentIndex{
				{AgentID: agentA, Domain: "example.com", LocalID: "a"},
			}
		}).Once()

		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: agentA, Status: models.SoulAgentStatusActive}
		}).Once()

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"q": {"example.com/agent-a"}}}}
		resp, err := s.handleSoulPublicSearch(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}
	})

	t.Run("missing_query", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{}}}
		if _, err := s.handleSoulPublicSearch(ctx); err == nil {
			t.Fatalf("expected bad_request")
		}
	})

	t.Run("invalid_capability", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"capability": {"x x"}}}}
		if _, err := s.handleSoulPublicSearch(ctx); err == nil {
			t.Fatalf("expected bad_request")
		}
	})

	t.Run("registration_error_passthrough", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		packs := &fakeSoulPublicPacks{err: errors.New("boom")}
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}

		ctx := &apptheory.Context{Params: map[string]string{"agentId": agentA}}
		if _, err := s.handleSoulPublicGetRegistration(ctx); err == nil {
			t.Fatalf("expected internal error")
		}
	})
}

func TestHandleSoulPublicSearch_Filters(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)

	t.Run("claimLevel_requires_capability", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
			"q":          {"example.com"},
			"claimLevel": {"challenge-passed"},
		}}}
		if _, err := s.handleSoulPublicSearch(ctx); err == nil {
			t.Fatalf("expected bad_request")
		}
	})

	t.Run("claimLevel_filters_capability_results", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qCapIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulCapabilityAgentIndex](t, args, 0)
			*dest = []*models.SoulCapabilityAgentIndex{
				{AgentID: agentA, Domain: "example.com", LocalID: "a", ClaimLevel: "self-declared"},
				{AgentID: agentB, Domain: "example.com", LocalID: "b", ClaimLevel: "challenge-passed"},
			}
		}).Once()

		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{Status: models.SoulAgentStatusActive}
		}).Maybe()

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
			"capability": {"social"},
			"q":          {"example.com"},
			"claimLevel": {"challenge-passed"},
		}}}

		resp, err := s.handleSoulPublicSearch(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}

		var out soulSearchResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != agentB {
			t.Fatalf("unexpected response: %#v", out)
		}
	})

	t.Run("boundary_filters_domain_results", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
			*dest = []*models.SoulDomainAgentIndex{
				{AgentID: agentA, Domain: "example.com", LocalID: "a"},
				{AgentID: agentB, Domain: "example.com", LocalID: "b"},
			}
		}).Once()

		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{Status: models.SoulAgentStatusActive}
		}).Times(2)

		boundaryCalls := 0
		tdb.qBoundary.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
			if boundaryCalls == 0 {
				*dest = []*models.SoulAgentBoundary{{AgentID: agentA, BoundaryID: "b1", Category: "refusal", Statement: "no finance tasks"}}
			} else {
				*dest = []*models.SoulAgentBoundary{{AgentID: agentB, BoundaryID: "b2", Category: "refusal", Statement: "no politics tasks"}}
			}
			boundaryCalls++
		}).Times(2)

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
			"q":        {"example.com"},
			"boundary": {"finance"},
		}}}
		resp, err := s.handleSoulPublicSearch(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}

		var out soulSearchResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != agentA {
			t.Fatalf("unexpected response: %#v", out)
		}
	})

	t.Run("claimLevel_boundary_status_combination", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulPublicTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		tdb.qCapIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulCapabilityAgentIndex](t, args, 0)
			*dest = []*models.SoulCapabilityAgentIndex{
				{AgentID: agentA, Domain: "example.com", LocalID: "a", ClaimLevel: "challenge-passed"},
				{AgentID: agentB, Domain: "example.com", LocalID: "b", ClaimLevel: "challenge-passed"},
			}
		}).Once()

		firstCalls := 0
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			status := models.SoulAgentStatusActive
			if firstCalls > 0 {
				status = models.SoulAgentStatusSuspended
			}
			*dest = models.SoulAgentIdentity{Status: status}
			firstCalls++
		}).Times(2)

		tdb.qBoundary.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
			*dest = []*models.SoulAgentBoundary{{AgentID: agentA, BoundaryID: "b1", Category: "refusal", Statement: "no finance tasks"}}
		}).Once()

		ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
			"capability": {"social"},
			"q":          {"example.com"},
			"claimLevel": {"challenge-passed"},
			"boundary":   {"finance"},
			"status":     {"active"},
		}}}
		resp, err := s.handleSoulPublicSearch(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
		}

		var out soulSearchResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != agentA {
			t.Fatalf("unexpected response: %#v", out)
		}
	})
}
