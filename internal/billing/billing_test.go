package billing

import (
	"testing"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestPricedCredits(t *testing.T) {
	t.Parallel()

	if got := PricedCredits(0, 9000); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	if got := PricedCredits(10, 0); got != 10 {
		t.Fatalf("expected passthrough, got %d", got)
	}
	if got := PricedCredits(10, 10000); got != 10 {
		t.Fatalf("expected passthrough for >=10000, got %d", got)
	}
	if got := PricedCredits(1, 9999); got != 1 {
		t.Fatalf("expected ceil to 1, got %d", got)
	}
}

func TestUsageLedgerEntryID_IsDeterministic(t *testing.T) {
	t.Parallel()

	a := UsageLedgerEntryID("inst", "2026-02", "req", "m", "t", 10)
	b := UsageLedgerEntryID("inst", "2026-02", "req", "m", "t", 10)
	if a == "" || a != b {
		t.Fatalf("expected deterministic id, got %q vs %q", a, b)
	}
	c := UsageLedgerEntryID("inst", "2026-02", "req", "m", "other", 10)
	if c == a {
		t.Fatalf("expected different id for different target")
	}
}

func TestDebitPartsAndType(t *testing.T) {
	t.Parallel()

	inc, over := PartsForDebit(100, 50, 30)
	if inc != 30 || over != 0 {
		t.Fatalf("unexpected parts: inc=%d over=%d", inc, over)
	}

	inc, over = PartsForDebit(100, 90, 30)
	if inc != 10 || over != 20 {
		t.Fatalf("unexpected parts: inc=%d over=%d", inc, over)
	}

	inc, over = PartsForDebit(100, 200, 30)
	if inc != 0 || over != 30 {
		t.Fatalf("unexpected parts: inc=%d over=%d", inc, over)
	}

	if got := TypeFromParts(10, 20); got != models.BillingTypeMixed {
		t.Fatalf("expected mixed, got %q", got)
	}
	if got := TypeFromParts(0, 10); got != models.BillingTypeOverage {
		t.Fatalf("expected overage, got %q", got)
	}
	if got := TypeFromParts(10, 0); got != models.BillingTypeIncluded {
		t.Fatalf("expected included, got %q", got)
	}
	if got := TypeFromParts(0, 0); got != models.BillingTypeNone {
		t.Fatalf("expected none, got %q", got)
	}
}
