package soul

import (
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// SoulRegistryABI is the minimal ABI required for registry operations performed by the control plane.
const SoulRegistryABI = `[
  {"type":"function","name":"mintSoul","stateMutability":"nonpayable","inputs":[{"name":"to","type":"address"},{"name":"agentId","type":"uint256"},{"name":"metaURI","type":"string"}],"outputs":[]},
  {"type":"function","name":"rotateWallet","stateMutability":"nonpayable","inputs":[{"name":"agentId","type":"uint256"},{"name":"newWallet","type":"address"},{"name":"nonce","type":"uint256"},{"name":"deadline","type":"uint256"},{"name":"currentSig","type":"bytes"},{"name":"newSig","type":"bytes"}],"outputs":[]},
  {"type":"function","name":"getAgentWallet","stateMutability":"view","inputs":[{"name":"agentId","type":"uint256"}],"outputs":[{"name":"","type":"address"}]},
  {"type":"function","name":"agentNonces","stateMutability":"view","inputs":[{"name":"","type":"uint256"}],"outputs":[{"name":"","type":"uint256"}]}
]`

var soulRegistryParsedABI = mustParseABI(SoulRegistryABI)

func mustParseABI(raw string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return a
}
