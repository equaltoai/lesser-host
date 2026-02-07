package testutil

import (
	"testing"

	"github.com/stretchr/testify/mock"
)

func RequireType[T any](t *testing.T, value any) T {
	t.Helper()

	typed, ok := value.(T)
	if !ok {
		var zero T
		t.Fatalf("expected %T, got %T", zero, value)
	}

	return typed
}

func RequireMockArg[T any](t *testing.T, args mock.Arguments, index int) T {
	t.Helper()

	value := args.Get(index)
	typed, ok := value.(T)
	if !ok {
		var zero T
		t.Fatalf("expected arg %d to be %T, got %T", index, zero, value)
	}

	return typed
}
