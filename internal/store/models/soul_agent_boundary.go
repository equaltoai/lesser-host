package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulBoundaryCategory* constants enumerate boundary categories.
const (
	SoulBoundaryCategoryRefusal           = "refusal"
	SoulBoundaryCategoryScopeLimit        = "scope_limit"
	SoulBoundaryCategoryEthicalCommitment = "ethical_commitment"
	SoulBoundaryCategoryCircuitBreaker    = "circuit_breaker"
)

// SoulAgentBoundary stores an append-only boundary declaration for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: BOUNDARY#{boundaryId}
type SoulAgentBoundary struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID    string `theorydb:"attr:agentId" json:"agent_id"`
	BoundaryID string `theorydb:"attr:boundaryId" json:"boundary_id"`

	Category  string `theorydb:"attr:category" json:"category"`
	Statement string `theorydb:"attr:statement" json:"statement"`
	Rationale string `theorydb:"attr:rationale" json:"rationale,omitempty"`

	AddedInVersion int    `theorydb:"attr:addedInVersion" json:"added_in_version,omitempty"`
	Supersedes     string `theorydb:"attr:supersedes" json:"supersedes,omitempty"`
	Signature      string `theorydb:"attr:signature" json:"signature,omitempty"`

	AddedAt time.Time `theorydb:"attr:addedAt" json:"added_at"`
}

// TableName returns the database table name for SoulAgentBoundary.
func (SoulAgentBoundary) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentBoundary.
func (b *SoulAgentBoundary) BeforeCreate() error {
	if b.AddedAt.IsZero() {
		b.AddedAt = time.Now().UTC()
	}
	return b.UpdateKeys()
}

// UpdateKeys updates the database keys for SoulAgentBoundary.
func (b *SoulAgentBoundary) UpdateKeys() error {
	b.AgentID = strings.ToLower(strings.TrimSpace(b.AgentID))
	b.BoundaryID = strings.TrimSpace(b.BoundaryID)
	b.Category = strings.ToLower(strings.TrimSpace(b.Category))
	b.Statement = strings.TrimSpace(b.Statement)
	b.Rationale = strings.TrimSpace(b.Rationale)
	b.Supersedes = strings.TrimSpace(b.Supersedes)
	b.Signature = strings.ToLower(strings.TrimSpace(b.Signature))

	b.PK = fmt.Sprintf("SOUL#AGENT#%s", b.AgentID)
	b.SK = fmt.Sprintf("BOUNDARY#%s", b.BoundaryID)
	return nil
}

// GetPK returns the partition key for SoulAgentBoundary.
func (b *SoulAgentBoundary) GetPK() string { return b.PK }

// GetSK returns the sort key for SoulAgentBoundary.
func (b *SoulAgentBoundary) GetSK() string { return b.SK }
