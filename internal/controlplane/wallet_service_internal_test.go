package controlplane

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func TestBuildWalletAuthMessage(t *testing.T) {
	t.Parallel()

	issued := time.Unix(10, 0).UTC()
	expires := issued.Add(5 * time.Minute)
	msg := buildWalletAuthMessage("lesser.host", "0xAbC", 1, "nonce", "alice", issued, expires)

	if !strings.Contains(msg, "lesser.host wants you to sign in") {
		t.Fatalf("expected domain in message: %q", msg)
	}
	if !strings.Contains(msg, "\n0xabc\n") {
		t.Fatalf("expected normalized address in message: %q", msg)
	}
	if !strings.Contains(msg, "Chain ID: 1") {
		t.Fatalf("expected chain id in message: %q", msg)
	}
	if !strings.Contains(msg, "Nonce: nonce") {
		t.Fatalf("expected nonce in message: %q", msg)
	}
}

func TestVerifyEthereumSignature(t *testing.T) {
	t.Parallel()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()

	message := "hello"
	hash := crypto.Keccak256Hash([]byte("\x19Ethereum Signed Message:\n5hello"))

	sig, err := crypto.Sign(hash.Bytes(), key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	sigHex := "0x" + hex.EncodeToString(sig)
	if err := verifyEthereumSignature(addr, message, sigHex); err != nil {
		t.Fatalf("expected signature valid, got %v", err)
	}

	// Also accept 27/28 style signatures.
	sig2 := append([]byte(nil), sig...)
	sig2[64] += 27
	sigHex2 := "0x" + hex.EncodeToString(sig2)
	if err := verifyEthereumSignature(addr, message, sigHex2); err != nil {
		t.Fatalf("expected signature (27/28) valid, got %v", err)
	}

	if err := verifyEthereumSignature("0x0000000000000000000000000000000000000000", message, sigHex); err == nil {
		t.Fatalf("expected mismatch error")
	}
	if err := verifyEthereumSignature(addr, message, "0xnope"); err == nil {
		t.Fatalf("expected decode error")
	}
	if err := verifyEthereumSignature(addr, message, "0x"+hex.EncodeToString(sig[:64])); err == nil {
		t.Fatalf("expected length error")
	}
}

func TestParseWalletLoginRequest(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, err := parseWalletLoginRequest(ctx); err == nil {
		t.Fatalf("expected error for missing fields")
	}

	ctx.Request.Body = []byte(`{"challengeId":"c","address":"a","signature":"s","message":"m"}`)
	if _, err := parseWalletLoginRequest(ctx); err != nil {
		t.Fatalf("parseWalletLoginRequest: %v", err)
	}
}
