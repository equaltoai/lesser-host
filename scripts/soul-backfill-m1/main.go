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

	if pageSize <= 0 {
		pageSize = 200
	}
	if pageSize > 200 {
		pageSize = 200
	}

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
		var items []*models.SoulAgentContinuity
		qb := st.DB.WithContext(ctx).
			Model(&models.SoulAgentContinuity{}).
			Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
			Where("SK", "BEGINS_WITH", "CONTINUITY#").
			OrderBy("SK", "DESC").
			Limit(pageSize)
		if cursor != "" {
			qb = qb.Cursor(cursor)
		}

		paged, err := qb.AllPaginated(&items)
		if err != nil {
			die("query continuity: %v", err)
		}

		for _, item := range items {
			if item == nil {
				continue
			}
			scanned++

			if len(item.ReferencesV2) > 0 {
				continue
			}
			legacy := strings.TrimSpace(item.ReferencesJSON)
			if legacy == "" {
				continue
			}

			refs, ok := parseLegacyStringArray(legacy)
			if !ok || len(refs) == 0 {
				errs++
				fmt.Printf("warn continuity invalid references pk=%s sk=%s\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK))
				continue
			}

			if maxUpdates > 0 && updated >= maxUpdates {
				return updated, scanned, errs
			}

			if apply {
				item.ReferencesV2 = refs
				if err := st.DB.WithContext(ctx).Model(item).IfExists().Update("ReferencesV2"); err != nil {
					errs++
					fmt.Printf("warn continuity update failed pk=%s sk=%s err=%v\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK), err)
					continue
				}
			} else {
				fmt.Printf("dry-run continuity would update pk=%s sk=%s\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK))
			}

			updated++
		}

		nextCursor := ""
		hasMore := false
		if paged != nil {
			nextCursor = strings.TrimSpace(paged.NextCursor)
			hasMore = paged.HasMore
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
		var items []*models.SoulAgentRelationship
		qb := st.DB.WithContext(ctx).
			Model(&models.SoulAgentRelationship{}).
			Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
			Where("SK", "BEGINS_WITH", "RELATIONSHIP#").
			OrderBy("SK", "ASC").
			Limit(pageSize)
		if cursor != "" {
			qb = qb.Cursor(cursor)
		}

		paged, err := qb.AllPaginated(&items)
		if err != nil {
			die("query relationships: %v", err)
		}

		for _, item := range items {
			if item == nil {
				continue
			}
			scanned++

			needsContext := item.ContextV2 == nil && strings.TrimSpace(item.ContextJSON) != ""
			needsTaskType := strings.TrimSpace(item.TaskType) == ""
			if !needsContext && !needsTaskType {
				continue
			}

			ctxMap := item.ContextV2
			if ctxMap == nil {
				legacy := strings.TrimSpace(item.ContextJSON)
				if legacy != "" {
					if err := json.Unmarshal([]byte(legacy), &ctxMap); err != nil {
						errs++
						fmt.Printf("warn relationship invalid context pk=%s sk=%s\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK))
						continue
					}
				}
			}
			taskType := extractTaskTypeFromContext(ctxMap)

			updateFields := make([]string, 0, 2)
			if needsContext && ctxMap != nil {
				item.ContextV2 = ctxMap
				updateFields = append(updateFields, "ContextV2")
			}
			if needsTaskType && taskType != "" {
				item.TaskType = taskType
				updateFields = append(updateFields, "TaskType")
			}
			if len(updateFields) == 0 {
				continue
			}

			if maxUpdates > 0 && updated >= maxUpdates {
				return updated, scanned, errs
			}

			if apply {
				if err := st.DB.WithContext(ctx).Model(item).IfExists().Update(updateFields...); err != nil {
					errs++
					fmt.Printf("warn relationship update failed pk=%s sk=%s err=%v\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK), err)
					continue
				}
			} else {
				fmt.Printf("dry-run relationship would update pk=%s sk=%s fields=%s\n", strings.TrimSpace(item.PK), strings.TrimSpace(item.SK), strings.Join(updateFields, ","))
			}

			updated++
		}

		nextCursor := ""
		hasMore := false
		if paged != nil {
			nextCursor = strings.TrimSpace(paged.NextCursor)
			hasMore = paged.HasMore
		}
		if !hasMore || nextCursor == "" {
			return updated, scanned, errs
		}
		cursor = nextCursor
	}
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
