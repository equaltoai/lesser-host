package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type mailboxCleanupConfig struct {
	stage        string
	tableName    string
	instanceSlug string
	agentID      string
	apply        bool
	pageSize     int32
	maxActions   int
}

type mailboxCleanupSummary struct {
	Scanned            int
	GhostDeletes       int
	ThreadUpdates      int
	EventThreadUpdates int
	Skipped            int
	Errors             int
}

type dynamoMailboxRow struct {
	PK           string
	SK           string
	DeliveryID   string
	MessageID    string
	ThreadID     string
	InstanceSlug string
	AgentID      string
	Direction    string
	ChannelType  string
	GSI1PK       string
	GSI1SK       string
	GSI2PK       string
	GSI2SK       string
}

type mailboxCleanupActionKind string

const (
	mailboxCleanupDeleteGhost  mailboxCleanupActionKind = "delete_ghost"
	mailboxCleanupUpdateThread mailboxCleanupActionKind = "update_thread"
)

type mailboxCleanupAction struct {
	Kind         mailboxCleanupActionKind
	Row          dynamoMailboxRow
	TargetThread string
	Reason       string
}

func main() {
	cfg, parseErr := parseMailboxCleanupFlags()
	if parseErr != nil {
		die("%v", parseErr)
	}
	if validateErr := validateMailboxCleanupConfig(cfg); validateErr != nil {
		die("%v", validateErr)
	}

	mode := "dry-run"
	if cfg.apply {
		mode = "apply"
	}
	fmt.Printf("soul-comm-mailbox-hardening-cleanup mode=%s stage=%s table=%s instance=%s agent=%s\n", mode, cfg.stage, cfg.tableName, cfg.instanceSlug, cfg.agentID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	awsCfg, awsErr := awsconfig.LoadDefaultConfig(ctx)
	if awsErr != nil {
		die("load aws config: %v", awsErr)
	}
	summary, runErr := runMailboxCleanup(ctx, dynamodb.NewFromConfig(awsCfg), cfg)
	if runErr != nil {
		die("cleanup failed: %v", runErr)
	}
	fmt.Printf("summary scanned=%d ghostDeletes=%d threadUpdates=%d eventThreadUpdates=%d skipped=%d errors=%d\n", summary.Scanned, summary.GhostDeletes, summary.ThreadUpdates, summary.EventThreadUpdates, summary.Skipped, summary.Errors)
	if summary.Errors > 0 {
		os.Exit(1)
	}
}

func parseMailboxCleanupFlags() (mailboxCleanupConfig, error) {
	return parseMailboxCleanupArgs(os.Args[1:], os.Getenv)
}

func parseMailboxCleanupArgs(args []string, getenv func(string) string) (mailboxCleanupConfig, error) {
	stageDefault := strings.TrimSpace(getenv("STAGE"))
	if stageDefault == "" {
		stageDefault = "lab"
	}
	tableDefault := strings.TrimSpace(getenv("STATE_TABLE_NAME"))
	if tableDefault == "" {
		tableDefault = fmt.Sprintf("lesser-host-%s-state", stageDefault)
	}
	cfg := mailboxCleanupConfig{}
	pageSize := 100
	fs := flag.NewFlagSet("soul-comm-mailbox-hardening-cleanup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.stage, "stage", stageDefault, "Stage to repair; only lab is allowed")
	fs.StringVar(&cfg.tableName, "table-name", tableDefault, "DynamoDB state table name")
	fs.StringVar(&cfg.instanceSlug, "instance-slug", "", "Target instance slug")
	fs.StringVar(&cfg.agentID, "agent-id", "", "Target soul agent id")
	fs.BoolVar(&cfg.apply, "apply", false, "Apply repairs (default: dry-run)")
	fs.IntVar(&cfg.maxActions, "max-actions", 0, "Maximum current-row repair actions (0 = unlimited)")
	fs.IntVar(&pageSize, "page-size", 100, "Mailbox query page size (max 200)")
	if err := fs.Parse(args); err != nil {
		return mailboxCleanupConfig{}, err
	}
	cfg.stage = strings.ToLower(strings.TrimSpace(cfg.stage))
	cfg.tableName = strings.TrimSpace(cfg.tableName)
	cfg.instanceSlug = strings.ToLower(strings.TrimSpace(cfg.instanceSlug))
	cfg.agentID = strings.ToLower(strings.TrimSpace(cfg.agentID))
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 100
	}
	cfg.pageSize = int32(pageSize)
	return cfg, nil
}

func validateMailboxCleanupConfig(cfg mailboxCleanupConfig) error {
	if cfg.stage != "lab" {
		return fmt.Errorf("refusing to repair non-lab stage %q", cfg.stage)
	}
	if cfg.tableName == "" {
		return fmt.Errorf("missing --table-name or STATE_TABLE_NAME")
	}
	if cfg.instanceSlug == "" {
		return fmt.Errorf("missing --instance-slug")
	}
	if cfg.agentID == "" {
		return fmt.Errorf("missing --agent-id")
	}
	return nil
}

type mailboxCleanupDynamo interface {
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

func runMailboxCleanup(ctx context.Context, client mailboxCleanupDynamo, cfg mailboxCleanupConfig) (mailboxCleanupSummary, error) {
	rows, err := queryMailboxRows(ctx, client, cfg)
	if err != nil {
		return mailboxCleanupSummary{}, err
	}
	actions := planMailboxCleanup(cfg, rows)
	if cfg.maxActions > 0 && len(actions) > cfg.maxActions {
		actions = actions[:cfg.maxActions]
	}
	summary := mailboxCleanupSummary{Scanned: len(rows)}
	for _, action := range actions {
		applyMailboxCleanupAction(ctx, client, cfg, action, &summary)
	}
	return summary, nil
}

func applyMailboxCleanupAction(ctx context.Context, client mailboxCleanupDynamo, cfg mailboxCleanupConfig, action mailboxCleanupAction, summary *mailboxCleanupSummary) {
	switch action.Kind {
	case mailboxCleanupDeleteGhost:
		applyMailboxGhostDelete(ctx, client, cfg, action, summary)
	case mailboxCleanupUpdateThread:
		applyMailboxThreadUpdate(ctx, client, cfg, action, summary)
	default:
		summary.Skipped++
	}
}

func applyMailboxGhostDelete(ctx context.Context, client mailboxCleanupDynamo, cfg mailboxCleanupConfig, action mailboxCleanupAction, summary *mailboxCleanupSummary) {
	if cfg.apply {
		if err := deleteGhostMailboxRow(ctx, client, cfg, action.Row); err != nil {
			summary.Errors++
			fmt.Printf("warn delete ghost failed pk=%s sk=%s err=%v\n", action.Row.PK, action.Row.SK, err)
			return
		}
	} else {
		fmt.Printf("dry-run would delete ghost pk=%s sk=%s reason=%s\n", action.Row.PK, action.Row.SK, action.Reason)
	}
	summary.GhostDeletes++
}

func applyMailboxThreadUpdate(ctx context.Context, client mailboxCleanupDynamo, cfg mailboxCleanupConfig, action mailboxCleanupAction, summary *mailboxCleanupSummary) {
	if cfg.apply {
		if err := updateMailboxThread(ctx, client, cfg, action.Row, action.TargetThread); err != nil {
			summary.Errors++
			fmt.Printf("warn update thread failed delivery=%s pk=%s sk=%s targetThread=%s err=%v\n", action.Row.DeliveryID, action.Row.PK, action.Row.SK, action.TargetThread, err)
			return
		}
		events, eventErrs := updateMailboxEventThreads(ctx, client, cfg, action.Row, action.TargetThread)
		summary.EventThreadUpdates += events
		summary.Errors += eventErrs
	} else {
		fmt.Printf("dry-run would update thread delivery=%s message=%s from=%s to=%s reason=%s\n", action.Row.DeliveryID, action.Row.MessageID, action.Row.ThreadID, action.TargetThread, action.Reason)
	}
	summary.ThreadUpdates++
}

func queryMailboxRows(ctx context.Context, client mailboxCleanupDynamo, cfg mailboxCleanupConfig) ([]dynamoMailboxRow, error) {
	pk := models.SoulCommMailboxAgentPK(cfg.instanceSlug, cfg.agentID)
	var rows []dynamoMailboxRow
	var startKey map[string]types.AttributeValue
	for {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(cfg.tableName),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :skPrefix)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":       &types.AttributeValueMemberS{Value: pk},
				":skPrefix": &types.AttributeValueMemberS{Value: "MSG#"},
			},
			Limit:             aws.Int32(cfg.pageSize),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range out.Items {
			rows = append(rows, mailboxRowFromItem(item))
		}
		if len(out.LastEvaluatedKey) == 0 {
			return rows, nil
		}
		startKey = out.LastEvaluatedKey
	}
}

func planMailboxCleanup(cfg mailboxCleanupConfig, rows []dynamoMailboxRow) []mailboxCleanupAction {
	actions := make([]mailboxCleanupAction, 0)
	for _, row := range rows {
		if isGhostMailboxRow(row) {
			actions = append(actions, mailboxCleanupAction{Kind: mailboxCleanupDeleteGhost, Row: row, Reason: "missing canonical delivery/index attributes"})
		}
	}

	threadMap := splitMailboxThreadMap(cfg, rows, canonicalMailboxThreadsByRoot(cfg, rows))
	for _, row := range rows {
		canonicalThread := threadMap[row.ThreadID]
		if canonicalThread == "" || canonicalThread == row.ThreadID {
			continue
		}
		actions = append(actions, mailboxCleanupAction{Kind: mailboxCleanupUpdateThread, Row: row, TargetThread: canonicalThread, Reason: "normalize self-send split thread"})
	}

	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Kind != actions[j].Kind {
			return actions[i].Kind < actions[j].Kind
		}
		return actions[i].Row.SK < actions[j].Row.SK
	})
	return actions
}

func canonicalMailboxThreadsByRoot(cfg mailboxCleanupConfig, rows []dynamoMailboxRow) map[string]string {
	canonicalByRoot := map[string]string{}
	for _, row := range rows {
		if !strings.EqualFold(row.Direction, models.SoulCommDirectionOutbound) || !isBareHostMessageRoot(row.MessageID) {
			continue
		}
		canonicalThread := models.SoulCommMailboxThreadID(cfg.instanceSlug, cfg.agentID, row.ChannelType, row.MessageID)
		if strings.TrimSpace(row.ThreadID) == canonicalThread {
			canonicalByRoot[row.MessageID] = canonicalThread
		}
	}
	return canonicalByRoot
}

func splitMailboxThreadMap(_ mailboxCleanupConfig, rows []dynamoMailboxRow, canonicalByRoot map[string]string) map[string]string {
	threadMap := map[string]string{}
	for _, row := range rows {
		if isGhostMailboxRow(row) || !strings.EqualFold(row.Direction, models.SoulCommDirectionInbound) {
			continue
		}
		root := models.SoulCommMailboxCanonicalThreadRoot(row.MessageID)
		if root == "" || root == strings.TrimSpace(row.MessageID) {
			continue
		}
		canonicalThread := canonicalByRoot[root]
		if canonicalThread == "" || canonicalThread == row.ThreadID {
			continue
		}
		if existing := threadMap[row.ThreadID]; existing != "" && existing != canonicalThread {
			continue
		}
		threadMap[row.ThreadID] = canonicalThread
	}
	return threadMap
}

func isGhostMailboxRow(row dynamoMailboxRow) bool {
	return strings.HasPrefix(strings.TrimSpace(row.SK), "MSG#") &&
		strings.TrimSpace(row.DeliveryID) == "" &&
		strings.TrimSpace(row.MessageID) == "" &&
		strings.TrimSpace(row.GSI1PK) == "" &&
		strings.TrimSpace(row.GSI2PK) == ""
}

func isBareHostMessageRoot(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(strings.ToLower(value), "comm-msg-") && !strings.Contains(value, "@")
}

func deleteGhostMailboxRow(ctx context.Context, client mailboxCleanupDynamo, cfg mailboxCleanupConfig, row dynamoMailboxRow) error {
	_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(cfg.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: row.PK},
			"SK": &types.AttributeValueMemberS{Value: row.SK},
		},
		ConditionExpression: aws.String("attribute_not_exists(deliveryId) AND attribute_not_exists(gsi1PK) AND attribute_not_exists(gsi2PK)"),
	})
	return err
}

func updateMailboxThread(ctx context.Context, client mailboxCleanupDynamo, cfg mailboxCleanupConfig, row dynamoMailboxRow, targetThread string) error {
	return updateThreadFields(ctx, client, cfg.tableName, row.PK, row.SK, targetThread, models.SoulCommMailboxThreadPK(cfg.instanceSlug, cfg.agentID, targetThread), row.SK, "attribute_exists(deliveryId) AND attribute_exists(gsi1PK)", true)
}

func updateMailboxEventThreads(ctx context.Context, client mailboxCleanupDynamo, cfg mailboxCleanupConfig, row dynamoMailboxRow, targetThread string) (updated int, errs int) {
	if strings.TrimSpace(row.DeliveryID) == "" {
		return 0, 0
	}
	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(cfg.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :skPrefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":       &types.AttributeValueMemberS{Value: models.SoulCommMailboxDeliveryPK(row.DeliveryID)},
			":skPrefix": &types.AttributeValueMemberS{Value: "EVENT#"},
		},
	})
	if err != nil {
		fmt.Printf("warn query events failed delivery=%s err=%v\n", row.DeliveryID, err)
		return 0, 1
	}
	for _, item := range out.Items {
		event := mailboxRowFromItem(item)
		if strings.TrimSpace(event.ThreadID) != strings.TrimSpace(row.ThreadID) && strings.TrimSpace(event.GSI2PK) == models.SoulCommMailboxThreadPK(cfg.instanceSlug, cfg.agentID, targetThread) {
			continue
		}
		if err := updateThreadFields(ctx, client, cfg.tableName, event.PK, event.SK, targetThread, models.SoulCommMailboxThreadPK(cfg.instanceSlug, cfg.agentID, targetThread), event.SK, "attribute_exists(eventId) AND attribute_exists(deliveryId)", false); err != nil {
			fmt.Printf("warn update event thread failed delivery=%s sk=%s err=%v\n", row.DeliveryID, event.SK, err)
			errs++
			continue
		}
		updated++
	}
	return updated, errs
}

func updateThreadFields(ctx context.Context, client mailboxCleanupDynamo, tableName string, pk string, sk string, threadID string, gsi2PK string, gsi2SK string, condition string, touchUpdatedAt bool) error {
	updateExpression := "SET threadId = :threadId, gsi2PK = :gsi2PK, gsi2SK = :gsi2SK"
	values := map[string]types.AttributeValue{
		":threadId": &types.AttributeValueMemberS{Value: threadID},
		":gsi2PK":   &types.AttributeValueMemberS{Value: gsi2PK},
		":gsi2SK":   &types.AttributeValueMemberS{Value: gsi2SK},
	}
	if touchUpdatedAt {
		updateExpression += ", updatedAt = :updatedAt"
		values[":updatedAt"] = &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)}
	}
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression:          aws.String(updateExpression),
		ConditionExpression:       aws.String(condition),
		ExpressionAttributeValues: values,
	})
	return err
}

func mailboxRowFromItem(item map[string]types.AttributeValue) dynamoMailboxRow {
	return dynamoMailboxRow{
		PK:           attrString(item, "PK"),
		SK:           attrString(item, "SK"),
		DeliveryID:   attrString(item, "deliveryId"),
		MessageID:    attrString(item, "messageId"),
		ThreadID:     attrString(item, "threadId"),
		InstanceSlug: attrString(item, "instanceSlug"),
		AgentID:      attrString(item, "agentId"),
		Direction:    attrString(item, "direction"),
		ChannelType:  attrString(item, "channelType"),
		GSI1PK:       attrString(item, "gsi1PK"),
		GSI1SK:       attrString(item, "gsi1SK"),
		GSI2PK:       attrString(item, "gsi2PK"),
		GSI2SK:       attrString(item, "gsi2SK"),
	}
}

func attrString(item map[string]types.AttributeValue, key string) string {
	if item == nil {
		return ""
	}
	if value, ok := item[key].(*types.AttributeValueMemberS); ok {
		return strings.TrimSpace(value.Value)
	}
	return ""
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
