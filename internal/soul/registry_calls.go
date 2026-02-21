package soul

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// EncodeMintSoulCall returns ABI-encoded call data for SoulRegistry.mintSoul(to, agentId, metaURI).
func EncodeMintSoulCall(to common.Address, agentID *big.Int, metaURI string) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("mintSoul", to, agentID, metaURI)
}
