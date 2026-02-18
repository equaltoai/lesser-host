package testutil

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRequireTypeAndMockArg_SuccessPaths(t *testing.T) {
	t.Parallel()

	require.Equal(t, 123, RequireType[int](t, 123))

	type foo struct{ V int }
	f := &foo{V: 1}
	args := mock.Arguments{f}
	require.Same(t, f, RequireMockArg[*foo](t, args, 0))
}
