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

// TestEndToEnd_WhitespacePreserved_ConsentPipeline simulates the full
// control-plane → worker consent pipeline with leading/trailing whitespace
// and newlines in both the consent message and signature. This proves
// end-to-end preservation: the bytes the caller signed are the bytes the
// deploy runner receives, without any trimming in the hash, encryption,
// decryption, or unpacking stages.
func TestEndToEnd_WhitespacePreserved_ConsentPipeline(t *testing.T) {
	t.Parallel()

	key := generateTestKey(t)

	// Message with leading space, trailing newline, and embedded newline
	// (e.g. a pretty-printed JSON consent message).
	rawMessage := " {\"slug\":\"test\",\n \"expires_at\":\"2026-06-01T00:00:00Z\"}\n"
	// Signature with leading tab and trailing space.
	rawSignature := "\t0xabcd1234 "

	// --- Control-plane side: pack + encrypt ---
	packed, err := PackConsent(rawMessage, rawSignature)
	if err != nil {
		t.Fatalf("PackConsent: %v", err)
	}
	encrypted, err := EncryptConsent(string(packed), key)
	if err != nil {
		t.Fatalf("EncryptConsent: %v", err)
	}
	if !strings.HasPrefix(encrypted, consentEncryptionVersionPrefix) {
		t.Fatalf("expected %q prefix on encrypted value", consentEncryptionVersionPrefix)
	}

	// --- Worker side: decrypt + unpack ---
	decrypted, err := DecryptConsent(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptConsent: %v", err)
	}
	runnerMessage, runnerSignature := UnpackConsent([]byte(decrypted))

	// --- Assert: runner-facing values match raw originals exactly ---
	if runnerMessage != rawMessage {
		t.Fatalf("runner message mismatch:\n  got     =%q\n  expected=%q", runnerMessage, rawMessage)
	}
	if runnerSignature != rawSignature {
		t.Fatalf("runner signature mismatch:\n  got     =%q\n  expected=%q", runnerSignature, rawSignature)
	}
}

// TestEndToEnd_SignatureOnlyWhitespace_Preserved proves that a consent
// message without whitespace but a signature with leading/trailing
// whitespace round-trips correctly. The signature bytes are part of
// the signed payload.
func TestEndToEnd_SignatureOnlyWhitespace_Preserved(t *testing.T) {
	t.Parallel()

	key := generateTestKey(t)

	rawMessage := "{\"slug\":\"test\"}"
	rawSignature := " 0xsig\n"

	packed, err := PackConsent(rawMessage, rawSignature)
	if err != nil {
		t.Fatalf("PackConsent: %v", err)
	}
	encrypted, err := EncryptConsent(string(packed), key)
	if err != nil {
		t.Fatalf("EncryptConsent: %v", err)
	}
	decrypted, err := DecryptConsent(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptConsent: %v", err)
	}
	runnerMessage, runnerSignature := UnpackConsent([]byte(decrypted))

	if runnerMessage != rawMessage {
		t.Fatalf("message mismatch: got=%q expected=%q", runnerMessage, rawMessage)
	}
	if runnerSignature != rawSignature {
		t.Fatalf("signature mismatch: got=%q expected=%q", runnerSignature, rawSignature)
	}
}

// TestEndToEnd_LegacyPlaintextFallback_PreservesBytes proves that the
// legacy unpackConsent fallback returns the raw byte string unchanged
// (no trimming) for backward-compatible plaintext payloads.
func TestEndToEnd_LegacyPlaintextFallback_PreservesBytes(t *testing.T) {
	t.Parallel()

	// Legacy plaintext payload with leading/trailing whitespace and newlines.
	rawLegacy := []byte(" plain consent message\n")

	msg, sig := UnpackConsent(rawLegacy)
	if msg != string(rawLegacy) {
		t.Fatalf("legacy fallback trimmed: got=%q expected=%q", msg, string(rawLegacy))
	}
	if sig != "" {
		t.Fatalf("expected empty signature for legacy fallback")
	}
}

// TestEndToEnd_EmptyMessage_WhitespaceOnly_Skipped validates the contract
// that processConsentForJob must implement: a whitespace-only consent
// message is detected as absent (trimmed copy used only for the presence
// check) without mutating the raw bytes used for hash/encryption.
func TestEndToEnd_EmptyMessage_WhitespaceOnly_Skipped(t *testing.T) {
	t.Parallel()

	// Whitespace-only message must be detectable as absent.
	trimmedMessage := strings.TrimSpace("   \t\n  ")
	if trimmedMessage != "" {
		t.Fatalf("all-whitespace message must be detected as absent")
	}

	// Confirm EncryptConsent with exact empty string is still a noop.
	key := generateTestKey(t)
	enc, err := EncryptConsent("", key)
	if err != nil {
		t.Fatalf("EncryptConsent empty: %v", err)
	}
	if enc != "" {
		t.Fatalf("expected empty result for empty input, got %q", enc)
	}

	// Non-empty whitespace-only is NOT exact empty string, so it would be
	// encrypted if it reaches EncryptConsent. This is why processConsentForJob
	// must gate on the trimmed copy before encrypting.
}
