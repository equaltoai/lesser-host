package soul

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// EncodeMintSoulCall returns ABI-encoded call data for SoulRegistry.mintSoul(to, agentId, metaURI, avatarStyle, deadline, permit).
func EncodeMintSoulCall(to common.Address, agentID *big.Int, metaURI string, avatarStyle uint8, deadline *big.Int, permit []byte) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	if deadline == nil {
		deadline = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("mintSoul", to, agentID, metaURI, avatarStyle, deadline, permit)
}

// EncodeMintSoulOwnerCall returns ABI-encoded call data for SoulRegistry.mintSoulOwner(to, agentId, metaURI, avatarStyle).
func EncodeMintSoulOwnerCall(to common.Address, agentID *big.Int, metaURI string, avatarStyle uint8) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("mintSoulOwner", to, agentID, metaURI, avatarStyle)
}

// EncodeSelfMintSoulCall returns ABI-encoded call data for SoulRegistry.selfMintSoul(to, agentId, metaURI, avatarStyle, principal, deadline, attestationSig).
func EncodeSelfMintSoulCall(to common.Address, agentID *big.Int, metaURI string, avatarStyle uint8, principal common.Address, deadline *big.Int, attestationSig []byte) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	if deadline == nil {
		deadline = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("selfMintSoul", to, agentID, metaURI, avatarStyle, principal, deadline, attestationSig)
}

// EncodeBurnSoulCall returns ABI-encoded call data for SoulRegistry.burnSoul(agentId).
func EncodeBurnSoulCall(agentID *big.Int) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("burnSoul", agentID)
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

// EncodePrincipalOfCall returns ABI-encoded call data for SoulRegistry.principalOf(agentId).
func EncodePrincipalOfCall(agentID *big.Int) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("principalOf", agentID)
}

// DecodePrincipalOfResult decodes the ABI result for SoulRegistry.principalOf(agentId).
func DecodePrincipalOfResult(ret []byte) (common.Address, error) {
	out, err := soulRegistryParsedABI.Unpack("principalOf", ret)
	if err != nil {
		return common.Address{}, err
	}
	if len(out) != 1 {
		return common.Address{}, errors.New("unexpected principalOf result shape")
	}
	addr, ok := out[0].(common.Address)
	if !ok {
		return common.Address{}, errors.New("unexpected principalOf result type")
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

// EncodeTransferCountCall returns ABI-encoded call data for SoulRegistry.transferCount(tokenId).
func EncodeTransferCountCall(agentID *big.Int) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("transferCount", agentID)
}

// DecodeTransferCountResult decodes the ABI result for SoulRegistry.transferCount(tokenId).
func DecodeTransferCountResult(ret []byte) (*big.Int, error) {
	out, err := soulRegistryParsedABI.Unpack("transferCount", ret)
	if err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, errors.New("unexpected transferCount result shape")
	}
	n, ok := out[0].(*big.Int)
	if !ok {
		return nil, errors.New("unexpected transferCount result type")
	}
	return n, nil
}

// EncodeLastTransferredAtCall returns ABI-encoded call data for SoulRegistry.lastTransferredAt(tokenId).
func EncodeLastTransferredAtCall(agentID *big.Int) ([]byte, error) {
	if agentID == nil {
		agentID = new(big.Int)
	}
	return soulRegistryParsedABI.Pack("lastTransferredAt", agentID)
}

// DecodeLastTransferredAtResult decodes the ABI result for SoulRegistry.lastTransferredAt(tokenId).
func DecodeLastTransferredAtResult(ret []byte) (*big.Int, error) {
	out, err := soulRegistryParsedABI.Unpack("lastTransferredAt", ret)
	if err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, errors.New("unexpected lastTransferredAt result shape")
	}
	n, ok := out[0].(*big.Int)
	if !ok {
		return nil, errors.New("unexpected lastTransferredAt result type")
	}
	return n, nil
}

// EncodeMintFeeCall returns ABI-encoded call data for SoulRegistry.mintFee().
func EncodeMintFeeCall() ([]byte, error) {
	return soulRegistryParsedABI.Pack("mintFee")
}

// DecodeMintFeeResult decodes the ABI result for SoulRegistry.mintFee().
func DecodeMintFeeResult(ret []byte) (*big.Int, error) {
	out, err := soulRegistryParsedABI.Unpack("mintFee", ret)
	if err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, errors.New("unexpected mintFee result shape")
	}
	n, ok := out[0].(*big.Int)
	if !ok {
		return nil, errors.New("unexpected mintFee result type")
	}
	return n, nil
}

// EncodeMintSignerCall returns ABI-encoded call data for SoulRegistry.mintSigner().
func EncodeMintSignerCall() ([]byte, error) {
	return soulRegistryParsedABI.Pack("mintSigner")
}

// DecodeMintSignerResult decodes the ABI result for SoulRegistry.mintSigner().
func DecodeMintSignerResult(ret []byte) (common.Address, error) {
	out, err := soulRegistryParsedABI.Unpack("mintSigner", ret)
	if err != nil {
		return common.Address{}, err
	}
	if len(out) != 1 {
		return common.Address{}, errors.New("unexpected mintSigner result shape")
	}
	addr, ok := out[0].(common.Address)
	if !ok {
		return common.Address{}, errors.New("unexpected mintSigner result type")
	}
	return addr, nil
}
