#!/usr/bin/env bash
set -euo pipefail

# Generate an Ethereum keypair for use as a mint signer.
# Prints the address (for setMintSigner) and the private key (for SSM storage).
# Does NOT store the key — that is a manual step.

if ! command -v openssl &>/dev/null; then
  echo "ERROR: openssl is required" >&2
  exit 1
fi

# Generate a random 32-byte private key.
PRIVATE_KEY=$(openssl rand -hex 32)

# Derive the address using Go.
ADDRESS=$(cd "$(dirname "$0")/.." && go run -mod=mod -exec '' \
  -ldflags='-s -w' \
  github.com/ethereum/go-ethereum/cmd/ethkey \
  inspect --private "$PRIVATE_KEY" 2>/dev/null | grep -i '^address:' | awk '{print $2}' || true)

# Fallback: if ethkey isn't available, use a tiny Go program.
if [ -z "$ADDRESS" ]; then
  ADDRESS=$(cd "$(dirname "$0")/.." && go run -mod=mod <<'GOEOF'
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

func main() {
	key, err := crypto.HexToECDSA(strings.TrimPrefix(os.Args[1], "0x"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(crypto.PubkeyToAddress(key.PublicKey).Hex())
}
GOEOF
  "$PRIVATE_KEY" 2>/dev/null || true)
fi

echo ""
echo "=== Mint Signer Keypair ==="
echo ""
echo "Address (for setMintSigner):  ${ADDRESS:-<derive manually>}"
echo "Private key (for SSM):        $PRIVATE_KEY"
echo ""
echo "Next steps:"
echo "  1. Store in SSM:  aws ssm put-parameter --name /lesser-host/soul/lab/mint-signer-key --value $PRIVATE_KEY --type SecureString"
echo "  2. Set mint fee:  SoulRegistry.setMintFee(500000000000000)  # 0.0005 ETH"
echo "  3. Set signer:    SoulRegistry.setMintSigner(${ADDRESS:-<address>})"
echo ""
