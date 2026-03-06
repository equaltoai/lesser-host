package controlplane

import (
	"fmt"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
)

func listSoulPublicItems[T any](
	s *Server,
	ctx *apptheory.Context,
	agentIDHex string,
	model any,
	skPrefix string,
	listFailureMessage string,
) (itemsOut []T, hasMore bool, nextCursor string, appErr *apptheory.AppError) {
	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var items []*T
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(model).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", skPrefix).
		OrderBy("SK", "DESC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: listFailureMessage}
	}

	itemsOut = make([]T, 0, len(items))
	for _, item := range items {
		if item != nil {
			itemsOut = append(itemsOut, *item)
		}
	}

	if paged != nil {
		hasMore = paged.HasMore
		nextCursor = strings.TrimSpace(paged.NextCursor)
	}

	return itemsOut, hasMore, nextCursor, nil
}

func listSoulPublicAgentItems[T any](
	s *Server,
	ctx *apptheory.Context,
	model any,
	skPrefix string,
	listFailureMessage string,
) (itemsOut []T, hasMore bool, nextCursor string, appErr *apptheory.AppError) {
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, false, "", appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, false, "", &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, parseErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if parseErr != nil {
		return nil, false, "", parseErr
	}

	return listSoulPublicItems[T](s, ctx, agentIDHex, model, skPrefix, listFailureMessage)
}
