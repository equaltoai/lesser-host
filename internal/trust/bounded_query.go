package trust

import (
	"fmt"
	"strings"

	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
)

// Bounded partition-walk defaults (issue #1061 part B). Every DynamoDB read in
// a walk is a key-bounded query with a capped page (Limit) resumed via the
// opaque cursor (AllPaginated), and the loop stops after
// trustPartitionWalkMaxPages pages so a single request can never issue an
// unbounded read even when the caller genuinely needs the full partition.
const (
	trustPartitionWalkPageSize = 100
	trustPartitionWalkMaxPages = 20
)

// trustPartitionAll walks an entire key-bounded partition with page-capped
// reads and returns every item, resuming via the cursor between pages. It
// fails closed with an explicit error if the walk exceeds maxPages.
func trustPartitionAll[T any](qb core.Query, pageSize int, maxPages int) ([]*T, error) {
	var out []*T
	cursor := ""
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("bounded partition walk exceeded %d pages of %d items each", maxPages, pageSize)
		}
		pageQB := qb.Limit(pageSize)
		if cursor != "" {
			pageQB = pageQB.Cursor(cursor)
		}
		var items []*T
		paged, err := pageQB.AllPaginated(&items)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if paged == nil || !paged.HasMore || strings.TrimSpace(paged.NextCursor) == "" {
			return out, nil
		}
		cursor = strings.TrimSpace(paged.NextCursor)
	}
}
