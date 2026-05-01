package attestations

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// AttestationID derives the legacy deterministic attestation ID from canonical identity inputs.
//
// New trust writes should use InstanceAttestationID so public attestations are
// bound to the authenticated instance that produced them. This helper remains
// for tests and for recognizing legacy, pre-live records.
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

// InstanceAttestationID derives a deterministic attestation ID bound to the
// authenticated instance slug. Binding the slug prevents one tenant from
// squatting or minting attestations for another tenant's tuple.
func InstanceAttestationID(instanceSlug string, actorURI string, objectURI string, contentHash string, module string, policyVersion string) string {
	instanceSlug = strings.ToLower(strings.TrimSpace(instanceSlug))
	actorURI = strings.TrimSpace(actorURI)
	objectURI = strings.TrimSpace(objectURI)
	contentHash = strings.ToLower(strings.TrimSpace(contentHash))
	module = strings.ToLower(strings.TrimSpace(module))
	policyVersion = strings.TrimSpace(policyVersion)

	canonical := strings.Join([]string{
		"lesser.host/attestation/v1/instance",
		instanceSlug,
		actorURI,
		objectURI,
		contentHash,
		module,
		policyVersion,
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
