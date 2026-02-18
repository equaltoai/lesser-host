package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWalletModels_KeyGettersAndDefaults(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	ch := &WalletChallenge{ID: "c1", ExpiresAt: now.Add(time.Minute)}
	require.NoError(t, ch.UpdateKeys())
	require.Equal(t, ch.PK, ch.GetPK())
	require.Equal(t, ch.SK, ch.GetSK())

	cred := &WalletCredential{Username: "alice", Address: "0xAbC"}
	require.NoError(t, cred.BeforeCreate())
	require.False(t, cred.LinkedAt.IsZero())
	require.False(t, cred.LastUsed.IsZero())
	require.Equal(t, walletTypeEthereum, cred.Type)

	idx := &WalletIndex{Username: "alice", Address: "0xAbC"}
	require.NoError(t, idx.BeforeCreate())
	require.Equal(t, walletTypeEthereum, idx.WalletType)
	require.NotEmpty(t, idx.PK)
	require.NotEmpty(t, idx.SK)
}
