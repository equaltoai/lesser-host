package controlplane

import (
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/costtelemetry"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// ---------------------------------------------------------------------------
// test constants
// ---------------------------------------------------------------------------

const (
	testCostSlug1 = "demo"
	testCostDate1 = "2026-05-25"
	testCostDate2 = "2026-05-26"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newCostTestDB creates a mock DB pre-wired for Instance and CostTelemetry
// model queries.
func newCostTestDB() (*ttmocks.MockExtendedDB, *ttmocks.MockQuery, *ttmocks.MockQuery) {
	db, qs := newTestDBWithModelQueries("*models.Instance", "*models.CostTelemetry")
	return db, qs[0], qs[1]
}

// stubCostInstanceFirst configures the Instance mock query's First to return
// the given instance.
func stubCostInstanceFirst(t *testing.T, q *ttmocks.MockQuery, inst models.Instance) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = inst
	}).Maybe()
}

// stubCostRecords configures the CostTelemetry mock query's All to return
// the given records.
func stubCostRecords(t *testing.T, q *ttmocks.MockQuery, records []*models.CostTelemetry, err error) {
	t.Helper()
	q.On("All", mock.AnythingOfType("*[]*models.CostTelemetry")).Return(err).Run(func(args mock.Arguments) {
		if err != nil {
			return
		}
		dest := testutil.RequireMockArg[*[]*models.CostTelemetry](t, args, 0)
		*dest = records
	}).Once()
}

// costRecord builds a minimal CostTelemetry record with decoded entries in
// EntriesJSON.
func costRecord(slug, date string, dayCost float64, currency string, entries []costtelemetry.ReconciledCostEntry) *models.CostTelemetry {
	entriesJSON, err := json.Marshal(entries)
	if err != nil {
		panic(err)
	}
	return &models.CostTelemetry{
		InstanceSlug: slug,
		Date:         date,
		DayCost:      dayCost,
		Currency:     currency,
		EntriesJSON:  string(entriesJSON),
	}
}

// newCostHandlerCtx builds a context with the given slug and optional query
// params.
func newCostHandlerCtx(slug string, query map[string][]string) *apptheory.Context {
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": slug},
	}
	if query != nil {
		ctx.Request.Query = query
	}
	return ctx
}

// jsonContains asserts that the marshaled JSON of v does not contain any of
// the forbidden strings.
func jsonContains(t *testing.T, v any, forbidden ...string) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, f := range forbidden {
		if contains(s, f) {
			t.Errorf("response contained forbidden field %q", f)
		}
	}
}

func contains(s, needle string) bool {
	return len(s) >= len(needle) && searchSubstring(s, needle)
}

func searchSubstring(s, needle string) bool {
	for i := 0; i <= len(s)-len(needle); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func TestHandlePortalGetInstanceCost_Success(t *testing.T) {
	t.Parallel()

	tdb, qInst, qCost := newCostTestDB()
	s := &Server{store: store.New(tdb)}

	stubCostInstanceFirst(t, qInst, models.Instance{
		Slug:  testCostSlug1,
		Owner: "alice",
	})

	entries := []costtelemetry.ReconciledCostEntry{
		{Date: testCostDate1, Service: "Lambda", Cost: 0.25, Currency: "USD"},
		{Date: testCostDate1, Service: "DynamoDB", Cost: 0.10, Currency: "USD"},
	}

	stubCostRecords(t, qCost, []*models.CostTelemetry{
		costRecord(testCostSlug1, testCostDate1, 0.35, "USD", entries),
	}, nil)

	ctx := newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	})

	resp, err := s.handlePortalGetInstanceCost(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected response: %#v err=%v", resp, err)
	}

	var body portalCostResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if body.InstanceSlug != testCostSlug1 {
		t.Errorf("expected slug %q, got %q", testCostSlug1, body.InstanceSlug)
	}
	if body.Count != 1 {
		t.Fatalf("expected 1 day, got %d", body.Count)
	}
	if body.TotalCost != 0.35 {
		t.Errorf("expected total 0.35, got %f", body.TotalCost)
	}
	if body.Currency != "USD" {
		t.Errorf("expected currency USD, got %q", body.Currency)
	}

	day := body.Days[0]
	if day.Date != testCostDate1 {
		t.Errorf("expected date %q, got %q", testCostDate1, day.Date)
	}
	if day.DayCost != 0.35 {
		t.Errorf("expected day cost 0.35, got %f", day.DayCost)
	}
	if len(day.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(day.Entries))
	}
	if day.Entries[0].Service != "Lambda" || day.Entries[1].Service != "DynamoDB" {
		t.Errorf("unexpected entries: %+v", day.Entries)
	}

	// Redaction: response must not contain internal fields.
	jsonContains(t, body, "account_id", "PK", "SK", "ttl", "entries_json", "EntriesJSON")
}

func TestHandlePortalGetInstanceCost_WrongOwnerForbidden(t *testing.T) {
	t.Parallel()

	tdb, qInst, qCost := newCostTestDB()
	s := &Server{store: store.New(tdb)}

	// Instance is owned by "bob"; caller is "alice".
	stubCostInstanceFirst(t, qInst, models.Instance{
		Slug:  testCostSlug1,
		Owner: "bob",
	})

	ctx := newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	})

	_, err := s.handlePortalGetInstanceCost(ctx)
	if err == nil {
		t.Fatal("expected forbidden error for wrong owner")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != appErrCodeForbidden {
		t.Fatalf("expected app.forbidden, got %#v", err)
	}

	// Prove cost telemetry was never read.
	qCost.AssertNotCalled(t, "All", mock.Anything)
}

func TestHandlePortalGetInstanceCost_OperatorBypass(t *testing.T) {
	t.Parallel()

	tdb, qInst, qCost := newCostTestDB()
	s := &Server{store: store.New(tdb)}

	stubCostInstanceFirst(t, qInst, models.Instance{
		Slug:  testCostSlug1,
		Owner: "bob", // not the caller
	})

	stubCostRecords(t, qCost, []*models.CostTelemetry{
		costRecord(testCostSlug1, testCostDate1, 1.50, "USD", nil),
	}, nil)

	ctx := newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	})
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handlePortalGetInstanceCost(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("operator should bypass ownership: resp=%#v err=%v", resp, err)
	}
}

func TestHandlePortalGetInstanceCost_DefaultWindow(t *testing.T) {
	t.Parallel()

	tdb, qInst, qCost := newCostTestDB()
	s := &Server{store: store.New(tdb)}

	stubCostInstanceFirst(t, qInst, models.Instance{
		Slug:  testCostSlug1,
		Owner: "alice",
	})

	// When the handler has no query params, it defaults to past 30 days.
	// We don't verify exact dates (they depend on time.Now()), but we verify
	// the handler succeeds and the store is queried.
	stubCostRecords(t, qCost, []*models.CostTelemetry{}, nil)

	ctx := newCostHandlerCtx(testCostSlug1, nil)

	resp, err := s.handlePortalGetInstanceCost(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected response: %#v err=%v", resp, err)
	}

	var body portalCostResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.FromDate == "" || body.ToDate == "" {
		t.Errorf("expected default date window, got from=%q to=%q", body.FromDate, body.ToDate)
	}
}

func TestHandlePortalGetInstanceCost_InvalidDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
		code string
	}{
		{"from only", "2026-05-01", "", "app.bad_request"},
		{"to only", "", "2026-05-26", "app.bad_request"},
		{"bad from", "not-a-date", "2026-05-26", "app.bad_request"},
		{"bad to", "2026-05-01", "nope", "app.bad_request"},
		{"from after to", "2026-05-26", "2026-05-01", "app.bad_request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tdb, qInst, _ := newCostTestDB()
			s := &Server{store: store.New(tdb)}

			stubCostInstanceFirst(t, qInst, models.Instance{
				Slug:  testCostSlug1,
				Owner: "alice",
			})

			ctx := newCostHandlerCtx(testCostSlug1, map[string][]string{
				"from": {tt.from},
				"to":   {tt.to},
			})

			_, err := s.handlePortalGetInstanceCost(ctx)
			if err == nil {
				t.Fatal("expected error")
			}
			appErr, ok := err.(*apptheory.AppError)
			if !ok || appErr.Code != tt.code {
				t.Fatalf("expected %s, got %#v", tt.code, err)
			}
		})
	}
}

func TestHandlePortalGetInstanceCost_InstanceNotFound(t *testing.T) {
	t.Parallel()

	tdb, qInst, _ := newCostTestDB()
	s := &Server{store: store.New(tdb)}

	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	})

	_, err := s.handlePortalGetInstanceCost(ctx)
	if err == nil {
		t.Fatal("expected not_found")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != soulMintAppErrCodeNotFound {
		t.Fatalf("expected app.not_found, got %#v", err)
	}
}
