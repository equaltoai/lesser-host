package controlplane

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestCoverage_HelperValidationBranches(t *testing.T) {
	t.Parallel()

	t.Run("normalizeAdminWalletType", func(t *testing.T) {
		t.Parallel()

		got, err := normalizeAdminWalletType("")
		require.Nil(t, err)
		require.Equal(t, walletTypeEthereum, got)

		got, err = normalizeAdminWalletType("ethereum")
		require.Nil(t, err)
		require.Equal(t, walletTypeEthereum, got)

		_, err = normalizeAdminWalletType("solana")
		require.NotNil(t, err)
	})

	t.Run("normalizeAdminWalletAddress", func(t *testing.T) {
		t.Parallel()

		_, err := normalizeAdminWalletAddress("")
		require.NotNil(t, err)

		_, err = normalizeAdminWalletAddress(testNotAnAddress)
		require.NotNil(t, err)

		_, err = normalizeAdminWalletAddress(reservedWalletLesserHostAdmin)
		require.NotNil(t, err)

		got, err := normalizeAdminWalletAddress("0x00000000000000000000000000000000000000aa")
		require.Nil(t, err)
		require.Equal(t, "0x00000000000000000000000000000000000000aa", got)
	})

	t.Run("normalizeAdminWalletChainID", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, 1, normalizeAdminWalletChainID(0))
		require.Equal(t, 1, normalizeAdminWalletChainID(-5))
		require.Equal(t, 5, normalizeAdminWalletChainID(5))
	})

	t.Run("normalizeTipRegistryRegistrationKind", func(t *testing.T) {
		t.Parallel()

		kind, err := normalizeTipRegistryRegistrationKind("")
		require.Nil(t, err)
		require.Equal(t, "register_host", kind)

		kind, err = normalizeTipRegistryRegistrationKind("update_host")
		require.Nil(t, err)
		require.Equal(t, "update_host", kind)

		_, err = normalizeTipRegistryRegistrationKind("nope")
		require.NotNil(t, err)
	})

	t.Run("validateTipHostFeeBps", func(t *testing.T) {
		t.Parallel()

		_, err := validateTipHostFeeBps(-1)
		require.NotNil(t, err)

		_, err = validateTipHostFeeBps(501)
		require.NotNil(t, err)

		fee, err := validateTipHostFeeBps(0)
		require.Nil(t, err)
		require.Equal(t, uint16(0), fee)

		fee, err = validateTipHostFeeBps(500)
		require.Nil(t, err)
		require.Equal(t, uint16(500), fee)
	})

	t.Run("encodeTipRegistryOperationData", func(t *testing.T) {
		t.Parallel()

		hostID := common.HexToHash("0x01")
		wallet := common.HexToAddress("0x00000000000000000000000000000000000000aa")

		_, kind, err := encodeTipRegistryOperationData("register_host", hostID, wallet, 0)
		require.Nil(t, err)
		require.Equal(t, "register_host", kind)

		_, kind, err = encodeTipRegistryOperationData("update_host", hostID, wallet, 0)
		require.Nil(t, err)
		require.Equal(t, "update_host", kind)

		_, _, err = encodeTipRegistryOperationData("bad", hostID, wallet, 0)
		require.NotNil(t, err)
	})
}
