package soulattestations

import (
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// RootAttestationABI is the minimal ABI required for ReputationAttestation/ValidationAttestation root publishing.
const RootAttestationABI = `[
  {"type":"function","name":"publishRoot","stateMutability":"nonpayable","inputs":[{"name":"root","type":"bytes32"},{"name":"blockRef","type":"uint256"},{"name":"count","type":"uint256"}],"outputs":[]},
  {"type":"function","name":"latestRoot","stateMutability":"view","inputs":[],"outputs":[{"name":"root","type":"bytes32"},{"name":"blockRef","type":"uint256"},{"name":"count","type":"uint256"},{"name":"timestamp","type":"uint256"}]}
]`

var rootAttestationParsedABI = mustParseABI(RootAttestationABI)

func mustParseABI(raw string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return a
}
