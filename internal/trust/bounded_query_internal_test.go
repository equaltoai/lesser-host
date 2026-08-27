package trust

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// TestTrustPartitionAll_ExceedsPageCapFailsClosed verifies the trust bounded
// partition walk (issue #1061 part B) fails closed instead of silently
// truncating a partition that exceeds the page cap.
func TestTrustPartitionAll_ExceedsPageCapFailsClosed(t *testing.T) {
	t.Parallel()

	q := new(ttmocks.MockQuery)
	q.On("Limit", mock.Anything).Return(q).Maybe()
	q.On("Cursor", mock.Anything).Return(q).Maybe()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Maybe()

	_, err := trustPartitionAll[models.Domain](q, trustPartitionWalkPageSize, 2)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "exceeded 2 pages"), "expected page-cap error, got %v", err)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}
