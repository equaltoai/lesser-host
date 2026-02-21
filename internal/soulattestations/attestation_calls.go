package soulattestations

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

func EncodePublishRootCall(root common.Hash, blockRef uint64, count uint64) ([]byte, error) {
	return rootAttestationParsedABI.Pack(
		"publishRoot",
		root,
		new(big.Int).SetUint64(blockRef),
		new(big.Int).SetUint64(count),
	)
}
