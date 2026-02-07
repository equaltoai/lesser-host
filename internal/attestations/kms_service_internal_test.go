package attestations

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"testing"
	"time"
)

func TestNewKMSService_DedupAndDefaults(t *testing.T) {
	t.Parallel()

	s := NewKMSService(" key ", []string{"k1", " k1 ", "k2", ""})
	if s == nil {
		t.Fatalf("expected service")
	}
	if s.signingKeyID != "key" {
		t.Fatalf("unexpected signing key: %q", s.signingKeyID)
	}
	if len(s.publicKeyIDs) != 2 {
		t.Fatalf("expected 2 public keys, got %#v", s.publicKeyIDs)
	}
}

func TestKMSService_EnabledAndErrors(t *testing.T) {
	t.Parallel()

	if NewKMSService("", nil).Enabled() {
		t.Fatalf("expected disabled without signing key")
	}
	if (&KMSService{}).Enabled() {
		t.Fatalf("expected disabled with empty signing key")
	}
	if _, err := (*KMSService)(nil).JWKS(context.Background()); err == nil {
		t.Fatalf("expected error for nil service")
	}
	if _, err := NewKMSService("sign", nil).JWKS(context.Background()); err == nil {
		t.Fatalf("expected error for missing public keys")
	}
	if _, _, err := (*KMSService)(nil).SignPayloadJWS(context.Background(), []byte("x")); err == nil {
		t.Fatalf("expected error for nil service sign")
	}
	if _, _, err := NewKMSService("", []string{"k"}).SignPayloadJWS(context.Background(), []byte("x")); err == nil {
		t.Fatalf("expected error for missing signing key")
	}
}

func TestKMSService_JWKS_UsesMemoAndCaches(t *testing.T) {
	t.Parallel()

	key1, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	key2, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	s := NewKMSService("sign", []string{"k2", "k1"})
	s.publicKeyMemo["k1"] = &key1.PublicKey
	s.publicKeyMemo["k2"] = &key2.PublicKey

	jwks, err := s.JWKS(context.Background())
	if err != nil {
		t.Fatalf("JWKS err: %v", err)
	}
	if len(jwks.Keys) != 2 {
		t.Fatalf("expected 2 keys, got %#v", jwks.Keys)
	}
	// Sorted by kid.
	if jwks.Keys[0].Kid != "k1" || jwks.Keys[1].Kid != "k2" {
		t.Fatalf("unexpected key ordering: %#v", jwks.Keys)
	}

	// Cached read should return without needing KMS.
	s.jwksMu.Lock()
	s.jwksCached = jwks
	s.jwksCachedAt = time.Now().UTC()
	s.jwksCacheTTL = 1 * time.Hour
	s.jwksMu.Unlock()

	jwks2, err := s.JWKS(context.Background())
	if err != nil {
		t.Fatalf("JWKS cached err: %v", err)
	}
	if len(jwks2.Keys) != 2 {
		t.Fatalf("expected cached keys, got %#v", jwks2.Keys)
	}
}

func TestKMSService_RSAKeyMemoAndClientErrors(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	s := NewKMSService("sign", []string{"k1"})
	s.publicKeyMemo["k1"] = &key.PublicKey

	// Memo hit.
	got, err := s.rsaPublicKey(context.Background(), "k1")
	if err != nil || got == nil {
		t.Fatalf("expected memo hit, got pub=%v err=%v", got, err)
	}

	// Force kmsClient() to return an error without hitting AWS.
	s.once.Do(func() {})
	s.err = fmt.Errorf("boom")
	if _, err := s.kmsClient(context.Background()); err == nil {
		t.Fatalf("expected kmsClient error")
	}
	if _, err := s.rsaPublicKey(context.Background(), "k2"); err == nil {
		t.Fatalf("expected rsaPublicKey error when kmsClient fails")
	}
	if _, _, err := s.SignPayloadJWS(context.Background(), []byte("x")); err == nil {
		t.Fatalf("expected SignPayloadJWS error when kmsClient fails")
	}
}
