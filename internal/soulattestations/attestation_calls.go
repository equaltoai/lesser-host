package soulattestations

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

func EncodePublishRootCall(root common.Hash, blockRef int64, count int) ([]byte, error) {
	if blockRef < 0 {
		return nil, fmt.Errorf("blockRef must be >= 0")
	}
	if count < 0 {
		return nil, fmt.Errorf("count must be >= 0")
	}

	return rootAttestationParsedABI.Pack(
		"publishRoot",
		root,
		new(big.Int).SetInt64(blockRef),
		new(big.Int).SetInt64(int64(count)),
	)
}
