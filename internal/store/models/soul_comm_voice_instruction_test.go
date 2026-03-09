package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSoulCommVoiceInstructionLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	item := SoulCommVoiceInstruction{
		MessageID: " comm-msg-1 ",
		AgentID:   " 0xABCD ",
		From:      " +15550142 ",
		To:        " +15550143 ",
		Body:      " hello there ",
		Voice:     " Polly.Amy-Neural ",
		CreatedAt: now,
	}

	require.Equal(t, MainTableName(), item.TableName())
	require.NoError(t, item.BeforeCreate())
	require.Equal(t, "COMM#MSG#comm-msg-1", item.GetPK())
	require.Equal(t, "VOICE#INSTRUCTION", item.GetSK())
	require.Equal(t, "0xabcd", item.AgentID)
	require.Equal(t, "+15550142", item.From)
	require.Equal(t, "+15550143", item.To)
	require.Equal(t, "hello there", item.Body)
	require.Equal(t, "Polly.Amy-Neural", item.Voice)
	require.Equal(t, now.Add(24*time.Hour).Unix(), item.TTL)

	item.Body = " updated body "
	require.NoError(t, item.BeforeUpdate())
	require.Equal(t, "updated body", item.Body)
}

func TestSoulCommVoiceInstructionRequiresFields(t *testing.T) {
	t.Parallel()

	require.ErrorContains(t, (&SoulCommVoiceInstruction{}).BeforeCreate(), "messageId")
	require.ErrorContains(t, (&SoulCommVoiceInstruction{MessageID: "comm-msg-1"}).BeforeCreate(), "agentId")
	require.ErrorContains(t, (&SoulCommVoiceInstruction{MessageID: "comm-msg-1", AgentID: "0xabc"}).BeforeCreate(), "from")
	require.ErrorContains(t, (&SoulCommVoiceInstruction{MessageID: "comm-msg-1", AgentID: "0xabc", From: "+15550142"}).BeforeCreate(), "to")
	require.ErrorContains(t, (&SoulCommVoiceInstruction{MessageID: "comm-msg-1", AgentID: "0xabc", From: "+15550142", To: "+15550143"}).BeforeCreate(), "body")
}
