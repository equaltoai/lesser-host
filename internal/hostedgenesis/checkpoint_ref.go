package hostedgenesis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const checkpointRefPrefix = "checkpoint://hosted-genesis/"

const (
	checkpointKindInput       = "input"
	checkpointKindAssistant   = "assistant"
	checkpointKindDeclaration = "declaration"
	checkpointKindRef         = "ref"
)

// CheckpointRef returns a compact, stable, TableTheory-update-safe checkpoint
// reference for Host-owned hosted-genesis progress. The reference deliberately
// does not embed raw conversation ids or turn ids: generated ids can contain
// substrings such as "--" that are valid identifiers but are rejected by
// TableTheory's update-expression value guard as injection-like content. The
// digest is enough to correlate Host-owned checkpoints without persisting raw
// prompts, transcripts, provider credentials, Instance API keys, wallet
// signatures, SSM values, AWS credentials, or MicroVM endpoint tokens.
func CheckpointRef(kind string, parts ...string) string {
	kind = normalizeCheckpointKind(kind)
	h := sha256.New()
	_, _ = h.Write([]byte(kind))
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("%s%s/%s", checkpointRefPrefix, kind, hex.EncodeToString(sum[:])[:16])
}

// NormalizeCheckpointRef converts legacy checkpoint refs that embedded raw ids
// into the compact digest form. Already-compact refs are preserved. Blank refs
// remain blank.
func NormalizeCheckpointRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	rest := strings.TrimPrefix(ref, checkpointRefPrefix)
	if rest == ref {
		return CheckpointRef("ref", ref)
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && isCheckpointKind(parts[0]) && isLowerHex(parts[1], 16) {
		return ref
	}
	return CheckpointRef(checkpointKindFromLegacyParts(parts), ref)
}

func normalizeCheckpointKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if isCheckpointKind(kind) {
		return kind
	}
	return checkpointKindRef
}

func isCheckpointKind(kind string) bool {
	switch kind {
	case checkpointKindInput, checkpointKindAssistant, checkpointKindDeclaration, checkpointKindRef:
		return true
	default:
		return false
	}
}

func checkpointKindFromLegacyParts(parts []string) string {
	for _, part := range parts {
		normalized := strings.ToLower(strings.TrimSpace(part))
		if isCheckpointKind(normalized) {
			return normalized
		}
		switch {
		case strings.HasPrefix(normalized, "input"):
			return checkpointKindInput
		case strings.HasPrefix(normalized, "assistant"):
			return checkpointKindAssistant
		case strings.HasPrefix(normalized, "declaration"), strings.HasPrefix(normalized, "decl"):
			return checkpointKindDeclaration
		}
	}
	return checkpointKindRef
}

func isLowerHex(value string, n int) bool {
	if len(value) != n {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
