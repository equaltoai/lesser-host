package hostedgenesis

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidTurnLedger   = errors.New("hosted genesis turn ledger is invalid")
	ErrIdempotencyConflict = errors.New("hosted genesis idempotency key conflict")
	ErrDuplicateTurnID     = errors.New("hosted genesis duplicate turn id")
	ErrInvalidPublishGate  = errors.New("hosted genesis publish/finalize gate is invalid")
)

// TurnLedgerEntry is the durable, compact record that a hosted genesis turn was
// accepted. It carries ids, hashes, checkpoint refs, and billing refs only. Raw
// prompts, message lists, transcripts, provider keys, Instance API keys, wallet
// signatures, signing material, SSM values, AWS credentials, provider secrets,
// MicroVM endpoint tokens, and browser Host credentials do not belong here.
type TurnLedgerEntry struct {
	TurnID                 string    `json:"turn_id"`
	IdempotencyKey         string    `json:"idempotency_key,omitempty"`
	RequestHash            string    `json:"request_hash,omitempty"`
	InputCheckpointRef     string    `json:"input_checkpoint_ref,omitempty"`
	AssistantCheckpointRef string    `json:"assistant_checkpoint_ref,omitempty"`
	BillingLedgerRef       string    `json:"billing_ledger_ref,omitempty"`
	ChargedCredits         int64     `json:"charged_credits,omitempty"`
	MessageCount           int       `json:"message_count"`
	AcceptedAt             time.Time `json:"accepted_at"`
}

// Normalize trims client-safe ids and hashes before persistence or comparison.
func (e TurnLedgerEntry) Normalize() TurnLedgerEntry {
	e.TurnID = strings.TrimSpace(e.TurnID)
	e.IdempotencyKey = strings.TrimSpace(e.IdempotencyKey)
	e.RequestHash = strings.ToLower(strings.TrimSpace(e.RequestHash))
	e.InputCheckpointRef = NormalizeCheckpointRef(e.InputCheckpointRef)
	e.AssistantCheckpointRef = NormalizeCheckpointRef(e.AssistantCheckpointRef)
	e.BillingLedgerRef = strings.TrimSpace(e.BillingLedgerRef)
	if !e.AcceptedAt.IsZero() {
		e.AcceptedAt = e.AcceptedAt.UTC()
	}
	return e
}

// Validate enforces the compact turn-ledger contract.
func (e TurnLedgerEntry) Validate() error {
	e = e.Normalize()
	if e.TurnID == "" || e.MessageCount <= 0 || e.AcceptedAt.IsZero() || e.ChargedCredits < 0 {
		return ErrInvalidTurnLedger
	}
	if e.IdempotencyKey != "" && !isSHA256HexOrDigest(e.RequestHash) {
		return ErrInvalidTurnLedger
	}
	return nil
}

// TurnLedgerDecision describes whether a turn acceptance appended a new ledger
// row or replayed an existing idempotent acceptance. Replays must not append a
// user turn, advance message_count/latest_turn_id, or debit credits again.
type TurnLedgerDecision struct {
	Entries      []TurnLedgerEntry
	Turn         TurnLedgerEntry
	Replayed     bool
	ShouldDebit  bool
	LatestTurnID string
	MessageCount int
}

// ApplyTurnLedger applies idempotency semantics to the compact turn ledger.
// Same idempotency key + request hash returns the original turn. Same key with
// a different request hash fails closed. New keys append exactly one entry.
func ApplyTurnLedger(existing []TurnLedgerEntry, incoming TurnLedgerEntry) (TurnLedgerDecision, error) {
	entries, err := normalizeAndValidateLedger(existing)
	if err != nil {
		return TurnLedgerDecision{}, err
	}
	incoming = incoming.Normalize()
	if incoming.MessageCount <= 0 {
		incoming.MessageCount = len(entries) + 1
	}
	if incoming.AcceptedAt.IsZero() {
		incoming.AcceptedAt = time.Now().UTC()
	}
	if err := incoming.Validate(); err != nil {
		return TurnLedgerDecision{}, err
	}

	for _, entry := range entries {
		if incoming.IdempotencyKey != "" && entry.IdempotencyKey == incoming.IdempotencyKey {
			if entry.RequestHash != incoming.RequestHash {
				return TurnLedgerDecision{}, fmt.Errorf("%w: idempotency key already used for a different request", ErrIdempotencyConflict)
			}
			return TurnLedgerDecision{
				Entries:      append([]TurnLedgerEntry(nil), entries...),
				Turn:         entry,
				Replayed:     true,
				ShouldDebit:  false,
				LatestTurnID: entry.TurnID,
				MessageCount: entry.MessageCount,
			}, nil
		}
		if entry.TurnID == incoming.TurnID {
			return TurnLedgerDecision{}, ErrDuplicateTurnID
		}
	}

	entries = append(entries, incoming)
	return TurnLedgerDecision{
		Entries:      entries,
		Turn:         incoming,
		Replayed:     false,
		ShouldDebit:  true,
		LatestTurnID: incoming.TurnID,
		MessageCount: incoming.MessageCount,
	}, nil
}

// ValidateTurnLedger verifies a complete compact turn ledger without mutating
// it. Duplicate idempotency keys or turn ids are invalid because replayed
// requests must point at the original row rather than adding another turn.
func ValidateTurnLedger(entries []TurnLedgerEntry) error {
	_, err := normalizeAndValidateLedger(entries)
	return err
}

func normalizeAndValidateLedger(entries []TurnLedgerEntry) ([]TurnLedgerEntry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]TurnLedgerEntry, 0, len(entries))
	seenTurns := map[string]struct{}{}
	seenIdempotency := map[string]string{}
	for _, entry := range entries {
		entry = entry.Normalize()
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		if _, exists := seenTurns[entry.TurnID]; exists {
			return nil, ErrDuplicateTurnID
		}
		seenTurns[entry.TurnID] = struct{}{}
		if entry.IdempotencyKey != "" {
			if existingHash, exists := seenIdempotency[entry.IdempotencyKey]; exists {
				if existingHash != entry.RequestHash {
					return nil, ErrIdempotencyConflict
				}
				return nil, ErrInvalidTurnLedger
			}
			seenIdempotency[entry.IdempotencyKey] = entry.RequestHash
		}
		out = append(out, entry)
	}
	return out, nil
}

func isSHA256HexOrDigest(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if isSHA256Digest(value) {
		return true
	}
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
