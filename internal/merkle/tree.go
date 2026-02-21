package merkle

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type Tree struct {
	Levels [][]common.Hash // Levels[0] = leaves, Levels[len-1][0] = root
}

func Build(leaves []common.Hash) (*Tree, error) {
	if len(leaves) == 0 {
		return &Tree{Levels: [][]common.Hash{{}}}, nil
	}

	level0 := make([]common.Hash, 0, len(leaves))
	level0 = append(level0, leaves...)
	levels := [][]common.Hash{level0}

	cur := level0
	for len(cur) > 1 {
		next := make([]common.Hash, 0, (len(cur)+1)/2)
		for i := 0; i < len(cur); i += 2 {
			left := cur[i]
			right := left
			if i+1 < len(cur) {
				right = cur[i+1]
			}
			next = append(next, parentHash(left, right))
		}
		levels = append(levels, next)
		cur = next
	}

	return &Tree{Levels: levels}, nil
}

func (t *Tree) Root() common.Hash {
	if t == nil || len(t.Levels) == 0 {
		return common.Hash{}
	}
	last := t.Levels[len(t.Levels)-1]
	if len(last) == 0 {
		return common.Hash{}
	}
	return last[0]
}

func (t *Tree) Proof(index int) ([]common.Hash, error) {
	if t == nil || len(t.Levels) == 0 {
		return nil, fmt.Errorf("tree is nil")
	}
	if index < 0 {
		return nil, fmt.Errorf("invalid index")
	}
	if len(t.Levels[0]) == 0 {
		return nil, fmt.Errorf("empty tree")
	}
	if index >= len(t.Levels[0]) {
		return nil, fmt.Errorf("index out of range")
	}

	proof := make([]common.Hash, 0, len(t.Levels)-1)
	idx := index

	for level := 0; level < len(t.Levels)-1; level++ {
		nodes := t.Levels[level]
		sib := idx ^ 1
		if sib >= len(nodes) {
			sib = idx
		}
		proof = append(proof, nodes[sib])
		idx /= 2
	}

	return proof, nil
}

func Verify(leaf common.Hash, index int, proof []common.Hash, root common.Hash) bool {
	if index < 0 {
		return false
	}
	hash := leaf
	idx := index
	for _, sib := range proof {
		if idx%2 == 0 {
			hash = parentHash(hash, sib)
		} else {
			hash = parentHash(sib, hash)
		}
		idx /= 2
	}
	return hash == root
}

func parentHash(left common.Hash, right common.Hash) common.Hash {
	var buf [64]byte
	copy(buf[0:32], left.Bytes())
	copy(buf[32:64], right.Bytes())
	return crypto.Keccak256Hash(buf[:])
}
