// Package main implements the soul-agent-identity-gsi3-backfill operator tool.
//
// It backfills the gsi3 status enumeration index attributes (gsi3PK/gsi3SK)
// onto existing SoulAgentIdentity items after the stack update that creates
// the index has been deployed (issue #1061 part C1). Dry-run by default;
// mutations only under --apply. The tool writes ONLY the two gsi3 attributes,
// via conditional updates that never clobber concurrent live writes, and
// persists a LastEvaluatedKey checkpoint so an interrupted run can resume.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	// gsi3 is the SoulAgentIdentity status enumeration index. The stack
	// update that creates it is a separate deploy; this tool must not be run
	// until the operator has deployed that update (preflight refuses otherwise).
	gsi3IndexName = "gsi3"

	identityItemSK = "IDENTITY"

	// identityScanProjection keeps reads key-only plus the attributes needed to
	// compute and verify the gsi3 keys. No full item payloads are fetched.
	identityScanProjection = "PK, SK, agentId, status, gsi3PK, gsi3SK"

	attrGsi3PK = "gsi3PK"
	attrGsi3SK = "gsi3SK"

	defaultPageSize = 100
	maxPageSize     = 200
	defaultSleepMS  = 100

	// markerDryRunSample caps the dry-run sample lines so an operator sees what
	// would change without flooding the terminal.
	markerDryRunSample = 20
)

// ddbAPI is the minimal DynamoDB surface the backfill needs. It is satisfied by
// *dynamodb.Client and mocked in tests.
type ddbAPI interface {
	DescribeTable(ctx context.Context, params *dynamodb.DescribeTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// options are the resolved CLI options for a run.
type options struct {
	stage      string
	profile    string
	region     string
	table      string
	checkpoint string
	apply      bool
	pageSize   int
	sleepMS    int
	resume     bool
}

// modelPlan is the per-model backfill contract. Part C2 (SoulAgentMintConversation
// gsi3, issue #1067) adds a second plan reusing the same scan/checkpoint/
// throttling machinery instead of duplicating it.
type modelPlan struct {
	name       string
	markerPK   string
	markerSK   string
	filter     func(*dynamodb.ScanInput)
	projection string
	classify   func(item map[string]types.AttributeValue) (gsiPK string, gsiSK string, needsWrite bool, err error)
}

// identityPlan is the SoulAgentIdentity plan (part C1).
func identityPlan() modelPlan {
	return modelPlan{
		name:     "SoulAgentIdentity",
		markerPK: models.SoulAgentIdentityGSI3BackfillMarkerPK,
		markerSK: models.SoulAgentIdentityGSI3BackfillMarkerSK,
		filter: func(in *dynamodb.ScanInput) {
			in.FilterExpression = aws.String("SK = :sk")
			in.ExpressionAttributeValues = map[string]types.AttributeValue{
				":sk": &types.AttributeValueMemberS{Value: identityItemSK},
			}
		},
		projection: identityScanProjection,
		classify:   classifyIdentityItem,
	}
}

// classifyIdentityItem computes the expected gsi3 keys for an identity item and
// reports whether the item still needs a write. A write is needed only when the
// gsi3 attributes are absent or differ from the item's current status-derived
// keys; missing agentId/status is an error (item cannot be classified).
func classifyIdentityItem(item map[string]types.AttributeValue) (string, string, bool, error) {
	agentID := strings.ToLower(strings.TrimSpace(avString(item["agentId"])))
	status := strings.ToLower(strings.TrimSpace(avString(item["status"])))
	if agentID == "" || status == "" {
		return "", "", false, fmt.Errorf("item missing agentId/status")
	}
	expectedPK := fmt.Sprintf("IDENTITY#%s", status)
	expectedSK := agentID
	havePK := avString(item[attrGsi3PK])
	haveSK := avString(item[attrGsi3SK])
	needsWrite := havePK != expectedPK || haveSK != expectedSK
	return expectedPK, expectedSK, needsWrite, nil
}

// checkpoint is the persisted resume state. It stores the last evaluated key of
// the base-table scan plus cumulative counts, so a re-run with --resume
// continues where the interrupted run stopped instead of restarting.
type checkpoint struct {
	Stage          string `json:"stage"`
	LastPK         string `json:"lastPk,omitempty"`
	LastSK         string `json:"lastSk,omitempty"`
	Scanned        int64  `json:"scanned"`
	Updated        int64  `json:"updated"`
	AlreadyCorrect int64  `json:"alreadyCorrect"`
	Errors         int64  `json:"errors"`
}

// report is the final count report — the operator's proof the backfill ran.
type report struct {
	Scanned        int64
	Updated        int64
	AlreadyCorrect int64
	Errors         int64
	Marker         string // written | would-write (dry-run) | not-written (errors)
	CompletedAt    string
	Interrupted    bool
}

func (r report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scanned=%d updated=%d already_correct=%d errors=%d marker=%s",
		r.Scanned, r.Updated, r.AlreadyCorrect, r.Errors, r.Marker)
	if r.CompletedAt != "" {
		fmt.Fprintf(&b, " completed_at=%s", r.CompletedAt)
	}
	return b.String()
}

// run executes the backfill for the resolved options. It is the testable core:
// all DynamoDB access goes through ddb, progress is written to out, and sleepFn
// (nil = real throttling sleep) is injectable for deterministic tests.
func run(ctx context.Context, opt options, ddb ddbAPI, out io.Writer, sleepFn func(time.Duration)) (*report, error) {
	if opt.table == "" {
		return nil, fmt.Errorf("table is required")
	}

	plan := identityPlan()
	ckpt, err := initCheckpoint(opt, out)
	if err != nil {
		return nil, err
	}
	if err := preflight(ctx, ddb, opt.table); err != nil {
		return nil, err
	}
	sleep := sleepFn
	if sleep == nil {
		sleep = throttleSleep
	}

	r := &report{}
	dryRunSample := 0
	for {
		if err := ctx.Err(); err != nil {
			return interruptedReport(opt, ckpt, r, err)
		}
		page, err := scanIdentityPage(ctx, opt, plan, ckpt, ddb)
		if err != nil {
			return nil, err
		}
		processIdentityItems(ctx, opt, plan, ddb, page.Items, &ckpt, &dryRunSample, out)
		lastPK, lastSK, hasMore := lastKeyFromScan(page)
		if !hasMore {
			ckpt.LastPK = ""
			ckpt.LastSK = ""
			_ = removeCheckpoint(opt.checkpoint)
			break
		}
		ckpt.LastPK = lastPK
		ckpt.LastSK = lastSK
		if saveErr := saveCheckpoint(opt.checkpoint, ckpt); saveErr != nil {
			return nil, fmt.Errorf("save checkpoint: %w", saveErr)
		}
		writef(out, "page done scanned=%d updated=%d already_correct=%d errors=%d\n",
			ckpt.Scanned, ckpt.Updated, ckpt.AlreadyCorrect, ckpt.Errors)
		sleep(throttleDuration(opt.sleepMS))
	}

	r.Scanned = ckpt.Scanned
	r.Updated = ckpt.Updated
	r.AlreadyCorrect = ckpt.AlreadyCorrect
	r.Errors = ckpt.Errors
	if err := finalizeBackfill(ctx, opt, plan, ddb, r, out); err != nil {
		return nil, err
	}
	return r, nil
}

// initCheckpoint loads the resume checkpoint (with --resume) or starts fresh,
// printing a warning when a stale checkpoint would be overwritten.
func initCheckpoint(opt options, out io.Writer) (checkpoint, error) {
	ckpt := checkpoint{Stage: opt.stage}
	if opt.resume {
		loaded, err := loadCheckpoint(opt.checkpoint)
		if err != nil {
			return ckpt, fmt.Errorf("resume checkpoint: %w", err)
		}
		ckpt = *loaded
		writef(out, "resuming from checkpoint %s (stage=%s)\n", opt.checkpoint, ckpt.Stage)
		return ckpt, nil
	}
	if _, err := statPath(opt.checkpoint); err == nil {
		writef(out, "note: existing checkpoint %s found; pass --resume to continue it, or delete it to start over\n", opt.checkpoint)
	}
	return ckpt, nil
}

// interruptedReport snapshots the checkpoint into the report, persists it for
// resume, and returns the interruption error.
func interruptedReport(opt options, ckpt checkpoint, r *report, cause error) (*report, error) {
	r.Scanned = ckpt.Scanned
	r.Updated = ckpt.Updated
	r.AlreadyCorrect = ckpt.AlreadyCorrect
	r.Errors = ckpt.Errors
	r.Interrupted = true
	r.Marker = "incomplete"
	if saveErr := saveCheckpoint(opt.checkpoint, ckpt); saveErr != nil {
		return nil, fmt.Errorf("save checkpoint after interrupt: %w", saveErr)
	}
	return r, fmt.Errorf("interrupted: %w (checkpoint saved to %s; rerun with --resume)", cause, opt.checkpoint)
}

// scanIdentityPage builds and executes one bounded scan page, resuming from the
// checkpoint's last evaluated key when present.
func scanIdentityPage(ctx context.Context, opt options, plan modelPlan, ckpt checkpoint, ddb ddbAPI) (*dynamodb.ScanOutput, error) {
	pageSize := opt.pageSize
	if pageSize <= 0 || pageSize > maxPageSize {
		pageSize = defaultPageSize
	}
	in := &dynamodb.ScanInput{
		TableName:            aws.String(opt.table),
		Limit:                aws.Int32(int32(pageSize)),
		ProjectionExpression: aws.String(plan.projection),
	}
	plan.filter(in)
	if ckpt.LastPK != "" || ckpt.LastSK != "" {
		in.ExclusiveStartKey = map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: ckpt.LastPK},
			"SK": &types.AttributeValueMemberS{Value: ckpt.LastSK},
		}
	}
	page, err := ddb.Scan(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("scan page: %w", err)
	}
	return page, nil
}

// processIdentityItems classifies each item and records the outcome: no write
// needed (already correct), dry-run would-update, conditional apply, or error.
func processIdentityItems(ctx context.Context, opt options, plan modelPlan, ddb ddbAPI, items []map[string]types.AttributeValue, ckpt *checkpoint, dryRunSample *int, out io.Writer) {
	for _, item := range items {
		ckpt.Scanned++
		expectedPK, expectedSK, needsWrite, classifyErr := plan.classify(item)
		if classifyErr != nil {
			ckpt.Errors++
			writef(out, "warn skip unclassifiable %s item agent=%s err=%v\n", plan.name, avString(item["agentId"]), classifyErr)
			continue
		}
		if !needsWrite {
			ckpt.AlreadyCorrect++
			continue
		}
		if !opt.apply {
			if *dryRunSample < markerDryRunSample {
				writef(out, "dry-run would set gsi3 on %s agent=%s\n", plan.name, expectedSK)
				*dryRunSample++
			}
			ckpt.Updated++
			continue
		}
		if err := applyItemGSI3(ctx, ddb, opt.table, item, expectedPK, expectedSK); err != nil {
			if errors.Is(err, errConditionallyCovered) {
				// A concurrent live write set the gsi3 attributes between our
				// read and this update; the item is covered.
				ckpt.AlreadyCorrect++
				continue
			}
			ckpt.Errors++
			writef(out, "warn gsi3 update failed %s agent=%s err=%v\n", plan.name, expectedSK, err)
			continue
		}
		ckpt.Updated++
	}
}

// finalizeBackfill decides the marker outcome: dry-run reports would-write;
// apply with errors refuses to write the marker (fail closed); apply clean
// writes the completeness marker.
func finalizeBackfill(ctx context.Context, opt options, plan modelPlan, ddb ddbAPI, r *report, out io.Writer) error {
	if !opt.apply {
		r.Marker = "would-write"
		return nil
	}
	if r.Errors > 0 {
		// Fail closed: the marker is written only after a complete, error-free
		// pass, so the request-path consumers keep failing explicitly until the
		// operator fixes the errors and reruns.
		r.Marker = "not-written"
		writef(out, "marker NOT written: %d errors; fix and rerun\n", r.Errors)
		return nil
	}
	r.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeMarker(ctx, ddb, opt.table, plan, r); err != nil {
		return fmt.Errorf("write backfill marker: %w", err)
	}
	r.Marker = "written"
	writef(out, "marker written: %s\n", plan.markerPK+"/"+plan.markerSK)
	return nil
}

// preflight refuses to run unless the target table exists and the gsi3 index is
// present and ACTIVE. The stack update deploys before the backfill, so a
// missing or creating index means the operator must deploy first.
func preflight(ctx context.Context, ddb ddbAPI, table string) error {
	out, err := ddb.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		return fmt.Errorf("preflight describe-table %s: %w", table, err)
	}
	if out == nil || out.Table == nil {
		return fmt.Errorf("preflight: describe-table %s returned no table", table)
	}
	for _, idx := range out.Table.GlobalSecondaryIndexes {
		if idx.IndexName == nil || *idx.IndexName != gsi3IndexName {
			continue
		}
		if idx.IndexStatus != types.IndexStatusActive {
			return fmt.Errorf("preflight refused: gsi3 on %s is %s, not ACTIVE; wait for the index creation to finish", table, idx.IndexStatus)
		}
		return nil
	}
	return fmt.Errorf("preflight refused: gsi3 does not exist on %s; deploy the stack update first (one GSI per deploy), then rerun", table)
}

// applyItemGSI3 sets the two gsi3 attributes with a conditional update that
// only writes when both attributes are still absent, so a concurrent live write
// is never clobbered. A conditional failure means a live write already covered
// the item — the caller counts it as already-correct.
func applyItemGSI3(ctx context.Context, ddb ddbAPI, table string, item map[string]types.AttributeValue, expectedPK, expectedSK string) error {
	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: avString(item["PK"])},
			"SK": &types.AttributeValueMemberS{Value: avString(item["SK"])},
		},
		UpdateExpression: aws.String("SET " + attrGsi3PK + " = :gsi3pk, " + attrGsi3SK + " = :gsi3sk"),
		ConditionExpression: aws.String(
			"attribute_not_exists(" + attrGsi3PK + ") AND attribute_not_exists(" + attrGsi3SK + ")",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":gsi3pk": &types.AttributeValueMemberS{Value: expectedPK},
			":gsi3sk": &types.AttributeValueMemberS{Value: expectedSK},
		},
	}
	_, err := ddb.UpdateItem(ctx, in)
	if err == nil {
		return nil
	}
	var cfe *types.ConditionalCheckFailedException
	if errors.As(err, &cfe) {
		return errConditionallyCovered
	}
	return err
}

var errConditionallyCovered = errors.New("gsi3 attributes already present (concurrent write)")

// writeMarker persists the backfill completeness marker consumed by the
// request-path identity enumerations. Attribute names and time encoding match
// the SoulAgentIdentityGSI3BackfillMarker TableTheory model.
func writeMarker(ctx context.Context, ddb ddbAPI, table string, plan modelPlan, r *report) error {
	in := &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item: map[string]types.AttributeValue{
			"PK":             &types.AttributeValueMemberS{Value: plan.markerPK},
			"SK":             &types.AttributeValueMemberS{Value: plan.markerSK},
			"scanned":        &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", r.Scanned)},
			"updated":        &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", r.Updated)},
			"alreadyCorrect": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", r.AlreadyCorrect)},
			"errors":         &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", r.Errors)},
			"completedAt":    &types.AttributeValueMemberS{Value: r.CompletedAt},
		},
	}
	_, err := ddb.PutItem(ctx, in)
	return err
}

func avString(v types.AttributeValue) string {
	if m, ok := v.(*types.AttributeValueMemberS); ok {
		return m.Value
	}
	return ""
}

// lastKeyFromScan extracts the PK/SK of the scan's last evaluated key. Scans of
// the base table page over its key schema, so PK/SK are the whole key.
func lastKeyFromScan(out *dynamodb.ScanOutput) (pk string, sk string, hasMore bool) {
	if out == nil || out.LastEvaluatedKey == nil {
		return "", "", false
	}
	pk = avString(out.LastEvaluatedKey["PK"])
	sk = avString(out.LastEvaluatedKey["SK"])
	if pk == "" && sk == "" {
		return "", "", false
	}
	return pk, sk, true
}

// throttleDuration returns the per-page throttle with jitter: base plus up to
// 50% of base, so parallel operator runs do not synchronize on a fixed cadence.
func throttleDuration(baseMS int) time.Duration {
	if baseMS <= 0 {
		baseMS = defaultSleepMS
	}
	// #nosec G404 -- jitter for scan throttling; not security-sensitive
	jitter := rand.IntN(baseMS/2 + 1)
	return time.Duration(baseMS+jitter) * time.Millisecond
}

func throttleSleep(d time.Duration) {
	time.Sleep(d)
}

func writef(out io.Writer, format string, args ...any) {
	if out == nil {
		return
	}
	// #nosec G705 -- CLI progress/report output; format strings are package
	// constants, never user-supplied data
	_, _ = fmt.Fprintf(out, format, args...)
}
