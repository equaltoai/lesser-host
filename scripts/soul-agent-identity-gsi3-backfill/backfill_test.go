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
			},
		},
	}
}

func item(pk, agentID, status string, havePK, haveSK string) map[string]types.AttributeValue {
	it := map[string]types.AttributeValue{
		"PK":      &types.AttributeValueMemberS{Value: pk},
		"SK":      &types.AttributeValueMemberS{Value: "IDENTITY"},
		"agentId": &types.AttributeValueMemberS{Value: agentID},
		"status":  &types.AttributeValueMemberS{Value: status},
	}
	if havePK != "" {
		it["gsi3PK"] = &types.AttributeValueMemberS{Value: havePK}
	}
	if haveSK != "" {
		it["gsi3SK"] = &types.AttributeValueMemberS{Value: haveSK}
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

func TestRun_PreflightRefusesMissingGSI(t *testing.T) {
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
	require.Contains(t, err.Error(), "gsi3 does not exist")
	require.Empty(t, ddb.scanCalls, "no scan should run when preflight refuses")
}

func TestRun_PreflightRefusesIndexNotActive(t *testing.T) {
	t.Parallel()
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			out := activeTable()
			out.Table.GlobalSecondaryIndexes[2].IndexStatus = types.IndexStatusCreating
			return out, nil
		},
	}
	var out bytes.Buffer
	_, err := run(context.Background(), defaultOpt(), ddb, &out, noSleep)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not ACTIVE")
	require.Empty(t, ddb.scanCalls)
}

func TestRun_DryRunPurity(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"
	agentC := "0x00000000000000000000000000000000000000000000000000000000000000cc"
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					item("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
					item("SOUL#AGENT#"+agentB, agentB, "active", "", ""),
					item("SOUL#AGENT#"+agentC, agentC, "active", "IDENTITY#active", agentC),
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
	require.Equal(t, int64(3), rep.Scanned)
	require.Equal(t, int64(2), rep.Updated)
	require.Equal(t, int64(1), rep.AlreadyCorrect)
	require.Equal(t, int64(0), rep.Errors)
	require.Equal(t, "would-write", rep.Marker)
	require.Empty(t, ddb.updateCalls, "dry-run must never issue writes")
	require.Empty(t, ddb.putCalls, "dry-run must never write the marker")
	require.Contains(t, out.String(), "dry-run would set gsi3")
	require.NotContains(t, out.String(), "warn")
}

func TestRun_ApplyWritesAndMarker(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					item("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
					item("SOUL#AGENT#"+agentB, agentB, "suspended", "", ""),
				},
			}, nil
		},
		updateItem: func(_ context.Context, in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			require.Contains(t, aws.ToString(in.ConditionExpression), "attribute_not_exists(gsi3PK)")
			require.Contains(t, aws.ToString(in.ConditionExpression), "attribute_not_exists(gsi3SK)")
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
	require.Equal(t, int64(2), rep.Updated)
	require.Equal(t, "written", rep.Marker)
	require.NotEmpty(t, rep.CompletedAt)
	require.Len(t, ddb.updateCalls, 2)

	// The two updates must carry the status-derived expected keys.
	require.Equal(t, "IDENTITY#active", avString(ddb.updateCalls[0].ExpressionAttributeValues[":gsi3pk"]))
	require.Equal(t, agentA, avString(ddb.updateCalls[0].ExpressionAttributeValues[":gsi3sk"]))
	require.Equal(t, "IDENTITY#suspended", avString(ddb.updateCalls[1].ExpressionAttributeValues[":gsi3pk"]))
	require.Equal(t, agentB, avString(ddb.updateCalls[1].ExpressionAttributeValues[":gsi3sk"]))

	require.Len(t, ddb.putCalls, 1)
	marker := ddb.putCalls[0].Item
	require.Equal(t, models.SoulAgentIdentityGSI3BackfillMarkerPK, avString(marker["PK"]))
	require.Equal(t, models.SoulAgentIdentityGSI3BackfillMarkerSK, avString(marker["SK"]))
	require.Equal(t, "2", avN(marker["scanned"]))
	require.Equal(t, "2", avN(marker["updated"]))
	require.Equal(t, "0", avN(marker["alreadyCorrect"]))
	require.Equal(t, "0", avN(marker["errors"]))
	require.NotEmpty(t, avString(marker["completedAt"]))
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
					item("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
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
	require.Equal(t, int64(0), rep.Updated)
	require.Equal(t, int64(1), rep.AlreadyCorrect)
	require.Equal(t, int64(0), rep.Errors)
	require.Equal(t, "written", rep.Marker)
	require.Len(t, ddb.updateCalls, 1)
}

func TestRun_ErrorsPreventMarker(t *testing.T) {
	t.Parallel()
	// An item with no status cannot be classified: counted as error, marker
	// must NOT be written so consumers keep failing closed.
	ddb := &fakeDDB{
		describeTable: func(_ context.Context, _ *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return activeTable(), nil
		},
		scan: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					{"PK": &types.AttributeValueMemberS{Value: "SOUL#AGENT#0xaa"}, "SK": &types.AttributeValueMemberS{Value: "IDENTITY"}},
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
	require.Equal(t, int64(1), rep.Errors)
	require.Equal(t, "not-written", rep.Marker)
	require.Empty(t, ddb.putCalls, "marker must not be written while errors remain")
	require.Contains(t, out.String(), "marker NOT written")
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
						item("SOUL#AGENT#"+agentA, agentA, "active", "", ""),
					},
					LastEvaluatedKey: page1,
				}, nil
			}
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{
					item("SOUL#AGENT#"+agentB, agentB, "active", "", ""),
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
	require.Equal(t, int64(2), rep2.Scanned)
	require.Equal(t, int64(2), rep2.Updated)
	require.Equal(t, "written", rep2.Marker)
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

func TestClassifyIdentityItem(t *testing.T) {
	t.Parallel()
	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"

	t.Run("needs write when absent", func(t *testing.T) {
		t.Parallel()
		it := item("SOUL#AGENT#"+agentA, agentA, "Active", "", "")
		pk, sk, need, err := classifyIdentityItem(it)
		require.NoError(t, err)
		require.True(t, need)
		require.Equal(t, "IDENTITY#active", pk)
		require.Equal(t, agentA, sk)
	})

	t.Run("already correct", func(t *testing.T) {
		t.Parallel()
		it := item("SOUL#AGENT#"+agentA, agentA, "active", "IDENTITY#active", agentA)
		_, _, need, err := classifyIdentityItem(it)
		require.NoError(t, err)
		require.False(t, need)
	})

	t.Run("stale keys need rewrite", func(t *testing.T) {
		t.Parallel()
		it := item("SOUL#AGENT#"+agentA, agentA, "active", "IDENTITY#pending", agentA)
		_, _, need, err := classifyIdentityItem(it)
		require.NoError(t, err)
		require.True(t, need)
	})

	t.Run("missing status is an error", func(t *testing.T) {
		t.Parallel()
		it := item("SOUL#AGENT#"+agentA, agentA, "", "", "")
		_, _, _, err := classifyIdentityItem(it)
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing agentId/status")
	})
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

func TestReportString(t *testing.T) {
	t.Parallel()
	rep := report{Scanned: 5, Updated: 3, AlreadyCorrect: 1, Errors: 1, Marker: "not-written"}
	s := rep.String()
	for _, want := range []string{"scanned=5", "updated=3", "already_correct=1", "errors=1", "marker=not-written"} {
		require.Contains(t, s, want)
	}
	require.NotContains(t, s, "completed_at")
}
