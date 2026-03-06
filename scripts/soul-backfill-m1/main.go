package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func main() {
	var (
		agentID        string
		apply          bool
		backfillCont   bool
		backfillRel    bool
		pageSize       int
		maxUpdatesCont int
		maxUpdatesRel  int
	)

	flag.StringVar(&agentID, "agent-id", "", "Target agent id (0x... 32-byte hex)")
	flag.BoolVar(&apply, "apply", false, "Apply updates (default: dry-run)")
	flag.BoolVar(&backfillCont, "continuity", true, "Backfill continuity references (references -> referencesV2)")
	flag.BoolVar(&backfillRel, "relationships", true, "Backfill relationship context/taskType (context -> contextV2/taskType)")
	flag.IntVar(&pageSize, "page-size", 200, "Query page size (max 200)")
	flag.IntVar(&maxUpdatesCont, "max-updates-continuity", 0, "Max continuity updates (0 = unlimited)")
	flag.IntVar(&maxUpdatesRel, "max-updates-relationships", 0, "Max relationship updates (0 = unlimited)")
	flag.Parse()

	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		die("missing required --agent-id")
	}
	if strings.TrimSpace(os.Getenv("STATE_TABLE_NAME")) == "" {
		die("STATE_TABLE_NAME is required")
	}

	mode := "dry-run"
	if apply {
		mode = "apply"
	}
	fmt.Printf("soul-backfill-m1 mode=%s table=%s agent=%s\n", mode, models.MainTableName(), agentID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	db, err := store.LambdaInit()
	if err != nil {
		die("init store: %v", err)
	}
	st := store.New(db)

	pageSize = normalizePageSize(pageSize)

	if backfillCont {
		updated, scanned, errs := backfillContinuityReferences(ctx, st, agentID, pageSize, maxUpdatesCont, apply)
		fmt.Printf("continuity scanned=%d updated=%d errors=%d\n", scanned, updated, errs)
	}

	if backfillRel {
		updated, scanned, errs := backfillRelationshipContext(ctx, st, agentID, pageSize, maxUpdatesRel, apply)
		fmt.Printf("relationships scanned=%d updated=%d errors=%d\n", scanned, updated, errs)
	}
}

func backfillContinuityReferences(ctx context.Context, st *store.Store, agentID string, pageSize int, maxUpdates int, apply bool) (updated int, scanned int, errs int) {
	cursor := ""
	for {
		items, nextCursor, hasMore, err := queryContinuityPage(ctx, st, agentID, pageSize, cursor)
		if err != nil {
			die("query continuity: %v", err)
		}
		stop := false
		for _, item := range items {
			updated, scanned, errs, stop = processContinuityItem(ctx, st, item, maxUpdates, apply, updated, scanned, errs)
			if stop {
				return updated, scanned, errs
			}
		}
		if !hasMore || nextCursor == "" {
			return updated, scanned, errs
		}
		cursor = nextCursor
	}
}

func backfillRelationshipContext(ctx context.Context, st *store.Store, agentID string, pageSize int, maxUpdates int, apply bool) (updated int, scanned int, errs int) {
	cursor := ""
	for {
		items, nextCursor, hasMore, err := queryRelationshipPage(ctx, st, agentID, pageSize, cursor)
		if err != nil {
			die("query relationships: %v", err)
		}
		stop := false
		for _, item := range items {
			updated, scanned, errs, stop = processRelationshipItem(ctx, st, item, maxUpdates, apply, updated, scanned, errs)
			if stop {
				return updated, scanned, errs
			}
		}
		if !hasMore || nextCursor == "" {
			return updated, scanned, errs
		}
		cursor = nextCursor
	}
}

func normalizePageSize(pageSize int) int {
	if pageSize <= 0 || pageSize > 200 {
		return 200
	}
	return pageSize
}

func queryContinuityPage(ctx context.Context, st *store.Store, agentID string, pageSize int, cursor string) ([]*models.SoulAgentContinuity, string, bool, error) {
	return queryBackfillPage[models.SoulAgentContinuity](ctx, st, &models.SoulAgentContinuity{}, agentID, "CONTINUITY#", "DESC", pageSize, cursor)
}

func processContinuityItem(ctx context.Context, st *store.Store, item *models.SoulAgentContinuity, maxUpdates int, apply bool, updated int, scanned int, errs int) (int, int, int, bool) {
	if item == nil {
		return updated, scanned, errs, false
	}
	scanned++
	refs, shouldUpdate, ok := continuityRefsForBackfill(item)
	if !ok {
		errs++
		fmt.Printf("warn continuity invalid references pk=%s sk=%s\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK))
		return updated, scanned, errs, false
	}
	if !shouldUpdate {
		return updated, scanned, errs, false
	}
	if maxUpdates > 0 && updated >= maxUpdates {
		return updated, scanned, errs, true
	}
	if apply {
		item.ReferencesV2 = refs
		if err := st.DB.WithContext(ctx).Model(item).IfExists().Update("ReferencesV2"); err != nil {
			errs++
			fmt.Printf("warn continuity update failed pk=%s sk=%s err=%v\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK), err)
			return updated, scanned, errs, false
		}
	} else {
		fmt.Printf("dry-run continuity would update pk=%s sk=%s\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK))
	}
	return updated + 1, scanned, errs, false
}

func continuityRefsForBackfill(item *models.SoulAgentContinuity) ([]string, bool, bool) {
	if item == nil || len(item.ReferencesV2) > 0 {
		return nil, false, true
	}
	legacy := strings.TrimSpace(item.ReferencesJSON)
	if legacy == "" {
		return nil, false, true
	}
	refs, ok := parseLegacyStringArray(legacy)
	if !ok {
		return nil, false, false
	}
	return refs, len(refs) > 0, true
}

func queryRelationshipPage(ctx context.Context, st *store.Store, agentID string, pageSize int, cursor string) ([]*models.SoulAgentRelationship, string, bool, error) {
	return queryBackfillPage[models.SoulAgentRelationship](ctx, st, &models.SoulAgentRelationship{}, agentID, "RELATIONSHIP#", "ASC", pageSize, cursor)
}

func processRelationshipItem(ctx context.Context, st *store.Store, item *models.SoulAgentRelationship, maxUpdates int, apply bool, updated int, scanned int, errs int) (int, int, int, bool) {
	if item == nil {
		return updated, scanned, errs, false
	}
	scanned++
	updateFields, ctxMap, ok := relationshipBackfillFields(item)
	if !ok {
		errs++
		fmt.Printf("warn relationship invalid context pk=%s sk=%s\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK))
		return updated, scanned, errs, false
	}
	if len(updateFields) == 0 {
		return updated, scanned, errs, false
	}
	if maxUpdates > 0 && updated >= maxUpdates {
		return updated, scanned, errs, true
	}
	item.ContextV2 = ctxMap
	if apply {
		if err := st.DB.WithContext(ctx).Model(item).IfExists().Update(updateFields...); err != nil {
			errs++
			fmt.Printf("warn relationship update failed pk=%s sk=%s err=%v\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK), err)
			return updated, scanned, errs, false
		}
	} else {
		fmt.Printf("dry-run relationship would update pk=%s sk=%s fields=%s\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK), strings.Join(updateFields, ","))
	}
	return updated + 1, scanned, errs, false
}

func relationshipBackfillFields(item *models.SoulAgentRelationship) ([]string, map[string]any, bool) {
	needsContext := item.ContextV2 == nil && strings.TrimSpace(item.ContextJSON) != ""
	needsTaskType := strings.TrimSpace(item.TaskType) == ""
	if !needsContext && !needsTaskType {
		return nil, item.ContextV2, true
	}
	ctxMap, ok := resolveRelationshipContextMap(item)
	if !ok {
		return nil, nil, false
	}
	taskType := extractTaskTypeFromContext(ctxMap)
	updateFields := make([]string, 0, 2)
	if needsContext && ctxMap != nil {
		updateFields = append(updateFields, "ContextV2")
	}
	if needsTaskType && taskType != "" {
		item.TaskType = taskType
		updateFields = append(updateFields, "TaskType")
	}
	return updateFields, ctxMap, true
}

func resolveRelationshipContextMap(item *models.SoulAgentRelationship) (map[string]any, bool) {
	ctxMap := item.ContextV2
	if ctxMap != nil {
		return ctxMap, true
	}
	legacy := strings.TrimSpace(item.ContextJSON)
	if legacy == "" {
		return nil, true
	}
	if err := json.Unmarshal([]byte(legacy), &ctxMap); err != nil {
		return nil, false
	}
	return ctxMap, true
}

func queryBackfillPage[T any](ctx context.Context, st *store.Store, model any, agentID string, skPrefix string, order string, pageSize int, cursor string) ([]*T, string, bool, error) {
	var items []*T
	qb := st.DB.WithContext(ctx).
		Model(model).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", skPrefix).
		OrderBy("SK", order).
		Limit(pageSize)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}
	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, "", false, err
	}
	nextCursor := ""
	hasMore := false
	if paged != nil {
		nextCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = paged.HasMore
	}
	return items, nextCursor, hasMore, nil
}

func parseLegacyStringArray(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	var refs []string
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil, false
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		out = append(out, r)
	}
	return out, true
}

func extractTaskTypeFromContext(m map[string]any) string {
	if m == nil {
		return ""
	}
	raw, _ := m["taskType"].(string)
	if raw == "" {
		raw, _ = m["task_type"].(string)
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func die(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
