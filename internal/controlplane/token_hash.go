package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func sha256HexTrimmed(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
