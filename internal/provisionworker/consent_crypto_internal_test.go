package provisionworker

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func generateTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	return key
}

func TestEncryptDecryptConsent_MsgAndSigRoundTrip(t *testing.T) {
	t.Parallel()

	key := generateTestKey(t)
	message := `{"slug":"test","expires_at":"2026-06-01T00:00:00Z"}`
	signature := "0xabcd1234"

	packed, err := PackConsent(message, signature)
	if err != nil {
		t.Fatalf("PackConsent: %v", err)
	}
	enc, err := EncryptConsent(string(packed), key)
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
	msg, sig := UnpackConsent([]byte(dec))
	if msg != message {
		t.Fatalf("round-trip message mismatch: msg=%q expected=%q", msg, message)
	}
	if sig != signature {
		t.Fatalf("round-trip signature mismatch: sig=%q expected=%q", sig, signature)
	}
}

func TestEncryptDecryptConsent_MsgOnlyRoundTrip(t *testing.T) {
	t.Parallel()

	key := generateTestKey(t)
	message := `{"slug":"test"}`

	packed, err := PackConsent(message, "")
	if err != nil {
		t.Fatalf("PackConsent: %v", err)
	}
	enc, err := EncryptConsent(string(packed), key)
	if err != nil {
		t.Fatalf("EncryptConsent: %v", err)
	}
	dec, err := DecryptConsent(enc, key)
	if err != nil {
		t.Fatalf("DecryptConsent: %v", err)
	}
	msg, sig := UnpackConsent([]byte(dec))
	if msg != message {
		t.Fatalf("round-trip message mismatch: msg=%q expected=%q", msg, message)
	}
	if sig != "" {
		t.Fatalf("expected empty signature, got %q", sig)
	}
}

func TestEncryptDecryptConsent_ExactBytesPreserved(t *testing.T) {
	t.Parallel()

	key := generateTestKey(t)
	// Message with trailing whitespace and newlines must round-trip exactly.
	// CSR-017: the consent message may be signed, so every byte matters.
	message := "{\"slug\":\"test\"}\n"
	signature := "0xsig\t"

	packed, err := PackConsent(message, signature)
	if err != nil {
		t.Fatalf("PackConsent: %v", err)
	}
	enc, err := EncryptConsent(string(packed), key)
	if err != nil {
		t.Fatalf("EncryptConsent: %v", err)
	}
	dec, err := DecryptConsent(enc, key)
	if err != nil {
		t.Fatalf("DecryptConsent: %v", err)
	}
	msg, sig := UnpackConsent([]byte(dec))
	if msg != message {
		t.Fatalf("exact bytes mismatch for message: got=%q expected=%q", msg, message)
	}
	if sig != signature {
		t.Fatalf("exact bytes mismatch for signature: got=%q expected=%q", sig, signature)
	}
}

func TestEncryptDecryptConsent_EmptyPlaintextIsNoop(t *testing.T) {
	t.Parallel()

	key := generateTestKey(t)
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
}

func TestDecryptConsent_LegacyPlaintextPassthrough(t *testing.T) {
	t.Parallel()

	key := generateTestKey(t)
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

func TestEncryptDecryptConsent_WrongKeySize(t *testing.T) {
	t.Parallel()

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
}

func TestEncryptDecryptConsent_WrongKeyDecrypt(t *testing.T) {
	t.Parallel()

	key1 := generateTestKey(t)
	key2 := generateTestKey(t)

	enc, err := EncryptConsent("hello", key1)
	if err != nil {
		t.Fatalf("EncryptConsent: %v", err)
	}
	_, err = DecryptConsent(enc, key2)
	if err == nil {
		t.Fatalf("expected decryption error with wrong key")
	}
}

func TestEncryptDecryptConsent_TamperedCiphertext(t *testing.T) {
	t.Parallel()

	key := generateTestKey(t)
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
}

func TestEncryptConsent_FailClosedOnKeyError(t *testing.T) {
	t.Parallel()

	packed, err := PackConsent("hello", "")
	if err != nil {
		t.Fatalf("PackConsent: %v", err)
	}
	// Empty key should error.
	_, err = EncryptConsent(string(packed), nil)
	if err == nil {
		t.Fatalf("expected error for nil key, got nil")
	}
	// Wrong size key should error.
	shortKey := make([]byte, 16)
	_, err = EncryptConsent(string(packed), shortKey)
	if err == nil {
		t.Fatalf("expected error for 16-byte key, got nil")
	}
}

func TestUnpackConsent_NewlinesInMessage(t *testing.T) {
	t.Parallel()

	// CSR-017: The structured JSON packet must survive a consent message
	// that contains newlines. This proves the newline-delimited packing
	// vulnerability is closed.
	message := "line1\nline2\nline3"
	signature := "0xsig"

	packed, err := PackConsent(message, signature)
	if err != nil {
		t.Fatalf("PackConsent: %v", err)
	}
	msg, sig := UnpackConsent(packed)
	if msg != message {
		t.Fatalf("message with newlines mismatch: got=%q expected=%q", msg, message)
	}
	if sig != signature {
		t.Fatalf("signature mismatch: got=%q expected=%q", sig, signature)
	}
}

func TestUnpackConsent_LegacyPlainPayload(t *testing.T) {
	t.Parallel()

	// If the decrypted payload is not valid JSON (e.g. legacy plain
	// message), unpackConsent returns the raw string as message with
	// empty signature.
	raw := []byte("just a raw message")
	msg, sig := UnpackConsent(raw)
	if msg != "just a raw message" {
		t.Fatalf("legacy unpack mismatch: got=%q", msg)
	}
	if sig != "" {
		t.Fatalf("expected empty sig for legacy, got=%q", sig)
	}
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
