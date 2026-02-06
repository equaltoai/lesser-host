package attestations

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestBuildCompactJWSRS256_Verifies(t *testing.T) {
	t.Parallel()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	payload := PayloadV1{
		Type:          PayloadTypeV1,
		ActorURI:      "at://did:example:alice/app.bsky.feed.post/123",
		ObjectURI:     "at://did:example:alice/app.bsky.feed.post/123",
		ContentHash:   "sha256:deadbeef",
		Module:        "link_safety_basic",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		CreatedAt:     time.Unix(1, 0).UTC(),
		ExpiresAt:     time.Unix(2, 0).UTC(),
		Result: map[string]any{
			"ok": true,
		},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload: %v", err)
	}

	kid := "test-key"
	jws, err := BuildCompactJWSRS256(context.Background(), kid, payloadBytes, func(_ context.Context, digest []byte) ([]byte, error) {
		return rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest)
	})
	if err != nil {
		t.Fatalf("BuildCompactJWSRS256: %v", err)
	}

	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWS parts, got %d", len(parts))
	}

	headerBytes, decodedPayload, sigBytes, err := ParseCompactJWS(jws)
	if err != nil {
		t.Fatalf("ParseCompactJWS: %v", err)
	}
	if string(decodedPayload) != string(payloadBytes) {
		t.Fatalf("decoded payload mismatch")
	}

	var hdr CompactJWSHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		t.Fatalf("Unmarshal header: %v", err)
	}
	if hdr.Alg != "RS256" {
		t.Fatalf("expected alg=RS256, got %q", hdr.Alg)
	}
	if hdr.Kid != kid {
		t.Fatalf("expected kid=%q, got %q", kid, hdr.Kid)
	}

	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(&priv.PublicKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		t.Fatalf("VerifyPKCS1v15: %v", err)
	}
}

func TestJWKFromRSAPublicKey_RoundTrip(t *testing.T) {
	t.Parallel()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	jwk, err := JWKFromRSAPublicKey("kid", &priv.PublicKey)
	if err != nil {
		t.Fatalf("JWKFromRSAPublicKey: %v", err)
	}
	if jwk.Kty != "RSA" || jwk.N == "" || jwk.E == "" {
		t.Fatalf("unexpected jwk: %+v", jwk)
	}

	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		t.Fatalf("Decode n: %v", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		t.Fatalf("Decode e: %v", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	if n.Cmp(priv.N) != 0 {
		t.Fatalf("modulus mismatch")
	}
	if int(e.Int64()) != priv.E {
		t.Fatalf("exponent mismatch")
	}
}

func TestAttestationID_IsDeterministic(t *testing.T) {
	t.Parallel()

	id1 := AttestationID("actor", "object", "hash", "module", "v1")
	id2 := AttestationID("actor", "object", "hash", "module", "v1")
	if id1 == "" || len(id1) != 64 {
		t.Fatalf("unexpected id: %q", id1)
	}
	if id1 != id2 {
		t.Fatalf("expected ids to match: %q vs %q", id1, id2)
	}

	id3 := AttestationID("actor", "object", "hash", "module2", "v1")
	if id3 == id1 {
		t.Fatalf("expected different ids")
	}
}
