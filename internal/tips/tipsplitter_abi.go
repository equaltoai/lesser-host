package tips

import (
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// TipSplitterABI is the minimal ABI required for host registry + allowlist operations.
const TipSplitterABI = `[
  {"type":"function","name":"registerHost","stateMutability":"nonpayable","inputs":[{"name":"hostId","type":"bytes32"},{"name":"wallet","type":"address"},{"name":"feeBps","type":"uint16"}],"outputs":[]},
  {"type":"function","name":"updateHost","stateMutability":"nonpayable","inputs":[{"name":"hostId","type":"bytes32"},{"name":"wallet","type":"address"},{"name":"feeBps","type":"uint16"}],"outputs":[]},
  {"type":"function","name":"setHostActive","stateMutability":"nonpayable","inputs":[{"name":"hostId","type":"bytes32"},{"name":"active","type":"bool"}],"outputs":[]},
  {"type":"function","name":"setTokenAllowed","stateMutability":"nonpayable","inputs":[{"name":"token","type":"address"},{"name":"allowed","type":"bool"}],"outputs":[]},
  {"type":"function","name":"hosts","stateMutability":"view","inputs":[{"name":"","type":"bytes32"}],"outputs":[{"name":"wallet","type":"address"},{"name":"feeBps","type":"uint16"},{"name":"isActive","type":"bool"}]},
  {"type":"function","name":"allowedTokens","stateMutability":"view","inputs":[{"name":"","type":"address"}],"outputs":[{"name":"","type":"bool"}]}
]`

var tipSplitterParsedABI = mustParseABI(TipSplitterABI)

func mustParseABI(raw string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return a
}
