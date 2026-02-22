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
				return EncodeMintSoulCall(to, nil, "https://example.com/meta")
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
