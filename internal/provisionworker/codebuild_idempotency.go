package provisionworker

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// codebuildIdempotencyToken returns a stable token for CodeBuild StartBuild's IdempotencyToken.
// CodeBuild tokens are only valid for ~5 minutes, which is enough to dedupe at-least-once SQS deliveries.
//
// The returned value is a 32-character lowercase hex string, or "" if all parts are empty.
func codebuildIdempotencyToken(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	hasAny := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			hasAny = true
		}
		normalized = append(normalized, part)
	}
	if !hasAny {
		return ""
	}

	sum := sha256.Sum256([]byte(strings.Join(normalized, "\n")))
	return hex.EncodeToString(sum[:])[:32]
}
