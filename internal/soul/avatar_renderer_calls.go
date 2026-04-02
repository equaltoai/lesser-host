package soul

import (
	"errors"
	"math/big"
)

// EncodeRendererRenderAvatarCall returns ABI-encoded call data for ISoulAvatarRenderer.renderAvatar(tokenId).
func EncodeRendererRenderAvatarCall(tokenID *big.Int) ([]byte, error) {
	if tokenID == nil {
		tokenID = new(big.Int)
	}
	return soulAvatarRendererParsedABI.Pack("renderAvatar", tokenID)
}

// DecodeRendererRenderAvatarResult decodes the ABI result for ISoulAvatarRenderer.renderAvatar(tokenId).
func DecodeRendererRenderAvatarResult(ret []byte) (string, error) {
	out, err := soulAvatarRendererParsedABI.Unpack("renderAvatar", ret)
	if err != nil {
		return "", err
	}
	if len(out) != 1 {
		return "", errors.New("unexpected renderAvatar result shape")
	}
	value, ok := out[0].(string)
	if !ok {
		return "", errors.New("unexpected renderAvatar result type")
	}
	return value, nil
}

// EncodeRendererStyleNameCall returns ABI-encoded call data for ISoulAvatarRenderer.styleName().
func EncodeRendererStyleNameCall() ([]byte, error) {
	return soulAvatarRendererParsedABI.Pack("styleName")
}

// DecodeRendererStyleNameResult decodes the ABI result for ISoulAvatarRenderer.styleName().
func DecodeRendererStyleNameResult(ret []byte) (string, error) {
	out, err := soulAvatarRendererParsedABI.Unpack("styleName", ret)
	if err != nil {
		return "", err
	}
	if len(out) != 1 {
		return "", errors.New("unexpected styleName result shape")
	}
	value, ok := out[0].(string)
	if !ok {
		return "", errors.New("unexpected styleName result type")
	}
	return value, nil
}
