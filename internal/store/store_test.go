package store

import (
	"context"
	"errors"
	"testing"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestIsNotFound(t *testing.T) {
	t.Parallel()

	if IsNotFound(nil) {
		t.Fatalf("expected false")
	}
	if !IsNotFound(theoryErrors.ErrItemNotFound) {
		t.Fatalf("expected true")
	}
}

func TestStore_LambdaTimeoutGuard_NoOpsWithoutDeadline(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()

	db.On("WithContext", ctx).Return(db).Once()

	st := New(db)
	got := st.DB.WithContext(ctx)
	if got != db {
		t.Fatalf("expected original DB for non-deadline context, got %T", got)
	}

	db.AssertNotCalled(t, "WithLambdaTimeout", mock.Anything)
	db.AssertExpectations(t)
}

func TestStore_LambdaTimeoutGuard_AppliesDeadlineAndPreservesTransactWrite(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Minute))
	defer cancel()

	base := ttmocks.NewMockExtendedDBStrict()
	guarded := ttmocks.NewMockExtendedDBStrict()
	expectedErr := errors.New("transaction delegated")

	base.On("WithLambdaTimeout", ctx).Return(guarded).Once()
	guarded.On("WithContext", ctx).Return(guarded).Once()

	st := New(base)
	got := st.DB.WithContext(ctx)
	if got != guarded {
		t.Fatalf("expected guarded DB for deadline context, got %T", got)
	}

	base.On("WithLambdaTimeout", ctx).Return(guarded).Once()
	guarded.On("TransactWrite", ctx, mock.Anything).Return(expectedErr).Once()

	err := st.DB.TransactWrite(ctx, nil)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected delegated TransactWrite error %v, got %v", expectedErr, err)
	}

	base.AssertExpectations(t)
	guarded.AssertExpectations(t)
}

func TestStore_DBHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var s *Store
	if err := s.requireDB(); err == nil {
		t.Fatalf("expected error for nil store")
	}

	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.Anything).Return(q)

	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
	q.On("ConsistentRead").Return(q)
	q.On("First", mock.Anything).Return(nil)
	q.On("CreateOrUpdate").Return(nil)

	s = New(db)

	var out models.Attestation
	if err := s.getByPKSK(ctx, &models.Attestation{}, "PK", "SK", &out); err != nil {
		t.Fatalf("getByPKSK: %v", err)
	}
	if err := s.putModel(ctx, &models.Attestation{}); err != nil {
		t.Fatalf("putModel: %v", err)
	}
}

func TestStore_AIQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.Anything).Return(q)

	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
	q.On("ConsistentRead").Return(q)

	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.AIJob](t, args, 0)
		dest.ID = "job-1"
	})

	q.On("CreateOrUpdate").Return(nil)
	q.On("Index", mock.Anything).Return(q)
	q.On("Limit", mock.Anything).Return(q)
	q.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AIJob](t, args, 0)
		*dest = []*models.AIJob{{ID: "a"}, {ID: "b"}}
	})

	st := New(db)

	if _, err := st.GetAIJob(ctx, ""); err == nil {
		t.Fatalf("expected error for empty job id")
	}
	job, err := st.GetAIJob(ctx, "job-1")
	if err != nil {
		t.Fatalf("GetAIJob: %v", err)
	}
	if job == nil || job.ID != "job-1" {
		t.Fatalf("unexpected job: %#v", job)
	}

	if putErr := st.PutAIJob(ctx, nil); putErr == nil {
		t.Fatalf("expected error for nil job")
	}
	if putErr := st.PutAIJob(ctx, &models.AIJob{ID: "job-1"}); putErr != nil {
		t.Fatalf("PutAIJob: %v", putErr)
	}

	if _, countErr := st.CountQueuedAIJobsByInstance(ctx, "", 10); countErr == nil {
		t.Fatalf("expected error for empty slug")
	}
	n, err := st.CountQueuedAIJobsByInstance(ctx, "slug", -1)
	if err != nil {
		t.Fatalf("CountQueuedAIJobsByInstance: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}
