package controlplane

import (
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulAnchorAssuranceSourceHostRecord     = "host_record"
	soulAnchorAssuranceSourceOnchainReceipt = "onchain_receipt"
	soulAnchorEvidenceKindHostRecord        = "host_registry_record"
	soulAnchorEvidenceKindMintTransaction   = "mint_transaction"
)

type soulAnchorAssuranceView struct {
	State          string                   `json:"state"`
	Source         string                   `json:"source"`
	CapabilityGate bool                     `json:"capability_gate"`
	Mutable        bool                     `json:"mutable"`
	Revocable      bool                     `json:"revocable"`
	Evidence       []soulAnchorEvidenceView `json:"evidence,omitempty"`
}

type soulAnchorEvidenceView struct {
	Kind        string     `json:"kind"`
	TxHash      string     `json:"tx_hash,omitempty"`
	OperationID string     `json:"operation_id,omitempty"`
	TokenID     string     `json:"token_id,omitempty"`
	ChainID     int64      `json:"chain_id,omitempty"`
	RecordedAt  *time.Time `json:"recorded_at,omitempty"`
}

func buildSoulAnchorAssuranceFromIdentity(identity *models.SoulAgentIdentity, chainID int64) soulAnchorAssuranceView {
	policy := effectiveSoulAgentPolicy(identity)
	if identity == nil {
		return buildSoulAnchorAssurance(policy.AnchorState, "", "", "", time.Time{}, chainID, time.Time{})
	}
	return buildSoulAnchorAssurance(
		policy.AnchorState,
		identity.MintTxHash,
		"",
		identity.TokenID,
		identity.MintedAt,
		chainID,
		identity.UpdatedAt,
	)
}

func buildSoulAnchorAssuranceFromLifecycleEvent(event *models.SoulAgentPromotionLifecycleEvent, chainID int64) *soulAnchorAssuranceView {
	if event == nil {
		return nil
	}
	state := strings.TrimSpace(event.AnchorState)
	txHash := strings.TrimSpace(event.AnchorEvidenceTxHash)
	if state == "" && txHash == "" && event.AnchorEvidenceAt.IsZero() {
		return nil
	}
	view := buildSoulAnchorAssurance(
		state,
		txHash,
		event.OperationID,
		"",
		event.AnchorEvidenceAt,
		chainID,
		event.OccurredAt,
	)
	return &view
}

func buildSoulAnchorAssurance(state string, txHash string, operationID string, tokenID string, evidenceAt time.Time, chainID int64, hostRecordedAt time.Time) soulAnchorAssuranceView {
	state = normalizeSoulAnchorAssuranceState(state)
	txHash = strings.ToLower(strings.TrimSpace(txHash))
	operationID = strings.TrimSpace(operationID)
	tokenID = strings.TrimSpace(tokenID)

	view := soulAnchorAssuranceView{
		State:          state,
		Source:         soulAnchorAssuranceSourceHostRecord,
		CapabilityGate: false,
		Mutable:        true,
		Revocable:      true,
	}
	if state == models.SoulAnchorStateImmutableOnchain {
		view.Mutable = false
		view.Revocable = false
		if txHash != "" {
			view.Source = soulAnchorAssuranceSourceOnchainReceipt
		}
	}

	if txHash != "" {
		view.Evidence = append(view.Evidence, soulAnchorEvidenceView{
			Kind:        soulAnchorEvidenceKindMintTransaction,
			TxHash:      txHash,
			OperationID: operationID,
			TokenID:     tokenID,
			ChainID:     positiveInt64OrZero(chainID),
			RecordedAt:  timePtrIfNonZero(evidenceAt),
		})
		return view
	}

	view.Evidence = append(view.Evidence, soulAnchorEvidenceView{
		Kind:        soulAnchorEvidenceKindHostRecord,
		OperationID: operationID,
		TokenID:     tokenID,
		ChainID:     positiveInt64OrZero(chainID),
		RecordedAt:  timePtrIfNonZero(firstNonZeroTime(evidenceAt, hostRecordedAt)),
	})
	return view
}

func normalizeSoulAnchorAssuranceState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case models.SoulAnchorStateImmutableOnchain:
		return models.SoulAnchorStateImmutableOnchain
	default:
		return models.SoulAnchorStateHostedOffchain
	}
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

func timePtrIfNonZero(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	out := value.UTC()
	return &out
}

func positiveInt64OrZero(value int64) int64 {
	if value > 0 {
		return value
	}
	return 0
}
