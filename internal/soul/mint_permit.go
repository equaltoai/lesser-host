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

// SignMintPermit produces an EIP-712 signature for the MintPermit typed data.
// The returned bytes are a 65-byte signature with v = 27 or 28.
func SignMintPermit(
	privateKeyHex string,
	chainID int64,
	contractAddress common.Address,
	to common.Address,
	agentID *big.Int,
	metaURI string,
	avatarStyle uint8,
	deadline *big.Int,
) ([]byte, error) {
	privateKeyHex = strings.TrimPrefix(strings.TrimSpace(privateKeyHex), "0x")
	if privateKeyHex == "" {
		return nil, fmt.Errorf("mint permit: empty private key")
	}

	key, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("mint permit: invalid private key: %w", err)
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
			"MintPermit": {
				{Name: "to", Type: "address"},
				{Name: "agentId", Type: "uint256"},
				{Name: "metaURI", Type: "string"},
				{Name: "avatarStyle", Type: "uint8"},
				{Name: "deadline", Type: "uint256"},
			},
		},
		PrimaryType: "MintPermit",
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
			"deadline":    deadline.String(),
		},
	}

	digest, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return nil, fmt.Errorf("mint permit: typed data hash failed: %w", err)
	}

	sig, err := crypto.Sign(digest, key)
	if err != nil {
		return nil, fmt.Errorf("mint permit: sign failed: %w", err)
	}

	// Adjust v from 0/1 to 27/28 for Solidity ecrecover compatibility.
	if sig[64] < 27 {
		sig[64] += 27
	}

	return sig, nil
}
