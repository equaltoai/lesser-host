package soul

import (
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

var localAgentIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,62}[a-z0-9]$`)

// NormalizeLocalAgentID normalizes and validates a lesser-soul local agent id.
//
// Rules are locked by lesser-soul ADR 0002.
func NormalizeLocalAgentID(raw string) (string, error) {
	local := strings.TrimSpace(raw)
	local = strings.TrimPrefix(local, "@")
	local = strings.TrimSuffix(local, "/")
	local = strings.ToLower(local)

	if local == "" {
		return "", errors.New("local_id is required")
	}
	if strings.ContainsAny(local, "/:@") {
		return "", errors.New("local_id must not contain /, :, or @")
	}
	if len(local) < 3 || len(local) > 64 {
		return "", errors.New("local_id must be between 3 and 64 characters")
	}
	if !localAgentIDRE.MatchString(local) {
		return "", errors.New("invalid local_id")
	}
	return local, nil
}

// DeriveAgentIDHex derives the deterministic soul agent id per lesser-soul ADR 0002:
// uint256(keccak256(utf8("${normalizedDomain}/${normalizedLocalAgentId}"))).
func DeriveAgentIDHex(normalizedDomain string, normalizedLocalAgentID string) (string, error) {
	normalizedDomain = strings.ToLower(strings.TrimSpace(normalizedDomain))
	normalizedLocalAgentID = strings.ToLower(strings.TrimSpace(normalizedLocalAgentID))
	if normalizedDomain == "" {
		return "", errors.New("normalizedDomain is required")
	}
	if normalizedLocalAgentID == "" {
		return "", errors.New("normalizedLocalAgentID is required")
	}

	sum := crypto.Keccak256([]byte(normalizedDomain + "/" + normalizedLocalAgentID))
	return "0x" + hex.EncodeToString(sum), nil
}

