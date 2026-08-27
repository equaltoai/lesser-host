package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type operatorAuditTestDB struct {
	db     *ttmocks.MockExtendedDB
	qAudit *ttmocks.MockQuery
}

func newOperatorAuditTestDB() operatorAuditTestDB {
	db := ttmocks.NewMockExtendedDB()
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	qAudit.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qAudit).Maybe()
	qAudit.On("Limit", mock.Anything).Return(qAudit).Maybe()

	return operatorAuditTestDB{db: db, qAudit: qAudit}
}

func TestParseRFC3339Time_CoversBranches(t *testing.T) {
	t.Parallel()

	t.Run("empty_ok", func(t *testing.T) {
		out, err := parseRFC3339Time(" ")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !out.IsZero() {
			t.Fatalf("expected zero time, got %v", out)
		}
	})

	t.Run("nano_ok", func(t *testing.T) {
		in := "2026-02-07T01:02:03.123456789Z"
		out, err := parseRFC3339Time(in)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if out.Format(time.RFC3339Nano) != in {
			t.Fatalf("expected %q, got %q", in, out.Format(time.RFC3339Nano))
		}
	})

	t.Run("invalid_err", func(t *testing.T) {
		if _, err := parseRFC3339Time("not-a-time"); err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestHandleListOperatorAuditLog_TargetAndGlobalQueryPaths(t *testing.T) {
	t.Parallel()

	tdb := newOperatorAuditTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Pin the literal Limits: the target-scoped keyed query keeps its Limit(200)
	// and the global filter-scan walk uses the literal page size 100 (issue
	// #1061 part D). The generic harness Limit stub is removed first because
	// testify resolves first-registered-match-wins.
	filterMockQueryCalls(tdb.qAudit, "Limit")
	tdb.qAudit.On("Limit", 200).Return(tdb.qAudit).Once()
	tdb.qAudit.On("Limit", 100).Return(tdb.qAudit).Once()

	// Target-scoped query path.
	tdb.qAudit.On("All", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AuditLogEntry](t, args, 0)
		*dest = []*models.AuditLogEntry{
			nil,
			{Actor: "alice", Action: "x", RequestID: "r1", CreatedAt: time.Unix(10, 0).UTC()},
			{Actor: "bob", Action: "x", RequestID: "r1", CreatedAt: time.Unix(20, 0).UTC()},
		}
	}).Once()

	ctx := operatorCtx()
	ctx.Request.Query = map[string][]string{
		"target": {"instance:demo"},
		"actor":  {"bob"},
		"limit":  {"1"},
	}
	resp, err := s.handleListOperatorAuditLog(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var parsed listOperatorAuditLogResponse
	if unmarshalErr := json.Unmarshal(resp.Body, &parsed); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if parsed.Count != 1 || len(parsed.Entries) != 1 || parsed.Entries[0].Actor != "bob" {
		t.Fatalf("unexpected output: %#v", parsed)
	}

	// Global query path (no target).
	tdb.qAudit.On("AllPaginated", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AuditLogEntry](t, args, 0)
		*dest = []*models.AuditLogEntry{
			{Actor: "alice", Action: "a", CreatedAt: time.Unix(5, 0).UTC()},
		}
	}).Once()

	ctx2 := operatorCtx()
	ctx2.Request.Query = map[string][]string{"limit": {"2"}}
	resp, err = s.handleListOperatorAuditLog(ctx2)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}
