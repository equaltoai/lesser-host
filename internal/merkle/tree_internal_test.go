package merkle

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestBuildAndVerifyProofs(t *testing.T) {
	t.Parallel()

	leaves := []string{"a", "b", "c"}
	hashes := make([]common.Hash, 0, len(leaves))
	for _, s := range leaves {
		hashes = append(hashes, crypto.Keccak256Hash([]byte(s)))
	}

	tree, err := Build(hashes)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := tree.Root()
	if root == (common.Hash{}) {
		t.Fatalf("expected non-zero root")
	}

	for i, leaf := range hashes {
		proof, err := tree.Proof(i)
		if err != nil {
			t.Fatalf("Proof(%d): %v", i, err)
		}
		if !Verify(leaf, i, proof, root) {
			t.Fatalf("expected proof to verify for index %d", i)
		}
	}
}
