package main

import (
	"context"
	"testing"

	"github.com/theory-cloud/tabletheory/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type backfillM1TestDB struct {
	db    *ttmocks.MockExtendedDB
	qCont *ttmocks.MockQuery
	qRel  *ttmocks.MockQuery
}

func newBackfillM1TestDB() backfillM1TestDB {
	db := ttmocks.NewMockExtendedDB()
	qCont := new(ttmocks.MockQuery)
	qRel := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentContinuity")).Return(qCont).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentRelationship")).Return(qRel).Maybe()

	for _, q := range []*ttmocks.MockQuery{qCont, qRel} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("Cursor", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Update", mock.Anything, mock.Anything).Return(nil).Maybe()
	}

	return backfillM1TestDB{db: db, qCont: qCont, qRel: qRel}
}

func TestParseLegacyStringArray(t *testing.T) {
	t.Parallel()

	if refs, ok := parseLegacyStringArray(""); !ok || refs != nil {
		t.Fatalf("expected empty input to succeed with nil refs, got refs=%#v ok=%v", refs, ok)
	}
	if refs, ok := parseLegacyStringArray(`[" a ","", "b"]`); !ok || len(refs) != 2 || refs[0] != "a" || refs[1] != "b" {
		t.Fatalf("unexpected parsed refs: %#v ok=%v", refs, ok)
	}
	if _, ok := parseLegacyStringArray(`{"bad":true}`); ok {
		t.Fatalf("expected invalid json array parse to fail")
	}
}

func TestExtractTaskTypeFromContext(t *testing.T) {
	t.Parallel()

	if got := extractTaskTypeFromContext(nil); got != "" {
		t.Fatalf("expected empty task type, got %q", got)
	}
	if got := extractTaskTypeFromContext(map[string]any{"task_type": " Translation "}); got != "translation" {
		t.Fatalf("unexpected snake_case task type: %q", got)
	}
	if got := extractTaskTypeFromContext(map[string]any{"taskType": " Summarization "}); got != "summarization" {
		t.Fatalf("unexpected camelCase task type: %q", got)
	}
}

func TestBackfillContinuityReferences_DryRunAndApply(t *testing.T) {
	ctx := context.Background()

	t.Run("dry run counts migrated and invalid records", func(t *testing.T) {
		tdb := newBackfillM1TestDB()
		tdb.qCont.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentContinuity")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentContinuity](t, args, 0)
			*dest = []*models.SoulAgentContinuity{
				nil,
				{ReferencesV2: []string{"keep"}},
				{ReferencesJSON: `{"bad":true}`},
				{ReferencesJSON: `[" https://one ","","https://two"]`},
			}
		}).Once()

		updated, scanned, errs := backfillContinuityReferences(ctx, store.New(tdb.db), "0xagent", 25, 0, false)
		if updated != 1 || scanned != 3 || errs != 1 {
			t.Fatalf("unexpected dry-run result: updated=%d scanned=%d errs=%d", updated, scanned, errs)
		}
	})

	t.Run("apply writes normalized references", func(t *testing.T) {
		tdb := newBackfillM1TestDB()
		var updatedItem *models.SoulAgentContinuity
		tdb.qCont.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentContinuity")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentContinuity](t, args, 0)
			item := &models.SoulAgentContinuity{ReferencesJSON: `[" ref-a ","ref-b"]`}
			updatedItem = item
			*dest = []*models.SoulAgentContinuity{item}
		}).Once()
		tdb.qCont.On("Update", "ReferencesV2").Return(nil).Once()

		updated, scanned, errs := backfillContinuityReferences(ctx, store.New(tdb.db), "0xagent", 25, 0, true)
		if updated != 1 || scanned != 1 || errs != 0 {
			t.Fatalf("unexpected apply result: updated=%d scanned=%d errs=%d", updated, scanned, errs)
		}
		if updatedItem == nil || len(updatedItem.ReferencesV2) != 2 || updatedItem.ReferencesV2[0] != "ref-a" || updatedItem.ReferencesV2[1] != "ref-b" {
			t.Fatalf("expected normalized references, got %#v", updatedItem)
		}
	})
}

func TestBackfillRelationshipContext_DryRunAndApply(t *testing.T) {
	ctx := context.Background()

	t.Run("dry run counts context migrations and invalid payloads", func(t *testing.T) {
		tdb := newBackfillM1TestDB()
		tdb.qRel.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentRelationship")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentRelationship](t, args, 0)
			*dest = []*models.SoulAgentRelationship{
				{ContextJSON: `{"taskType":"summarization"}`},
				{ContextJSON: `{"bad":`},
				{ContextV2: map[string]any{"task_type": "translation"}},
			}
		}).Once()

		updated, scanned, errs := backfillRelationshipContext(ctx, store.New(tdb.db), "0xagent", 25, 0, false)
		if updated != 2 || scanned != 3 || errs != 1 {
			t.Fatalf("unexpected dry-run result: updated=%d scanned=%d errs=%d", updated, scanned, errs)
		}
	})

	t.Run("apply persists both context and task type", func(t *testing.T) {
		tdb := newBackfillM1TestDB()
		var item *models.SoulAgentRelationship
		tdb.qRel.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentRelationship")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentRelationship](t, args, 0)
			item = &models.SoulAgentRelationship{ContextJSON: `{"taskType":"classification"}`}
			*dest = []*models.SoulAgentRelationship{item}
		}).Once()
		tdb.qRel.On("Update", "ContextV2", "TaskType").Return(nil).Once()

		updated, scanned, errs := backfillRelationshipContext(ctx, store.New(tdb.db), "0xagent", 25, 0, true)
		if updated != 1 || scanned != 1 || errs != 0 {
			t.Fatalf("unexpected apply result: updated=%d scanned=%d errs=%d", updated, scanned, errs)
		}
		if item == nil || item.TaskType != "classification" {
			t.Fatalf("expected task type to be populated, got %#v", item)
		}
		if got, _ := item.ContextV2["taskType"].(string); got != "classification" {
			t.Fatalf("expected context to be populated, got %#v", item.ContextV2)
		}
	})
}

func TestBackfillContinuityReferences_HonorsMaxUpdatesAndCursor(t *testing.T) {
	ctx := context.Background()
	tdb := newBackfillM1TestDB()
	call := 0
	tdb.qCont.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentContinuity")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		call++
		dest := testutil.RequireMockArg[*[]*models.SoulAgentContinuity](t, args, 0)
		if call == 1 {
			*dest = []*models.SoulAgentContinuity{{ReferencesJSON: `["ref-a"]`}}
			tdb.qCont.ExpectedCalls[len(tdb.qCont.ExpectedCalls)-1].ReturnArguments = mock.Arguments{&core.PaginatedResult{HasMore: true, NextCursor: "cursor-1"}, nil}
			return
		}
		*dest = []*models.SoulAgentContinuity{{ReferencesJSON: `["ref-b"]`}}
	}).Twice()

	updated, scanned, errs := backfillContinuityReferences(ctx, store.New(tdb.db), "0xagent", 25, 1, false)
	if updated != 1 || scanned != 2 || errs != 0 {
		t.Fatalf("unexpected paginated result: updated=%d scanned=%d errs=%d", updated, scanned, errs)
	}
}
