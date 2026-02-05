package tips

import (
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// HostIDFromDomain returns the on-chain hostId (bytes32) for a normalized domain.
//
// The canonical hostId is keccak256(utf8(normalizedDomain)).
func HostIDFromDomain(normalizedDomain string) common.Hash {
	normalizedDomain = strings.ToLower(strings.TrimSpace(normalizedDomain))
	return crypto.Keccak256Hash([]byte(normalizedDomain))
}
