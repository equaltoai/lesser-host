package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulRelationshipsTestDB struct {
	db   *ttmocks.MockExtendedDB
	qRel *ttmocks.MockQuery
	qEnd *ttmocks.MockQuery
}

func newSoulRelationshipsTestDB() soulRelationshipsTestDB {
	db := ttmocks.NewMockExtendedDB()
	qRel := new(ttmocks.MockQuery)
	qEnd := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentRelationship")).Return(qRel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPeerEndorsement")).Return(qEnd).Maybe()

	for _, q := range []*ttmocks.MockQuery{qRel, qEnd} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("Cursor", mock.Anything).Return(q).Maybe()
	}

	return soulRelationshipsTestDB{db: db, qRel: qRel, qEnd: qEnd}
}

func TestHandleSoulPublicGetRelationships_FiltersByTypeAndTaskType(t *testing.T) {
	t.Parallel()

	tdb := newSoulRelationshipsTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentIDHex := "0x" + strings.Repeat("11", 32)

	tdb.qRel.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentRelationship](t, args, 0)
		*dest = []*models.SoulAgentRelationship{
			{
				FromAgentID: "0xfrom1",
				ToAgentID:   agentIDHex,
				Type:        models.SoulRelationshipTypeDelegation,
				Context:     `{"taskType":"summarization"}`,
				Message:     "delegation for summaries",
				CreatedAt:   time.Date(2026, 3, 1, 1, 0, 0, 0, time.UTC),
			},
			{
				FromAgentID: "0xfrom2",
				ToAgentID:   agentIDHex,
				Type:        models.SoulRelationshipTypeDelegation,
				Context:     `{"taskType":"translation"}`,
				Message:     "delegation for translations",
				CreatedAt:   time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC),
			},
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID: "r1",
		Params:    map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Query: map[string][]string{
				"type":     {"delegation"},
				"taskType": {"summarization"},
			},
		},
	}

	resp, err := s.handleSoulPublicGetRelationships(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulListRelationshipsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Relationships) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(out.Relationships))
	}
	if out.Relationships[0].FromAgentID != "0xfrom1" {
		t.Fatalf("unexpected relationship: %#v", out.Relationships[0])
	}
}

func TestHandleSoulPublicGetRelationships_IncludesV1Endorsements(t *testing.T) {
	t.Parallel()

	tdb := newSoulRelationshipsTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentIDHex := "0x" + strings.Repeat("11", 32)

	tdb.qRel.On("AllPaginated", mock.Anything).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentRelationship](t, args, 0)
		*dest = nil
	}).Once()

	tdb.qEnd.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{
			{
				AgentID:         agentIDHex,
				EndorserAgentID: "0xendorser",
				Message:         "good agent",
				Signature:       "0xsig",
				CreatedAt:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			},
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID: "r1",
		Params:    map[string]string{"agentId": agentIDHex},
		Request:   apptheory.Request{Query: map[string][]string{}},
	}

	resp, err := s.handleSoulPublicGetRelationships(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulListRelationshipsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Relationships) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(out.Relationships))
	}
	if out.Relationships[0].Type != models.SoulRelationshipTypeEndorsement {
		t.Fatalf("expected endorsement type, got %q", out.Relationships[0].Type)
	}
	if out.Relationships[0].FromAgentID != "0xendorser" {
		t.Fatalf("unexpected from agent id: %q", out.Relationships[0].FromAgentID)
	}
}
