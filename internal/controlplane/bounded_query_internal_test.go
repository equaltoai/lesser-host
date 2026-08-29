package controlplane

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

// TestCollectPartitionAll_ResumesViaCursorAndBindsEveryPage verifies the
// shared bounded partition walk (issue #1061 part B) applies Limit on every
// page, resumes via the opaque cursor, never issues a Scan, and collects the
// full result set across pages.
func TestCollectPartitionAll_ResumesViaCursorAndBindsEveryPage(t *testing.T) {
	t.Parallel()

	q := new(ttmocks.MockQuery)
	appliedLimits := []int{}
	q.On("Limit", 100).Return(q).Times(2).Run(func(args mock.Arguments) {
		if n, ok := args.Get(0).(int); ok {
			appliedLimits = append(appliedLimits, n)
		}
	})
	q.On("Cursor", "after-1").Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulDomainAgentIndex")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "after-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: "0xaaa"}}
	}).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulDomainAgentIndex")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{AgentID: "0xbbb"}}
	}).Once()

	items, err := collectPartitionAll[models.SoulDomainAgentIndex](q, partitionWalkPageSize, partitionWalkMaxPages)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, []int{100, 100}, appliedLimits)
	q.AssertExpectations(t)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestCollectPartitionAll_ExceedsPageCapFailsClosed verifies the bounded walk
// refuses to return a silently truncated partition when the page cap is hit.
func TestCollectPartitionAll_ExceedsPageCapFailsClosed(t *testing.T) {
	t.Parallel()

	// The cap check is `page >= maxPages`: with maxPages=2 the walk reads
	// exactly two pages (Limit x2, Cursor x1, AllPaginated x2) and then errors,
	// never a third page. Pinning the fixed call counts makes the off-by-one
	// mutation (`page > maxPages`) fail: it would issue a third read.
	q := new(ttmocks.MockQuery)
	q.On("Limit", mock.Anything).Return(q).Times(2)
	q.On("Cursor", mock.Anything).Return(q).Times(1)
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulDomainAgentIndex")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Times(2)

	_, err := collectPartitionAll[models.SoulDomainAgentIndex](q, partitionWalkPageSize, 2)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "exceeded 2 pages"), "expected page-cap error, got %v", err)
	q.AssertExpectations(t)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}
