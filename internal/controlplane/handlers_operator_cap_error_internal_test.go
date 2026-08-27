package controlplane

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store"
)

// expectActiveInstancesWalkExhaustion drives listActiveInstances to the cap:
// five HasMore pages (the walk's 5-page bound) leave the sixth page refused,
// so listActiveInstances fails closed with app.internal "failed to list
// instances". The three operator fleet handlers (releases/drift/remediate)
// must propagate that listing error to the caller — never an empty or partial
// success body (issue #1061 part D cap-error propagation pins).
func expectActiveInstancesWalkExhaustion(t *testing.T, q *ttmocks.MockQuery) {
	t.Helper()
	// The harness pre-registers Limit(100) with Maybe(); the walk's five pages
	// are absorbed by it. Cursor + AllPaginated have no default stub.
	q.On("Cursor", mock.Anything).Return(q).Times(4)
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.Instance")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "cap-exhaustion"}, nil).Times(5)
}

func requireActiveInstancesListingError(t *testing.T, resp *apptheory.Response, err error) {
	t.Helper()
	require.Nil(t, resp, "cap exhaustion must not produce a success body")
	require.Error(t, err, "cap exhaustion must surface as an error, never a swallowed listing")
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok, "expected *apptheory.AppTheoryError, got %T: %v", err, err)
	require.Equal(t, "app.internal", appErr.Code)
	require.Contains(t, appErr.Message, "failed to list instances")
}

// TestHandleOperatorReleases_CapExhaustionPropagates pins the releases-list
// leg (handlers_operator_releases.go:52-55): when listActiveInstances fails
// closed on the bounded walk, handleOperatorReleases must return the error.
// Mutating the leg to `instances, _ := s.listActiveInstances(ctx)` makes the
// handler answer 200 with an empty channel body — this test dies.
func TestHandleOperatorReleases_CapExhaustionPropagates(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db)}

	expectActiveInstancesWalkExhaustion(t, tdb.qInstance)

	resp, err := s.handleOperatorReleases(operatorAuthenticatedCtx())
	requireActiveInstancesListingError(t, resp, err)
	tdb.qInstance.AssertExpectations(t)
	tdb.qInstance.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestHandleOperatorInstancesDrift_CapExhaustionPropagates pins the drift leg
// (handlers_operator_drift.go:23-26): a swallowed listing error would return
// drift telemetry for an empty fleet (200 with zero totals) — this test dies.
func TestHandleOperatorInstancesDrift_CapExhaustionPropagates(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db)}

	expectActiveInstancesWalkExhaustion(t, tdb.qInstance)

	resp, err := s.handleOperatorInstancesDrift(operatorAuthenticatedCtx())
	requireActiveInstancesListingError(t, resp, err)
	tdb.qInstance.AssertExpectations(t)
	tdb.qInstance.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestHandleOperatorRemediateMCPDrift_CapExhaustionPropagates pins the
// remediate leg (handlers_operator_remediate_mcp.go:47-50): a swallowed
// listing error would report "created 0 / skipped 0" as if the fleet were
// clean — this test dies.
func TestHandleOperatorRemediateMCPDrift_CapExhaustionPropagates(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db)}

	expectActiveInstancesWalkExhaustion(t, tdb.qInstance)

	resp, err := s.handleOperatorRemediateMCPDrift(operatorAuthenticatedCtx())
	requireActiveInstancesListingError(t, resp, err)
	tdb.qInstance.AssertExpectations(t)
	tdb.qInstance.AssertNotCalled(t, "Scan", mock.Anything)
}
