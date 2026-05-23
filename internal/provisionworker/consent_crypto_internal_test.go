package provisionworker

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func TestEncryptDecryptConsent_RoundTrip(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}

	t.Run("message_with_signature", func(t *testing.T) {
		message := `{"slug":"test","expires_at":"2026-06-01T00:00:00Z"}`
		signature := "0xabcd1234"
		payload := message + "\n" + signature

		enc, err := EncryptConsent(payload, key)
		if err != nil {
			t.Fatalf("EncryptConsent: %v", err)
		}
		if !strings.HasPrefix(enc, consentEncryptionVersionPrefix) {
			t.Fatalf("expected %q prefix, got %q", consentEncryptionVersionPrefix, enc)
		}

		dec, err := DecryptConsent(enc, key)
		if err != nil {
			t.Fatalf("DecryptConsent: %v", err)
		}
		if dec != payload {
			t.Fatalf("round-trip mismatch: dec=%q payload=%q", dec, payload)
		}
	})

	t.Run("message_only", func(t *testing.T) {
		message := `{"slug":"test"}`
		enc, err := EncryptConsent(message, key)
		if err != nil {
			t.Fatalf("EncryptConsent: %v", err)
		}
		dec, err := DecryptConsent(enc, key)
		if err != nil {
			t.Fatalf("DecryptConsent: %v", err)
		}
		if dec != message {
			t.Fatalf("round-trip mismatch: dec=%q payload=%q", dec, message)
		}
	})

	t.Run("empty_plaintext_is_noop", func(t *testing.T) {
		enc, err := EncryptConsent("", key)
		if err != nil {
			t.Fatalf("EncryptConsent empty: %v", err)
		}
		if enc != "" {
			t.Fatalf("expected empty, got %q", enc)
		}

		dec, err := DecryptConsent("", key)
		if err != nil {
			t.Fatalf("DecryptConsent empty: %v", err)
		}
		if dec != "" {
			t.Fatalf("expected empty, got %q", dec)
		}
	})
}

func TestDecryptConsent_LegacyPlaintextPassthrough(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}

	// Legacy value without "enc:v1:" prefix should be returned as-is.
	legacy := `{"slug":"test","expires_at":"2026-06-01T00:00:00Z"}`
	dec, err := DecryptConsent(legacy, key)
	if err != nil {
		t.Fatalf("DecryptConsent legacy: %v", err)
	}
	if dec != legacy {
		t.Fatalf("legacy mismatch: dec=%q legacy=%q", dec, legacy)
	}
}

func TestEncryptDecryptConsent_KeyErrors(t *testing.T) {
	t.Parallel()

	t.Run("wrong_key_size", func(t *testing.T) {
		shortKey := make([]byte, 16)
		if _, err := rand.Read(shortKey); err != nil {
			t.Fatalf("rand key: %v", err)
		}
		_, err := EncryptConsent("hello", shortKey)
		if err == nil {
			t.Fatalf("expected error for 16-byte key")
		}
		if !strings.Contains(err.Error(), "32 bytes") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wrong_key_decrypt", func(t *testing.T) {
		key1 := make([]byte, 32)
		key2 := make([]byte, 32)
		if _, err := rand.Read(key1); err != nil {
			t.Fatalf("rand key1: %v", err)
		}
		if _, err := rand.Read(key2); err != nil {
			t.Fatalf("rand key2: %v", err)
		}

		enc, err := EncryptConsent("hello", key1)
		if err != nil {
			t.Fatalf("EncryptConsent: %v", err)
		}
		_, err = DecryptConsent(enc, key2)
		if err == nil {
			t.Fatalf("expected decryption error with wrong key")
		}
	})

	t.Run("tampered_ciphertext", func(t *testing.T) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			t.Fatalf("rand key: %v", err)
		}
		enc, err := EncryptConsent("hello", key)
		if err != nil {
			t.Fatalf("EncryptConsent: %v", err)
		}
		// Flip last byte.
		tampered := enc[:len(enc)-1] + string(enc[len(enc)-1]^0xff)
		_, err = DecryptConsent(tampered, key)
		if err == nil {
			t.Fatalf("expected decryption error with tampered ciphertext")
		}
	})
}

func TestConsentEncryptionKeyHex(t *testing.T) {
	t.Parallel()

	t.Run("valid_hex_key", func(t *testing.T) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			t.Fatalf("rand key: %v", err)
		}
		keyHex := hex.EncodeToString(key)

		decoded, err := ConsentEncryptionKeyHex(keyHex)
		if err != nil {
			t.Fatalf("ConsentEncryptionKeyHex: %v", err)
		}
		if len(decoded) != 32 {
			t.Fatalf("expected 32 bytes, got %d", len(decoded))
		}
		// Re-encode should match.
		if hex.EncodeToString(decoded) != keyHex {
			t.Fatalf("re-encode mismatch")
		}
	})

	t.Run("empty_key_errors", func(t *testing.T) {
		_, err := ConsentEncryptionKeyHex("")
		if err == nil {
			t.Fatalf("expected error for empty key")
		}
	})

	t.Run("wrong_length_key_errors", func(t *testing.T) {
		_, err := ConsentEncryptionKeyHex("abcdef")
		if err == nil {
			t.Fatalf("expected error for short key")
		}
	})
}
