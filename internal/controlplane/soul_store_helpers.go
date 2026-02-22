package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func getSoulAgentItemBySK[T any](s *Server, ctx context.Context, agentID string, sk string) (*T, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}

	agentID = strings.ToLower(strings.TrimSpace(agentID))
	sk = strings.TrimSpace(sk)
	if agentID == "" || sk == "" {
		return nil, errors.New("agent id and sort key are required")
	}

	var item T
	err := s.store.DB.WithContext(ctx).
		Model(new(T)).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", sk).
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}
