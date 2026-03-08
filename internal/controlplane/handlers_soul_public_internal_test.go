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
	qBoundIdx *ttmocks.MockQuery
	qChannel  *ttmocks.MockQuery
	qPrefs    *ttmocks.MockQuery
	qEmailIdx *ttmocks.MockQuery
	qPhoneIdx *ttmocks.MockQuery
	qENS      *ttmocks.MockQuery
	qChanIdx  *ttmocks.MockQuery
}

func newSoulPublicTestDB() soulPublicTestDB {
	db := ttmocks.NewMockExtendedDB()
	qID := new(ttmocks.MockQuery)
	qRep := new(ttmocks.MockQuery)
	qVal := new(ttmocks.MockQuery)
	qDomIdx := new(ttmocks.MockQuery)
	qCapIdx := new(ttmocks.MockQuery)
	qBoundIdx := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qPrefs := new(ttmocks.MockQuery)
	qEmailIdx := new(ttmocks.MockQuery)
	qPhoneIdx := new(ttmocks.MockQuery)
	qENS := new(ttmocks.MockQuery)
	qChanIdx := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qRep).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentValidationRecord")).Return(qVal).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulDomainAgentIndex")).Return(qDomIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(qCapIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(qBoundIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(qPrefs).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(qEmailIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(qPhoneIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(qENS).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulChannelAgentIndex")).Return(qChanIdx).Maybe()

	for _, q := range []*ttmocks.MockQuery{qID, qRep, qVal, qDomIdx, qCapIdx, qBoundIdx, qChannel, qPrefs, qEmailIdx, qPhoneIdx, qENS, qChanIdx} {
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
		qBoundIdx: qBoundIdx,
		qChannel:  qChannel,
		qPrefs:    qPrefs,
		qEmailIdx: qEmailIdx,
		qPhoneIdx: qPhoneIdx,
		qENS:      qENS,
		qChanIdx:  qChanIdx,
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

	s := &Server{cfg: config.Config{SoulEnabled: true}}
	s.setSoulPublicHeaders(nil, nil, "")
	resp := &apptheory.Response{}
	s.setSoulPublicHeaders(nil, resp, "")
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

	if resp.Headers == nil || len(resp.Headers["cache-control"]) != 1 || resp.Headers["cache-control"][0] != boundaryTestCacheControl {
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

func TestHandleSoulPublicSearch_CapabilityBranch(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qCapIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulCapabilityAgentIndex](t, args, 0)
		*dest = []*models.SoulCapabilityAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "a"},
			{AgentID: agentB, Domain: "example.com", LocalID: "b"},
		}
	}).Once()
	mockSoulPublicIdentityStatuses(t, &tdb, agentA, agentB)

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"capability": {"social"},
		"q":          {"example.com"},
		"cursor":     {"c1"},
		"limit":      {"10"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentA)
}

func TestHandleSoulPublicSearch_DomainBranch(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: agentA, Domain: "example.com", LocalID: "a"}}
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
}

func TestHandleSoulPublicSearch_DomainParamAndLocalQuery(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: agentA, Domain: "example.com", LocalID: "agent-alice"}}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentA, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"domain": {"example.com"},
		"q":      {"agent-a"},
	}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
}

func TestHandleSoulPublicSearch_MissingQuery(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{}}}
	if _, err := s.handleSoulPublicSearch(ctx); err == nil {
		t.Fatalf("expected bad_request")
	}
}

func TestHandleSoulPublicSearch_InvalidCapability(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"capability": {"x x"}}}}
	if _, err := s.handleSoulPublicSearch(ctx); err == nil {
		t.Fatalf("expected bad_request")
	}
}

func TestHandleSoulPublicSearch_InvalidPrincipal(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":         {"example.com"},
		"principal": {"not-a-wallet"},
	}}}
	if _, err := s.handleSoulPublicSearch(ctx); err == nil {
		t.Fatalf("expected bad_request")
	}
}

func TestHandleSoulPublicGetRegistration_RegistrationErrorPassthrough(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	tdb := newSoulPublicTestDB()
	packs := &fakeSoulPublicPacks{err: errors.New("boom")}
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}, soulPacks: packs}
	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentA}}
	if _, err := s.handleSoulPublicGetRegistration(ctx); err == nil {
		t.Fatalf("expected internal error")
	}
}

func TestHandleSoulPublicSearch_ClaimLevelRequiresCapability(t *testing.T) {
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
}

func TestHandleSoulPublicSearch_ClaimLevelFiltersCapabilityResults(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
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
	assertSoulPublicSearchResponse(t, s, ctx, agentB)
}

func TestHandleSoulPublicSearch_PrincipalFiltersDomainResults(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	principal := "0x00000000000000000000000000000000000000aa"
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qDomIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "agent-a"},
			{AgentID: agentB, Domain: "example.com", LocalID: "agent-b"},
		}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:          agentA,
			Domain:           "example.com",
			LocalID:          "agent-a",
			PrincipalAddress: principal,
			Status:           models.SoulAgentStatusActive,
		}
	}).Once()
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:          agentB,
			Domain:           "example.com",
			LocalID:          "agent-b",
			PrincipalAddress: "0x00000000000000000000000000000000000000bb",
			Status:           models.SoulAgentStatusActive,
		}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":         {"example.com"},
		"principal": {principal},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentA)
}

func TestHandleSoulPublicSearch_BoundaryFiltersDomainResults(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qBoundIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulBoundaryKeywordAgentIndex](t, args, 0)
		*dest = []*models.SoulBoundaryKeywordAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "a", Keyword: "finance"},
			{AgentID: agentB, Domain: "example.com", LocalID: "b", Keyword: "finance"},
		}
	}).Once()
	mockSoulPublicIdentityStatuses(t, &tdb, agentA, agentB)

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"q":        {"example.com"},
		"boundary": {"finance"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentA)
}

func TestHandleSoulPublicSearch_ClaimLevelBoundaryStatusCombination(t *testing.T) {
	t.Parallel()

	agentA := "0x" + strings.Repeat("aa", 32)
	agentB := "0x" + strings.Repeat("bb", 32)
	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	tdb.qCapIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulCapabilityAgentIndex](t, args, 0)
		*dest = []*models.SoulCapabilityAgentIndex{
			{AgentID: agentA, Domain: "example.com", LocalID: "a", ClaimLevel: "challenge-passed"},
			{AgentID: agentB, Domain: "example.com", LocalID: "b", ClaimLevel: "challenge-passed"},
		}
	}).Once()
	mockSoulPublicIdentityStatuses(t, &tdb, agentA, agentB)
	tdb.qBoundIdx.On("First", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulBoundaryKeywordAgentIndex](t, args, 0)
		*dest = models.SoulBoundaryKeywordAgentIndex{AgentID: agentA, Domain: "example.com", LocalID: "a", Keyword: "finance"}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{
		"capability": {"social"},
		"q":          {"example.com"},
		"claimLevel": {"challenge-passed"},
		"boundary":   {"finance"},
		"status":     {"active"},
	}}}
	assertSoulPublicSearchResponse(t, s, ctx, agentA)
}

func TestHandleSoulPublicGetAgentChannels_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("11", 32)
	identityUpdated := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	prefsUpdated := identityUpdated.Add(2 * time.Hour)
	emailUpdated := identityUpdated.Add(3 * time.Hour)
	verifiedAt := identityUpdated.Add(30 * time.Minute)

	mockSoulPublicGetAgentChannelsSuccess(t, &tdb, agentID, identityUpdated, prefsUpdated, emailUpdated, verifiedAt)

	ctx := &apptheory.Context{Params: map[string]string{"agentId": agentID}}
	resp, err := s.handleSoulPublicGetAgentChannels(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSoulPublicAgentChannelsResponse(t, resp, agentID, emailUpdated)
}

func mockSoulPublicIdentityStatuses(t *testing.T, tdb *soulPublicTestDB, activeAgentID string, suspendedAgentID string) {
	t.Helper()

	firstCalls := 0
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		status := models.SoulAgentStatusActive
		agentID := activeAgentID
		if firstCalls > 0 {
			status = models.SoulAgentStatusSuspended
			agentID = suspendedAgentID
		}
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: status}
		firstCalls++
	}).Times(2)
}

func assertSoulPublicSearchResponse(t *testing.T, s *Server, ctx *apptheory.Context, expectedAgentID string) {
	t.Helper()

	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
	var out soulSearchResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != expectedAgentID {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func mockSoulPublicGetAgentChannelsSuccess(
	t *testing.T,
	tdb *soulPublicTestDB,
	agentID string,
	identityUpdated time.Time,
	prefsUpdated time.Time,
	emailUpdated time.Time,
	verifiedAt time.Time,
) {
	t.Helper()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentID,
			Domain:    "example.com",
			LocalID:   "agent-bob",
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: identityUpdated,
		}
	}).Once()
	tdb.qPrefs.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentContactPreferences](t, args, 0)
		*dest = models.SoulAgentContactPreferences{
			AgentID:              agentID,
			Preferred:            "email",
			AvailabilitySchedule: "always",
			ResponseTarget:       "PT4H",
			ResponseGuarantee:    "best-effort",
			Languages:            []string{"en"},
			UpdatedAt:            prefsUpdated,
		}
	}).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:      agentID,
			ChannelType:  models.SoulChannelTypeEmail,
			Identifier:   "agent-bob@lessersoul.ai",
			Capabilities: []string{"receive", "send"},
			Protocols:    []string{"smtp"},
			Verified:     true,
			VerifiedAt:   verifiedAt,
			Status:       models.SoulChannelStatusActive,
			UpdatedAt:    emailUpdated,
		}
	}).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
}

func assertSoulPublicAgentChannelsResponse(t *testing.T, resp *apptheory.Response, agentID string, emailUpdated time.Time) {
	t.Helper()
	assertSoulPublicAgentChannelsStatusAndHeaders(t, resp)
	out := decodeSoulPublicAgentChannelsResponse(t, resp)
	assertSoulPublicAgentChannelsBody(t, out, agentID)
	assertSoulPublicAgentChannelsUpdatedAt(t, out.UpdatedAt, emailUpdated)
}

func assertSoulPublicAgentChannelsStatusAndHeaders(t *testing.T, resp *apptheory.Response) {
	t.Helper()
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if resp.Headers == nil || len(resp.Headers["cache-control"]) != 1 || resp.Headers["cache-control"][0] != boundaryTestCacheControl {
		t.Fatalf("unexpected headers: %#v", resp.Headers)
	}
}

func decodeSoulPublicAgentChannelsResponse(t *testing.T, resp *apptheory.Response) soulPublicAgentChannelsResponse {
	t.Helper()
	var out soulPublicAgentChannelsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func assertSoulPublicAgentChannelsBody(t *testing.T, out soulPublicAgentChannelsResponse, agentID string) {
	t.Helper()
	if out.AgentID != agentID {
		t.Fatalf("expected agentId %q, got %q", agentID, out.AgentID)
	}
	if out.Channels.ENS != nil || out.Channels.Phone != nil || out.Channels.Email == nil {
		t.Fatalf("unexpected channels: %#v", out.Channels)
	}
	if out.Channels.Email.Address != "agent-bob@lessersoul.ai" || !out.Channels.Email.Verified || out.Channels.Email.VerifiedAt == "" {
		t.Fatalf("unexpected email channel: %#v", out.Channels.Email)
	}
	if out.ContactPreferences == nil || out.ContactPreferences.Preferred != commChannelEmail || len(out.ContactPreferences.Languages) != 1 {
		t.Fatalf("unexpected prefs: %#v", out.ContactPreferences)
	}
}

func assertSoulPublicAgentChannelsUpdatedAt(t *testing.T, got string, emailUpdated time.Time) {
	t.Helper()
	if got != emailUpdated.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("expected updatedAt %q, got %q", emailUpdated.UTC().Format(time.RFC3339Nano), got)
	}
}

func TestHandleSoulPublicResolveEmail_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("22", 32)

	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
		*dest = models.SoulEmailAgentIndex{Email: "agent-bob@lessersoul.ai", AgentID: agentID}
	}).Once()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Params: map[string]string{"emailAddress": "agent-bob@lessersoul.ai"}}
	resp, err := s.handleSoulPublicResolveEmail(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulPublicAgentResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Agent.AgentID != agentID {
		t.Fatalf("expected agent_id %q, got %#v", agentID, out)
	}
}

func TestHandleSoulPublicSearch_ChannelFilter(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("aa", 32)

	tdb.qChanIdx.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulChannelAgentIndex](t, args, 0)
		*dest = []*models.SoulChannelAgentIndex{
			{AgentID: agentID, Domain: "example.com", LocalID: "agent-a", ChannelType: "email"},
		}
	}).Once()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"channel": {"email"}}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulSearchResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != agentID {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandleSoulPublicSearch_ENS(t *testing.T) {
	t.Parallel()

	tdb := newSoulPublicTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("bb", 32)

	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent-bob.lessersoul.eth", AgentID: agentID}
	}).Once()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", LocalID: "agent-bob", Status: models.SoulAgentStatusActive}
	}).Once()

	ctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"ens": {"agent-bob.lessersoul.eth"}}}}
	resp, err := s.handleSoulPublicSearch(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out soulSearchResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Results) != 1 || out.Results[0].AgentID != agentID {
		t.Fatalf("unexpected response: %#v", out)
	}
}
