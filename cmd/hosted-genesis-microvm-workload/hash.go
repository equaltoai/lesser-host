package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
)

// hashDeclarationDraft returns the sha256 digest of the canonical JSON encoding
// of the extracted declaration draft, prefixed with "sha256:" to match the
// hostedgenesis DeclarationCheckpoint hash contract.
func hashDeclarationDraft(draft llm.MintConversationDeclarationsDraft) (string, error) {
	body, err := json.Marshal(draft)
	if err != nil {
		return "", fmt.Errorf("marshal declaration draft: %w", err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
