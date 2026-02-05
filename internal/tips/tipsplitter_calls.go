package tips

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// HostConfig is the decoded host registry entry from TipSplitter.hosts(hostId).
type HostConfig struct {
	Wallet   common.Address
	FeeBps   uint16
	IsActive bool
}

// EncodeRegisterHostCall returns ABI-encoded call data for TipSplitter.registerHost.
func EncodeRegisterHostCall(hostID common.Hash, wallet common.Address, feeBps uint16) ([]byte, error) {
	return tipSplitterParsedABI.Pack("registerHost", hostID, wallet, feeBps)
}

// EncodeUpdateHostCall returns ABI-encoded call data for TipSplitter.updateHost.
func EncodeUpdateHostCall(hostID common.Hash, wallet common.Address, feeBps uint16) ([]byte, error) {
	return tipSplitterParsedABI.Pack("updateHost", hostID, wallet, feeBps)
}

// EncodeSetHostActiveCall returns ABI-encoded call data for TipSplitter.setHostActive.
func EncodeSetHostActiveCall(hostID common.Hash, active bool) ([]byte, error) {
	return tipSplitterParsedABI.Pack("setHostActive", hostID, active)
}

// EncodeSetTokenAllowedCall returns ABI-encoded call data for TipSplitter.setTokenAllowed.
func EncodeSetTokenAllowedCall(token common.Address, allowed bool) ([]byte, error) {
	return tipSplitterParsedABI.Pack("setTokenAllowed", token, allowed)
}

// EncodeGetHostCall returns ABI-encoded call data for TipSplitter.hosts(hostId).
func EncodeGetHostCall(hostID common.Hash) ([]byte, error) {
	return tipSplitterParsedABI.Pack("hosts", hostID)
}

// DecodeGetHostResult decodes the return data of TipSplitter.hosts(hostId).
func DecodeGetHostResult(ret []byte) (*HostConfig, error) {
	if len(ret) == 0 {
		return nil, fmt.Errorf("empty return data")
	}
	values, err := tipSplitterParsedABI.Unpack("hosts", ret)
	if err != nil {
		return nil, err
	}
	if len(values) != 3 {
		return nil, fmt.Errorf("unexpected host result len=%d", len(values))
	}

	wallet, ok := values[0].(common.Address)
	if !ok {
		return nil, fmt.Errorf("unexpected wallet type %T", values[0])
	}

	feeBps, err := toUint16(values[1])
	if err != nil {
		return nil, fmt.Errorf("feeBps: %w", err)
	}

	active, ok := values[2].(bool)
	if !ok {
		return nil, fmt.Errorf("unexpected isActive type %T", values[2])
	}

	return &HostConfig{Wallet: wallet, FeeBps: feeBps, IsActive: active}, nil
}

// EncodeIsTokenAllowedCall returns ABI-encoded call data for TipSplitter.allowedTokens(token).
func EncodeIsTokenAllowedCall(token common.Address) ([]byte, error) {
	return tipSplitterParsedABI.Pack("allowedTokens", token)
}

// DecodeIsTokenAllowedResult decodes the return data of TipSplitter.allowedTokens(token).
func DecodeIsTokenAllowedResult(ret []byte) (bool, error) {
	values, err := tipSplitterParsedABI.Unpack("allowedTokens", ret)
	if err != nil {
		return false, err
	}
	if len(values) != 1 {
		return false, fmt.Errorf("unexpected allowedTokens result len=%d", len(values))
	}
	b, ok := values[0].(bool)
	if !ok {
		return false, fmt.Errorf("unexpected allowedTokens type %T", values[0])
	}
	return b, nil
}

func toUint16(v any) (uint16, error) {
	switch n := v.(type) {
	case uint8:
		return uint16(n), nil
	case uint16:
		return n, nil
	case uint32:
		if n > 0xFFFF {
			return 0, fmt.Errorf("overflow")
		}
		return uint16(n), nil
	case uint64:
		if n > 0xFFFF {
			return 0, fmt.Errorf("overflow")
		}
		return uint16(n), nil
	case int:
		if n < 0 || n > 0xFFFF {
			return 0, fmt.Errorf("overflow")
		}
		return uint16(n), nil
	case *big.Int:
		if n == nil || n.Sign() < 0 || n.BitLen() > 16 {
			return 0, fmt.Errorf("overflow")
		}
		u := n.Uint64()
		//nolint:gosec // bitlen checked (<= 16)
		return uint16(u), nil
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}
