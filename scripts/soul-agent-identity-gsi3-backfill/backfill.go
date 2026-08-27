// Package main implements the soul-agent-identity-gsi3-backfill operator tool.
//
// It backfills the status/time-ordered GSI key attributes onto existing soul
// items after the stack updates that create the indexes have been deployed
// (issue #1061): the SoulAgentIdentity gsi3 status enumeration index (part C1)
// and the SoulAgentMintConversation gsi4 agent-scoped time-ordered index (part
// C2, #1067). Both models are covered in one run: a single bounded scan routes
// each item to its model plan, and each model gets its own completeness marker
// (written only after a complete error-free apply pass for THAT model).
//
// Dry-run by default; mutations only under --apply. The tool writes ONLY the
// GSI attributes, via conditional updates that never clobber concurrent live
// writes, and persists a LastEvaluatedKey checkpoint so an interrupted run can
// resume. The name is kept from part C1 so the #1069 deploy notes invocation
// keeps working; see the README for the dual-model semantics.
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
	// gsi3 is the SoulAgentIdentity status enumeration index (part C1) and gsi4
	// is the SoulAgentMintConversation agent-scoped time-ordered index (part C2,
	// #1067). Both stack updates deploy one GSI per UpdateTable; this tool must
	// not be run until the operator has deployed both (preflight refuses
	// otherwise).
	gsi3IndexName = "gsi3"
	gsi4IndexName = "gsi4"

	identityItemSK      = "IDENTITY"
	mintConversationSKP = "MINT_CONVERSATION#"

	// identityScanProjection keeps reads key-only plus the attributes needed to
	// compute and verify the gsi3 keys. No full item payloads are fetched.
	// status is a DynamoDB reserved keyword and cannot appear literally in a
	// ProjectionExpression; it is aliased as #s and the plan supplies that
	// alias via ExpressionAttributeNames.
	identityScanProjection = "PK, SK, agentId, #s, gsi3PK, gsi3SK"

	attrGsi3PK = "gsi3PK"
	attrGsi3SK = "gsi3SK"
	attrGsi4PK = "gsi4PK"
	attrGsi4SK = "gsi4SK"

	// scanProjection keeps reads key-only plus the attributes needed to compute
	// and verify the index keys of every model. No full item payloads are
	// fetched. It extends the identity projection (part C1) with the
	// mint-conversation attributes (part C2); reserved keywords are named
	// through the aliases the plans supply (modelPlan.names).
	scanProjection = identityScanProjection + ", conversationId, createdAt, " +
		attrGsi4PK + ", " + attrGsi4SK

	// scanFilterExpression routes only identity and mint-conversation items into
	// the pages; unrelated table models are skipped by the filter (the scan
	// still stops per page as soon as Limit matching items are collected).
	scanFilterExpression = "(SK = :skIdentity) OR (begins_with(SK, :mintPrefix))"

	defaultPageSize = 100
	maxPageSize     = 200
	defaultSleepMS  = 100

	// checkpointVersion is the resume checkpoint format. v2 added per-model
	// counters for the dual-model run; v1 (identity-only flat counters) is
	// refused on resume so a stale checkpoint can never certify a partial run.
	checkpointVersion = 2

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

// gsiAttrs names the two index attributes a model plan backfills.
type gsiAttrs struct {
	pkAttr string
	skAttr string
}

// modelPlan is the per-model backfill contract. One run covers every plan in
// backfillPlans(); each plan owns its index attributes, its item routing
// predicate, its classify function, and its completeness marker, so a marker is
// written only after a complete error-free apply pass for THAT model.
type modelPlan struct {
	name      string
	indexName string
	markerPK  string
	markerSK  string
	attrs     gsiAttrs
	matches   func(item map[string]types.AttributeValue) bool
	names     map[string]string // ExpressionAttributeNames for the scan (e.g. reserved-keyword aliases)
	classify  func(item map[string]types.AttributeValue) (gsiPK string, gsiSK string, needsWrite bool, err error)
}

// backfillPlans returns every model plan this tool covers. Part C2 (#1067)
// added the SoulAgentMintConversation plan (gsi4) to the C1 identity plan
// (gsi3); the scan/checkpoint/throttle machinery is model-agnostic.
func backfillPlans() []modelPlan {
	return []modelPlan{identityPlan(), mintConversationPlan()}
}

// identityPlan is the SoulAgentIdentity plan (part C1).
func identityPlan() modelPlan {
	return modelPlan{
		name:      "SoulAgentIdentity",
		indexName: gsi3IndexName,
		markerPK:  models.SoulAgentIdentityGSI3BackfillMarkerPK,
		markerSK:  models.SoulAgentIdentityGSI3BackfillMarkerSK,
		attrs:     gsiAttrs{pkAttr: attrGsi3PK, skAttr: attrGsi3SK},
		matches:   identityItemMatches,
		names:     map[string]string{"#s": "status"}, // status is a reserved keyword, aliased in the scan
		classify:  classifyIdentityItem,
	}
}

// mintConversationPlan is the SoulAgentMintConversation plan (part C2, #1067).
func mintConversationPlan() modelPlan {
	return modelPlan{
		name:      "SoulAgentMintConversation",
		indexName: gsi4IndexName,
		markerPK:  models.SoulAgentMintConversationGSI4BackfillMarkerPK,
		markerSK:  models.SoulAgentMintConversationGSI4BackfillMarkerSK,
		attrs:     gsiAttrs{pkAttr: attrGsi4PK, skAttr: attrGsi4SK},
		matches:   mintConversationItemMatches,
		classify:  classifyMintConversationItem,
	}
}

func identityItemMatches(item map[string]types.AttributeValue) bool {
	return avString(item["SK"]) == identityItemSK
}

func mintConversationItemMatches(item map[string]types.AttributeValue) bool {
	return strings.HasPrefix(avString(item["SK"]), mintConversationSKP)
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

// classifyMintConversationItem computes the expected gsi4 keys for a mint
// conversation item and reports whether the item still needs a write. The keys
// derive from the immutable agentId + createdAt via the same helpers the
// TableTheory model uses, so the backfill produces byte-identical index keys to
// live writes. Missing or unparseable createdAt is an error (item cannot be
// classified; the marker is withheld until the operator remediates).
func classifyMintConversationItem(item map[string]types.AttributeValue) (string, string, bool, error) {
	agentID := strings.ToLower(strings.TrimSpace(avString(item["agentId"])))
	conversationID := strings.TrimSpace(avString(item["conversationId"]))
	createdAtRaw := strings.TrimSpace(avString(item["createdAt"]))
	if agentID == "" || conversationID == "" || createdAtRaw == "" {
		return "", "", false, fmt.Errorf("item missing agentId/conversationId/createdAt")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		return "", "", false, fmt.Errorf("item createdAt %q is not an RFC3339 timestamp: %v", createdAtRaw, err)
	}
	expectedPK := models.SoulMintConversationGSI4PK(agentID)
	expectedSK := models.SoulMintConversationGSI4SK(createdAt, conversationID)
	havePK := avString(item[attrGsi4PK])
	haveSK := avString(item[attrGsi4SK])
	needsWrite := havePK != expectedPK || haveSK != expectedSK
	return expectedPK, expectedSK, needsWrite, nil
}

// modelCheckpoint is the per-model resume state: cumulative counters for one
// model within a run. The scan position (LastPK/LastSK) is shared across models
// because one bounded scan covers the whole table.
type modelCheckpoint struct {
	Scanned        int64 `json:"scanned"`
	Updated        int64 `json:"updated"`
	Repaired       int64 `json:"repaired"`
	AlreadyCorrect int64 `json:"alreadyCorrect"`
	Errors         int64 `json:"errors"`
}

// checkpoint is the persisted resume state. It stores the run mode, the
// stage/table it is bound to, the last evaluated key of the base-table scan,
// and per-model cumulative counts, so a re-run with --resume continues where
// the interrupted run stopped instead of restarting. The mode and table/stage
// binding are validated on resume (initCheckpoint) so a dry-run checkpoint can
// never be resumed as --apply and certify a partial backfill.
type checkpoint struct {
	Version int                         `json:"version"`
	Mode    string                      `json:"mode"`
	Stage   string                      `json:"stage"`
	Table   string                      `json:"table,omitempty"`
	LastPK  string                      `json:"lastPk,omitempty"`
	LastSK  string                      `json:"lastSk,omitempty"`
	Models  map[string]*modelCheckpoint `json:"models,omitempty"`
}

// modelReport is the final per-model count report — the operator's proof the
// model's backfill ran. Marker states: written | would-write (dry-run) |
// not-written (errors) | incomplete (interrupted).
type modelReport struct {
	Scanned        int64  `json:"scanned"`
	Updated        int64  `json:"updated"`
	Repaired       int64  `json:"repaired,omitempty"`
	AlreadyCorrect int64  `json:"already_correct"`
	Errors         int64  `json:"errors"`
	Marker         string `json:"marker"`
}

func (r modelReport) String() string {
	return fmt.Sprintf("scanned=%d updated=%d repaired=%d already_correct=%d errors=%d marker=%s",
		r.Scanned, r.Updated, r.Repaired, r.AlreadyCorrect, r.Errors, r.Marker)
}

// report is the final aggregate report over every model plan.
type report struct {
	Models      map[string]*modelReport `json:"models"`
	CompletedAt string                  `json:"completed_at,omitempty"`
	Interrupted bool                    `json:"interrupted,omitempty"`
}

func (r report) String() string {
	var b strings.Builder
	for _, plan := range backfillPlans() {
		model := r.Models[plan.name]
		if model == nil {
			continue
		}
		fmt.Fprintf(&b, "%s %s\n", plan.name, model.String())
	}
	if r.CompletedAt != "" {
		fmt.Fprintf(&b, "completed_at=%s", r.CompletedAt)
	}
	return strings.TrimSpace(b.String())
}

// newModelReports seeds the report with one modelReport per plan.
func newModelReports(plans []modelPlan) map[string]*modelReport {
	out := make(map[string]*modelReport, len(plans))
	for _, plan := range plans {
		out[plan.name] = &modelReport{}
	}
	return out
}

// totals aggregates the per-model counters for the page-progress line.
func (ckpt *checkpoint) totals() (scanned, updated, repaired, alreadyCorrect, errors int64) {
	for _, m := range ckpt.Models {
		if m == nil {
			continue
		}
		scanned += m.Scanned
		updated += m.Updated
		repaired += m.Repaired
		alreadyCorrect += m.AlreadyCorrect
		errors += m.Errors
	}
	return
}

// run executes the backfill for the resolved options. It is the testable core:
// all DynamoDB access goes through ddb, progress is written to out, and sleepFn
// (nil = real throttling sleep) is injectable for deterministic tests.
func run(ctx context.Context, opt options, ddb ddbAPI, out io.Writer, sleepFn func(time.Duration)) (*report, error) {
	if opt.table == "" {
		return nil, fmt.Errorf("table is required")
	}

	plans := backfillPlans()
	ckpt, err := initCheckpoint(opt, plans, out)
	if err != nil {
		return nil, err
	}
	if err := preflight(ctx, ddb, opt.table, plans); err != nil {
		return nil, err
	}
	sleep := sleepFn
	if sleep == nil {
		sleep = throttleSleep
	}

	r := &report{Models: newModelReports(plans)}
	dryRunSample := 0
	for {
		if err := ctx.Err(); err != nil {
			return interruptedReport(opt, ckpt, r, err)
		}
		page, err := scanPage(ctx, opt, plans, ckpt, ddb)
		if err != nil {
			return nil, err
		}
		processItems(ctx, opt, plans, ddb, page.Items, &ckpt, &dryRunSample, out)
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
		scanned, updated, repaired, alreadyCorrect, errors := ckpt.totals()
		writef(out, "page done scanned=%d updated=%d repaired=%d already_correct=%d errors=%d\n",
			scanned, updated, repaired, alreadyCorrect, errors)
		sleep(throttleDuration(opt.sleepMS))
	}

	for _, plan := range plans {
		copyModelReport(ckpt.Models[plan.name], r.Models[plan.name])
		if err := finalizeBackfill(ctx, opt, plan, ddb, r.Models[plan.name], out); err != nil {
			return nil, err
		}
	}
	if !opt.apply {
		r.CompletedAt = ""
	} else {
		r.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return r, nil
}

// copyModelReport snapshots a checkpoint model's counters into the report.
func copyModelReport(from *modelCheckpoint, to *modelReport) {
	if from == nil || to == nil {
		return
	}
	to.Scanned = from.Scanned
	to.Updated = from.Updated
	to.Repaired = from.Repaired
	to.AlreadyCorrect = from.AlreadyCorrect
	to.Errors = from.Errors
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
// refuse a mismatched run (see validateCheckpointBinding), and with the
// per-model counter map so a dual-model run can never resume with a single
// model's state.
func initCheckpoint(opt options, plans []modelPlan, out io.Writer) (checkpoint, error) {
	mode := runMode(opt)
	ckpt := checkpoint{
		Version: checkpointVersion,
		Mode:    mode,
		Stage:   opt.stage,
		Table:   opt.table,
		Models:  make(map[string]*modelCheckpoint, len(plans)),
	}
	for _, plan := range plans {
		ckpt.Models[plan.name] = &modelCheckpoint{}
	}
	if opt.resume {
		loaded, err := loadCheckpoint(opt.checkpoint)
		if err != nil {
			return ckpt, fmt.Errorf("resume checkpoint: %w", err)
		}
		if err := validateCheckpointBinding(*loaded, opt); err != nil {
			return ckpt, err
		}
		if len(loaded.Models) == 0 {
			return ckpt, fmt.Errorf(
				"checkpoint %s carries no per-model state; restart without --resume, or delete the checkpoint",
				opt.checkpoint,
			)
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

// validateCheckpointBinding refuses to resume a checkpoint whose format, run
// mode, stage, or table differs from the current run. A dry-run checkpoint
// resumed with --apply would skip every pre-checkpoint item and could certify a
// partial backfill with the completeness marker; a checkpoint from another
// stage/table is simply the wrong data; a pre-dual-model checkpoint has no
// per-model counters and cannot certify a dual-model run. In all cases the
// operator must restart without --resume or delete the checkpoint.
func validateCheckpointBinding(loaded checkpoint, opt options) error {
	if loaded.Version != checkpointVersion {
		return fmt.Errorf(
			"checkpoint %s uses format v%d but this tool expects v%d (per-model state); restart without --resume, or delete the checkpoint",
			opt.checkpoint, loaded.Version, checkpointVersion,
		)
	}
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
	for _, plan := range backfillPlans() {
		model := r.Models[plan.name]
		if model == nil {
			model = &modelReport{}
			r.Models[plan.name] = model
		}
		copyModelReport(ckpt.Models[plan.name], model)
		model.Marker = "incomplete"
	}
	r.Interrupted = true
	if saveErr := saveCheckpoint(opt.checkpoint, ckpt); saveErr != nil {
		return nil, fmt.Errorf("save checkpoint after interrupt: %w", saveErr)
	}
	return r, fmt.Errorf("interrupted: %w (checkpoint saved to %s; rerun with --resume)", cause, opt.checkpoint)
}

// scanPage builds and executes one bounded scan page over every model's items,
// resuming from the checkpoint's last evaluated key when present.
func scanPage(ctx context.Context, opt options, plans []modelPlan, ckpt checkpoint, ddb ddbAPI) (*dynamodb.ScanOutput, error) {
	pageSize := opt.pageSize
	if pageSize <= 0 || pageSize > maxPageSize {
		pageSize = defaultPageSize
	}
	in := &dynamodb.ScanInput{
		TableName:            aws.String(opt.table),
		Limit:                aws.Int32(int32(pageSize)),
		ProjectionExpression: aws.String(scanProjection),
		FilterExpression:     aws.String(scanFilterExpression),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":skIdentity": &types.AttributeValueMemberS{Value: identityItemSK},
			":mintPrefix": &types.AttributeValueMemberS{Value: mintConversationSKP},
		},
	}
	// Reserved-keyword aliases come from the plans (status is aliased as #s by
	// the identity plan; see modelPlan.names). The shared scan applies the
	// union so a reserved word is never named literally in the projection or
	// filter.
	for _, plan := range plans {
		if len(plan.names) > 0 {
			if in.ExpressionAttributeNames == nil {
				in.ExpressionAttributeNames = make(map[string]string, len(plan.names))
			}
			for alias, attribute := range plan.names {
				in.ExpressionAttributeNames[alias] = attribute
			}
		}
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

// processItems routes each item to its model plan, classifies it, and records
// the outcome: no write needed (already correct), dry-run would-update,
// conditional apply (absent keys), stale-key repair, or error.
func processItems(ctx context.Context, opt options, plans []modelPlan, ddb ddbAPI, items []map[string]types.AttributeValue, ckpt *checkpoint, dryRunSample *int, out io.Writer) {
	for _, item := range items {
		plan, ok := planForItem(plans, item)
		if !ok {
			writef(out, "warn skip item not covered by any model plan pk=%s sk=%s\n", avString(item["PK"]), avString(item["SK"]))
			continue
		}
		model := ckpt.Models[plan.name]
		if model == nil {
			model = &modelCheckpoint{}
			ckpt.Models[plan.name] = model
		}
		model.Scanned++
		expectedPK, expectedSK, needsWrite, classifyErr := plan.classify(item)
		if classifyErr != nil {
			model.Errors++
			writef(out, "warn skip unclassifiable %s item agent=%s err=%v\n", plan.name, avString(item["agentId"]), classifyErr)
			continue
		}
		if !needsWrite {
			// Truly correct: both index attributes already match the computed
			// keys. Only this case counts as already-correct.
			model.AlreadyCorrect++
			continue
		}
		if !opt.apply {
			if *dryRunSample < markerDryRunSample {
				writef(out, "dry-run would set %s on %s agent=%s\n", plan.attrs.pkAttr+"/"+plan.attrs.skAttr, plan.name, expectedSK)
				*dryRunSample++
			}
			model.Updated++
			continue
		}
		applyOrRepairItem(ctx, ddb, opt.table, item, plan, expectedPK, expectedSK, model, out)
	}
}

// planForItem returns the plan that owns the item, keyed by its SK prefix.
func planForItem(plans []modelPlan, item map[string]types.AttributeValue) (modelPlan, bool) {
	for _, plan := range plans {
		if plan.matches(item) {
			return plan, true
		}
	}
	return modelPlan{}, false
}

// applyOrRepairItem writes the two index attributes on an item that needs a
// write, choosing the absent-keys conditional create or the observed-values
// repair, and records the outcome in the model checkpoint counters.
func applyOrRepairItem(ctx context.Context, ddb ddbAPI, table string, item map[string]types.AttributeValue, plan modelPlan, expectedPK, expectedSK string, model *modelCheckpoint, out io.Writer) {
	if keysAbsent(item, plan.attrs) {
		// Absent keys: conditional create guarded by attribute_not_exists,
		// so a concurrent live write is never clobbered.
		if err := applyItemGSI(ctx, ddb, table, item, plan.attrs, expectedPK, expectedSK); err != nil {
			if errors.Is(err, errConditionallyCovered) {
				// A concurrent live write set the index attributes between our
				// read and this update; the item is covered.
				model.AlreadyCorrect++
				return
			}
			model.Errors++
			writef(out, "warn %s update failed agent=%s err=%v\n", plan.name, expectedSK, err)
			return
		}
		model.Updated++
		return
	}
	// Present-but-wrong keys (stale): repair write bound to the observed stale
	// values. A conditional failure means a concurrent writer changed the keys
	// and we cannot certify the item — counted as an error so the marker is
	// withheld (fail closed).
	if err := repairItemGSI(ctx, ddb, table, item, plan.attrs, expectedPK, expectedSK); err != nil {
		model.Errors++
		writef(out, "warn %s repair failed agent=%s err=%v\n", plan.name, expectedSK, err)
		return
	}
	model.Repaired++
}

// finalizeBackfill decides the marker outcome per model: dry-run reports
// would-write; apply with errors for THAT model refuses to write that model's
// marker (fail closed); apply clean writes the model's completeness marker. A
// clean identity pass can certify the identity marker even while a separate
// mint-conversation failure withholds the mint marker.
func finalizeBackfill(ctx context.Context, opt options, plan modelPlan, ddb ddbAPI, r *modelReport, out io.Writer) error {
	if r == nil {
		r = &modelReport{}
	}
	if !opt.apply {
		r.Marker = "would-write"
		return nil
	}
	if r.Errors > 0 {
		// Fail closed: the marker is written only after a complete, error-free
		// pass for this model, so the request-path consumers keep failing
		// explicitly until the operator fixes the errors and reruns.
		r.Marker = "not-written"
		writef(out, "marker NOT written for %s: %d errors; fix and rerun\n", plan.name, r.Errors)
		return nil
	}
	if err := writeMarker(ctx, ddb, opt.table, plan, r); err != nil {
		return fmt.Errorf("write backfill marker: %w", err)
	}
	r.Marker = "written"
	writef(out, "marker written: %s\n", plan.markerPK+"/"+plan.markerSK)
	return nil
}

// preflight refuses to run unless the target table exists and BOTH indexes are
// present and ACTIVE. The stack updates deploy before the backfill, so a
// missing or creating index means the operator must deploy first.
func preflight(ctx context.Context, ddb ddbAPI, table string, plans []modelPlan) error {
	out, err := ddb.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		return fmt.Errorf("preflight describe-table %s: %w", table, err)
	}
	if out == nil || out.Table == nil {
		return fmt.Errorf("preflight: describe-table %s returned no table", table)
	}
	indexStatus := make(map[string]types.IndexStatus)
	for _, idx := range out.Table.GlobalSecondaryIndexes {
		if idx.IndexName == nil {
			continue
		}
		indexStatus[*idx.IndexName] = idx.IndexStatus
	}
	for _, plan := range plans {
		status, ok := indexStatus[plan.indexName]
		if !ok {
			return fmt.Errorf("preflight refused: %s (%s) does not exist on %s; deploy the stack update first (one GSI per deploy), then rerun", plan.name, plan.indexName, table)
		}
		if status != types.IndexStatusActive {
			return fmt.Errorf("preflight refused: %s (%s) on %s is %s, not ACTIVE; wait for the index creation to finish", plan.name, plan.indexName, table, status)
		}
	}
	return nil
}

// keysAbsent reports whether the item carries neither of the plan's index
// attributes. Items with at least one present-but-wrong key are stale and need
// a repair write bound to the observed values, not the absent-keys conditional
// create.
func keysAbsent(item map[string]types.AttributeValue, attrs gsiAttrs) bool {
	_, havePK := item[attrs.pkAttr]
	_, haveSK := item[attrs.skAttr]
	return !havePK && !haveSK
}

// applyItemGSI sets the two index attributes with a conditional update that
// only writes when both attributes are still absent, so a concurrent live write
// is never clobbered. A conditional failure means a live write already covered
// the item — the caller counts it as already-correct. This is the backfill
// (absent-keys) path only; stale keys go through repairItemGSI.
func applyItemGSI(ctx context.Context, ddb ddbAPI, table string, item map[string]types.AttributeValue, attrs gsiAttrs, expectedPK, expectedSK string) error {
	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: avString(item["PK"])},
			"SK": &types.AttributeValueMemberS{Value: avString(item["SK"])},
		},
		UpdateExpression: aws.String("SET " + attrs.pkAttr + " = :gsiPk, " + attrs.skAttr + " = :gsiSk"),
		ConditionExpression: aws.String(
			"attribute_not_exists(" + attrs.pkAttr + ") AND attribute_not_exists(" + attrs.skAttr + ")",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":gsiPk": &types.AttributeValueMemberS{Value: expectedPK},
			":gsiSk": &types.AttributeValueMemberS{Value: expectedSK},
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

// repairItemGSI fixes an item whose index attributes are present but stale
// (they differ from the computed keys the scan derived). The conditional write
// is bound to the OBSERVED stale values, so it only succeeds when no concurrent
// live writer changed the attributes between our scan and this update — a
// repair can never clobber a fresh write. Any failure (including a conditional
// failure, i.e. a concurrent writer) is counted as an error by the caller and
// withholds the completeness marker: the tool cannot certify an item it did not
// repair or verify.
func repairItemGSI(ctx context.Context, ddb ddbAPI, table string, item map[string]types.AttributeValue, attrs gsiAttrs, expectedPK, expectedSK string) error {
	observedPK := avString(item[attrs.pkAttr])
	observedSK := avString(item[attrs.skAttr])
	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: avString(item["PK"])},
			"SK": &types.AttributeValueMemberS{Value: avString(item["SK"])},
		},
		UpdateExpression: aws.String("SET " + attrs.pkAttr + " = :gsiPk, " + attrs.skAttr + " = :gsiSk"),
		ConditionExpression: aws.String(
			attrs.pkAttr + " = :obsGsiPk AND " + attrs.skAttr + " = :obsGsiSk",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":gsiPk":    &types.AttributeValueMemberS{Value: expectedPK},
			":gsiSk":    &types.AttributeValueMemberS{Value: expectedSK},
			":obsGsiPk": &types.AttributeValueMemberS{Value: observedPK},
			":obsGsiSk": &types.AttributeValueMemberS{Value: observedSK},
		},
	}
	_, err := ddb.UpdateItem(ctx, in)
	return err
}

var errConditionallyCovered = errors.New("index attributes already present (concurrent write)")

// writeMarker persists one model's backfill completeness marker consumed by the
// request-path consumers. Attribute names and time encoding match the
// TableTheory marker model for the plan.
func writeMarker(ctx context.Context, ddb ddbAPI, table string, plan modelPlan, r *modelReport) error {
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
			"completedAt":    &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)},
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
