package billing

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// PricedCredits applies a per-instance pricing multiplier (basis points) to a base credit cost.
// It ceils the result to avoid systematic undercharging.
func PricedCredits(base int64, multiplierBps int64) int64 {
	if base <= 0 {
		return 0
	}
	if multiplierBps <= 0 {
		return base
	}
	if multiplierBps >= 10000 {
		return base
	}
	return (base*multiplierBps + 9999) / 10000
}

// UsageLedgerEntryID derives a deterministic ID for a usage ledger entry.
func UsageLedgerEntryID(instanceSlug string, month string, requestID string, module string, target string, debitedCredits int64) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"usage",
		strings.TrimSpace(instanceSlug),
		strings.TrimSpace(month),
		strings.TrimSpace(requestID),
		strings.TrimSpace(module),
		strings.TrimSpace(target),
		strconv.FormatInt(debitedCredits, 10),
	}, "|")))
	return hex.EncodeToString(sum[:])
}

// PartsForDebit splits a debit into included and overage portions.
func PartsForDebit(includedCredits int64, usedCredits int64, delta int64) (includedDebited int64, overageDebited int64) {
	remaining := includedCredits - usedCredits
	if remaining <= 0 {
		return 0, delta
	}
	if remaining >= delta {
		return delta, 0
	}
	return remaining, delta - remaining
}

// TypeFromParts determines the billing type based on included and overage portions.
func TypeFromParts(includedDebited int64, overageDebited int64) string {
	if includedDebited > 0 && overageDebited > 0 {
		return models.BillingTypeMixed
	}
	if overageDebited > 0 {
		return models.BillingTypeOverage
	}
	if includedDebited > 0 {
		return models.BillingTypeIncluded
	}
	return models.BillingTypeNone
}
