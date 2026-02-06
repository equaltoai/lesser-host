package tips

import (
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestHostIDFromDomain_NormalizesAndHashes(t *testing.T) {
	t.Parallel()

	got1 := HostIDFromDomain(" Example.COM ")
	got2 := HostIDFromDomain("example.com")
	if got1 != got2 {
		t.Fatalf("expected normalized hashes equal: %s != %s", got1.Hex(), got2.Hex())
	}

	want := crypto.Keccak256Hash([]byte("example.com"))
	if got2 != want {
		t.Fatalf("unexpected hash: got %s want %s", got2.Hex(), want.Hex())
	}
}

