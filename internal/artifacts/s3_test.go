package artifacts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStore_ValidationAndClientInitErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if _, err := (*Store)(nil).s3Client(ctx); err == nil {
		t.Fatalf("expected error for nil store")
	}

	s := New("  bucket  ")
	if s.bucket != "bucket" {
		t.Fatalf("expected trimmed bucket, got %q", s.bucket)
	}

	// Force init path to skip awsconfig.LoadDefaultConfig by completing the once.
	s.once.Do(func() {})
	if _, err := s.s3Client(ctx); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not initialized error, got %v", err)
	}

	s2 := New("bucket")
	s2.err = errors.New("boom")
	s2.once.Do(func() {})
	if _, err := s2.s3Client(ctx); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestStore_OperationsValidateInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if err := (*Store)(nil).PutObject(ctx, "k", []byte("x"), "", ""); err == nil {
		t.Fatalf("expected error for nil store")
	}
	if err := New("").PutObject(ctx, "k", []byte("x"), "", ""); err == nil {
		t.Fatalf("expected error for missing bucket")
	}
	if err := New("b").PutObject(ctx, "", []byte("x"), "", ""); err == nil {
		t.Fatalf("expected error for missing key")
	}

	if _, _, _, err := (*Store)(nil).GetObject(ctx, "k", 1); err == nil {
		t.Fatalf("expected error for nil store")
	}
	if _, _, _, err := New("").GetObject(ctx, "k", 1); err == nil {
		t.Fatalf("expected error for missing bucket")
	}
	if _, _, _, err := New("b").GetObject(ctx, "", 1); err == nil {
		t.Fatalf("expected error for missing key")
	}

	if _, _, _, err := (*Store)(nil).HeadObject(ctx, "k"); err == nil {
		t.Fatalf("expected error for nil store")
	}
	if _, _, _, err := New("").HeadObject(ctx, "k"); err == nil {
		t.Fatalf("expected error for missing bucket")
	}
	if _, _, _, err := New("b").HeadObject(ctx, ""); err == nil {
		t.Fatalf("expected error for missing key")
	}

	if err := (*Store)(nil).DeleteObject(ctx, "k"); err == nil {
		t.Fatalf("expected error for nil store")
	}
	if err := New("").DeleteObject(ctx, "k"); err == nil {
		t.Fatalf("expected error for missing bucket")
	}
	if err := New("b").DeleteObject(ctx, ""); err == nil {
		t.Fatalf("expected error for missing key")
	}
}
