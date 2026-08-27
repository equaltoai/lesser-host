package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
)

func clampListLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

// Bounded partition-walk defaults (issue #1061 part B). Every DynamoDB read in
// a walk is a key-bounded query with a capped page (Limit) resumed via the
// opaque cursor (AllPaginated), and the loop stops after
// storePartitionWalkMaxPages pages so a caller that genuinely needs the full
// partition is bounded without silently truncating the result.
const (
	storePartitionWalkPageSize = 100
	storePartitionWalkMaxPages = 20
)

// allPartitionItemsBounded walks an entire key-bounded partition with
// page-capped reads and returns every item, resuming via the cursor between
// pages. It fails closed with an explicit error if the walk exceeds maxPages.
func allPartitionItemsBounded[T any](qb core.Query, pageSize int, maxPages int) ([]*T, error) {
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

func sortByCreatedAtDesc[T any](items []*T, createdAt func(*T) time.Time) {
	sort.Slice(items, func(i, j int) bool {
		if items[i] == nil {
			return false
		}
		if items[j] == nil {
			return true
		}
		return createdAt(items[i]).After(createdAt(items[j]))
	})
}

func listByInstanceGSI1[T any](
	store *Store,
	ctx context.Context,
	slug string,
	limit int,
	model any,
	gsi1PKFormat string,
	createdAt func(*T) time.Time,
) ([]*T, error) {
	if store == nil || store.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return nil, fmt.Errorf("instance slug is required")
	}
	limit = clampListLimit(limit)

	var items []*T
	err := store.DB.WithContext(ctx).
		Model(model).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf(gsi1PKFormat, slug)).
		OrderBy("gsi1SK", "DESC").
		Limit(limit).
		All(&items)
	if err != nil {
		return nil, err
	}

	sortByCreatedAtDesc(items, createdAt)
	return items, nil
}
