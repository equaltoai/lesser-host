package soul

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestMustParseABI_PanicsOnInvalidABI(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()

	_ = mustParseABI("{")
}

func TestSoulRegistryCalls_EncodeMethodIDs(t *testing.T) {
	t.Parallel()

	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	newWallet := common.HexToAddress("0x0000000000000000000000000000000000000002")

	cases := []struct {
		name   string
		method string
		call   func() ([]byte, error)
	}{
		{
			name:   "mintSoul",
			method: "mintSoul",
			call: func() ([]byte, error) {
				return EncodeMintSoulCall(to, nil, "https://example.com/meta", 0, big.NewInt(99), []byte("sig"))
			},
		},
		{
			name:   "mintSoulOwner",
			method: "mintSoulOwner",
			call: func() ([]byte, error) {
				return EncodeMintSoulOwnerCall(to, nil, "https://example.com/meta", 0)
			},
		},
		{
			name:   "burnSoul",
			method: "burnSoul",
			call: func() ([]byte, error) {
				return EncodeBurnSoulCall(nil)
			},
		},
		{
			name:   "rotateWallet",
			method: "rotateWallet",
			call: func() ([]byte, error) {
				return EncodeRotateWalletCall(nil, newWallet, nil, nil, []byte("a"), []byte("b"))
			},
		},
		{
			name:   "getAgentWallet",
			method: "getAgentWallet",
			call: func() ([]byte, error) {
				return EncodeGetAgentWalletCall(nil)
			},
		},
		{
			name:   "agentNonces",
			method: "agentNonces",
			call: func() ([]byte, error) {
				return EncodeAgentNoncesCall(nil)
			},
		},
		{
			name:   "transferCount",
			method: "transferCount",
			call: func() ([]byte, error) {
				return EncodeTransferCountCall(nil)
			},
		},
		{
			name:   "lastTransferredAt",
			method: "lastTransferredAt",
			call: func() ([]byte, error) {
				return EncodeLastTransferredAtCall(nil)
			},
		},
		{
			name:   "mintFee",
			method: "mintFee",
			call: func() ([]byte, error) {
				return EncodeMintFeeCall()
			},
		},
		{
			name:   "mintSigner",
			method: "mintSigner",
			call: func() ([]byte, error) {
				return EncodeMintSignerCall()
			},
		},
	}

	for _, c := range cases {
		got, err := c.call()
		if err != nil {
			t.Fatalf("%s: unexpected err: %v", c.name, err)
		}
		if len(got) < 4 {
			t.Fatalf("%s: expected call data, got len=%d", c.name, len(got))
		}
		wantID := soulRegistryParsedABI.Methods[c.method].ID
		if !bytes.Equal(got[:4], wantID) {
			t.Fatalf("%s: method id mismatch got=%x want=%x", c.name, got[:4], wantID)
		}
	}
}

func TestDecodeGetAgentWalletResult_RoundTrip(t *testing.T) {
	t.Parallel()

	want := common.HexToAddress("0x0000000000000000000000000000000000000003")
	ret, err := soulRegistryParsedABI.Methods["getAgentWallet"].Outputs.Pack(want)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	got, err := DecodeGetAgentWalletResult(ret)
	if err != nil {
		t.Fatalf("DecodeGetAgentWalletResult: %v", err)
	}
	if got != want {
		t.Fatalf("got=%s want=%s", got.Hex(), want.Hex())
	}
}

func TestDecodeGetAgentWalletResult_InvalidBytes(t *testing.T) {
	t.Parallel()

	if _, err := DecodeGetAgentWalletResult([]byte{0x01}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDecodeAgentNoncesResult_RoundTrip(t *testing.T) {
	t.Parallel()

	want := big.NewInt(42)
	ret, err := soulRegistryParsedABI.Methods["agentNonces"].Outputs.Pack(want)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	got, err := DecodeAgentNoncesResult(ret)
	if err != nil {
		t.Fatalf("DecodeAgentNoncesResult: %v", err)
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestDecodeAgentNoncesResult_InvalidBytes(t *testing.T) {
	t.Parallel()

	if _, err := DecodeAgentNoncesResult([]byte{0x01}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDecodeTransferCountResult_RoundTrip(t *testing.T) {
	t.Parallel()

	want := big.NewInt(7)
	ret, err := soulRegistryParsedABI.Methods["transferCount"].Outputs.Pack(want)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	got, err := DecodeTransferCountResult(ret)
	if err != nil {
		t.Fatalf("DecodeTransferCountResult: %v", err)
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestDecodeTransferCountResult_InvalidBytes(t *testing.T) {
	t.Parallel()

	if _, err := DecodeTransferCountResult([]byte{0x01}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDecodeLastTransferredAtResult_RoundTrip(t *testing.T) {
	t.Parallel()

	want := big.NewInt(1700000000)
	ret, err := soulRegistryParsedABI.Methods["lastTransferredAt"].Outputs.Pack(want)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	got, err := DecodeLastTransferredAtResult(ret)
	if err != nil {
		t.Fatalf("DecodeLastTransferredAtResult: %v", err)
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestDecodeLastTransferredAtResult_InvalidBytes(t *testing.T) {
	t.Parallel()

	if _, err := DecodeLastTransferredAtResult([]byte{0x01}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDecodeMintFeeResult_RoundTrip(t *testing.T) {
	t.Parallel()

	want := big.NewInt(500000000000000) // 0.0005 ETH
	ret, err := soulRegistryParsedABI.Methods["mintFee"].Outputs.Pack(want)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	got, err := DecodeMintFeeResult(ret)
	if err != nil {
		t.Fatalf("DecodeMintFeeResult: %v", err)
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestDecodeMintFeeResult_InvalidBytes(t *testing.T) {
	t.Parallel()

	if _, err := DecodeMintFeeResult([]byte{0x01}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDecodeMintSignerResult_RoundTrip(t *testing.T) {
	t.Parallel()

	want := common.HexToAddress("0x0000000000000000000000000000000000000099")
	ret, err := soulRegistryParsedABI.Methods["mintSigner"].Outputs.Pack(want)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	got, err := DecodeMintSignerResult(ret)
	if err != nil {
		t.Fatalf("DecodeMintSignerResult: %v", err)
	}
	if got != want {
		t.Fatalf("got=%s want=%s", got.Hex(), want.Hex())
	}
}

func TestDecodeMintSignerResult_InvalidBytes(t *testing.T) {
	t.Parallel()

	if _, err := DecodeMintSignerResult([]byte{0x01}); err == nil {
		t.Fatalf("expected error")
	}
}
