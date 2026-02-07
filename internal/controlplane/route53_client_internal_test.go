package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRoute53ClientGet_ErrorsAndNotInitialized(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if _, err := (*route53Client)(nil).get(ctx); err == nil {
		t.Fatalf("expected error for nil client")
	}

	r := newRoute53Client()
	r.once.Do(func() {})
	if _, err := r.get(ctx); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not initialized error, got %v", err)
	}

	r2 := newRoute53Client()
	r2.err = errors.New("boom")
	r2.once.Do(func() {})
	if _, err := r2.get(ctx); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected injected error, got %v", err)
	}
}

