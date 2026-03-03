package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/soulsearch"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func main() {
	var (
		agentID    string
		apply      bool
		pageSize   int
		maxCreates int
	)

	flag.StringVar(&agentID, "agent-id", "", "Target agent id (0x... 32-byte hex)")
	flag.BoolVar(&apply, "apply", false, "Apply updates (default: dry-run)")
	flag.IntVar(&pageSize, "page-size", 200, "Query page size (max 200)")
	flag.IntVar(&maxCreates, "max-creates", 0, "Max index creates (0 = unlimited)")
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
	fmt.Printf("soul-backfill-m11-boundary-index mode=%s table=%s agent=%s\n", mode, models.MainTableName(), agentID)

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

	identity, err := getSoulAgentIdentity(ctx, st, agentID)
	if err != nil {
		die("get identity: %v", err)
	}
	domain := strings.ToLower(strings.TrimSpace(identity.Domain))
	localID := strings.TrimSpace(identity.LocalID)
	if domain == "" || localID == "" {
		die("identity missing domain/local_id")
	}

	created, existing, scanned, errs := backfillBoundaryIndex(ctx, st, identity, pageSize, maxCreates, apply)
	fmt.Printf("boundaries scanned=%d index_created=%d index_existing=%d errors=%d\n", scanned, created, existing, errs)
}

func getSoulAgentIdentity(ctx context.Context, st *store.Store, agentID string) (*models.SoulAgentIdentity, error) {
	if st == nil || st.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return nil, fmt.Errorf("agent id is required")
	}

	var item models.SoulAgentIdentity
	err := st.DB.WithContext(ctx).
		Model(&models.SoulAgentIdentity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", "IDENTITY").
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func backfillBoundaryIndex(ctx context.Context, st *store.Store, identity *models.SoulAgentIdentity, pageSize int, maxCreates int, apply bool) (created int, existing int, scanned int, errs int) {
	if st == nil || st.DB == nil {
		die("store not initialized")
	}
	if identity == nil {
		die("identity is nil")
	}

	agentID := strings.ToLower(strings.TrimSpace(identity.AgentID))
	domain := strings.ToLower(strings.TrimSpace(identity.Domain))
	localID := strings.TrimSpace(identity.LocalID)

	cursor := ""
	seenKeywords := map[string]struct{}{}

	for {
		var items []*models.SoulAgentBoundary
		qb := st.DB.WithContext(ctx).
			Model(&models.SoulAgentBoundary{}).
			Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
			Where("SK", "BEGINS_WITH", "BOUNDARY#").
			OrderBy("SK", "ASC").
			Limit(pageSize)
		if cursor != "" {
			qb = qb.Cursor(cursor)
		}

		paged, err := qb.AllPaginated(&items)
		if err != nil {
			die("query boundaries: %v", err)
		}

		for _, b := range items {
			if b == nil {
				continue
			}
			scanned++

			keywords := soulsearch.ExtractBoundaryKeywords(b.Category, b.Statement, b.Rationale)
			for _, kw := range keywords {
				if strings.TrimSpace(kw) == "" {
					continue
				}
				if _, ok := seenKeywords[kw]; ok {
					continue
				}
				seenKeywords[kw] = struct{}{}

				if maxCreates > 0 && created >= maxCreates {
					return created, existing, scanned, errs
				}

				item := &models.SoulBoundaryKeywordAgentIndex{
					Keyword: kw,
					Domain:  domain,
					LocalID: localID,
					AgentID: agentID,
				}
				_ = item.UpdateKeys()

				if apply {
					if err := st.DB.WithContext(ctx).Model(item).IfNotExists().Create(); err != nil {
						if theoryErrors.IsConditionFailed(err) {
							existing++
							continue
						}
						errs++
						fmt.Printf("warn index create failed agent=%s keyword=%s err=%v\n", agentID, kw, err)
						continue
					}
					created++
				} else {
					fmt.Printf("dry-run index would create agent=%s keyword=%s\n", agentID, kw)
					created++
				}
			}
		}

		nextCursor := ""
		hasMore := false
		if paged != nil {
			nextCursor = strings.TrimSpace(paged.NextCursor)
			hasMore = paged.HasMore
		}
		if !hasMore || nextCursor == "" {
			return created, existing, scanned, errs
		}
		cursor = nextCursor
	}
}

func die(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, msg+"\n", args...)
	os.Exit(1)
}
