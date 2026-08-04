package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/theory-cloud/tabletheory/v3"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// hostedGenesisFailureShapeProbe is deliberately narrow. A normal
// HostedGenesisSession read cannot decode a legacy top-level DynamoDB BOOL at
// failure into *hostedgenesis.Failure, so the repair path first reads the exact
// tenant/session row through an any-typed compatibility probe.
type hostedGenesisFailureShapeProbe struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK      string `theorydb:"pk,attr:PK"`
	SK      string `theorydb:"sk,attr:SK"`
	Version int64  `theorydb:"version"`

	Status  string `theorydb:"attr:status"`
	Failure any    `theorydb:"attr:failure"`
}

func (hostedGenesisFailureShapeProbe) TableName() string { return models.MainTableName() }

type hostedGenesisMalformedFailureBinding struct {
	instanceSlug   string
	registrationID string
	agentID        string
	conversationID string
	pk             string
	sk             string
}

// hostedGenesisSessionFieldsWithoutFailure is the complete durable projection
// except for Failure. It is used only after the compatibility probe proves the
// exact impossible BOOL shape that prevented the normal model read.
var hostedGenesisSessionFieldsWithoutFailure = []string{
	"PK",
	"SK",
	"GSI1PK",
	"GSI1SK",
	"GSI2PK",
	"GSI2SK",
	"Version",
	"InstanceSlug",
	"RegistrationID",
	"AgentID",
	"ConversationID",
	"Status",
	"Model",
	"LatestTurnID",
	"MessageCount",
	"TurnLedger",
	"InputCheckpointRef",
	"AssistantCheckpointRef",
	"ExecutionStateRef",
	"MicroVMExecutionID",
	"MicroVMLifecycleRef",
	"DeclarationCheckpoint",
	"DeclarationCandidate",
	"CandidateRevision",
	"CandidateHash",
	"CandidatePhase",
	"Publication",
	"TraceIDs",
	"VMCheckpoint",
	"RequestID",
	"CreatedAt",
	"UpdatedAt",
	"CompletedAt",
}

// RepairHostedGenesisMalformedFailure removes only the impossible legacy shape
// failure=BOOL(true) from one exact tenant/registration/agent/session row.
//
// A non-failed session cannot legitimately carry that shape. The repair first
// reloads every other durable field, validates the session binding, then removes
// Failure in a single TableTheory transaction guarded by PK/SK, version, status,
// and the exact BOOL value. It increments Version so concurrent writers remain
// ordered. The caller receives retry_same_step and must retry the request that
// failed before turn acceptance.
//
// A failed session needs a structured Failure envelope to define recovery. If
// that envelope is itself a BOOL, Host cannot reconstruct it safely; no write is
// attempted and restart_soul_bootstrap is returned instead.
func (s *Store) RepairHostedGenesisMalformedFailure(ctx context.Context, instanceSlug string, registrationID string, agentID string, conversationID string) (hostedgenesis.RecoveryAction, error) {
	binding, bindingOK := newHostedGenesisMalformedFailureBinding(instanceSlug, registrationID, agentID, conversationID)
	if s == nil || s.DB == nil || !bindingOK {
		return "", theoryErrors.ErrItemNotFound
	}

	probe, err := s.getHostedGenesisFailureShapeProbe(ctx, binding.pk, binding.sk)
	if err != nil {
		return "", err
	}
	status, action, planErr := hostedGenesisMalformedFailureRepairPlan(probe)
	if planErr != nil || action != hostedgenesis.RecoveryActionRetrySameStep {
		return action, planErr
	}
	return s.repairHostedGenesisMalformedFailureSession(ctx, binding, probe, status)
}

func (s *Store) repairHostedGenesisMalformedFailureSession(ctx context.Context, binding hostedGenesisMalformedFailureBinding, probe *hostedGenesisFailureShapeProbe, status hostedgenesis.Status) (hostedgenesis.RecoveryAction, error) {
	session, err := s.getHostedGenesisSessionWithoutFailure(ctx, binding.pk, binding.sk)
	if err != nil {
		return "", err
	}
	if !hostedGenesisMalformedFailureBindingMatches(session, probe, binding, status) {
		return "", theoryErrors.ErrConditionFailed
	}
	// Validate every projected durable field before removing the malformed
	// attribute. This rechecks candidate, MicroVM, status, and ledger invariants;
	// an otherwise-invalid row is not guessed or rewritten.
	if validationErr := session.BeforeUpdate(); validationErr != nil {
		return hostedgenesis.RecoveryActionRestartSoulBootstrap, nil
	}
	if session.GetPK() != binding.pk || session.GetSK() != binding.sk {
		return "", theoryErrors.ErrConditionFailed
	}

	err = s.removeHostedGenesisMalformedFailure(ctx, session, probe.Version, binding, status)
	if err == nil {
		return hostedgenesis.RecoveryActionRetrySameStep, nil
	}
	if !theoryErrors.IsConditionFailed(err) {
		return "", err
	}
	return s.hostedGenesisMalformedFailureConflictAction(ctx, binding, err)
}

func newHostedGenesisMalformedFailureBinding(instanceSlug string, registrationID string, agentID string, conversationID string) (hostedGenesisMalformedFailureBinding, bool) {
	binding := hostedGenesisMalformedFailureBinding{
		instanceSlug:   strings.ToLower(strings.TrimSpace(instanceSlug)),
		registrationID: strings.TrimSpace(registrationID),
		agentID:        strings.ToLower(strings.TrimSpace(agentID)),
		conversationID: strings.TrimSpace(conversationID),
	}
	if binding.instanceSlug == "" || binding.registrationID == "" || binding.agentID == "" || binding.conversationID == "" {
		return hostedGenesisMalformedFailureBinding{}, false
	}
	binding.pk = models.HostedGenesisSessionPK(binding.instanceSlug)
	binding.sk = models.HostedGenesisSessionSK(binding.conversationID)
	return binding, true
}

func hostedGenesisMalformedFailureRepairPlan(probe *hostedGenesisFailureShapeProbe) (hostedgenesis.Status, hostedgenesis.RecoveryAction, error) {
	if probe == nil {
		return "", "", theoryErrors.ErrItemNotFound
	}
	malformed, ok := probe.Failure.(bool)
	if !ok || !malformed {
		return "", "", nil
	}
	status := hostedgenesis.NormalizeStatus(probe.Status)
	if status == hostedgenesis.StatusFailed {
		return status, hostedgenesis.RecoveryActionRestartSoulBootstrap, nil
	}
	if !hostedgenesis.IsAllowedStatus(status) || probe.Version < 0 {
		return "", "", fmt.Errorf("hosted genesis malformed failure repair state is invalid")
	}
	return status, hostedgenesis.RecoveryActionRetrySameStep, nil
}

func hostedGenesisMalformedFailureBindingMatches(session *models.HostedGenesisSession, probe *hostedGenesisFailureShapeProbe, binding hostedGenesisMalformedFailureBinding, status hostedgenesis.Status) bool {
	return session != nil &&
		probe != nil &&
		probe.PK == binding.pk &&
		probe.SK == binding.sk &&
		session.Version == probe.Version &&
		hostedgenesis.NormalizeStatus(session.Status) == status &&
		session.GetPK() == binding.pk &&
		session.GetSK() == binding.sk &&
		strings.ToLower(strings.TrimSpace(session.InstanceSlug)) == binding.instanceSlug &&
		strings.TrimSpace(session.RegistrationID) == binding.registrationID &&
		strings.ToLower(strings.TrimSpace(session.AgentID)) == binding.agentID &&
		strings.TrimSpace(session.ConversationID) == binding.conversationID
}

func (s *Store) removeHostedGenesisMalformedFailure(ctx context.Context, session *models.HostedGenesisSession, expectedVersion int64, binding hostedGenesisMalformedFailureBinding, expectedStatus hostedgenesis.Status) error {
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(session, func(ub core.UpdateBuilder) error {
			ub.Remove("Failure")
			ub.Add("Version", int64(1))
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.AtVersion(expectedVersion),
			tabletheory.Condition("InstanceSlug", "=", binding.instanceSlug),
			tabletheory.Condition("RegistrationID", "=", binding.registrationID),
			tabletheory.Condition("AgentID", "=", binding.agentID),
			tabletheory.Condition("ConversationID", "=", binding.conversationID),
			tabletheory.Condition("Status", "=", string(expectedStatus)),
			tabletheory.Condition("Failure", "=", true),
		)
		return nil
	})
}

func (s *Store) hostedGenesisMalformedFailureConflictAction(ctx context.Context, binding hostedGenesisMalformedFailureBinding, conditionErr error) (hostedgenesis.RecoveryAction, error) {
	// A concurrent request may have completed the exact same idempotent repair.
	// Accept that outcome only after the ordinary strict model read succeeds.
	current, reloadErr := s.GetHostedGenesisSession(ctx, binding.instanceSlug, binding.conversationID)
	if reloadErr != nil {
		return "", conditionErr
	}
	if strings.ToLower(strings.TrimSpace(current.InstanceSlug)) != binding.instanceSlug ||
		strings.TrimSpace(current.RegistrationID) != binding.registrationID ||
		strings.ToLower(strings.TrimSpace(current.AgentID)) != binding.agentID ||
		strings.TrimSpace(current.ConversationID) != binding.conversationID {
		return "", conditionErr
	}
	if hostedgenesis.NormalizeStatus(current.Status) == hostedgenesis.StatusFailed {
		return hostedgenesis.RecoveryActionRestartSoulBootstrap, nil
	}
	return hostedgenesis.RecoveryActionRetrySameStep, nil
}

func (s *Store) getHostedGenesisFailureShapeProbe(ctx context.Context, pk string, sk string) (*hostedGenesisFailureShapeProbe, error) {
	var probe hostedGenesisFailureShapeProbe
	err := s.DB.WithContext(ctx).
		Model(&hostedGenesisFailureShapeProbe{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&probe)
	if err != nil {
		return nil, err
	}
	return &probe, nil
}

func (s *Store) getHostedGenesisSessionWithoutFailure(ctx context.Context, pk string, sk string) (*models.HostedGenesisSession, error) {
	var session models.HostedGenesisSession
	err := s.DB.WithContext(ctx).
		Model(&models.HostedGenesisSession{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		Select(hostedGenesisSessionFieldsWithoutFailure...).
		First(&session)
	if err != nil {
		return nil, err
	}
	if err := hostedgenesis.NormalizePersistedDeclarationCandidate(session.DeclarationCandidate); err != nil {
		return nil, fmt.Errorf("normalize hosted genesis declaration candidate: %w", err)
	}
	return &session, nil
}

func setOrRemoveHostedGenesisFailure(ub core.UpdateBuilder, failure *hostedgenesis.Failure) {
	if failure == nil {
		ub.Remove("Failure")
		return
	}
	ub.Set("Failure", failure)
}
