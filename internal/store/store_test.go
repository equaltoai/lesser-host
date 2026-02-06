package store

import (
	"context"
	"testing"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store/models"
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
		dest := args.Get(0).(*models.AIJob)
		dest.ID = "job-1"
	})

	q.On("CreateOrUpdate").Return(nil)
	q.On("Index", mock.Anything).Return(q)
	q.On("Limit", mock.Anything).Return(q)
	q.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.AIJob)
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

	if err := st.PutAIJob(ctx, nil); err == nil {
		t.Fatalf("expected error for nil job")
	}
	if err := st.PutAIJob(ctx, &models.AIJob{ID: "job-1"}); err != nil {
		t.Fatalf("PutAIJob: %v", err)
	}

	if _, err := st.CountQueuedAIJobsByInstance(ctx, "", 10); err == nil {
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
