package provisionworker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const consentEncryptionVersionPrefix = "enc:v1:"

// ErrConsentEncryptionKeyRequired is returned when the consent encryption key
// is empty or missing.
var ErrConsentEncryptionKeyRequired = errors.New("consent encryption key is required")

// EncryptConsent encrypts a plaintext consent payload using AES-256-GCM with
// the supplied 32-byte key. Returns a base64-encoded ciphertext prefixed with
// "enc:v1:" so the decryption path can distinguish encrypted values from
// legacy plaintext consent stored in older job records.
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
// Returns the trimmed plaintext on success.
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

	return strings.TrimSpace(string(plaintext)), nil
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
