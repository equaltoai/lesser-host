package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
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

	// Target-scoped query path.
	tdb.qAudit.On("All", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.AuditLogEntry)
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
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Count != 1 || len(parsed.Entries) != 1 || parsed.Entries[0].Actor != "bob" {
		t.Fatalf("unexpected output: %#v", parsed)
	}

	// Global query path (no target).
	tdb.qAudit.On("All", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.AuditLogEntry)
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
