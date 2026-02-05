package store

import (
	"context"
	"fmt"
)

func (s *Store) requireDB() error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	return nil
}

func (s *Store) getByPKSK(ctx context.Context, modelType any, pk string, sk string, out any) error {
	if err := s.requireDB(); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).
		Model(modelType).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(out)
}

func (s *Store) putModel(ctx context.Context, item any) error {
	if err := s.requireDB(); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}
