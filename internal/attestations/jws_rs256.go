package attestations

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// CompactJWSHeader is the JSON header used in compact JWS serialization.
type CompactJWSHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid,omitempty"`
	Typ string `json:"typ,omitempty"`
}

// BuildCompactJWSRS256 builds a compact JWS signed with RS256.
func BuildCompactJWSRS256(ctx context.Context, kid string, payload []byte, signDigest func(context.Context, []byte) ([]byte, error)) (string, error) {
	if len(payload) == 0 {
		return "", fmt.Errorf("payload is required")
	}
	if signDigest == nil {
		return "", fmt.Errorf("signDigest is required")
	}
	kid = strings.TrimSpace(kid)

	header := CompactJWSHeader{
		Alg: "RS256",
		Kid: kid,
		Typ: "lesser.host-attestation+jws",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}

	hb64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	pb64 := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := hb64 + "." + pb64

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := signDigest(ctx, digest[:])
	if err != nil {
		return "", err
	}
	if len(sig) == 0 {
		return "", fmt.Errorf("empty signature")
	}

	sb64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sb64, nil
}

// ParseCompactJWS splits and decodes a compact JWS into header, payload, and signature bytes.
func ParseCompactJWS(jws string) ([]byte, []byte, []byte, error) {
	jws = strings.TrimSpace(jws)
	if jws == "" {
		return nil, nil, nil, fmt.Errorf("jws is required")
	}

	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return nil, nil, nil, fmt.Errorf("invalid jws")
	}

	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid jws header")
	}
	pl, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid jws payload")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid jws signature")
	}
	return hdr, pl, sig, nil
}
