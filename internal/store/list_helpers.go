package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
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
		Limit(limit).
		All(&items)
	if err != nil {
		return nil, err
	}

	sortByCreatedAtDesc(items, createdAt)
	return items, nil
}
