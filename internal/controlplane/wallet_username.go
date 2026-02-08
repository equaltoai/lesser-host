package controlplane

import "strings"

func walletAddressFromUsername(username string) string {
	username = strings.ToLower(strings.TrimSpace(username))
	if !strings.HasPrefix(username, "wallet-") {
		return ""
	}
	hex := strings.TrimSpace(strings.TrimPrefix(username, "wallet-"))
	if hex == "" {
		return ""
	}
	return "0x" + hex
}
