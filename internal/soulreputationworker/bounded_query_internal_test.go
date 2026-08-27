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
	q.On("Limit", mock.Anything).Return(q).Times(2).Run(func(args mock.Arguments) {
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
	require.Equal(t, []int{workerPartitionWalkPageSize, workerPartitionWalkPageSize}, appliedLimits)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestWorkerPartitionAll_ExceedsPageCapFailsClosed verifies the worker walk
// fails the phase explicitly rather than scoring a truncated partition.
func TestWorkerPartitionAll_ExceedsPageCapFailsClosed(t *testing.T) {
	t.Parallel()

	q := new(ttmocks.MockQuery)
	q.On("Limit", mock.Anything).Return(q).Maybe()
	q.On("Cursor", mock.Anything).Return(q).Maybe()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentRelationship")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Maybe()

	_, err := workerPartitionAll[models.SoulAgentRelationship](q, workerPartitionWalkPageSize, 2)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "exceeded 2 pages"), "expected page-cap error, got %v", err)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}
