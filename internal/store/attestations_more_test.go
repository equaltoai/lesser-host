package store

import (
	"context"
	"testing"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestStore_AttestationsAndAIResults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.Anything).Return(q)

	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
	q.On("ConsistentRead").Return(q)
	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		switch dest := args.Get(0).(type) {
		case *models.AIResult:
			dest.ID = "res-1"
		case *models.Attestation:
			dest.ID = "att-1"
		}
	})
	q.On("CreateOrUpdate").Return(nil)

	st := New(db)

	if _, err := st.GetAIResult(ctx, " "); err == nil {
		t.Fatalf("expected error for empty result id")
	}
	if err := st.PutAIResult(ctx, nil); err == nil {
		t.Fatalf("expected error for nil result")
	}
	res, err := st.GetAIResult(ctx, "res-1")
	if err != nil || res == nil || res.ID != "res-1" {
		t.Fatalf("GetAIResult: res=%#v err=%v", res, err)
	}
	if putErr := st.PutAIResult(ctx, &models.AIResult{ID: "res-1"}); putErr != nil {
		t.Fatalf("PutAIResult: %v", putErr)
	}

	if _, getErr := st.GetAttestation(ctx, " "); getErr == nil {
		t.Fatalf("expected error for empty attestation id")
	}
	if putErr := st.PutAttestation(ctx, nil); putErr == nil {
		t.Fatalf("expected error for nil attestation")
	}
	att, err := st.GetAttestation(ctx, "att-1")
	if err != nil || att == nil || att.ID != "att-1" {
		t.Fatalf("GetAttestation: att=%#v err=%v", att, err)
	}
	if err := st.PutAttestation(ctx, &models.Attestation{ID: "att-1"}); err != nil {
		t.Fatalf("PutAttestation: %v", err)
	}
}
