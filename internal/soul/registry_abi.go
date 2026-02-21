package soul

import (
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// SoulRegistryABI is the minimal ABI required for mint operations.
const SoulRegistryABI = `[
  {"type":"function","name":"mintSoul","stateMutability":"nonpayable","inputs":[{"name":"to","type":"address"},{"name":"agentId","type":"uint256"},{"name":"metaURI","type":"string"}],"outputs":[]}
]`

var soulRegistryParsedABI = mustParseABI(SoulRegistryABI)

func mustParseABI(raw string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return a
}
