package soulattestations

import (
	"bytes"
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

func TestEncodePublishRootCall_PacksMethodID(t *testing.T) {
	t.Parallel()

	root := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000042")
	data, err := EncodePublishRootCall(root, 10, 2)
	if err != nil {
		t.Fatalf("EncodePublishRootCall: %v", err)
	}
	if len(data) < 4 {
		t.Fatalf("expected call data, got len=%d", len(data))
	}
	wantID := rootAttestationParsedABI.Methods["publishRoot"].ID
	if !bytes.Equal(data[:4], wantID) {
		t.Fatalf("method id mismatch got=%x want=%x", data[:4], wantID)
	}
}
