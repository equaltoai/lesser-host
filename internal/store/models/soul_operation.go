package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulOperationStatus* constants define lifecycle states for an on-chain registry operation.
const (
	SoulOperationStatusPending  = "pending"
	SoulOperationStatusProposed = "proposed"
	SoulOperationStatusExecuted = "executed"
	SoulOperationStatusFailed   = "failed"
)

// SoulOperationKind* constants enumerate supported operations.
const (
	SoulOperationKindMint                  = "mint"
	SoulOperationKindRotateWallet          = "rotate_wallet"
	SoulOperationKindPublishReputationRoot = "publish_reputation_root"
	SoulOperationKindPublishValidationRoot = "publish_validation_root"
	SoulOperationKindSuspend               = "suspend"
)

// SoulOperation represents a Safe-first on-chain operation request and its reconciliation metadata.
//
// Keys:
//
//	PK: SOUL#OP#{operationId}
//	SK: OPERATION
type SoulOperation struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	OperationID string `theorydb:"attr:operationId" json:"operation_id"`
	Kind        string `theorydb:"attr:kind" json:"kind"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id,omitempty"` // target agent (hex-encoded uint256)

	Status string `theorydb:"attr:status" json:"status"`

	SafePayloadJSON string `theorydb:"attr:safePayload" json:"safe_payload,omitempty"`

	ExecTxHash      string `theorydb:"attr:execTxHash" json:"exec_tx_hash,omitempty"`
	ExecBlockNumber int64  `theorydb:"attr:execBlockNumber" json:"exec_block_number,omitempty"`
	ExecSuccess     *bool  `theorydb:"attr:execSuccess" json:"exec_success,omitempty"`

	ReceiptJSON  string `theorydb:"attr:receiptJson" json:"receipt_json,omitempty"`
	SnapshotJSON string `theorydb:"attr:snapshotJson" json:"snapshot_json,omitempty"`

	CreatedAt  time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt  time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	ExecutedAt time.Time `theorydb:"attr:executedAt" json:"executed_at,omitempty"`
}

// TableName returns the database table name for SoulOperation.
func (SoulOperation) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulOperation.
func (o *SoulOperation) BeforeCreate() error {
	if err := o.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	if strings.TrimSpace(o.Status) == "" {
		o.Status = SoulOperationStatusPending
	}
	o.updateGSI1()
	return nil
}

// BeforeUpdate updates timestamps before updating SoulOperation.
func (o *SoulOperation) BeforeUpdate() error {
	o.UpdatedAt = time.Now().UTC()
	o.updateGSI1()
	return o.UpdateKeys()
}

// UpdateKeys updates the database keys for SoulOperation.
func (o *SoulOperation) UpdateKeys() error {
	o.OperationID = strings.TrimSpace(o.OperationID)
	o.Kind = strings.ToLower(strings.TrimSpace(o.Kind))
	o.AgentID = strings.ToLower(strings.TrimSpace(o.AgentID))
	o.Status = strings.ToLower(strings.TrimSpace(o.Status))
	o.SafePayloadJSON = strings.TrimSpace(o.SafePayloadJSON)
	o.ExecTxHash = strings.ToLower(strings.TrimSpace(o.ExecTxHash))
	o.ReceiptJSON = strings.TrimSpace(o.ReceiptJSON)
	o.SnapshotJSON = strings.TrimSpace(o.SnapshotJSON)

	o.PK = fmt.Sprintf("SOUL#OP#%s", o.OperationID)
	o.SK = "OPERATION"
	o.updateGSI1()
	return nil
}

// GetPK returns the partition key for SoulOperation.
func (o *SoulOperation) GetPK() string { return o.PK }

// GetSK returns the sort key for SoulOperation.
func (o *SoulOperation) GetSK() string { return o.SK }

func (o *SoulOperation) updateGSI1() {
	if o == nil {
		return
	}
	status := strings.ToLower(strings.TrimSpace(o.Status))
	if status == "" {
		o.GSI1PK = ""
		o.GSI1SK = ""
		return
	}
	createdAt := o.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	o.GSI1PK = fmt.Sprintf("SOUL_OP_STATUS#%s", status)
	o.GSI1SK = fmt.Sprintf("%s#%s", createdAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(o.OperationID))
}
