package soul

import (
	"errors"
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

// EncodeRotateWalletCall returns ABI-encoded call data for SoulRegistry.rotateWallet(...).
func EncodeRotateWalletCall(agentID *big.Int, newWallet common.Address, nonce *big.Int, deadline *big.Int, currentSig []byte, newSig []byte) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	if nonce == nil {
		nonce = new(big.Int)
	}
	if deadline == nil {
		deadline = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("rotateWallet", agentID, newWallet, nonce, deadline, currentSig, newSig)
}

// EncodeGetAgentWalletCall returns ABI-encoded call data for SoulRegistry.getAgentWallet(agentId).
func EncodeGetAgentWalletCall(agentID *big.Int) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("getAgentWallet", agentID)
}

// DecodeGetAgentWalletResult decodes the ABI result for SoulRegistry.getAgentWallet(agentId).
func DecodeGetAgentWalletResult(ret []byte) (common.Address, error) {
	out, err := soulRegistryParsedABI.Unpack("getAgentWallet", ret)
	if err != nil {
		return common.Address{}, err
	}
	if len(out) != 1 {
		return common.Address{}, errors.New("unexpected getAgentWallet result shape")
	}
	addr, ok := out[0].(common.Address)
	if !ok {
		return common.Address{}, errors.New("unexpected getAgentWallet result type")
	}
	return addr, nil
}

// EncodeAgentNoncesCall returns ABI-encoded call data for SoulRegistry.agentNonces(agentId).
func EncodeAgentNoncesCall(agentID *big.Int) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("agentNonces", agentID)
}

// DecodeAgentNoncesResult decodes the ABI result for SoulRegistry.agentNonces(agentId).
func DecodeAgentNoncesResult(ret []byte) (*big.Int, error) {
	out, err := soulRegistryParsedABI.Unpack("agentNonces", ret)
	if err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, errors.New("unexpected agentNonces result shape")
	}
	n, ok := out[0].(*big.Int)
	if !ok {
		return nil, errors.New("unexpected agentNonces result type")
	}
	return n, nil
}
