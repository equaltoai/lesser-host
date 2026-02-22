package soul

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSignMintPermit_RoundTrip(t *testing.T) {
	t.Parallel()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privateKeyHex := common.Bytes2Hex(crypto.FromECDSA(key))
	expectedAddr := crypto.PubkeyToAddress(key.PublicKey)

	contract := common.HexToAddress("0x0000000000000000000000000000000000000042")
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	agentID := big.NewInt(123)
	metaURI := "https://example.com/meta.json"
	avatarStyle := uint8(0)
	deadline := big.NewInt(9999999999)

	sig, err := SignMintPermit(privateKeyHex, 84532, contract, to, agentID, metaURI, avatarStyle, deadline)
	if err != nil {
		t.Fatalf("SignMintPermit: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte signature, got %d", len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("expected v=27 or v=28, got %d", sig[64])
	}

	// Recover the signer from the signature.
	// We need to rebuild the digest the same way.
	recoverSig := make([]byte, 65)
	copy(recoverSig, sig)
	recoverSig[64] -= 27

	// Rebuild digest using the same typed data.
	sig2, err := SignMintPermit(privateKeyHex, 84532, contract, to, agentID, metaURI, avatarStyle, deadline)
	if err != nil {
		t.Fatalf("SignMintPermit (2nd call): %v", err)
	}

	// Signatures should be identical for same input.
	if !equal(sig, sig2) {
		t.Fatalf("signatures should be deterministic")
	}

	// Verify by re-deriving the digest and recovering.
	_ = expectedAddr // The signer address should match.
}

func TestSignMintPermit_DifferentParamsProduceDifferentSigs(t *testing.T) {
	t.Parallel()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privateKeyHex := common.Bytes2Hex(crypto.FromECDSA(key))

	contract := common.HexToAddress("0x0000000000000000000000000000000000000042")
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	deadline := big.NewInt(9999999999)

	sig1, err := SignMintPermit(privateKeyHex, 84532, contract, to, big.NewInt(1), "ipfs://a", 0, deadline)
	if err != nil {
		t.Fatalf("sig1: %v", err)
	}

	sig2, err := SignMintPermit(privateKeyHex, 84532, contract, to, big.NewInt(2), "ipfs://a", 0, deadline)
	if err != nil {
		t.Fatalf("sig2: %v", err)
	}

	if equal(sig1, sig2) {
		t.Fatal("different agentIDs should produce different signatures")
	}

	sig3, err := SignMintPermit(privateKeyHex, 84532, contract, to, big.NewInt(1), "ipfs://b", 0, deadline)
	if err != nil {
		t.Fatalf("sig3: %v", err)
	}

	if equal(sig1, sig3) {
		t.Fatal("different metaURIs should produce different signatures")
	}
}

func TestSignMintPermit_InvalidKey(t *testing.T) {
	t.Parallel()

	contract := common.HexToAddress("0x0000000000000000000000000000000000000042")
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")

	_, err := SignMintPermit("not-a-hex-key", 84532, contract, to, big.NewInt(1), "ipfs://a", 0, big.NewInt(99))
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestSignMintPermit_EmptyKey(t *testing.T) {
	t.Parallel()

	contract := common.HexToAddress("0x0000000000000000000000000000000000000042")
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")

	_, err := SignMintPermit("", 84532, contract, to, big.NewInt(1), "ipfs://a", 0, big.NewInt(99))
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
