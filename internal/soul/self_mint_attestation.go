package soul

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// SignSelfMintAttestation produces an EIP-712 signature for the SelfMintAttestation typed data.
// The returned bytes are a 65-byte signature with v = 27 or 28.
//
// This must match contracts/contracts/SoulRegistry.sol:
// SelfMintAttestation(address to,uint256 agentId,string metaURI,uint8 avatarStyle,address principal,uint256 deadline,address submitter)
func SignSelfMintAttestation(
	privateKeyHex string,
	chainID int64,
	contractAddress common.Address,
	to common.Address,
	agentID *big.Int,
	metaURI string,
	avatarStyle uint8,
	principal common.Address,
	deadline *big.Int,
	submitter common.Address,
) ([]byte, error) {
	privateKeyHex = strings.TrimPrefix(strings.TrimSpace(privateKeyHex), "0x")
	if privateKeyHex == "" {
		return nil, fmt.Errorf("self mint attestation: empty private key")
	}

	key, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("self mint attestation: invalid private key: %w", err)
	}

	if agentID == nil {
		agentID = new(big.Int)
	}
	if deadline == nil {
		deadline = new(big.Int)
	}

	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"SelfMintAttestation": {
				{Name: "to", Type: "address"},
				{Name: "agentId", Type: "uint256"},
				{Name: "metaURI", Type: "string"},
				{Name: "avatarStyle", Type: "uint8"},
				{Name: "principal", Type: "address"},
				{Name: "deadline", Type: "uint256"},
				{Name: "submitter", Type: "address"},
			},
		},
		PrimaryType: "SelfMintAttestation",
		Domain: apitypes.TypedDataDomain{
			Name:              "LesserSoul",
			Version:           "1",
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: strings.ToLower(contractAddress.Hex()),
		},
		Message: apitypes.TypedDataMessage{
			"to":          strings.ToLower(to.Hex()),
			"agentId":     agentID.String(),
			"metaURI":     metaURI,
			"avatarStyle": fmt.Sprintf("%d", avatarStyle),
			"principal":   strings.ToLower(principal.Hex()),
			"deadline":    deadline.String(),
			"submitter":   strings.ToLower(submitter.Hex()),
		},
	}

	digest, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return nil, fmt.Errorf("self mint attestation: typed data hash failed: %w", err)
	}

	sig, err := crypto.Sign(digest, key)
	if err != nil {
		return nil, fmt.Errorf("self mint attestation: sign failed: %w", err)
	}

	// Adjust v from 0/1 to 27/28 for Solidity ecrecover compatibility.
	if sig[64] < 27 {
		sig[64] += 27
	}

	return sig, nil
}
