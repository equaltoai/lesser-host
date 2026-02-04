package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func usageLedgerEntryID(instanceSlug string, month string, requestID string, module string, target string, debitedCredits int64) string {
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

func billingPartsForDebit(includedCredits int64, usedCredits int64, delta int64) (includedDebited int64, overageDebited int64) {
	remaining := includedCredits - usedCredits
	if remaining <= 0 {
		return 0, delta
	}
	if remaining >= delta {
		return delta, 0
	}
	return remaining, delta - remaining
}

func billingTypeFromParts(includedDebited int64, overageDebited int64) string {
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
