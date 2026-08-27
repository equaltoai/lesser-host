package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// PutHostedGenesisMicroVMExecution writes Host-owned operational MicroVM cache
// state. Callers pass semantic fields only; the model derives PK/SK internally.
func (s *Store) PutHostedGenesisMicroVMExecution(ctx context.Context, item *models.HostedGenesisMicroVMExecution) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("hosted genesis microvm execution is required")
	}
	if err := item.BeforeCreate(); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}

// GetHostedGenesisMicroVMExecution loads Host-owned operational MicroVM cache
// for one tenant-bound hosted-genesis session.
func (s *Store) GetHostedGenesisMicroVMExecution(ctx context.Context, instanceSlug string, namespace string, sessionID string) (*models.HostedGenesisMicroVMExecution, error) {
	instanceSlug = strings.TrimSpace(instanceSlug)
	namespace = strings.TrimSpace(namespace)
	sessionID = strings.TrimSpace(sessionID)
	if instanceSlug == "" || namespace == "" || sessionID == "" {
		return nil, theoryErrors.ErrItemNotFound
	}
	var item models.HostedGenesisMicroVMExecution
	if err := s.getByPKSK(
		ctx,
		&models.HostedGenesisMicroVMExecution{},
		models.HostedGenesisMicroVMExecutionPK(instanceSlug, namespace),
		models.HostedGenesisMicroVMExecutionSK(sessionID),
		&item,
	); err != nil {
		return nil, err
	}
	return &item, nil
}

// ListHostedGenesisMicroVMExecutions returns Host-owned operational MicroVM
// cache rows for exactly one managed-instance slug and AppTheory namespace.
// The read is bounded (issue #1061 part B): page-capped PK queries resumed via
// the opaque cursor, failing closed if the partition exceeds the page cap.
func (s *Store) ListHostedGenesisMicroVMExecutions(ctx context.Context, instanceSlug string, namespace string) ([]*models.HostedGenesisMicroVMExecution, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	instanceSlug = strings.TrimSpace(instanceSlug)
	namespace = strings.TrimSpace(namespace)
	if instanceSlug == "" || namespace == "" {
		return nil, fmt.Errorf("hosted genesis microvm execution list requires instance slug and namespace")
	}
	items, err := allPartitionItemsBounded[models.HostedGenesisMicroVMExecution](
		s.DB.WithContext(ctx).
			Model(&models.HostedGenesisMicroVMExecution{}).
			Where("PK", "=", models.HostedGenesisMicroVMExecutionPK(instanceSlug, namespace)),
		storePartitionWalkPageSize,
		storePartitionWalkMaxPages,
	)
	if err != nil {
		if theoryErrors.IsNotFound(err) {
			return []*models.HostedGenesisMicroVMExecution{}, nil
		}
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i] == nil {
			return false
		}
		if items[j] == nil {
			return true
		}
		return items[i].SessionID < items[j].SessionID
	})
	return items, nil
}

// DeleteHostedGenesisMicroVMExecution removes only operational cache. Host's
// HostedGenesisSession source truth is deliberately untouched.
func (s *Store) DeleteHostedGenesisMicroVMExecution(ctx context.Context, instanceSlug string, namespace string, sessionID string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	instanceSlug = strings.TrimSpace(instanceSlug)
	namespace = strings.TrimSpace(namespace)
	sessionID = strings.TrimSpace(sessionID)
	if instanceSlug == "" || namespace == "" || sessionID == "" {
		return fmt.Errorf("hosted genesis microvm execution delete requires instance slug, namespace, and session id")
	}
	return s.DB.WithContext(ctx).
		Model(&models.HostedGenesisMicroVMExecution{}).
		Where("PK", "=", models.HostedGenesisMicroVMExecutionPK(instanceSlug, namespace)).
		Where("SK", "=", models.HostedGenesisMicroVMExecutionSK(sessionID)).
		Delete()
}
