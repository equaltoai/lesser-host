package models

import (
	"fmt"
	"strings"
	"time"
)

// TipRegistryOperationStatus* constants define lifecycle states for an on-chain registry operation.
const (
	TipRegistryOperationStatusPending  = "pending"
	TipRegistryOperationStatusProposed = "proposed"
	TipRegistryOperationStatusExecuted = "executed"
	TipRegistryOperationStatusFailed   = "failed"
)

// TipRegistryOperationKind* constants enumerate supported operations.
const (
	TipRegistryOperationKindRegisterHost  = "register_host"
	TipRegistryOperationKindUpdateHost    = "update_host"
	TipRegistryOperationKindSetHostActive = "set_host_active"
	TipRegistryOperationKindSetToken      = "set_token_allowed"
)

// TipRegistryOperation represents a Safe-first on-chain operation request and its reconciliation metadata.
type TipRegistryOperation struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID string `theorydb:"attr:id" json:"id"`

	Kind string `theorydb:"attr:kind" json:"kind"`

	ChainID         int64  `theorydb:"attr:chainID" json:"chain_id"`
	ContractAddress string `theorydb:"attr:contractAddress" json:"contract_address"`
	TxMode          string `theorydb:"attr:txMode" json:"tx_mode,omitempty"` // safe|direct
	SafeAddress     string `theorydb:"attr:safeAddress" json:"safe_address,omitempty"`

	DomainRaw        string `theorydb:"attr:domainRaw" json:"domain_raw,omitempty"`
	DomainNormalized string `theorydb:"attr:domainNormalized" json:"domain_normalized,omitempty"`
	HostIDHex        string `theorydb:"attr:hostIdHex" json:"host_id_hex,omitempty"`

	WalletAddr string `theorydb:"attr:walletAddress" json:"wallet_address,omitempty"`
	HostFeeBps int64  `theorydb:"attr:hostFeeBps" json:"host_fee_bps,omitempty"`
	Active     *bool  `theorydb:"attr:active" json:"active,omitempty"`

	TokenAddress string `theorydb:"attr:tokenAddress" json:"token_address,omitempty"`
	TokenAllowed *bool  `theorydb:"attr:tokenAllowed" json:"token_allowed,omitempty"`

	TxTo    string `theorydb:"attr:txTo" json:"tx_to,omitempty"`
	TxData  string `theorydb:"attr:txData" json:"tx_data,omitempty"`
	TxValue string `theorydb:"attr:txValue" json:"tx_value,omitempty"` // decimal string; normally "0"

	SafeTxHash string `theorydb:"attr:safeTxHash" json:"safe_tx_hash,omitempty"`

	ExecTxHash      string `theorydb:"attr:execTxHash" json:"exec_tx_hash,omitempty"`
	ExecBlockNumber int64  `theorydb:"attr:execBlockNumber" json:"exec_block_number,omitempty"`
	ExecSuccess     *bool  `theorydb:"attr:execSuccess" json:"exec_success,omitempty"`

	ReceiptJSON  string `theorydb:"attr:receiptJson" json:"receipt_json,omitempty"`
	SnapshotJSON string `theorydb:"attr:snapshotJson" json:"snapshot_json,omitempty"`

	Status string `theorydb:"attr:status" json:"status"`

	CreatedAt  time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt  time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	ProposedAt time.Time `theorydb:"attr:proposedAt" json:"proposed_at,omitempty"`
	ExecutedAt time.Time `theorydb:"attr:executedAt" json:"executed_at,omitempty"`
}

// TableName returns the database table name for TipRegistryOperation.
func (TipRegistryOperation) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating TipRegistryOperation.
func (o *TipRegistryOperation) BeforeCreate() error {
	if err := o.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	if strings.TrimSpace(o.Status) == "" {
		o.Status = TipRegistryOperationStatusPending
	}
	o.updateGSI1()
	return nil
}

// BeforeUpdate updates timestamps before updating TipRegistryOperation.
func (o *TipRegistryOperation) BeforeUpdate() error {
	o.UpdatedAt = time.Now().UTC()
	o.updateGSI1()
	return o.UpdateKeys()
}

// UpdateKeys updates the database keys for TipRegistryOperation.
func (o *TipRegistryOperation) UpdateKeys() error {
	o.ID = strings.TrimSpace(o.ID)
	o.Kind = strings.ToLower(strings.TrimSpace(o.Kind))
	o.ContractAddress = strings.ToLower(strings.TrimSpace(o.ContractAddress))
	o.TxMode = strings.ToLower(strings.TrimSpace(o.TxMode))
	o.SafeAddress = strings.ToLower(strings.TrimSpace(o.SafeAddress))
	o.DomainRaw = strings.TrimSpace(o.DomainRaw)
	o.DomainNormalized = strings.ToLower(strings.TrimSpace(o.DomainNormalized))
	o.HostIDHex = strings.ToLower(strings.TrimSpace(o.HostIDHex))
	o.WalletAddr = strings.ToLower(strings.TrimSpace(o.WalletAddr))
	o.TokenAddress = strings.ToLower(strings.TrimSpace(o.TokenAddress))
	o.TxTo = strings.ToLower(strings.TrimSpace(o.TxTo))
	o.TxData = strings.TrimSpace(o.TxData)
	o.TxValue = strings.TrimSpace(o.TxValue)
	o.SafeTxHash = strings.ToLower(strings.TrimSpace(o.SafeTxHash))
	o.ExecTxHash = strings.ToLower(strings.TrimSpace(o.ExecTxHash))
	o.ReceiptJSON = strings.TrimSpace(o.ReceiptJSON)
	o.SnapshotJSON = strings.TrimSpace(o.SnapshotJSON)
	o.Status = strings.ToLower(strings.TrimSpace(o.Status))

	o.PK = fmt.Sprintf("TIPREG_OP#%s", o.ID)
	o.SK = SKMetadata

	return nil
}

// GetPK returns the partition key for TipRegistryOperation.
func (o *TipRegistryOperation) GetPK() string { return o.PK }

// GetSK returns the sort key for TipRegistryOperation.
func (o *TipRegistryOperation) GetSK() string { return o.SK }

func (o *TipRegistryOperation) updateGSI1() {
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
	o.GSI1PK = fmt.Sprintf("TIPREG_OP_STATUS#%s", status)
	o.GSI1SK = fmt.Sprintf("%s#%s", createdAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(o.ID))
}
