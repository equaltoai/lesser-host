package store

import (
	"context"

	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
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
	return &Store{DB: lambdaTimeoutGuardDB(db)}
}

// lambdaTimeoutGuardDB returns a DB wrapper that applies TableTheory's
// Lambda-deadline-aware guard only for invocation contexts that actually carry
// a deadline. Tests, local tooling, and non-Lambda contexts keep the original
// mock/non-Lambda DB path unchanged.
func lambdaTimeoutGuardDB(db DB) DB {
	if db == nil {
		return nil
	}
	if _, ok := db.(*lambdaTimeoutDB); ok {
		return db
	}
	return &lambdaTimeoutDB{db: db}
}

type lambdaTimeoutDB struct {
	db DB
}

func (d *lambdaTimeoutDB) Model(model any) core.Query {
	return d.db.Model(model)
}

func (d *lambdaTimeoutDB) Migrate() error {
	return d.db.Migrate()
}

func (d *lambdaTimeoutDB) AutoMigrate(models ...any) error {
	return d.db.AutoMigrate(models...)
}

func (d *lambdaTimeoutDB) Close() error {
	return d.db.Close()
}

func (d *lambdaTimeoutDB) WithContext(ctx context.Context) core.DB {
	return applyLambdaTimeoutGuard(ctx, d.db).WithContext(ctx)
}

func (d *lambdaTimeoutDB) TransactWrite(ctx context.Context, fn func(core.TransactionBuilder) error) error {
	return applyLambdaTimeoutGuard(ctx, d.db).TransactWrite(ctx, fn)
}

func applyLambdaTimeoutGuard(ctx context.Context, db DB) DB {
	if db == nil || ctx == nil {
		return db
	}
	if _, ok := ctx.Deadline(); !ok {
		return db
	}

	// tabletheory.LambdaDB exposes the preferred typed helper preserving Lambda
	// caches, model registration, and TransactWrite support.
	if lambdaDB, ok := db.(interface {
		WithLambdaTimeout(context.Context) *tabletheory.LambdaDB
	}); ok {
		if guarded := lambdaDB.WithLambdaTimeout(ctx); guarded != nil {
			return guarded
		}
	}

	// Test doubles and lower-level TableTheory implementations expose the
	// core.ExtendedDB shape. Only use the result when it still satisfies host's
	// Store DB contract, including TransactWrite.
	if extended, ok := db.(interface {
		WithLambdaTimeout(context.Context) core.DB
	}); ok {
		if guarded, ok := extended.WithLambdaTimeout(ctx).(DB); ok && guarded != nil {
			return guarded
		}
	}

	return db
}

// IsNotFound reports whether an error represents a not-found condition.
func IsNotFound(err error) bool {
	return theoryErrors.IsNotFound(err)
}
