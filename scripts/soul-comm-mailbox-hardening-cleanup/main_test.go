package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const mailboxCleanupTestRoot = "comm-msg-root"
const mailboxCleanupOldThread = "comm-thread-old"
const mailboxCleanupTestStage = "lab"

func TestPlanMailboxCleanupDeletesGhostsAndNormalizesSelfSendThreads(t *testing.T) {
	cfg := mailboxCleanupConfig{stage: mailboxCleanupTestStage, instanceSlug: "simulacrum", agentID: "0xagent"}
	root := mailboxCleanupTestRoot
	canonicalThread := models.SoulCommMailboxThreadID(cfg.instanceSlug, cfg.agentID, "email", root)
	oldSplitThread := "comm-thread-old-split"
	rows := []dynamoMailboxRow{
		mailboxCleanupTestRow(cfg, "comm-delivery-out", root, models.SoulCommDirectionOutbound, canonicalThread),
		mailboxCleanupTestRow(cfg, "comm-delivery-in", root+"@lessersoul.ai", models.SoulCommDirectionInbound, oldSplitThread),
		mailboxCleanupTestRow(cfg, "comm-delivery-reply-out", "comm-msg-reply", models.SoulCommDirectionOutbound, oldSplitThread),
		mailboxCleanupTestRow(cfg, "comm-delivery-reply-in", "comm-msg-reply@lessersoul.ai", models.SoulCommDirectionInbound, oldSplitThread),
		mailboxCleanupTestRow(cfg, "comm-delivery-external", "comm-msg-external@example.net", models.SoulCommDirectionInbound, "comm-thread-external"),
		{PK: models.SoulCommMailboxAgentPK(cfg.instanceSlug, cfg.agentID), SK: "MSG#2026-04-26T22:48:05.342175181Z#comm-delivery-in"},
	}

	actions := planMailboxCleanup(cfg, rows)
	require.Len(t, actions, 4)
	require.Equal(t, mailboxCleanupDeleteGhost, actions[0].Kind)
	require.Equal(t, "MSG#2026-04-26T22:48:05.342175181Z#comm-delivery-in", actions[0].Row.SK)
	for _, action := range actions[1:] {
		require.Equal(t, mailboxCleanupUpdateThread, action.Kind)
		require.Equal(t, canonicalThread, action.TargetThread)
		require.Equal(t, oldSplitThread, action.Row.ThreadID)
	}
}

func TestPlanMailboxCleanupLeavesExternalThreadsUntouched(t *testing.T) {
	cfg := mailboxCleanupConfig{stage: mailboxCleanupTestStage, instanceSlug: "simulacrum", agentID: "0xagent"}
	rows := []dynamoMailboxRow{
		mailboxCleanupTestRow(cfg, "comm-delivery-external", mailboxCleanupTestRoot+"@example.net", models.SoulCommDirectionInbound, "comm-thread-external"),
		mailboxCleanupTestRow(cfg, "comm-delivery-out", mailboxCleanupTestRoot, models.SoulCommDirectionOutbound, models.SoulCommMailboxThreadID(cfg.instanceSlug, cfg.agentID, "email", mailboxCleanupTestRoot)),
	}

	require.Empty(t, planMailboxCleanup(cfg, rows))
}

func mailboxCleanupTestRow(cfg mailboxCleanupConfig, deliveryID string, messageID string, direction string, threadID string) dynamoMailboxRow {
	pk := models.SoulCommMailboxAgentPK(cfg.instanceSlug, cfg.agentID)
	sk := "MSG#2026-04-26T22:48:01.788000000Z#" + deliveryID
	return dynamoMailboxRow{
		PK:           pk,
		SK:           sk,
		DeliveryID:   deliveryID,
		MessageID:    messageID,
		ThreadID:     threadID,
		InstanceSlug: cfg.instanceSlug,
		AgentID:      cfg.agentID,
		Direction:    direction,
		ChannelType:  "email",
		GSI1PK:       models.SoulCommMailboxDeliveryPK(deliveryID),
		GSI1SK:       "CURRENT",
		GSI2PK:       models.SoulCommMailboxThreadPK(cfg.instanceSlug, cfg.agentID, threadID),
		GSI2SK:       sk,
	}
}

func TestRunMailboxCleanupApplyRepairsCurrentRowsAndEvents(t *testing.T) {
	cfg := mailboxCleanupConfig{stage: mailboxCleanupTestStage, tableName: "state", instanceSlug: "simulacrum", agentID: "0xagent", apply: true, pageSize: 100}
	root := mailboxCleanupTestRoot
	canonicalThread := models.SoulCommMailboxThreadID(cfg.instanceSlug, cfg.agentID, "email", root)
	oldThread := mailboxCleanupOldThread
	outbound := mailboxCleanupTestRow(cfg, "comm-delivery-out", root, models.SoulCommDirectionOutbound, canonicalThread)
	inbound := mailboxCleanupTestRow(cfg, "comm-delivery-in", root+"@lessersoul.ai", models.SoulCommDirectionInbound, oldThread)
	ghost := dynamoMailboxRow{PK: models.SoulCommMailboxAgentPK(cfg.instanceSlug, cfg.agentID), SK: "MSG#2026-04-26T22:48:05Z#comm-delivery-in"}
	event := dynamoMailboxRow{PK: models.SoulCommMailboxDeliveryPK(inbound.DeliveryID), SK: "EVENT#2026-04-26T22:48:06Z#event", DeliveryID: inbound.DeliveryID, ThreadID: oldThread}
	client := &fakeMailboxCleanupDynamo{
		currentRows: []dynamoMailboxRow{outbound, inbound, ghost},
		eventRows:   map[string][]dynamoMailboxRow{inbound.DeliveryID: {event}},
	}

	summary, err := runMailboxCleanup(t.Context(), client, cfg)
	require.NoError(t, err)
	require.Equal(t, mailboxCleanupSummary{Scanned: 3, GhostDeletes: 1, ThreadUpdates: 1, EventThreadUpdates: 1}, summary)
	require.Len(t, client.deleteInputs, 1)
	require.Len(t, client.updateInputs, 2)
	require.Contains(t, *client.updateInputs[0].UpdateExpression, "updatedAt")
	require.NotContains(t, *client.updateInputs[1].UpdateExpression, "updatedAt")
}

func TestRunMailboxCleanupDryRunDoesNotWrite(t *testing.T) {
	cfg := mailboxCleanupConfig{stage: mailboxCleanupTestStage, tableName: "state", instanceSlug: "simulacrum", agentID: "0xagent", pageSize: 100}
	root := mailboxCleanupTestRoot
	canonicalThread := models.SoulCommMailboxThreadID(cfg.instanceSlug, cfg.agentID, "email", root)
	oldThread := mailboxCleanupOldThread
	client := &fakeMailboxCleanupDynamo{currentRows: []dynamoMailboxRow{
		mailboxCleanupTestRow(cfg, "comm-delivery-out", root, models.SoulCommDirectionOutbound, canonicalThread),
		mailboxCleanupTestRow(cfg, "comm-delivery-in", root+"@lessersoul.ai", models.SoulCommDirectionInbound, oldThread),
	}}

	summary, err := runMailboxCleanup(t.Context(), client, cfg)
	require.NoError(t, err)
	require.Equal(t, mailboxCleanupSummary{Scanned: 2, ThreadUpdates: 1}, summary)
	require.Empty(t, client.deleteInputs)
	require.Empty(t, client.updateInputs)
}

func TestValidateMailboxCleanupConfig(t *testing.T) {
	require.NoError(t, validateMailboxCleanupConfig(mailboxCleanupConfig{
		stage:        mailboxCleanupTestStage,
		tableName:    "state",
		instanceSlug: "simulacrum",
		agentID:      "0xagent",
	}))
	require.ErrorContains(t, validateMailboxCleanupConfig(mailboxCleanupConfig{stage: "live", tableName: "state", instanceSlug: "simulacrum", agentID: "0xagent"}), "non-lab")
	require.ErrorContains(t, validateMailboxCleanupConfig(mailboxCleanupConfig{stage: mailboxCleanupTestStage, instanceSlug: "simulacrum", agentID: "0xagent"}), "table-name")
	require.ErrorContains(t, validateMailboxCleanupConfig(mailboxCleanupConfig{stage: mailboxCleanupTestStage, tableName: "state", agentID: "0xagent"}), "instance-slug")
	require.ErrorContains(t, validateMailboxCleanupConfig(mailboxCleanupConfig{stage: mailboxCleanupTestStage, tableName: "state", instanceSlug: "simulacrum"}), "agent-id")
}

func TestRunMailboxCleanupReportsWriteErrors(t *testing.T) {
	cfg := mailboxCleanupConfig{stage: mailboxCleanupTestStage, tableName: "state", instanceSlug: "simulacrum", agentID: "0xagent", apply: true, pageSize: 100}
	root := mailboxCleanupTestRoot
	canonicalThread := models.SoulCommMailboxThreadID(cfg.instanceSlug, cfg.agentID, "email", root)
	oldThread := mailboxCleanupOldThread
	client := &fakeMailboxCleanupDynamo{
		currentRows: []dynamoMailboxRow{
			mailboxCleanupTestRow(cfg, "comm-delivery-out", root, models.SoulCommDirectionOutbound, canonicalThread),
			mailboxCleanupTestRow(cfg, "comm-delivery-in", root+"@lessersoul.ai", models.SoulCommDirectionInbound, oldThread),
			{PK: models.SoulCommMailboxAgentPK(cfg.instanceSlug, cfg.agentID), SK: "MSG#2026-04-26T22:48:05Z#ghost"},
		},
		deleteErr: errors.New("delete failed"),
		updateErr: errors.New("update failed"),
	}

	summary, err := runMailboxCleanup(t.Context(), client, cfg)
	require.NoError(t, err)
	require.Equal(t, 2, summary.Errors)
	require.Zero(t, summary.GhostDeletes)
	require.Zero(t, summary.ThreadUpdates)
}

func TestUpdateMailboxEventThreadsReportsQueryError(t *testing.T) {
	cfg := mailboxCleanupConfig{stage: mailboxCleanupTestStage, tableName: "state", instanceSlug: "simulacrum", agentID: "0xagent"}
	row := mailboxCleanupTestRow(cfg, "comm-delivery-in", mailboxCleanupTestRoot+"@lessersoul.ai", models.SoulCommDirectionInbound, mailboxCleanupOldThread)
	client := &fakeMailboxCleanupDynamo{eventQueryErr: errors.New("query failed")}

	updated, errs := updateMailboxEventThreads(t.Context(), client, cfg, row, "comm-thread-new")
	require.Zero(t, updated)
	require.Equal(t, 1, errs)
}

func TestParseMailboxCleanupArgs(t *testing.T) {
	env := func(key string) string {
		switch key {
		case "STAGE":
			return mailboxCleanupTestStage
		case "STATE_TABLE_NAME":
			return "state-from-env"
		default:
			return ""
		}
	}
	cfg, err := parseMailboxCleanupArgs([]string{
		"--instance-slug", " Simulacrum ",
		"--agent-id", " 0xAgent ",
		"--apply",
		"--max-actions", "4",
		"--page-size", "500",
	}, env)
	require.NoError(t, err)
	require.Equal(t, mailboxCleanupTestStage, cfg.stage)
	require.Equal(t, "state-from-env", cfg.tableName)
	require.Equal(t, "simulacrum", cfg.instanceSlug)
	require.Equal(t, "0xagent", cfg.agentID)
	require.True(t, cfg.apply)
	require.Equal(t, 4, cfg.maxActions)
	require.EqualValues(t, 100, cfg.pageSize)

	cfg, err = parseMailboxCleanupArgs([]string{"--stage", mailboxCleanupTestStage, "--table-name", "state", "--instance-slug", "demo", "--agent-id", "0x1", "--page-size", "25"}, func(string) string { return "" })
	require.NoError(t, err)
	require.EqualValues(t, 25, cfg.pageSize)
}

type fakeMailboxCleanupDynamo struct {
	currentRows   []dynamoMailboxRow
	eventRows     map[string][]dynamoMailboxRow
	deleteInputs  []*dynamodb.DeleteItemInput
	updateInputs  []*dynamodb.UpdateItemInput
	deleteErr     error
	updateErr     error
	eventQueryErr error
}

func (f *fakeMailboxCleanupDynamo) Query(_ context.Context, input *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if fakeAttrString(input.ExpressionAttributeValues, ":skPrefix") == "EVENT#" {
		if f.eventQueryErr != nil {
			return nil, f.eventQueryErr
		}
		deliveryID := strings.TrimPrefix(fakeAttrString(input.ExpressionAttributeValues, ":pk"), "COMM#MAILBOX#DELIVERY#")
		return &dynamodb.QueryOutput{Items: mailboxCleanupItems(f.eventRows[deliveryID])}, nil
	}
	return &dynamodb.QueryOutput{Items: mailboxCleanupItems(f.currentRows)}, nil
}

func (f *fakeMailboxCleanupDynamo) DeleteItem(_ context.Context, input *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.deleteInputs = append(f.deleteInputs, input)
	return &dynamodb.DeleteItemOutput{}, f.deleteErr
}

func (f *fakeMailboxCleanupDynamo) UpdateItem(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateInputs = append(f.updateInputs, input)
	return &dynamodb.UpdateItemOutput{}, f.updateErr
}

func mailboxCleanupItems(rows []dynamoMailboxRow) []map[string]types.AttributeValue {
	items := make([]map[string]types.AttributeValue, 0, len(rows))
	for _, row := range rows {
		items = append(items, mailboxCleanupItem(row))
	}
	return items
}

func mailboxCleanupItem(row dynamoMailboxRow) map[string]types.AttributeValue {
	item := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: row.PK},
		"SK": &types.AttributeValueMemberS{Value: row.SK},
	}
	for key, value := range map[string]string{
		"deliveryId":   row.DeliveryID,
		"messageId":    row.MessageID,
		"threadId":     row.ThreadID,
		"instanceSlug": row.InstanceSlug,
		"agentId":      row.AgentID,
		"direction":    row.Direction,
		"channelType":  row.ChannelType,
		"gsi1PK":       row.GSI1PK,
		"gsi1SK":       row.GSI1SK,
		"gsi2PK":       row.GSI2PK,
		"gsi2SK":       row.GSI2SK,
	} {
		if value != "" {
			item[key] = &types.AttributeValueMemberS{Value: value}
		}
	}
	return item
}

func fakeAttrString(item map[string]types.AttributeValue, key string) string {
	if value, ok := item[key].(*types.AttributeValueMemberS); ok {
		return value.Value
	}
	return ""
}
