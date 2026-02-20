package controlplane

import "testing"

func TestNew_ConstructsApp(t *testing.T) {
	// Ensure AWS SDK config resolution is fast and hermetic in unit tests.
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	// Avoid SSM lookups in tests/local runs.
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("AWS_EXECUTION_ENV", "")
	t.Setenv("TIP_RPC_URL", "")
	t.Setenv("TIP_RPC_URL_SSM_PARAM", "/lesser-host/test/tip-rpc-url")

	if got := New(); got == nil {
		t.Fatalf("expected app, got nil")
	}
}
