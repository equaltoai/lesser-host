package main

import (
	"context"
	"testing"

	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type backfillM11TestDB struct {
	db        *ttmocks.MockExtendedDB
	qIdentity *ttmocks.MockQuery
	qBoundary *ttmocks.MockQuery
	qIndex    *ttmocks.MockQuery
}

func newBackfillM11TestDB() backfillM11TestDB {
	db := ttmocks.NewMockExtendedDB()
	qIdentity := new(ttmocks.MockQuery)
	qBoundary := new(ttmocks.MockQuery)
	qIndex := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(qBoundary).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulBoundaryKeywordAgentIndex")).Return(qIndex).Maybe()

	for _, q := range []*ttmocks.MockQuery{qIdentity, qBoundary, qIndex} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("Cursor", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
	}

	return backfillM11TestDB{
		db:        db,
		qIdentity: qIdentity,
		qBoundary: qBoundary,
		qIndex:    qIndex,
	}
}

func TestGetSoulAgentIdentity_ValidatesAndLoads(t *testing.T) {
	ctx := context.Background()

	tdb := newBackfillM11TestDB()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		dest.AgentID = "0xagent"
		dest.Domain = "example.com"
		dest.LocalID = "agent"
	}).Once()

	st := store.New(tdb.db)
	if _, err := getSoulAgentIdentity(ctx, st, " "); err == nil {
		t.Fatalf("expected validation error for empty agent id")
	}
	item, err := getSoulAgentIdentity(ctx, st, " 0xAGENT ")
	if err != nil || item == nil || item.AgentID != "0xagent" || item.Domain != "example.com" {
		t.Fatalf("unexpected identity result: item=%#v err=%v", item, err)
	}
}

func TestBackfillBoundaryIndex_DryRunAndApply(t *testing.T) {
	ctx := context.Background()
	identity := &models.SoulAgentIdentity{AgentID: "0xagent", Domain: "example.com", LocalID: "agent"}

	t.Run("dry run creates at most one keyword when capped", func(t *testing.T) {
		tdb := newBackfillM11TestDB()
		tdb.qBoundary.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
			*dest = []*models.SoulAgentBoundary{
				{Statement: "alpha"},
				{Statement: "alpha"},
			}
		}).Once()

		created, existing, scanned, errs := backfillBoundaryIndex(ctx, store.New(tdb.db), identity, 25, 1, false)
		if created != 1 || existing != 0 || scanned != 2 || errs != 0 {
			t.Fatalf("unexpected dry-run result: created=%d existing=%d scanned=%d errs=%d", created, existing, scanned, errs)
		}
	})

	t.Run("apply treats condition failure as existing index", func(t *testing.T) {
		tdb := newBackfillM11TestDB()
		tdb.qBoundary.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
			*dest = []*models.SoulAgentBoundary{{Statement: "alpha"}}
		}).Once()
		tdb.qIndex.On("Create").Return(theoryErrors.ErrConditionFailed).Once()

		created, existing, scanned, errs := backfillBoundaryIndex(ctx, store.New(tdb.db), identity, 25, 0, true)
		if created != 0 || existing != 1 || scanned != 1 || errs != 0 {
			t.Fatalf("unexpected apply result: created=%d existing=%d scanned=%d errs=%d", created, existing, scanned, errs)
		}
	})

	t.Run("apply creates new index entry", func(t *testing.T) {
		tdb := newBackfillM11TestDB()
		tdb.qBoundary.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
			*dest = []*models.SoulAgentBoundary{{Statement: "alpha"}}
		}).Once()
		tdb.qIndex.On("Create").Return(nil).Once()

		created, existing, scanned, errs := backfillBoundaryIndex(ctx, store.New(tdb.db), identity, 25, 0, true)
		if created != 1 || existing != 0 || scanned != 1 || errs != 0 {
			t.Fatalf("unexpected create result: created=%d existing=%d scanned=%d errs=%d", created, existing, scanned, errs)
		}
	})
}
