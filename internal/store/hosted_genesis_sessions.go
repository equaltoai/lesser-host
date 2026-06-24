package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetHostedGenesisSession loads a Host-owned durable hosted-genesis session by
// tenant slug and conversation id. The tenant slug is part of the primary key;
// callers must not look up hosted genesis sessions by conversation id alone.
func (s *Store) GetHostedGenesisSession(ctx context.Context, instanceSlug string, conversationID string) (*models.HostedGenesisSession, error) {
	instanceSlug = strings.TrimSpace(instanceSlug)
	conversationID = strings.TrimSpace(conversationID)
	if instanceSlug == "" || conversationID == "" {
		return nil, theoryErrors.ErrItemNotFound
	}
	var item models.HostedGenesisSession
	if err := s.getByPKSK(
		ctx,
		&models.HostedGenesisSession{},
		models.HostedGenesisSessionPK(instanceSlug),
		models.HostedGenesisSessionSK(conversationID),
		&item,
	); err != nil {
		return nil, err
	}
	return &item, nil
}

// CreateHostedGenesisSession writes a new durable hosted-genesis source-of-
// truth row. Use UpdateHostedGenesisSession for any later mutation so state
// transitions are guarded by the expected TableTheory version.
func (s *Store) CreateHostedGenesisSession(ctx context.Context, item *models.HostedGenesisSession) error {
	if s == nil || s.DB == nil || item == nil {
		return theoryErrors.ErrItemNotFound
	}
	if err := item.BeforeCreate(); err != nil {
		return err
	}
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Create(item)
		return nil
	})
}

// UpdateHostedGenesisSession updates a durable hosted-genesis source-of-truth
// row under an explicit optimistic-lock version and expected current status. A
// stale expectedVersion or expectedStatus must fail as a transaction condition
// error rather than silently overwriting state.
func (s *Store) UpdateHostedGenesisSession(ctx context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) error {
	if s == nil || s.DB == nil || item == nil {
		return theoryErrors.ErrItemNotFound
	}
	if expectedVersion < 0 {
		return fmt.Errorf("expected version must be non-negative")
	}
	expectedStatus = hostedgenesis.NormalizeStatus(string(expectedStatus))
	if !hostedgenesis.IsAllowedStatus(expectedStatus) {
		return hostedgenesis.ErrInvalidStatusTransition
	}
	if err := item.BeforeUpdate(); err != nil {
		return err
	}
	if err := hostedgenesis.ValidateTransition(expectedStatus, hostedgenesis.Status(item.Status)); err != nil {
		return err
	}
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(item, func(ub core.UpdateBuilder) error {
			ub.Set("Status", item.Status)
			ub.Set("LatestTurnID", item.LatestTurnID)
			ub.Set("MessageCount", item.MessageCount)
			ub.Set("TurnLedger", item.TurnLedger)
			ub.Set("InputCheckpointRef", item.InputCheckpointRef)
			ub.Set("AssistantCheckpointRef", item.AssistantCheckpointRef)
			ub.Set("ExecutionStateRef", item.ExecutionStateRef)
			ub.Set("MicroVMExecutionID", item.MicroVMExecutionID)
			ub.Set("DeclarationCheckpoint", item.DeclarationCheckpoint)
			ub.Set("Failure", item.Failure)
			ub.Set("TraceIDs", item.TraceIDs)
			ub.Set("RequestID", item.RequestID)
			ub.Set("UpdatedAt", item.UpdatedAt)
			ub.Set("CompletedAt", item.CompletedAt)
			ub.Add("Version", int64(1))
			return nil
		}, tabletheory.IfExists(), tabletheory.AtVersion(expectedVersion), tabletheory.Condition("Status", "=", string(expectedStatus)))
		return nil
	})
}
