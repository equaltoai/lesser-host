package store

import (
	"context"

	"github.com/theory-cloud/tabletheory/pkg/core"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
)

// DB is the storage interface used by Store.
type DB interface {
	core.DB
	TransactWrite(ctx context.Context, fn func(core.TransactionBuilder) error) error
}

// Store provides access to the application's persisted models.
type Store struct {
	DB DB
}

// New constructs a new Store.
func New(db DB) *Store {
	return &Store{DB: db}
}

// IsNotFound reports whether an error represents a not-found condition.
func IsNotFound(err error) bool {
	return theoryErrors.IsNotFound(err)
}
