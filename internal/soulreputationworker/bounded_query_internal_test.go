package soulreputationworker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// TestWorkerPartitionAll_ResumesViaCursor verifies the worker's bounded
// partition walk (issue #1061 part B) binds every page and resumes via the
// opaque cursor without ever issuing a Scan.
func TestWorkerPartitionAll_ResumesViaCursor(t *testing.T) {
	t.Parallel()

	q := new(ttmocks.MockQuery)
	appliedLimits := []int{}
	q.On("Limit", 100).Return(q).Times(2).Run(func(args mock.Arguments) {
		if n, ok := args.Get(0).(int); ok {
			appliedLimits = append(appliedLimits, n)
		}
	})
	q.On("Cursor", "after-1").Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentRelationship")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "after-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentRelationship](t, args, 0)
		*dest = []*models.SoulAgentRelationship{{FromAgentID: "0xa", ToAgentID: "0xb", Type: "delegation"}}
	}).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentRelationship")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentRelationship](t, args, 0)
		*dest = []*models.SoulAgentRelationship{{FromAgentID: "0xc", ToAgentID: "0xd", Type: "endorsement"}}
	}).Once()

	items, err := workerPartitionAll[models.SoulAgentRelationship](q, workerPartitionWalkPageSize, workerPartitionWalkMaxPages)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, []int{100, 100}, appliedLimits)
	q.AssertExpectations(t)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestWorkerPartitionAll_ExceedsPageCapFailsClosed verifies the worker walk
// fails the phase explicitly rather than scoring a truncated partition.
func TestWorkerPartitionAll_ExceedsPageCapFailsClosed(t *testing.T) {
	t.Parallel()

	// The cap check is `page >= maxPages`: with maxPages=2 the walk reads
	// exactly two pages (Limit x2, Cursor x1, AllPaginated x2) and then errors,
	// never a third page. Pinning the fixed call counts makes the off-by-one
	// mutation (`page > maxPages`) fail: it would issue a third read.
	q := new(ttmocks.MockQuery)
	q.On("Limit", mock.Anything).Return(q).Times(2)
	q.On("Cursor", mock.Anything).Return(q).Times(1)
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentRelationship")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Times(2)

	_, err := workerPartitionAll[models.SoulAgentRelationship](q, workerPartitionWalkPageSize, 2)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "exceeded 2 pages"), "expected page-cap error, got %v", err)
	q.AssertExpectations(t)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}
