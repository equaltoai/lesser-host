package soul

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSignSelfMintAttestation_Deterministic(t *testing.T) {
	t.Parallel()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privateKeyHex := common.Bytes2Hex(crypto.FromECDSA(key))

	contract := common.HexToAddress("0x0000000000000000000000000000000000000042")
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	principal := common.HexToAddress("0x0000000000000000000000000000000000000002")
	submitter := common.HexToAddress("0x0000000000000000000000000000000000000003")
	agentID := big.NewInt(123)
	metaURI := "https://example.com/meta.json"
	avatarStyle := uint8(0)
	deadline := big.NewInt(9999999999)

	sig1, err := SignSelfMintAttestation(privateKeyHex, 84532, contract, to, agentID, metaURI, avatarStyle, principal, deadline, submitter)
	if err != nil {
		t.Fatalf("SignSelfMintAttestation: %v", err)
	}
	if len(sig1) != 65 {
		t.Fatalf("expected 65-byte signature, got %d", len(sig1))
	}
	if sig1[64] != 27 && sig1[64] != 28 {
		t.Fatalf("expected v=27 or v=28, got %d", sig1[64])
	}

	sig2, err := SignSelfMintAttestation(privateKeyHex, 84532, contract, to, agentID, metaURI, avatarStyle, principal, deadline, submitter)
	if err != nil {
		t.Fatalf("SignSelfMintAttestation (2nd call): %v", err)
	}
	if !equal(sig1, sig2) {
		t.Fatalf("signatures should be deterministic")
	}
}

func TestSignSelfMintAttestation_DifferentInputsChangeSig(t *testing.T) {
	t.Parallel()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privateKeyHex := common.Bytes2Hex(crypto.FromECDSA(key))

	contract := common.HexToAddress("0x0000000000000000000000000000000000000042")
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	principal := common.HexToAddress("0x0000000000000000000000000000000000000002")
	submitter := common.HexToAddress("0x0000000000000000000000000000000000000003")
	deadline := big.NewInt(9999999999)

	sig1, err := SignSelfMintAttestation(privateKeyHex, 84532, contract, to, big.NewInt(1), "ipfs://a", 0, principal, deadline, submitter)
	if err != nil {
		t.Fatalf("sig1: %v", err)
	}

	sig2, err := SignSelfMintAttestation(privateKeyHex, 84532, contract, to, big.NewInt(2), "ipfs://a", 0, principal, deadline, submitter)
	if err != nil {
		t.Fatalf("sig2: %v", err)
	}
	if equal(sig1, sig2) {
		t.Fatal("different agentIDs should produce different signatures")
	}

	sig3, err := SignSelfMintAttestation(privateKeyHex, 84532, contract, to, big.NewInt(1), "ipfs://b", 0, principal, deadline, submitter)
	if err != nil {
		t.Fatalf("sig3: %v", err)
	}
	if equal(sig1, sig3) {
		t.Fatal("different metaURIs should produce different signatures")
	}
}
