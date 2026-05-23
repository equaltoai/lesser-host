package provisionworker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const consentEncryptionVersionPrefix = "enc:v1:"

// ErrConsentEncryptionKeyRequired is returned when the consent encryption key
// is empty or missing.
var ErrConsentEncryptionKeyRequired = errors.New("consent encryption key is required")

// consentCryptoPacket is a structured, non-ambiguous container for the consent
// message and signature to be encrypted together. Using JSON avoids the unsafe
// newline-delimited packing that could corrupt message/signature separation
// when the consent message contains newlines (e.g. pretty-printed JSON).
type consentCryptoPacket struct {
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

// packConsent serializes a consent message and signature into a JSON byte
// slice suitable for encryption. Empty signature is preserved (omitted in
// JSON), and the exact bytes of message are preserved (no trimming).
func packConsent(message, signature string) ([]byte, error) {
	p := consentCryptoPacket{
		Message:   message,
		Signature: signature,
	}
	return json.Marshal(p)
}

// unpackConsent deserializes an encrypted/decrypted consent payload JSON
// back into message and signature. If the payload cannot be parsed as a
// consentCryptoPacket, it is treated as a plain message with no signature
// for backward compatibility.
func unpackConsent(raw []byte) (message string, signature string) {
	var p consentCryptoPacket
	if err := json.Unmarshal(raw, &p); err != nil {
		return strings.TrimSpace(string(raw)), ""
	}
	// Return the exact bytes from the packet — no TrimSpace.
	return p.Message, p.Signature
}

// EncryptConsent encrypts a plaintext consent payload using AES-256-GCM with
// the supplied 32-byte key. Returns a base64-encoded ciphertext prefixed with
// "enc:v1:" so the decryption path can distinguish encrypted values from
// legacy plaintext consent stored in older job records.
//
// The plaintext is encrypted as-is (no trimming) so that signed consent bytes
// round-trip exactly.
func EncryptConsent(plaintext string, key []byte) (string, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", nil
	}
	if len(key) != 32 {
		return "", fmt.Errorf("%w: key must be 32 bytes", ErrConsentEncryptionKeyRequired)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return consentEncryptionVersionPrefix + base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

// DecryptConsent decrypts a consent value that was encrypted with
// EncryptConsent. If the value does not have the "enc:v1:" prefix, it is
// returned as-is for backward compatibility with legacy plaintext consent.
// Returns the exact decrypted bytes on success (no trimming).
func DecryptConsent(encoded string, key []byte) (string, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return "", nil
	}

	if !strings.HasPrefix(encoded, consentEncryptionVersionPrefix) {
		// Legacy plaintext: return as-is.
		return encoded, nil
	}

	b64 := strings.TrimPrefix(encoded, consentEncryptionVersionPrefix)
	if b64 == "" {
		return "", nil
	}

	ciphertext, err := base64.RawStdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("consent decrypt: base64: %w", err)
	}

	if len(key) != 32 {
		return "", fmt.Errorf("%w: key must be 32 bytes", ErrConsentEncryptionKeyRequired)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("consent decrypt: ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("consent decrypt: %w", err)
	}

	// Return exact bytes — no trimming. Callers that need
	// structured message/signature use unpackConsent instead.
	return string(plaintext), nil
}

// PackConsent serializes a consent message and signature into a JSON byte
// slice suitable for encryption. The message and signature bytes are
// preserved exactly — no trimming is applied by this function.
func PackConsent(message, signature string) ([]byte, error) {
	return packConsent(message, signature)
}

// UnpackConsent deserializes an encrypted-then-decrypted consent payload JSON
// back into message and signature. If the payload cannot be parsed as a
// consentCryptoPacket, it is treated as a plain message with no signature
// for backward compatibility with legacy non-structured payloads.
func UnpackConsent(raw []byte) (message string, signature string) {
	return unpackConsent(raw)
}

// ConsentEncryptionKeyHex decodes a hex-encoded consent encryption key string
// into a 32-byte key suitable for EncryptConsent / DecryptConsent. Returns an
// error if the key is not exactly 64 hex characters (32 bytes).
func ConsentEncryptionKeyHex(keyHex string) ([]byte, error) {
	keyHex = strings.TrimSpace(keyHex)
	if keyHex == "" {
		return nil, ErrConsentEncryptionKeyRequired
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("consent encryption key: hex decode: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("consent encryption key: decoded key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}
