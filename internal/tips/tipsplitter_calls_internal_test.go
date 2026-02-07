package tips

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestToUint16_SupportedAndErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      any
		want    uint16
		wantErr bool
	}{
		{name: "uint8", in: uint8(7), want: 7},
		{name: "uint16", in: uint16(9), want: 9},
		{name: "uint32", in: uint32(10), want: 10},
		{name: "uint64", in: uint64(11), want: 11},
		{name: "int", in: int(12), want: 12},
		{name: "big_int", in: big.NewInt(13), want: 13},

		{name: "uint32_overflow", in: uint32(70000), wantErr: true},
		{name: "int_negative", in: int(-1), wantErr: true},
		{name: "big_nil", in: (*big.Int)(nil), wantErr: true},
		{name: "big_overflow", in: new(big.Int).SetUint64(1 << 20), wantErr: true},
		{name: "unsupported", in: "nope", wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := toUint16(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestDecodeGetHostResult_ValidationAndSuccess(t *testing.T) {
	t.Parallel()

	if _, err := DecodeGetHostResult(nil); err == nil {
		t.Fatalf("expected error for empty return data")
	}
	if _, err := DecodeGetHostResult([]byte{0x01}); err == nil {
		t.Fatalf("expected error for invalid ABI return data")
	}

	wallet := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	feeBps := uint16(123)
	active := true

	ret, err := tipSplitterParsedABI.Methods["hosts"].Outputs.Pack(wallet, feeBps, active)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	got, err := DecodeGetHostResult(ret)
	if err != nil {
		t.Fatalf("DecodeGetHostResult err: %v", err)
	}
	if got.Wallet != wallet || got.FeeBps != 123 || got.IsActive != active {
		t.Fatalf("unexpected decoded host: %#v", got)
	}
}

func TestDecodeIsTokenAllowedResult(t *testing.T) {
	t.Parallel()

	ret, err := tipSplitterParsedABI.Methods["allowedTokens"].Outputs.Pack(true)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	got, err := DecodeIsTokenAllowedResult(ret)
	if err != nil {
		t.Fatalf("DecodeIsTokenAllowedResult err: %v", err)
	}
	if !got {
		t.Fatalf("expected allowed=true")
	}
}
