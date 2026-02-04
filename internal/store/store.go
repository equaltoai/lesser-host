package store

import (
	"context"

	"github.com/theory-cloud/tabletheory/pkg/core"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
)

type DB interface {
	core.DB
	TransactWrite(ctx context.Context, fn func(core.TransactionBuilder) error) error
}

type Store struct {
	DB DB
}

func New(db DB) *Store {
	return &Store{DB: db}
}

func IsNotFound(err error) bool {
	return theoryErrors.IsNotFound(err)
}
