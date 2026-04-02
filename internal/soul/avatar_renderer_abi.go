package soul

import "github.com/ethereum/go-ethereum/accounts/abi"

// SoulAvatarRendererABI is the minimal ABI required for public avatar rendering helpers.
const SoulAvatarRendererABI = `[
  {"type":"function","name":"renderAvatar","stateMutability":"view","inputs":[{"name":"tokenId","type":"uint256"}],"outputs":[{"name":"","type":"string"}]},
  {"type":"function","name":"styleName","stateMutability":"pure","inputs":[],"outputs":[{"name":"","type":"string"}]}
]`

var soulAvatarRendererParsedABI abi.ABI = mustParseABI(SoulAvatarRendererABI)
