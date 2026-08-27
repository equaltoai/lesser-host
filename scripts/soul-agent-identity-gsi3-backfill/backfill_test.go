package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// fakeDDB is an in-memory ddbAPI double for tool tests.
type fakeDDB struct {
	describeTable func(context.Context, *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error)
	scan          func(context.Context, *dynamodb.ScanInput) (*dynamodb.ScanOutput, error)
	updateItem    func(context.Context, *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error)
	putItem       func(context.Context, *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error)

	scanCalls   []*dynamodb.ScanInput
	updateCalls []*dynamodb.UpdateItemInput
	putCalls    []*dynamodb.PutItemInput
}

func (f *fakeDDB) DescribeTable(ctx context.Context, in *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	return f.describeTable(ctx, in)
}

func (f *fakeDDB) Scan(ctx context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	f.scanCalls = append(f.scanCalls, in)
	return f.scan(ctx, in)
}

func (f *fakeDDB) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateCalls = append(f.updateCalls, in)
	return f.updateItem(ctx, in)
}

func (f *fakeDDB) PutItem(ctx context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putCalls = append(f.putCalls, in)
	return f.putItem(ctx, in)
}

func activeTable() *dynamodb.DescribeTableOutput {
	return &dynamodb.DescribeTableOutput{
		Table: &types.TableDescription{
			TableName: aws.String("lesser-host-lab-state"),
			GlobalSecondaryIndexes: []types.GlobalSecondaryIndexDescription{
				{
					IndexName:   aws.String("gsi1"),
					IndexStatus: types.IndexStatusActive,
				},
				{
					IndexName:   aws.String("gsi2"),
					IndexStatus: types.IndexStatusActive,
				},
				{
					IndexName:   aws.String("gsi3"),
					IndexStatus: types.IndexStatusActive,
				},
				{
					IndexName:   aws.String("gsi4"),
					IndexStatus: types.IndexStatusActive,
				},
			},
		},
	}
}

// identityItem builds a SoulAgentIdentity scan row (SK=IDENTITY).
func identityItem(pk, agentID, status string, havePK, haveSK string) map[string]types.AttributeValue {
	it := map[string]types.AttributeValue{
		"PK":      &types.AttributeValueMemberS{Value: pk},
		"SK":      &types.AttributeValueMemberS{Value: "IDENTITY"},
		"agentId": &types.AttributeValueMemberS{Value: agentID},
		"status":  &types.AttributeValueMemberS{Value: status},
	}
	if havePK != "" {
		it[attrGsi3PK] = &types.AttributeValueMemberS{Value: havePK}
	}
	if haveSK != "" {
		it[attrGsi3SK] = &types.AttributeValueMemberS{Value: haveSK}
	}
	return it
}

// mintConvItem builds a SoulAgentMintConversation scan row
// (SK=MINT_CONVERSATION#<conv>). createdAtRaw is stored RFC3339Nano, the
// TableTheory time.Time encoding.
func mintConvItem(pk, agentID, conversationID, createdAtRaw string, havePK, haveSK string) map[string]types.AttributeValue {
	it := map[string]types.AttributeValue{
		"PK":             &types.AttributeValueMemberS{Value: pk},
		"SK":             &types.AttributeValueMemberS{Value: "MINT_CONVERSATION#" + conversationID},
		"agentId":        &types.AttributeValueMemberS{Value: agentID},
		"conversationId": &types.AttributeValueMemberS{Value: conversationID},
		"createdAt":      &types.AttributeValueMemberS{Value: createdAtRaw},
	}
	if havePK != "" {
		it[attrGsi4PK] = &types.AttributeValueMemberS{Value: havePK}
	}
	if haveSK != "" {
		it[attrGsi4SK] = &types.AttributeValueMemberS{Value: haveSK}
	}
	return it
}

func defaultOpt() options {
	return options{
		stage:      "lab",
		region:     "us-east-1",
		table:      "lesser-host-lab-state",
		checkpoint: "soul-agent-identity-gsi3-backfill.test.checkpoint.json",
		apply:      false,
		pageSize:   100,
		sleepMS:    0,
	}
}

func noSleep(time.Duration) {}

// freshCheckpoint builds a version-2 checkpoint with per-model counters for the
// identity and mint-conversation plans.
func freshCheckpoint(mode, stage, table string) checkpoint {
	ckpt := checkpoint{
		Version: checkpointVersion,
		Mode:    mode,
		Stage:   stage,
		Table:   table,
		Models:  make(map[string]*modelCheckpoint),
	}
	for _, plan := range backfillPlans() {
		ckpt.Models[plan.name] = &modelCheckpoint{}
	}
	return ckpt
}

// modelReportByName returns the report for a plan by model name.
func modelReportByName(t *testing.T, rep *report, name string) *modelReport {
	t.Helper()
	model, ok := rep.Models[name]
	if !ok || model == nil {
		t.Fatalf("missing model report for %s: %#v", name, rep.Models)
	}
	return model
}

func TestRun_PreflightRefusesMissingGSI(t *testing.T) {
	t.Parallel()

	t.Run("gsi3 missing", func(t *testing.T) {
		t.Parallel()
		ddb := &fakeDDB{
			describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
				return &dynamodb.DescribeTableOutput{Table: &types.TableDescription{GlobalSecondaryIndexes: nil}}, nil
			},
		}
		var out bytes.Buffer
		_, err := run(context.Background(), defaultOpt(), ddb, &out, noSleep)
		require.Error(t, err)
		require.Contains(t, err.Error(), "preflight refused")
		require.Contains(t, err.Error(), "SoulAgentIdentity (gsi3) does not exist")
		require.Empty(t, ddb.scanCalls, "no scan should run when preflight refuses")
	})

	t.Run("gsi4 missing (part C2 deploy not run)", func(t *testing.T) {
		t.Parallel()
		ddb := &fakeDDB{
			describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
				out := activeTable()
				out.Table.GlobalSecondaryIndexes = out.Table.GlobalSecondaryIndexes[:3] // drop gsi4
				return out, nil
			},
		}
		var out bytes.Buffer
		_, err := run(context.Background(), defaultOpt(), ddb, &out, noSleep)
		require.Error(t, err)
		require.Contains(t, err.Error(), "preflight refused")
		require.Contains(t, err.Error(), "SoulAgentMintConversation (gsi4) does not exist")
		require.Contains(t, err.Error(), "one GSI per deploy")
		require.Empty(t, ddb.scanCalls, "no scan should run when preflight refuses")
	})
}

func TestRun_PreflightRefusesIndexNotActive(t *testing.T) {
	t.Parallel()
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			out := activeTable()
			out.Table.GlobalSecondaryIndexes[3].IndexStatus = types.IndexStatusCreating
			return out, nil
		},
	}
	var out bytes.Buffer
	_, err := run(context.Background(), defaultOpt(), ddb, &out, noSleep)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SoulAgentMintConversation (gsi4)")
	require.Contains(t, err.Error(), "not ACTIVE")
	require.Empty(t, ddb.scanCalls)
}

func TestRun_DryRunPurity(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"
	agentC := "0x00000000000000000000000000000000000000000000000000000000000000cc"
	createdAt := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
					identityItem("SOUL#AGENT#"+agentB, agentB, "active", "", ""),
					identityItem("SOUL#AGENT#"+agentC, agentC, "active", "IDENTITY#active", agentC),
					mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", createdAt, "", ""),
					mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-2", createdAt, models.SoulMintConversationGSI4PK(agentA), models.SoulMintConversationGSI4SK(time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC), "conv-2")),
				},
			}, nil
		},
		updateItem: func(_ context.Context, _ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return &dynamodb.UpdateItemOutput{}, nil
		},
		putItem: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}
	opt := defaultOpt()
	opt.checkpoint = filepath.Join(t.TempDir(), "ckpt.json")

	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, noSleep)
	require.NoError(t, err)

	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(3), idReport.Scanned)
	require.Equal(t, int64(2), idReport.Updated)
	require.Equal(t, int64(1), idReport.AlreadyCorrect)
	require.Equal(t, int64(0), idReport.Errors)
	require.Equal(t, "would-write", idReport.Marker)

	mcReport := modelReportByName(t, rep, "SoulAgentMintConversation")
	require.Equal(t, int64(2), mcReport.Scanned)
	require.Equal(t, int64(1), mcReport.Updated)
	require.Equal(t, int64(1), mcReport.AlreadyCorrect)
	require.Equal(t, int64(0), mcReport.Errors)
	require.Equal(t, "would-write", mcReport.Marker)

	require.Empty(t, ddb.updateCalls, "dry-run must never issue writes")
	require.Empty(t, ddb.putCalls, "dry-run must never write the marker")
	require.Contains(t, out.String(), "dry-run would set gsi3PK/gsi3SK on SoulAgentIdentity")
	require.Contains(t, out.String(), "dry-run would set gsi4PK/gsi4SK on SoulAgentMintConversation")
	require.NotContains(t, out.String(), "warn")
}

func TestRun_ApplyWritesAndMarkers(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"
	createdAt := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	createdAtRaw := createdAt.Format(time.RFC3339Nano)
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
					identityItem("SOUL#AGENT#"+agentB, agentB, "suspended", "", ""),
					mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", createdAtRaw, "", ""),
				},
			}, nil
		},
		updateItem: func(_ context.Context, in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			cond := aws.ToString(in.ConditionExpression)
			if containsGsiAttr(cond, attrGsi3PK) {
				require.Contains(t, cond, "attribute_not_exists("+attrGsi3PK+")")
				require.Contains(t, cond, "attribute_not_exists("+attrGsi3SK+")")
			} else {
				require.Contains(t, cond, "attribute_not_exists("+attrGsi4PK+")")
				require.Contains(t, cond, "attribute_not_exists("+attrGsi4SK+")")
			}
			return &dynamodb.UpdateItemOutput{}, nil
		},
		putItem: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}
	opt := defaultOpt()
	opt.apply = true
	opt.checkpoint = filepath.Join(t.TempDir(), "ckpt.json")

	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, noSleep)
	require.NoError(t, err)

	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(2), idReport.Updated)
	require.Equal(t, "written", idReport.Marker)
	mcReport := modelReportByName(t, rep, "SoulAgentMintConversation")
	require.Equal(t, int64(1), mcReport.Updated)
	require.Equal(t, "written", mcReport.Marker)
	require.NotEmpty(t, rep.CompletedAt)
	require.Len(t, ddb.updateCalls, 3)

	// The two gsi3 updates must carry the status-derived expected keys.
	require.Equal(t, "IDENTITY#active", avString(ddb.updateCalls[0].ExpressionAttributeValues[":gsiPk"]))
	require.Equal(t, agentA, avString(ddb.updateCalls[0].ExpressionAttributeValues[":gsiSk"]))
	require.Equal(t, "IDENTITY#suspended", avString(ddb.updateCalls[1].ExpressionAttributeValues[":gsiPk"]))
	require.Equal(t, agentB, avString(ddb.updateCalls[1].ExpressionAttributeValues[":gsiSk"]))
	// The gsi4 update must carry the createdAt-derived expected keys.
	require.Equal(t, models.SoulMintConversationGSI4PK(agentA), avString(ddb.updateCalls[2].ExpressionAttributeValues[":gsiPk"]))
	require.Equal(t, models.SoulMintConversationGSI4SK(createdAt, "conv-1"), avString(ddb.updateCalls[2].ExpressionAttributeValues[":gsiSk"]))

	// Both completeness markers are written, one per model.
	require.Len(t, ddb.putCalls, 2)
	identityMarker := ddb.putCalls[0].Item
	require.Equal(t, models.SoulAgentIdentityGSI3BackfillMarkerPK, avString(identityMarker["PK"]))
	require.Equal(t, models.SoulAgentIdentityGSI3BackfillMarkerSK, avString(identityMarker["SK"]))
	require.Equal(t, "2", avN(identityMarker["scanned"]))
	require.Equal(t, "2", avN(identityMarker["updated"]))
	require.Equal(t, "0", avN(identityMarker["alreadyCorrect"]))
	require.Equal(t, "0", avN(identityMarker["errors"]))
	require.NotEmpty(t, avString(identityMarker["completedAt"]))

	mintMarker := ddb.putCalls[1].Item
	require.Equal(t, models.SoulAgentMintConversationGSI4BackfillMarkerPK, avString(mintMarker["PK"]))
	require.Equal(t, models.SoulAgentMintConversationGSI4BackfillMarkerSK, avString(mintMarker["SK"]))
	require.Equal(t, "1", avN(mintMarker["scanned"]))
	require.Equal(t, "1", avN(mintMarker["updated"]))
}

// containsGsiAttr reports whether the condition expression references the given
// index attribute, used to route update-assertions to the right plan.
func containsGsiAttr(expr, attr string) bool {
	return bytes.Contains([]byte(expr), []byte(attr))
}

func TestRun_ApplyConditionalConflictCountsAlreadyCorrect(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
				},
			}, nil
		},
		updateItem: func(_ context.Context, _ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, &types.ConditionalCheckFailedException{Message: aws.String("conditional request failed")}
		},
		putItem: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}
	opt := defaultOpt()
	opt.apply = true
	opt.checkpoint = filepath.Join(t.TempDir(), "ckpt.json")

	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, noSleep)
	require.NoError(t, err)
	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(0), idReport.Updated)
	require.Equal(t, int64(1), idReport.AlreadyCorrect)
	require.Equal(t, int64(0), idReport.Errors)
	require.Equal(t, "written", idReport.Marker)
	require.Len(t, ddb.updateCalls, 1)
}

// TestRun_ErrorsWithholdOnlyTheAffectedModelMarker pins the per-model marker
// gating (part C2): a clean identity pass can certify the identity marker even
// while an unclassifiable mint-conversation item (missing createdAt) withholds
// the mint marker. The consumers of the affected model keep failing closed.
func TestRun_ErrorsWithholdOnlyTheAffectedModelMarker(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
					// Mint conversation with no createdAt: cannot be classified.
					mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", "", "", ""),
				},
			}, nil
		},
		updateItem: func(_ context.Context, _ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return &dynamodb.UpdateItemOutput{}, nil
		},
		putItem: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}
	opt := defaultOpt()
	opt.apply = true
	opt.checkpoint = filepath.Join(t.TempDir(), "ckpt.json")

	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, noSleep)
	require.NoError(t, err)

	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(1), idReport.Updated)
	require.Equal(t, int64(0), idReport.Errors)
	require.Equal(t, "written", idReport.Marker, "a clean identity pass certifies the identity marker")

	mcReport := modelReportByName(t, rep, "SoulAgentMintConversation")
	require.Equal(t, int64(1), mcReport.Errors)
	require.Equal(t, "not-written", mcReport.Marker, "the mint marker is withheld while mint errors remain")

	// Only the identity marker is written.
	require.Len(t, ddb.putCalls, 1)
	require.Equal(t, models.SoulAgentIdentityGSI3BackfillMarkerPK, avString(ddb.putCalls[0].Item["PK"]))
	require.Contains(t, out.String(), "marker NOT written for SoulAgentMintConversation")
	require.Contains(t, out.String(), "missing agentId/conversationId/createdAt")
}

func TestRun_CheckpointResume(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"
	ckptPath := filepath.Join(t.TempDir(), "ckpt.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	page1 := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "SOUL#AGENT#" + agentA},
		"SK": &types.AttributeValueMemberS{Value: "IDENTITY"},
	}
	firstScan := true
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(ctx context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			if firstScan {
				firstScan = false
				cancel() // interrupt after the first page is processed
				return &dynamodb.ScanOutput{
					Items: []map[string]types.AttributeValue{
						identityItem("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
					},
					LastEvaluatedKey: page1,
				}, nil
			}
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentB, agentB, "active", "", ""),
				},
			}, nil
		},
		updateItem: func(_ context.Context, _ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return &dynamodb.UpdateItemOutput{}, nil
		},
		putItem: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	opt := defaultOpt()
	opt.apply = true
	opt.checkpoint = ckptPath

	var out1 bytes.Buffer
	_, err := run(ctx, opt, ddb, &out1, noSleep)
	require.Error(t, err)
	require.Contains(t, err.Error(), "interrupted")
	require.Contains(t, err.Error(), "--resume")
	require.FileExists(t, ckptPath)

	// Resume: the second scan must continue from the persisted last key and the
	// counts must carry over.
	opt2 := opt
	opt2.resume = true
	var out2 bytes.Buffer
	rep2, err := run(context.Background(), opt2, ddb, &out2, noSleep)
	require.NoError(t, err)
	idReport := modelReportByName(t, rep2, "SoulAgentIdentity")
	require.Equal(t, int64(2), idReport.Scanned)
	require.Equal(t, int64(2), idReport.Updated)
	require.Equal(t, "written", idReport.Marker)
	require.Len(t, ddb.scanCalls, 2)
	resumed := ddb.scanCalls[1]
	require.NotNil(t, resumed.ExclusiveStartKey)
	require.Equal(t, avString(page1["PK"]), avString(resumed.ExclusiveStartKey["PK"]))
	require.Equal(t, avString(page1["SK"]), avString(resumed.ExclusiveStartKey["SK"]))
	require.NoFileExists(t, ckptPath, "checkpoint is removed on completion")
}

func TestRun_ResumeRequiresExistingCheckpoint(t *testing.T) {
	t.Parallel()
	opt := defaultOpt()
	opt.resume = true
	opt.checkpoint = filepath.Join(t.TempDir(), "missing.json")
	var out bytes.Buffer
	_, err := run(context.Background(), opt, &fakeDDB{}, &out, noSleep)
	require.Error(t, err)
	require.Contains(t, err.Error(), "resume checkpoint")
}

func TestRun_ResumeRefusesCrossMode(t *testing.T) {
	t.Parallel()
	// An interrupted dry-run checkpoint must never be resumed with --apply:
	// that would skip every pre-checkpoint item and could certify a partial
	// backfill with the completeness marker.
	ckptPath := filepath.Join(t.TempDir(), "ckpt.json")
	ckpt := freshCheckpoint("dry-run", "lab", "lesser-host-lab-state")
	ckpt.LastPK = "SOUL#AGENT#0xaa"
	ckpt.LastSK = "IDENTITY"
	ckpt.Models["SoulAgentIdentity"].Scanned = 1
	require.NoError(t, saveCheckpoint(ckptPath, ckpt))

	opt := defaultOpt()
	opt.apply = true
	opt.resume = true
	opt.checkpoint = ckptPath
	var out bytes.Buffer
	_, err := run(context.Background(), opt, &fakeDDB{}, &out, noSleep)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cross-mode")
	require.Contains(t, err.Error(), "dry-run")
	require.Contains(t, err.Error(), "apply")
	require.Contains(t, err.Error(), "delete the checkpoint")
	require.Empty(t, out.String(), "no resume progress should be printed before the refusal")
}

func TestRun_ResumeRefusesLegacyFormat(t *testing.T) {
	t.Parallel()
	// A v1 checkpoint (identity-only flat counters, no per-model state) must not
	// be resumed by the dual-model tool: it could certify a partial run.
	ckptPath := filepath.Join(t.TempDir(), "ckpt.json")
	ckpt := checkpoint{
		Version: 1,
		Mode:    "apply",
		Stage:   "lab",
		Table:   "lesser-host-lab-state",
		LastPK:  "SOUL#AGENT#0xaa",
		LastSK:  "IDENTITY",
		Models:  map[string]*modelCheckpoint{"SoulAgentIdentity": {Scanned: 5}},
	}
	require.NoError(t, saveCheckpoint(ckptPath, ckpt))

	opt := defaultOpt()
	opt.apply = true
	opt.resume = true
	opt.checkpoint = ckptPath
	var out bytes.Buffer
	_, err := run(context.Background(), opt, &fakeDDB{}, &out, noSleep)
	require.Error(t, err)
	require.Contains(t, err.Error(), "format v1")
	require.Contains(t, err.Error(), "per-model state")
}

func TestRun_ResumeSameModeStillWorks(t *testing.T) {
	t.Parallel()
	// Same-mode resume must keep working: a dry-run checkpoint resumed as a
	// dry run continues from the persisted key instead of restarting.
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"
	ckptPath := filepath.Join(t.TempDir(), "ckpt.json")
	ckpt := freshCheckpoint("dry-run", "lab", "lesser-host-lab-state")
	ckpt.LastPK = "SOUL#AGENT#" + agentA
	ckpt.LastSK = "IDENTITY"
	ckpt.Models["SoulAgentIdentity"].Scanned = 1
	require.NoError(t, saveCheckpoint(ckptPath, ckpt))

	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			require.NotNil(t, in.ExclusiveStartKey, "resumed scan must continue from the persisted last key")
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentB, agentB, "active", "", ""),
				},
			}, nil
		},
	}
	opt := defaultOpt()
	opt.resume = true
	opt.checkpoint = ckptPath
	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, noSleep)
	require.NoError(t, err)
	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(2), idReport.Scanned, "scanned count must carry over from the checkpoint")
	require.Equal(t, "would-write", idReport.Marker)
}

func TestRun_ResumeRefusesStageTableMismatch(t *testing.T) {
	t.Parallel()

	t.Run("stage mismatch", func(t *testing.T) {
		t.Parallel()
		ckptPath := filepath.Join(t.TempDir(), "ckpt.json")
		require.NoError(t, saveCheckpoint(ckptPath, freshCheckpoint("dry-run", "live", "lesser-host-live-state")))
		opt := defaultOpt() // stage lab
		opt.resume = true
		opt.checkpoint = ckptPath
		var out bytes.Buffer
		_, err := run(context.Background(), opt, &fakeDDB{}, &out, noSleep)
		require.Error(t, err)
		require.Contains(t, err.Error(), "stage")
		require.Contains(t, err.Error(), "delete the checkpoint")
	})

	t.Run("table mismatch", func(t *testing.T) {
		t.Parallel()
		ckptPath := filepath.Join(t.TempDir(), "ckpt.json")
		require.NoError(t, saveCheckpoint(ckptPath, freshCheckpoint("dry-run", "lab", "some-other-table")))
		opt := defaultOpt() // table lesser-host-lab-state
		opt.resume = true
		opt.checkpoint = ckptPath
		var out bytes.Buffer
		_, err := run(context.Background(), opt, &fakeDDB{}, &out, noSleep)
		require.Error(t, err)
		require.Contains(t, err.Error(), "table")
		require.Contains(t, err.Error(), "delete the checkpoint")
	})
}

func TestRun_ApplyRepairsStaleKeys(t *testing.T) {
	t.Parallel()
	// An item whose index attributes are present but stale must be REPAIRED
	// with a write bound to the observed stale values — never counted
	// already-correct via the attribute_not_exists conditional. Covered for both
	// models.
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	createdAt := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	createdAtRaw := createdAt.Format(time.RFC3339Nano)
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					// status=active but gsi3PK still says IDENTITY#pending: stale.
					identityItem("SOUL#AGENT#"+agentA, agentA, "active", "IDENTITY#pending", agentA),
					// createdAt unchanged but gsi4SK points at a wrong timestamp: stale.
					mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", createdAtRaw,
						models.SoulMintConversationGSI4PK(agentA), "2020-01-01T00:00:00.000000000Z#conv-1"),
				},
			}, nil
		},
		updateItem: func(_ context.Context, in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			cond := aws.ToString(in.ConditionExpression)
			require.NotContains(t, cond, "attribute_not_exists", "stale-key repair must not use the absent-keys conditional")
			if containsGsiAttr(cond, attrGsi3PK) {
				require.Contains(t, cond, attrGsi3PK+" = :obsGsiPk")
				require.Contains(t, cond, attrGsi3SK+" = :obsGsiSk")
				require.Equal(t, "IDENTITY#pending", avString(in.ExpressionAttributeValues[":obsGsiPk"]), "repair must bind to the observed stale gsi3PK")
				require.Equal(t, agentA, avString(in.ExpressionAttributeValues[":obsGsiSk"]), "repair must bind to the observed stale gsi3SK")
				require.Equal(t, "IDENTITY#active", avString(in.ExpressionAttributeValues[":gsiPk"]))
			} else {
				require.Contains(t, cond, attrGsi4PK+" = :obsGsiPk")
				require.Contains(t, cond, attrGsi4SK+" = :obsGsiSk")
				require.Equal(t, models.SoulMintConversationGSI4PK(agentA), avString(in.ExpressionAttributeValues[":obsGsiPk"]))
				require.Equal(t, "2020-01-01T00:00:00.000000000Z#conv-1", avString(in.ExpressionAttributeValues[":obsGsiSk"]))
				require.Equal(t, models.SoulMintConversationGSI4SK(createdAt, "conv-1"), avString(in.ExpressionAttributeValues[":gsiSk"]))
			}
			return &dynamodb.UpdateItemOutput{}, nil
		},
		putItem: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}
	opt := defaultOpt()
	opt.apply = true
	opt.checkpoint = filepath.Join(t.TempDir(), "ckpt.json")

	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, noSleep)
	require.NoError(t, err)
	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(1), idReport.Repaired)
	require.Equal(t, int64(0), idReport.AlreadyCorrect, "stale item must never be counted already-correct")
	require.Equal(t, int64(0), idReport.Errors)
	require.Equal(t, "written", idReport.Marker)
	mcReport := modelReportByName(t, rep, "SoulAgentMintConversation")
	require.Equal(t, int64(1), mcReport.Repaired)
	require.Equal(t, int64(0), mcReport.Errors)
	require.Equal(t, "written", mcReport.Marker)
	require.Len(t, ddb.updateCalls, 2)
	require.Len(t, ddb.putCalls, 2)
	require.Equal(t, "1", avN(ddb.putCalls[0].Item["repaired"]))
	require.Equal(t, "1", avN(ddb.putCalls[1].Item["repaired"]))
}

func TestRun_ApplyRepairConditionalFailureBlocksMarker(t *testing.T) {
	t.Parallel()
	// If the repair write fails its condition (a concurrent writer changed the
	// keys between scan and repair), the item cannot be certified — count an
	// error and withhold the marker.
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentA, agentA, "active", "IDENTITY#pending", agentA),
				},
			}, nil
		},
		updateItem: func(_ context.Context, _ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, &types.ConditionalCheckFailedException{Message: aws.String("conditional request failed")}
		},
		putItem: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}
	opt := defaultOpt()
	opt.apply = true
	opt.checkpoint = filepath.Join(t.TempDir(), "ckpt.json")

	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, noSleep)
	require.NoError(t, err)
	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(0), idReport.Repaired)
	require.Equal(t, int64(1), idReport.Errors)
	require.Equal(t, "not-written", idReport.Marker)
	// The affected model's marker is withheld; the other model's clean pass
	// still certifies its own marker (per-model gating).
	require.Len(t, ddb.putCalls, 1)
	require.Equal(t, models.SoulAgentMintConversationGSI4BackfillMarkerPK, avString(ddb.putCalls[0].Item["PK"]))
	require.Contains(t, out.String(), "SoulAgentIdentity repair failed")
	require.Contains(t, out.String(), "marker NOT written")
}

func TestRun_ApplyAlreadyCorrectOnlyForCorrectItems(t *testing.T) {
	t.Parallel()
	// already_correct must be reserved for truly-correct items (both index
	// attributes already match the computed keys) — never for stale ones.
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	createdAt := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentA, agentA, "active", "IDENTITY#active", agentA),
					mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", createdAt.Format(time.RFC3339Nano),
						models.SoulMintConversationGSI4PK(agentA), models.SoulMintConversationGSI4SK(createdAt, "conv-1")),
				},
			}, nil
		},
		updateItem: func(_ context.Context, _ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return &dynamodb.UpdateItemOutput{}, nil
		},
		putItem: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}
	opt := defaultOpt()
	opt.apply = true
	opt.checkpoint = filepath.Join(t.TempDir(), "ckpt.json")

	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, noSleep)
	require.NoError(t, err)
	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(1), idReport.AlreadyCorrect)
	require.Equal(t, int64(0), idReport.Updated)
	require.Equal(t, int64(0), idReport.Repaired)
	require.Equal(t, int64(0), idReport.Errors)
	require.Equal(t, "written", idReport.Marker)
	mcReport := modelReportByName(t, rep, "SoulAgentMintConversation")
	require.Equal(t, int64(1), mcReport.AlreadyCorrect)
	require.Equal(t, "written", mcReport.Marker)
	require.Empty(t, ddb.updateCalls, "a truly-correct item must not trigger any write")
}

func TestRun_ThrottleSleepInvokedBetweenPages(t *testing.T) {
	t.Parallel()
	// The inter-page throttle sleep must actually be invoked; removing it would
	// silently remove scan throttling. The spy fails the test if run() stops
	// calling sleep between pages.
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"
	page1Key := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "SOUL#AGENT#" + agentA},
		"SK": &types.AttributeValueMemberS{Value: "IDENTITY"},
	}
	pageNum := 0
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			pageNum++
			if pageNum == 1 {
				return &dynamodb.ScanOutput{
					Items: []map[string]types.AttributeValue{
						identityItem("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
					},
					LastEvaluatedKey: page1Key,
				}, nil
			}
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					identityItem("SOUL#AGENT#"+agentB, agentB, "active", "", ""),
				},
			}, nil
		},
	}
	opt := defaultOpt()
	opt.checkpoint = filepath.Join(t.TempDir(), "ckpt.json")

	var slept []time.Duration
	sleepSpy := func(d time.Duration) { slept = append(slept, d) }

	var out bytes.Buffer
	rep, err := run(context.Background(), opt, ddb, &out, sleepSpy)
	require.NoError(t, err)
	idReport := modelReportByName(t, rep, "SoulAgentIdentity")
	require.Equal(t, int64(2), idReport.Scanned)
	require.NotEmpty(t, slept, "inter-page throttle sleep must be invoked between scan pages")
}

func TestClassifyIdentityItem(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"

	t.Run("needs write when absent", func(t *testing.T) {
		t.Parallel()
		it := identityItem("SOUL#AGENT#"+agentA, agentA, "Active", "", "")
		pk, sk, need, err := classifyIdentityItem(it)
		require.NoError(t, err)
		require.True(t, need)
		require.Equal(t, "IDENTITY#active", pk)
		require.Equal(t, agentA, sk)
	})

	t.Run("already correct", func(t *testing.T) {
		t.Parallel()
		it := identityItem("SOUL#AGENT#"+agentA, agentA, "active", "IDENTITY#active", agentA)
		_, _, need, err := classifyIdentityItem(it)
		require.NoError(t, err)
		require.False(t, need)
	})

	t.Run("stale keys need rewrite", func(t *testing.T) {
		t.Parallel()
		it := identityItem("SOUL#AGENT#"+agentA, agentA, "active", "IDENTITY#pending", agentA)
		_, _, need, err := classifyIdentityItem(it)
		require.NoError(t, err)
		require.True(t, need)
	})

	t.Run("missing status is an error", func(t *testing.T) {
		t.Parallel()
		it := identityItem("SOUL#AGENT#"+agentA, agentA, "", "", "")
		_, _, _, err := classifyIdentityItem(it)
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing agentId/status")
	})
}

func TestClassifyMintConversationItem(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	createdAt := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)

	t.Run("needs write when absent", func(t *testing.T) {
		t.Parallel()
		it := mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", createdAt.Format(time.RFC3339Nano), "", "")
		pk, sk, need, err := classifyMintConversationItem(it)
		require.NoError(t, err)
		require.True(t, need)
		require.Equal(t, models.SoulMintConversationGSI4PK(agentA), pk)
		require.Equal(t, models.SoulMintConversationGSI4SK(createdAt, "conv-1"), sk)
	})

	t.Run("already correct", func(t *testing.T) {
		t.Parallel()
		it := mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", createdAt.Format(time.RFC3339Nano),
			models.SoulMintConversationGSI4PK(agentA), models.SoulMintConversationGSI4SK(createdAt, "conv-1"))
		_, _, need, err := classifyMintConversationItem(it)
		require.NoError(t, err)
		require.False(t, need)
	})

	t.Run("stale keys need rewrite", func(t *testing.T) {
		t.Parallel()
		it := mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", createdAt.Format(time.RFC3339Nano),
			models.SoulMintConversationGSI4PK(agentA), "2020-01-01T00:00:00.000000000Z#conv-1")
		_, _, need, err := classifyMintConversationItem(it)
		require.NoError(t, err)
		require.True(t, need)
	})

	t.Run("missing createdAt is an error", func(t *testing.T) {
		t.Parallel()
		it := mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", "", "", "")
		_, _, _, err := classifyMintConversationItem(it)
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing agentId/conversationId/createdAt")
	})

	t.Run("unparseable createdAt is an error", func(t *testing.T) {
		t.Parallel()
		it := mintConvItem("SOUL#AGENT#"+agentA, agentA, "conv-1", "not-a-time", "", "")
		_, _, _, err := classifyMintConversationItem(it)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not an RFC3339 timestamp")
	})
}

func TestPlanForItemRoutesBySK(t *testing.T) {
	t.Parallel()
	plans := backfillPlans()

	plan, ok := planForItem(plans, identityItem("SOUL#AGENT#0xaa", "0xaa", "active", "", ""))
	require.True(t, ok)
	require.Equal(t, "SoulAgentIdentity", plan.name)

	plan, ok = planForItem(plans, mintConvItem("SOUL#AGENT#0xaa", "0xaa", "conv-1", "2026-03-07T12:00:00Z", "", ""))
	require.True(t, ok)
	require.Equal(t, "SoulAgentMintConversation", plan.name)

	_, ok = planForItem(plans, map[string]types.AttributeValue{"SK": &types.AttributeValueMemberS{Value: "SOME#OTHER"}})
	require.False(t, ok)
}

func TestParseArgs(t *testing.T) {
	t.Run("stage required", func(t *testing.T) {
		t.Parallel()
		_, err := parseArgs([]string{"--profile", "p"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "--stage")
	})

	t.Run("stage must be lab or live", func(t *testing.T) {
		t.Parallel()
		_, err := parseArgs([]string{"--stage", "prod"})
		require.Error(t, err)
	})

	t.Run("resolves table and defaults", func(t *testing.T) {
		t.Parallel()
		opt, err := parseArgs([]string{"--stage", "live", "--apply", "--page-size", "999", "--sleep-ms", "500"})
		require.NoError(t, err)
		require.Equal(t, "live", opt.stage)
		require.Equal(t, "lesser-host-live-state", opt.table)
		require.True(t, opt.apply)
		require.Equal(t, maxPageSize, opt.pageSize)
		require.Equal(t, 500, opt.sleepMS)
		require.Equal(t, "soul-agent-identity-gsi3-backfill.live.checkpoint.json", opt.checkpoint)
		require.Equal(t, defaultRegion, opt.region)
	})

	t.Run("profile flag overrides env", func(t *testing.T) {
		t.Setenv("AWS_PROFILE", "from-env")
		opt, err := parseArgs([]string{"--stage", "lab", "--profile", "from-flag"})
		require.NoError(t, err)
		require.Equal(t, "from-flag", opt.profile)
	})

	t.Run("profile falls back to env", func(t *testing.T) {
		t.Setenv("AWS_PROFILE", "from-env")
		opt, err := parseArgs([]string{"--stage", "lab"})
		require.NoError(t, err)
		require.Equal(t, "from-env", opt.profile)
	})
}

func TestThrottleDurationRange(t *testing.T) {
	t.Parallel()
	for i := 0; i < 200; i++ {
		d := throttleDuration(100)
		if d < 100*time.Millisecond || d > 150*time.Millisecond {
			t.Fatalf("throttleDuration(100) = %v, want [100ms, 150ms]", d)
		}
	}
}

func avN(v types.AttributeValue) string {
	if m, ok := v.(*types.AttributeValueMemberN); ok {
		return m.Value
	}
	return ""
}

func TestReportString_PerModel(t *testing.T) {
	t.Parallel()
	rep := report{Models: map[string]*modelReport{
		"SoulAgentIdentity":         {Scanned: 5, Updated: 3, Repaired: 1, AlreadyCorrect: 1, Errors: 0, Marker: "written"},
		"SoulAgentMintConversation": {Scanned: 2, Updated: 1, Repaired: 0, AlreadyCorrect: 1, Errors: 0, Marker: "written"},
	}, CompletedAt: "2026-08-27T00:00:00Z"}
	s := rep.String()
	for _, want := range []string{
		"SoulAgentIdentity scanned=5 updated=3 repaired=1 already_correct=1 errors=0 marker=written",
		"SoulAgentMintConversation scanned=2 updated=1 repaired=0 already_correct=1 errors=0 marker=written",
		"completed_at=2026-08-27T00:00:00Z",
	} {
		require.Contains(t, s, want)
	}
}
