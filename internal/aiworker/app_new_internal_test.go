package aiworker

import "testing"

func TestNew_ConstructsApp(t *testing.T) {
	// Ensure AWS SDK config resolution is fast and hermetic in unit tests.
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	// Drive queue registration branch coverage.
	t.Setenv("SAFETY_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/123456789012/lesser-host-safety")

	if got := New(); got == nil {
		t.Fatalf("expected app, got nil")
	}
}

