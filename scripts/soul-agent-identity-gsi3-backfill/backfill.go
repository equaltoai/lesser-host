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
	// status is a DynamoDB reserved keyword and cannot appear literally in a
	// ProjectionExpression; it is aliased as #s and the plan supplies that
	// alias via ExpressionAttributeNames.
	identityScanProjection = "PK, SK, agentId, #s, gsi3PK, gsi3SK"

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
	names      map[string]string // ExpressionAttributeNames for the scan (e.g. reserved-keyword aliases)
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
		names:      map[string]string{"#s": "status"}, // status is a reserved keyword, aliased in the scan
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

// checkpoint is the persisted resume state. It stores the run mode, the
// stage/table it is bound to, the last evaluated key of the base-table scan,
// and cumulative counts, so a re-run with --resume continues where the
// interrupted run stopped instead of restarting. The mode and table/stage
// binding are validated on resume (initCheckpoint) so a dry-run checkpoint can
// never be resumed as --apply and certify a partial backfill.
type checkpoint struct {
	Mode           string `json:"mode"`
	Stage          string `json:"stage"`
	Table          string `json:"table,omitempty"`
	LastPK         string `json:"lastPk,omitempty"`
	LastSK         string `json:"lastSk,omitempty"`
	Scanned        int64  `json:"scanned"`
	Updated        int64  `json:"updated"`
	Repaired       int64  `json:"repaired,omitempty"`
	AlreadyCorrect int64  `json:"alreadyCorrect"`
	Errors         int64  `json:"errors"`
}

// report is the final count report — the operator's proof the backfill ran.
type report struct {
	Scanned        int64  `json:"scanned"`
	Updated        int64  `json:"updated"`
	Repaired       int64  `json:"repaired,omitempty"`
	AlreadyCorrect int64  `json:"already_correct"`
	Errors         int64  `json:"errors"`
	Marker         string `json:"marker"` // written | would-write (dry-run) | not-written (errors)
	CompletedAt    string `json:"completed_at,omitempty"`
	Interrupted    bool   `json:"interrupted,omitempty"`
}

func (r report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scanned=%d updated=%d repaired=%d already_correct=%d errors=%d marker=%s",
		r.Scanned, r.Updated, r.Repaired, r.AlreadyCorrect, r.Errors, r.Marker)
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
		writef(out, "page done scanned=%d updated=%d repaired=%d already_correct=%d errors=%d\n",
			ckpt.Scanned, ckpt.Updated, ckpt.Repaired, ckpt.AlreadyCorrect, ckpt.Errors)
		sleep(throttleDuration(opt.sleepMS))
	}

	r.Scanned = ckpt.Scanned
	r.Updated = ckpt.Updated
	r.Repaired = ckpt.Repaired
	r.AlreadyCorrect = ckpt.AlreadyCorrect
	r.Errors = ckpt.Errors
	if err := finalizeBackfill(ctx, opt, plan, ddb, r, out); err != nil {
		return nil, err
	}
	return r, nil
}

// runMode returns the checkpoint/report mode label for the resolved options.
func runMode(opt options) string {
	if opt.apply {
		return "apply"
	}
	return "dry-run"
}

// initCheckpoint loads the resume checkpoint (with --resume) or starts fresh,
// printing a warning when a stale checkpoint would be overwritten. Fresh
// checkpoints are stamped with the run mode, stage, and table so a resume can
// refuse a mismatched run (see validateCheckpointBinding).
func initCheckpoint(opt options, out io.Writer) (checkpoint, error) {
	mode := runMode(opt)
	ckpt := checkpoint{Mode: mode, Stage: opt.stage, Table: opt.table}
	if opt.resume {
		loaded, err := loadCheckpoint(opt.checkpoint)
		if err != nil {
			return ckpt, fmt.Errorf("resume checkpoint: %w", err)
		}
		if err := validateCheckpointBinding(*loaded, opt); err != nil {
			return ckpt, err
		}
		ckpt = *loaded
		writef(out, "resuming from checkpoint %s (mode=%s stage=%s table=%s)\n", opt.checkpoint, ckpt.Mode, ckpt.Stage, ckpt.Table)
		return ckpt, nil
	}
	if _, err := statPath(opt.checkpoint); err == nil {
		writef(out, "note: existing checkpoint %s found; pass --resume to continue it, or delete it to start over\n", opt.checkpoint)
	}
	return ckpt, nil
}

// validateCheckpointBinding refuses to resume a checkpoint whose run mode,
// stage, or table differs from the current run. A dry-run checkpoint resumed
// with --apply would skip every pre-checkpoint item and could certify a partial
// backfill with the completeness marker; a checkpoint from another stage/table
// is simply the wrong data. In both cases the operator must restart without
// --resume or delete the checkpoint.
func validateCheckpointBinding(loaded checkpoint, opt options) error {
	mode := runMode(opt)
	if loaded.Mode != mode {
		return fmt.Errorf(
			"checkpoint %s was created by a %s run but this run is %s; refusing cross-mode resume (restart without --resume, or delete the checkpoint)",
			opt.checkpoint, orMode(loaded.Mode), mode,
		)
	}
	if loaded.Stage != "" && loaded.Stage != opt.stage {
		return fmt.Errorf(
			"checkpoint %s is bound to stage %q but this run resolved stage %q; refusing resume (restart without --resume, or delete the checkpoint)",
			opt.checkpoint, loaded.Stage, opt.stage,
		)
	}
	if loaded.Table != "" && loaded.Table != opt.table {
		return fmt.Errorf(
			"checkpoint %s is bound to table %q but this run resolved table %q; refusing resume (restart without --resume, or delete the checkpoint)",
			opt.checkpoint, loaded.Table, opt.table,
		)
	}
	return nil
}

func orMode(m string) string {
	if strings.TrimSpace(m) == "" {
		return "unknown"
	}
	return m
}

// interruptedReport snapshots the checkpoint into the report, persists it for
// resume, and returns the interruption error.
func interruptedReport(opt options, ckpt checkpoint, r *report, cause error) (*report, error) {
	r.Scanned = ckpt.Scanned
	r.Updated = ckpt.Updated
	r.Repaired = ckpt.Repaired
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
	if len(plan.names) > 0 {
		in.ExpressionAttributeNames = plan.names
	}
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
// needed (already correct), dry-run would-update, conditional apply (absent
// keys), stale-key repair, or error.
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
			// Truly correct: both gsi3 attributes already match the current
			// status-derived keys. Only this case counts as already-correct.
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
		applyOrRepairIdentityItem(ctx, ddb, opt.table, item, expectedPK, expectedSK, ckpt, out)
	}
}

// applyOrRepairIdentityItem writes the two gsi3 attributes on an item that needs
// a write, choosing the absent-keys conditional create or the observed-values
// repair, and records the outcome in the checkpoint counters.
func applyOrRepairIdentityItem(ctx context.Context, ddb ddbAPI, table string, item map[string]types.AttributeValue, expectedPK, expectedSK string, ckpt *checkpoint, out io.Writer) {
	if gsi3KeysAbsent(item) {
		// Absent keys: conditional create guarded by attribute_not_exists,
		// so a concurrent live write is never clobbered.
		if err := applyItemGSI3(ctx, ddb, table, item, expectedPK, expectedSK); err != nil {
			if errors.Is(err, errConditionallyCovered) {
				// A concurrent live write set the gsi3 attributes between our
				// read and this update; the item is covered.
				ckpt.AlreadyCorrect++
				return
			}
			ckpt.Errors++
			writef(out, "warn gsi3 update failed agent=%s err=%v\n", expectedSK, err)
			return
		}
		ckpt.Updated++
		return
	}
	// Present-but-wrong keys (stale, e.g. the pre-fix lifecycle writers):
	// repair write bound to the observed stale values. A conditional failure
	// means a concurrent writer changed the keys and we cannot certify the
	// item — counted as an error so the marker is withheld (fail closed).
	if err := repairItemGSI3(ctx, ddb, table, item, expectedPK, expectedSK); err != nil {
		ckpt.Errors++
		writef(out, "warn gsi3 repair failed agent=%s err=%v\n", expectedSK, err)
		return
	}
	ckpt.Repaired++
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

// gsi3KeysAbsent reports whether the item carries neither gsi3 attribute.
// Items with at least one present-but-wrong key are stale and need a repair
// write bound to the observed values, not the absent-keys conditional create.
func gsi3KeysAbsent(item map[string]types.AttributeValue) bool {
	_, havePK := item[attrGsi3PK]
	_, haveSK := item[attrGsi3SK]
	return !havePK && !haveSK
}

// applyItemGSI3 sets the two gsi3 attributes with a conditional update that
// only writes when both attributes are still absent, so a concurrent live write
// is never clobbered. A conditional failure means a live write already covered
// the item — the caller counts it as already-correct. This is the backfill
// (absent-keys) path only; stale keys go through repairItemGSI3.
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

// repairItemGSI3 fixes an item whose gsi3 attributes are present but stale
// (they differ from the status-derived keys the scan computed). The conditional
// write is bound to the OBSERVED stale values, so it only succeeds when no
// concurrent live writer changed the attributes between our scan and this
// update — a repair can never clobber a fresh write. Any failure (including a
// conditional failure, i.e. a concurrent writer) is counted as an error by the
// caller and withholds the completeness marker: the tool cannot certify an item
// it did not repair or verify.
func repairItemGSI3(ctx context.Context, ddb ddbAPI, table string, item map[string]types.AttributeValue, expectedPK, expectedSK string) error {
	observedPK := avString(item[attrGsi3PK])
	observedSK := avString(item[attrGsi3SK])
	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: avString(item["PK"])},
			"SK": &types.AttributeValueMemberS{Value: avString(item["SK"])},
		},
		UpdateExpression: aws.String("SET " + attrGsi3PK + " = :gsi3pk, " + attrGsi3SK + " = :gsi3sk"),
		ConditionExpression: aws.String(
			attrGsi3PK + " = :obsGsi3pk AND " + attrGsi3SK + " = :obsGsi3sk",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":gsi3pk":    &types.AttributeValueMemberS{Value: expectedPK},
			":gsi3sk":    &types.AttributeValueMemberS{Value: expectedSK},
			":obsGsi3pk": &types.AttributeValueMemberS{Value: observedPK},
			":obsGsi3sk": &types.AttributeValueMemberS{Value: observedSK},
		},
	}
	_, err := ddb.UpdateItem(ctx, in)
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
			"repaired":       &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", r.Repaired)},
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
