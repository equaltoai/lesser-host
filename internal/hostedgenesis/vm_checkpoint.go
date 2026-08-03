package hostedgenesis

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// VMCheckpointMetadata is the safe, Host-persisted checkpoint envelope authored
// by the in-VM Hosted Genesis actor. It carries ids, state markers, provider
// family/model labels, and bounded trace/session ids only. It must never carry
// raw prompts, transcripts, provider payloads, credentials, bearer tokens,
// Instance API keys, wallet signatures, SSM values, AWS credentials, or MicroVM
// endpoint auth material.
type VMCheckpointMetadata struct {
	_ struct{} `theorydb:"naming:snake_case"`

	Sequence          int64  `theorydb:"attr:sequence,omitempty" json:"sequence"`
	Ref               string `theorydb:"attr:ref,omitempty" json:"ref"`
	Hash              string `theorydb:"attr:hash,omitempty" json:"hash"`
	Step              string `theorydb:"attr:step,omitempty" json:"step"`
	Action            string `theorydb:"attr:action,omitempty" json:"action"`
	StatusFrom        string `theorydb:"attr:status_from,omitempty" json:"status_from"`
	StatusTo          string `theorydb:"attr:status_to,omitempty" json:"status_to"`
	Runtime           string `theorydb:"attr:runtime,omitempty" json:"runtime"`
	ProviderFamily    string `json:"provider_family,omitempty"`
	ModelID           string `json:"model_id,omitempty"`
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	TraceID           string `json:"trace_id,omitempty"`
	LatestTurnID      string `theorydb:"attr:latest_turn_id,omitempty" json:"latest_turn_id"`
	RequestID         string `json:"request_id,omitempty"`
}

// VMCheckpointInput supplies the safe fields used to build VMCheckpointMetadata.
type VMCheckpointInput struct {
	ConversationID     string
	LatestTurnID       string
	RequestID          string
	Sequence           int64
	Step               string
	Action             string
	StatusFrom         Status
	StatusTo           Status
	Runtime            string
	ProviderFamily     string
	ModelID            string
	ProviderSessionID  string
	TraceID            string
	AdditionalHashSalt string
}

var ErrInvalidVMCheckpoint = errors.New("hosted genesis vm checkpoint is invalid")

// NewVMCheckpointMetadata builds a deterministic, safe checkpoint envelope from
// actor-owned runtime state and Host-owned status/version markers.
func NewVMCheckpointMetadata(input VMCheckpointInput) (VMCheckpointMetadata, error) {
	sequence := input.Sequence
	if sequence <= 0 {
		sequence = 1
	}
	step := safeCheckpointToken(input.Step)
	action := safeCheckpointToken(input.Action)
	latestTurnID := strings.TrimSpace(input.LatestTurnID)
	conversationID := strings.TrimSpace(input.ConversationID)
	ref := CheckpointRef("vm-actor", conversationID, fmt.Sprintf("%s-%d-%s", firstNonEmptyCheckpoint(step, action, "step"), sequence, latestTurnID))
	m := VMCheckpointMetadata{
		Sequence:          sequence,
		Ref:               ref,
		Step:              step,
		Action:            action,
		StatusFrom:        string(NormalizeStatus(string(input.StatusFrom))),
		StatusTo:          string(NormalizeStatus(string(input.StatusTo))),
		Runtime:           strings.TrimSpace(input.Runtime),
		ProviderFamily:    safeCheckpointToken(input.ProviderFamily),
		ModelID:           safeCheckpointModel(input.ModelID),
		ProviderSessionID: safeCheckpointID(input.ProviderSessionID),
		TraceID:           safeCheckpointID(input.TraceID),
		LatestTurnID:      latestTurnID,
		RequestID:         strings.TrimSpace(input.RequestID),
	}
	m.Hash = vmCheckpointHash(m, input.AdditionalHashSalt)
	if err := m.Validate(); err != nil {
		return VMCheckpointMetadata{}, err
	}
	return m, nil
}

func (m VMCheckpointMetadata) Validate() error {
	m = m.Normalize()
	if m.Sequence <= 0 || strings.TrimSpace(m.Ref) == "" || !isSHA256Digest(m.Hash) ||
		strings.TrimSpace(m.Step) == "" || strings.TrimSpace(m.Action) == "" ||
		strings.TrimSpace(m.Runtime) == "" || strings.TrimSpace(m.LatestTurnID) == "" {
		return ErrInvalidVMCheckpoint
	}
	from := NormalizeStatus(m.StatusFrom)
	to := NormalizeStatus(m.StatusTo)
	if !IsAllowedStatus(from) || !IsAllowedStatus(to) {
		return ErrInvalidVMCheckpoint
	}
	if containsUnsafeCheckpointMaterial(m.Ref, m.Hash, m.Step, m.Action, m.Runtime, m.ProviderFamily, m.ModelID, m.ProviderSessionID, m.TraceID, m.LatestTurnID, m.RequestID) {
		return ErrInvalidVMCheckpoint
	}
	return nil
}

func (m VMCheckpointMetadata) Normalize() VMCheckpointMetadata {
	m.Ref = NormalizeCheckpointRef(m.Ref)
	m.Hash = strings.TrimSpace(m.Hash)
	m.Step = safeCheckpointToken(m.Step)
	m.Action = safeCheckpointToken(m.Action)
	m.StatusFrom = string(NormalizeStatus(m.StatusFrom))
	m.StatusTo = string(NormalizeStatus(m.StatusTo))
	m.Runtime = strings.TrimSpace(m.Runtime)
	m.ProviderFamily = safeCheckpointToken(m.ProviderFamily)
	m.ModelID = safeCheckpointModel(m.ModelID)
	m.ProviderSessionID = safeCheckpointID(m.ProviderSessionID)
	m.TraceID = safeCheckpointID(m.TraceID)
	m.LatestTurnID = strings.TrimSpace(m.LatestTurnID)
	m.RequestID = strings.TrimSpace(m.RequestID)
	return m
}

func vmCheckpointHash(m VMCheckpointMetadata, salt string) string {
	material := strings.Join([]string{
		fmt.Sprintf("%d", m.Sequence), m.Ref, m.Step, m.Action, m.StatusFrom, m.StatusTo,
		m.Runtime, m.ProviderFamily, m.ModelID, m.ProviderSessionID, m.TraceID, m.LatestTurnID, m.RequestID, strings.TrimSpace(salt),
	}, "\x00")
	sum := sha256.Sum256([]byte(material))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func safeCheckpointToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func safeCheckpointModel(value string) string {
	return strings.TrimSpace(value)
}

func safeCheckpointID(value string) string {
	return strings.TrimSpace(value)
}

func firstNonEmptyCheckpoint(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "checkpoint"
}

func containsUnsafeCheckpointMaterial(values ...string) bool {
	for _, value := range values {
		lower := strings.ToLower(strings.TrimSpace(value))
		if lower == "" {
			continue
		}
		for _, forbidden := range []string{
			"bearer ", "authorization", "api_key", "apikey", "secret", "token_value",
			"aws_access_key", "aws_secret", "aws_session", "wallet_signature",
			"raw transcript", "raw prompt", "ssm:", "x-aws-proxy-auth",
		} {
			if strings.Contains(lower, forbidden) {
				return true
			}
		}
	}
	return false
}
