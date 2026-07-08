package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// hashDeclarationJSON returns the sha256 digest of the exact ProducedDeclarations
// JSON persisted to the SoulAgentMintConversation record. The hosted-genesis
// projection verifies this byte-for-byte against the stored blob before exposing
// declarations to the instance API.
func hashDeclarationJSON(declarationsJSON string) (prefixed string, hexDigest string, err error) {
	body := strings.TrimSpace(declarationsJSON)
	if body == "" {
		return "", "", errors.New("declarations JSON is required")
	}
	sum := sha256.Sum256([]byte(body))
	hexDigest = hex.EncodeToString(sum[:])
	return "sha256:" + hexDigest, hexDigest, nil
}
