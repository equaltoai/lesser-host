package trust

import "testing"

func TestNew_ConstructsApp(t *testing.T) {
	// Ensure AWS SDK config resolution is fast and hermetic in unit tests.
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	// Avoid creating a Dynamo-backed rate limiter in unit tests.
	t.Setenv("STATE_TABLE_NAME", "")
	t.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", "")

	if got := New(); got == nil {
		t.Fatalf("expected app, got nil")
	}
}

