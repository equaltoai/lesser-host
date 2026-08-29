// TableTheory types/constants that share the report shape; keep them side by side.
//
//nolint:dupl // C1/C2 backfill marker models are intentionally parallel: distinct
package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentIdentityGSI3BackfillMarker* are the key constants for the
// operator backfill completeness marker for the SoulAgentIdentity gsi3 status
// enumeration index (issue #1061 part C1).
//
// The stack update that adds gsi3 deploys before the operator runs the
// backfill tool. During that window a gsi3 query would return an empty or
// partial identity set as if it were complete, so the request-path consumers
// (soul publish, soul reputation worker) fail closed until this marker exists.
// The backfill tool writes it ONLY after a complete apply pass with zero
// errors, making it the operator's proof of completion.
const (
	SoulAgentIdentityGSI3BackfillMarkerPK = "META#SOULAGENTIDENTITY#GSI3"
	SoulAgentIdentityGSI3BackfillMarkerSK = "BACKFILL"
)

// SoulAgentIdentityGSI3BackfillMarker records that the operator completed the
// gsi3 backfill for SoulAgentIdentity items in this table/stage.
//
// Keys:
//
//	PK: META#SOULAGENTIDENTITY#GSI3
//	SK: BACKFILL
type SoulAgentIdentityGSI3BackfillMarker struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Scanned        int64     `theorydb:"attr:scanned" json:"scanned"`
	Updated        int64     `theorydb:"attr:updated" json:"updated"`
	Repaired       int64     `theorydb:"attr:repaired" json:"repaired"`
	AlreadyCorrect int64     `theorydb:"attr:alreadyCorrect" json:"already_correct"`
	Errors         int64     `theorydb:"attr:errors" json:"errors"`
	CompletedAt    time.Time `theorydb:"attr:completedAt" json:"completed_at"`
}

// TableName returns the database table name for the backfill marker.
func (SoulAgentIdentityGSI3BackfillMarker) TableName() string { return MainTableName() }

// UpdateKeys sets the database keys for the backfill marker.
func (m *SoulAgentIdentityGSI3BackfillMarker) UpdateKeys() error {
	m.PK = SoulAgentIdentityGSI3BackfillMarkerPK
	m.SK = SoulAgentIdentityGSI3BackfillMarkerSK
	return nil
}

// GetPK returns the partition key for the backfill marker.
func (m *SoulAgentIdentityGSI3BackfillMarker) GetPK() string { return m.PK }

// GetSK returns the sort key for the backfill marker.
func (m *SoulAgentIdentityGSI3BackfillMarker) GetSK() string { return m.SK }

// String renders a one-line report of the marker without any table data.
func (m *SoulAgentIdentityGSI3BackfillMarker) String() string {
	if m == nil {
		return "nil"
	}
	return fmt.Sprintf(
		"scanned=%d updated=%d repaired=%d already_correct=%d errors=%d completed_at=%s",
		m.Scanned, m.Updated, m.Repaired, m.AlreadyCorrect, m.Errors,
		strings.TrimSpace(m.CompletedAt.Format(time.RFC3339)),
	)
}
