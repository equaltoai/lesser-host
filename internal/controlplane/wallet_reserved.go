package controlplane

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

const (
	reservedWalletLesserHostAdmin   = "0x80189edb676d51b2fb2257b2ad38e018b20ca46e"
	reservedWalletTipSplitterLesser = "0x1e14865a53a994b01b9ccfef42669dc0bfe98805"
)

var reservedWalletAddresses = map[string]struct{}{
	reservedWalletLesserHostAdmin:   {},
	reservedWalletTipSplitterLesser: {},
}

func normalizeEVMAddressLoose(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	addr = strings.TrimPrefix(addr, "0x")
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	return "0x" + addr
}

func isReservedWalletAddress(addr string) bool {
	addr = normalizeEVMAddressLoose(addr)
	if addr == "" {
		return false
	}
	_, ok := reservedWalletAddresses[addr]
	return ok
}

func validateNotReservedWalletAddress(addr string, field string) *apptheory.AppError {
	if !isReservedWalletAddress(addr) {
		return nil
	}
	field = strings.TrimSpace(field)
	msg := "wallet is reserved"
	if field != "" {
		msg = field + " is reserved"
	}
	return &apptheory.AppError{Code: "app.bad_request", Message: msg}
}

func validateNotReservedWalletUsername(username string) *apptheory.AppError {
	addr := walletAddressFromUsername(username)
	if addr == "" {
		return nil
	}
	return validateNotReservedWalletAddress(addr, "wallet")
}
