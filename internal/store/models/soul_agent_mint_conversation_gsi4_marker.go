// TableTheory types/constants that share the report shape; keep them side by side.
//
//nolint:dupl // C1/C2 backfill marker models are intentionally parallel: distinct
package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentMintConversationGSI4BackfillMarker* are the key constants for the
// operator backfill completeness marker for the SoulAgentMintConversation gsi4
// agent-scoped time-ordered index (issue #1067, part C2 of #1061).
//
// The stack update that adds gsi4 deploys before the operator runs the
// backfill tool. During that window a gsi4 query would return an empty or
// partial conversation set as if it were complete, so the request-path consumer
// (operator mint-conversation list) fails closed until this marker exists.
// The backfill tool writes it ONLY after a complete apply pass with zero
// errors, making it the operator's proof of completion.
const (
	SoulAgentMintConversationGSI4BackfillMarkerPK = "META#SOULAGENTMINTCONVERSATION#GSI4"
	SoulAgentMintConversationGSI4BackfillMarkerSK = "BACKFILL"
)

// SoulAgentMintConversationGSI4BackfillMarker records that the operator
// completed the gsi4 backfill for SoulAgentMintConversation items in this
// table/stage.
//
// Keys:
//
//	PK: META#SOULAGENTMINTCONVERSATION#GSI4
//	SK: BACKFILL
type SoulAgentMintConversationGSI4BackfillMarker struct {
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
func (SoulAgentMintConversationGSI4BackfillMarker) TableName() string { return MainTableName() }

// UpdateKeys sets the database keys for the backfill marker.
func (m *SoulAgentMintConversationGSI4BackfillMarker) UpdateKeys() error {
	m.PK = SoulAgentMintConversationGSI4BackfillMarkerPK
	m.SK = SoulAgentMintConversationGSI4BackfillMarkerSK
	return nil
}

// GetPK returns the partition key for the backfill marker.
func (m *SoulAgentMintConversationGSI4BackfillMarker) GetPK() string { return m.PK }

// GetSK returns the sort key for the backfill marker.
func (m *SoulAgentMintConversationGSI4BackfillMarker) GetSK() string { return m.SK }

// String renders a one-line report of the marker without any table data.
func (m *SoulAgentMintConversationGSI4BackfillMarker) String() string {
	if m == nil {
		return "nil"
	}
	return fmt.Sprintf(
		"scanned=%d updated=%d repaired=%d already_correct=%d errors=%d completed_at=%s",
		m.Scanned, m.Updated, m.Repaired, m.AlreadyCorrect, m.Errors,
		strings.TrimSpace(m.CompletedAt.Format(time.RFC3339)),
	)
}
