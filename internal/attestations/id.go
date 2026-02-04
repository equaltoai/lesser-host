package attestations

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func AttestationID(actorURI string, objectURI string, contentHash string, module string, policyVersion string) string {
	actorURI = strings.TrimSpace(actorURI)
	objectURI = strings.TrimSpace(objectURI)
	contentHash = strings.ToLower(strings.TrimSpace(contentHash))
	module = strings.ToLower(strings.TrimSpace(module))
	policyVersion = strings.TrimSpace(policyVersion)

	canonical := strings.Join([]string{
		"lesser.host/attestation/v1",
		actorURI,
		objectURI,
		contentHash,
		module,
		policyVersion,
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
