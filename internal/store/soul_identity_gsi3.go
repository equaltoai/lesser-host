package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	// SoulIdentityGSI3Name is the gsi3 status enumeration index for
	// SoulAgentIdentity (issue #1061 part C1):
	//
	//	gsi3PK = IDENTITY#<status>
	//	gsi3SK = <agentId>
	SoulIdentityGSI3Name = "gsi3"

	// soulIdentityGSI3QueryPageSize caps each gsi3 read page. The consumers
	// loop over pages with the opaque cursor, so a single DynamoDB read is
	// always bounded even when the full result set spans many pages.
	soulIdentityGSI3QueryPageSize = 100
)

// SoulIdentityGSI3PK builds the gsi3 partition key for a lifecycle status.
func SoulIdentityGSI3PK(status string) string {
	return "IDENTITY#" + strings.ToLower(strings.TrimSpace(status))
}

// ListSoulAgentIdentitiesByStatus returns every identity item with the given
// lifecycle status, enumerated through the gsi3 status index with bounded
// paginated reads (Limit + opaque cursor per page). This is the sanctioned
// replacement for the former SK=IDENTITY full-table scan: every read is a
// key-bounded GSI query and each page is capped.
func (s *Store) ListSoulAgentIdentitiesByStatus(ctx context.Context, status string, pageSize int) ([]*models.SoulAgentIdentity, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("store not initialized")
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return nil, errors.New("status is required")
	}
	if pageSize <= 0 {
		pageSize = soulIdentityGSI3QueryPageSize
	}
	if pageSize > 200 {
		pageSize = 200
	}

	statusKey := SoulIdentityGSI3PK(status)
	var out []*models.SoulAgentIdentity
	cursor := ""
	for {
		var page []*models.SoulAgentIdentity
		qb := s.DB.WithContext(ctx).
			Model(&models.SoulAgentIdentity{}).
			Index(SoulIdentityGSI3Name).
			Where("gsi3PK", "=", statusKey).
			Limit(pageSize)
		if cursor != "" {
			qb = qb.Cursor(cursor)
		}
		paged, err := qb.AllPaginated(&page)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if paged == nil || !paged.HasMore || strings.TrimSpace(paged.NextCursor) == "" {
			break
		}
		cursor = strings.TrimSpace(paged.NextCursor)
	}
	return out, nil
}

// RequireSoulAgentIdentityGSI3BackfillComplete fails closed until the operator
// has completed the gsi3 backfill for the SoulAgentIdentity model in this
// table/stage.
//
// The stack update that creates gsi3 deploys before the backfill runs. During
// that window a gsi3 query would silently return an empty or partial identity
// set as if it were complete, so every identity enumeration consumer must call
// this gate first. The backfill tool writes the marker item only after a
// complete apply pass with zero errors (see
// scripts/soul-agent-identity-gsi3-backfill).
func (s *Store) RequireSoulAgentIdentityGSI3BackfillComplete(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return errors.New("store not initialized")
	}
	var marker models.SoulAgentIdentityGSI3BackfillMarker
	err := s.DB.WithContext(ctx).
		Model(&models.SoulAgentIdentityGSI3BackfillMarker{}).
		Where("PK", "=", models.SoulAgentIdentityGSI3BackfillMarkerPK).
		Where("SK", "=", models.SoulAgentIdentityGSI3BackfillMarkerSK).
		First(&marker)
	if err == nil {
		return nil
	}
	if IsNotFound(err) {
		return fmt.Errorf(
			"soul agent identity gsi3 backfill not complete: run scripts/soul-agent-identity-gsi3-backfill --apply against this stage (marker %s/%s)",
			models.SoulAgentIdentityGSI3BackfillMarkerPK,
			models.SoulAgentIdentityGSI3BackfillMarkerSK,
		)
	}
	return fmt.Errorf("failed to read soul agent identity gsi3 backfill marker: %w", err)
}
