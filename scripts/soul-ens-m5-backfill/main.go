package main

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/equaltoai/lesser-host/internal/manageddomain"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	backfillSchemaVersion = 1
	defaultStage          = "lab"

	recordTypeChannel    = "channel"
	recordTypeResolution = "resolution"

	classificationCanonicalManaged       = "canonical_managed_instance_scoped"
	classificationLegacyManagedBare      = "legacy_managed_bare"
	classificationExternalENS            = "external_ens"
	classificationExternalLessersoulENS  = "external_or_unowned_lessersoul_ens"
	classificationMissingOrStale         = "missing_or_stale"
	classificationAmbiguous              = "ambiguous"
	classificationUnmanagedAgentContext  = "unmanaged_agent_context"
	classificationLegacyRollbackMaterial = "legacy_bare_rollback_material"

	actionUpdateChannelIdentifier   = "update_channel_identifier"
	actionCreateCanonicalResolution = "create_canonical_resolution"
	actionDeleteLegacyResolution    = "delete_legacy_resolution"
	actionNone                      = "none"

	actionStatusPlanned = "planned"
	actionStatusApplied = "applied"
	actionStatusSkipped = "skipped"
	actionStatusBlocked = "blocked"
	actionStatusNoop    = "noop"
)

type dynamoClient interface {
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

type backfillConfig struct {
	Stage       string
	TableName   string
	OutputPath  string
	RollbackOut string
	AgentID     string
	Apply       bool
	PageSize    int32
	MaxRecords  int
}

type backfillReport struct {
	SchemaVersion int              `json:"schema_version"`
	GeneratedAt   string           `json:"generated_at"`
	Mode          string           `json:"mode"`
	Stage         string           `json:"stage"`
	TableName     string           `json:"table_name"`
	Contract      backfillContract `json:"contract"`
	Summary       backfillSummary  `json:"summary"`
	Records       []backfillRecord `json:"records"`
	Issues        []string         `json:"issues,omitempty"`
}

type backfillContract struct {
	CanonicalNameFormat string `json:"canonical_name_format"`
	LegacyBarePolicy    string `json:"legacy_bare_policy"`
	ManagedPredicate    string `json:"managed_predicate"`
	ApplyPolicy         string `json:"apply_policy"`
	RollbackPolicy      string `json:"rollback_policy"`
}

type backfillSummary struct {
	ScannedENSChannels                 int `json:"scanned_ens_channels"`
	ScannedENSResolutions              int `json:"scanned_ens_resolutions"`
	CanonicalManagedChannels           int `json:"canonical_managed_channels"`
	LegacyManagedBareChannels          int `json:"legacy_managed_bare_channels"`
	ExternalChannels                   int `json:"external_channels"`
	AmbiguousChannels                  int `json:"ambiguous_channels"`
	MissingOrStaleChannels             int `json:"missing_or_stale_channels"`
	CanonicalManagedResolutions        int `json:"canonical_managed_resolutions"`
	LegacyManagedBareResolutions       int `json:"legacy_managed_bare_resolutions"`
	LegacyRollbackMaterialResolutions  int `json:"legacy_rollback_material_resolutions"`
	ExternalResolutions                int `json:"external_resolutions"`
	AmbiguousResolutions               int `json:"ambiguous_resolutions"`
	MissingOrStaleResolutions          int `json:"missing_or_stale_resolutions"`
	ProposedChannelUpdates             int `json:"proposed_channel_updates"`
	AppliedChannelUpdates              int `json:"applied_channel_updates"`
	ProposedCanonicalResolutionCreates int `json:"proposed_canonical_resolution_creates"`
	AppliedCanonicalResolutionCreates  int `json:"applied_canonical_resolution_creates"`
	ProposedLegacyResolutionDeletes    int `json:"proposed_legacy_resolution_deletes"`
	AppliedLegacyResolutionDeletes     int `json:"applied_legacy_resolution_deletes"`
	SkippedExternalRecords             int `json:"skipped_external_records"`
	BlockedRecords                     int `json:"blocked_records"`
	Errors                             int `json:"errors"`
}

type backfillRecord struct {
	RecordType       string           `json:"record_type"`
	PK               string           `json:"pk"`
	SK               string           `json:"sk"`
	AgentID          string           `json:"agent_id,omitempty"`
	CurrentENSName   string           `json:"current_ens_name,omitempty"`
	Classification   string           `json:"classification"`
	InstanceSlug     string           `json:"instance_slug,omitempty"`
	CanonicalName    string           `json:"canonical_name,omitempty"`
	LegacyName       string           `json:"legacy_name,omitempty"`
	PairedChannel    bool             `json:"paired_channel,omitempty"`
	PairedResolution bool             `json:"paired_resolution,omitempty"`
	Actions          []backfillAction `json:"actions,omitempty"`
	Issues           []string         `json:"issues,omitempty"`
}

type backfillAction struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Note   string `json:"note,omitempty"`
}

type rollbackReport struct {
	SchemaVersion int             `json:"schema_version"`
	GeneratedAt   string          `json:"generated_at"`
	Stage         string          `json:"stage"`
	TableName     string          `json:"table_name"`
	Entries       []rollbackEntry `json:"entries"`
}

type rollbackEntry struct {
	Kind             string         `json:"kind"`
	AgentID          string         `json:"agent_id,omitempty"`
	FromENSName      string         `json:"from_ens_name,omitempty"`
	ToENSName        string         `json:"to_ens_name,omitempty"`
	ChannelPK        string         `json:"channel_pk,omitempty"`
	ChannelSK        string         `json:"channel_sk,omitempty"`
	OldIdentifier    string         `json:"old_identifier,omitempty"`
	NewIdentifier    string         `json:"new_identifier,omitempty"`
	LegacyResolution map[string]any `json:"legacy_resolution,omitempty"`
	CanonicalKey     keyRef         `json:"canonical_key,omitempty"`
}

type keyRef struct {
	PK string `json:"pk,omitempty"`
	SK string `json:"sk,omitempty"`
}

type channelRow struct {
	PK                 string
	SK                 string
	AgentID            string
	Identifier         string
	ChannelType        string
	Status             string
	ENSResolverAddress string
	ENSChain           string
	Item               map[string]types.AttributeValue
}

type resolutionRow struct {
	PK      string
	SK      string
	ENSName string
	AgentID string
	Status  string
	Item    map[string]types.AttributeValue
}

type identityRow struct {
	AgentID         string
	Domain          string
	LocalID         string
	Status          string
	LifecycleStatus string
}

type domainRow struct {
	Domain             string
	InstanceSlug       string
	Status             string
	Type               string
	VerificationMethod string
}

type instanceRow struct {
	Slug             string
	HostedBaseDomain string
	Status           string
}

type managedContext struct {
	Managed       bool
	AgentID       string
	LocalID       string
	Domain        string
	InstanceSlug  string
	CanonicalName string
	LegacyName    string
	Issues        []string
}

type backfillState struct {
	channelsByAgent    map[string]channelRow
	resolutionsByName  map[string]resolutionRow
	identityCache      map[string]identityRow
	identityFoundCache map[string]bool
	domainCache        map[string]domainRow
	domainFoundCache   map[string]bool
	instanceCache      map[string]instanceRow
	instanceFoundCache map[string]bool
	ctxCache           map[string]managedContext
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, time.Now().UTC()))
}

func runCLI(args []string, getenv func(string) string, stdout io.Writer, stderr io.Writer, now time.Time) int {
	cfg, err := parseConfig(args, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "load aws config: %v\n", err)
		return 1
	}
	report, rollback, err := runBackfill(ctx, dynamodb.NewFromConfig(awsCfg), cfg, now)
	if err != nil {
		fmt.Fprintf(stderr, "ens backfill failed: %v\n", err)
		return 1
	}
	if cfg.Apply && cfg.RollbackOut != "" {
		if err := writeRollbackReport(rollback, cfg.RollbackOut); err != nil {
			fmt.Fprintf(stderr, "write rollback report: %v\n", err)
			return 1
		}
	}
	if err := writeBackfillReport(report, cfg.OutputPath, stdout); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	if report.Summary.Errors > 0 {
		return 1
	}
	if len(report.Issues) > 0 || report.Summary.BlockedRecords > 0 {
		return 2
	}
	return 0
}

func parseConfig(args []string, getenv func(string) string) (backfillConfig, error) {
	stageDefault := strings.ToLower(strings.TrimSpace(getenv("STAGE")))
	if stageDefault == "" {
		stageDefault = defaultStage
	}
	tableEnv := strings.TrimSpace(getenv("STATE_TABLE_NAME"))
	tableDefault := tableEnv
	if tableDefault == "" {
		tableDefault = defaultStateTableName(stageDefault)
	}
	stageFlagSet := flagPresent(args, "stage")
	tableFlagSet := flagPresent(args, "table-name")
	cfg := backfillConfig{Stage: stageDefault, TableName: tableDefault, PageSize: 100}
	fs := flag.NewFlagSet("soul-ens-m5-backfill", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Stage, "stage", cfg.Stage, "Control-plane stage")
	fs.StringVar(&cfg.TableName, "table-name", cfg.TableName, "DynamoDB state table name")
	fs.StringVar(&cfg.OutputPath, "out", "", "Output inventory/backfill evidence JSON path (default stdout)")
	fs.StringVar(&cfg.RollbackOut, "rollback-out", "", "Local rollback JSON path for apply mode; do not commit raw output")
	fs.StringVar(&cfg.AgentID, "agent-id", "", "Optional target agent id")
	fs.BoolVar(&cfg.Apply, "apply", false, "Apply idempotent managed-only ENS channel/resolution backfill")
	fs.IntVar(&cfg.MaxRecords, "max-records", 0, "Maximum records to include/apply after scanning (0 = unlimited)")
	pageSize := int(cfg.PageSize)
	fs.IntVar(&pageSize, "page-size", pageSize, "DynamoDB scan page size")
	if err := fs.Parse(args); err != nil {
		return backfillConfig{}, err
	}
	if pageSize > 1<<31-1 {
		return backfillConfig{}, errors.New("--page-size must fit int32")
	}
	if pageSize > 1000 {
		return backfillConfig{}, errors.New("--page-size must be <= 1000")
	}
	cfg.Stage = strings.ToLower(strings.TrimSpace(cfg.Stage))
	if cfg.Stage == "" {
		cfg.Stage = defaultStage
	}
	cfg.TableName = strings.TrimSpace(cfg.TableName)
	if !tableFlagSet {
		if stageFlagSet || tableEnv == "" {
			cfg.TableName = defaultStateTableName(cfg.Stage)
		}
	}
	cfg.OutputPath = strings.TrimSpace(cfg.OutputPath)
	cfg.RollbackOut = strings.TrimSpace(cfg.RollbackOut)
	cfg.AgentID = strings.ToLower(strings.TrimSpace(cfg.AgentID))
	cfg.PageSize = int32(pageSize) //nolint:gosec // Bounded to positive <=1000 above before converting for DynamoDB Limit.
	if cfg.TableName == "" {
		return backfillConfig{}, errors.New("--table-name or STATE_TABLE_NAME is required")
	}
	if cfg.PageSize <= 0 {
		return backfillConfig{}, errors.New("--page-size must be positive")
	}
	if cfg.MaxRecords < 0 {
		return backfillConfig{}, errors.New("--max-records must be non-negative")
	}
	if cfg.Apply && cfg.RollbackOut == "" {
		return backfillConfig{}, errors.New("--rollback-out is required with --apply so rollback material is captured outside git")
	}
	return cfg, nil
}

func defaultStateTableName(stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage == "" {
		stage = defaultStage
	}
	return fmt.Sprintf("lesser-host-%s-state", stage)
}

func flagPresent(args []string, name string) bool {
	long := "--" + name
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

func runBackfill(ctx context.Context, client dynamoClient, cfg backfillConfig, now time.Time) (backfillReport, rollbackReport, error) {
	channels, err := scanENSChannels(ctx, client, cfg)
	if err != nil {
		return backfillReport{}, rollbackReport{}, err
	}
	resolutions, err := scanENSResolutions(ctx, client, cfg)
	if err != nil {
		return backfillReport{}, rollbackReport{}, err
	}
	state := newBackfillState(channels, resolutions)
	report := newBackfillReport(cfg, now)
	report.Summary.ScannedENSChannels = len(channels)
	report.Summary.ScannedENSResolutions = len(resolutions)
	rollback := rollbackReport{SchemaVersion: backfillSchemaVersion, GeneratedAt: now.UTC().Format(time.RFC3339), Stage: cfg.Stage, TableName: cfg.TableName}

	appendChannelRecords(ctx, client, cfg, state, channels, now, &report, &rollback)
	if !stopForMaxRecords(cfg, len(report.Records)) {
		appendResolutionRecords(ctx, client, cfg, state, resolutions, now, &report, &rollback)
	}
	finalizeReport(&report)
	return report, rollback, nil
}

func appendChannelRecords(ctx context.Context, client dynamoClient, cfg backfillConfig, state *backfillState, channels []channelRow, now time.Time, report *backfillReport, rollback *rollbackReport) {
	for _, ch := range channels {
		if cfg.AgentID != "" && ch.AgentID != cfg.AgentID {
			continue
		}
		rec, rb, err := planChannelRecord(ctx, client, cfg, state, ch, now)
		if err != nil {
			rec = baseRecord(recordTypeChannel, ch.PK, ch.SK, ch.AgentID, ch.Identifier)
			rec.Classification = classificationAmbiguous
			rec.Issues = appendUnique(rec.Issues, "plan_error")
			report.Summary.Errors++
		}
		updateSummaryForRecord(&report.Summary, rec)
		report.Records = append(report.Records, rec)
		rollback.Entries = append(rollback.Entries, rb...)
		if stopForMaxRecords(cfg, len(report.Records)) {
			break
		}
	}
}

func appendResolutionRecords(ctx context.Context, client dynamoClient, cfg backfillConfig, state *backfillState, resolutions map[string]resolutionRow, now time.Time, report *backfillReport, rollback *rollbackReport) {
	for _, name := range sortedResolutionNames(resolutions) {
		res := resolutions[name]
		if cfg.AgentID != "" && res.AgentID != cfg.AgentID {
			continue
		}
		rec, rb, err := planResolutionRecord(ctx, client, cfg, state, res, now)
		if err != nil {
			rec = baseRecord(recordTypeResolution, res.PK, res.SK, res.AgentID, res.ENSName)
			rec.Classification = classificationAmbiguous
			rec.Issues = appendUnique(rec.Issues, "plan_error")
			report.Summary.Errors++
		}
		updateSummaryForRecord(&report.Summary, rec)
		report.Records = append(report.Records, rec)
		rollback.Entries = append(rollback.Entries, rb...)
		if stopForMaxRecords(cfg, len(report.Records)) {
			break
		}
	}
}

func newBackfillState(channels []channelRow, resolutions map[string]resolutionRow) *backfillState {
	byAgent := map[string]channelRow{}
	for _, ch := range channels {
		byAgent[ch.AgentID] = ch
	}
	return &backfillState{
		channelsByAgent:    byAgent,
		resolutionsByName:  resolutions,
		identityCache:      map[string]identityRow{},
		identityFoundCache: map[string]bool{},
		domainCache:        map[string]domainRow{},
		domainFoundCache:   map[string]bool{},
		instanceCache:      map[string]instanceRow{},
		instanceFoundCache: map[string]bool{},
		ctxCache:           map[string]managedContext{},
	}
}

func newBackfillReport(cfg backfillConfig, now time.Time) backfillReport {
	mode := "dry-run"
	if cfg.Apply {
		mode = "apply"
	}
	return backfillReport{
		SchemaVersion: backfillSchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Mode:          mode,
		Stage:         cfg.Stage,
		TableName:     cfg.TableName,
		Contract: backfillContract{
			CanonicalNameFormat: "<name>.<instance-slug>.lessersoul.eth",
			LegacyBarePolicy:    "legacy bare managed names fail closed for public discovery/search/gateway; this tool reports or removes only migration-owned stale records and never creates runtime aliases",
			ManagedPredicate:    "agent identity localId + active host Domain.InstanceSlug + matching Instance.HostedBaseDomain derive the expected legacy and canonical names",
			ApplyPolicy:         "only records whose current ENS name equals the derived legacy bare managed name and whose canonical target is absent or owned by the same agent are mutated",
			RollbackPolicy:      "apply mode requires --rollback-out; rollback JSON is local operator material and must not be committed",
		},
	}
}

func planChannelRecord(ctx context.Context, client dynamoClient, cfg backfillConfig, state *backfillState, ch channelRow, now time.Time) (backfillRecord, []rollbackEntry, error) {
	rec := baseRecord(recordTypeChannel, ch.PK, ch.SK, ch.AgentID, ch.Identifier)
	mctx, err := state.managedContext(ctx, client, cfg, ch.AgentID)
	if err != nil {
		return rec, nil, err
	}
	applyManagedContext(&rec, mctx)
	if !mctx.Managed {
		return planUnmanagedChannelRecord(rec, ch, mctx), nil, nil
	}

	name := normalizeENSName(ch.Identifier)
	switch name {
	case mctx.CanonicalName:
		rec.Classification = classificationCanonicalManaged
		rec.PairedResolution = resolutionOwnedBy(state.resolutionsByName[mctx.CanonicalName], ch.AgentID)
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusNoop, Note: "already canonical"})
		return rec, nil, nil
	case mctx.LegacyName:
		rec.Classification = classificationLegacyManagedBare
	default:
		rec.Classification = classifyManagedMismatchedName(name)
		rec.Issues = appendUnique(rec.Issues, "ens_channel_identifier_not_expected_for_managed_agent")
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusSkipped, Note: "not the derived legacy managed name"})
		return rec, nil, nil
	}

	return planLegacyManagedChannelRecord(ctx, client, cfg, state, ch, mctx, rec, now)
}

func planUnmanagedChannelRecord(rec backfillRecord, ch channelRow, mctx managedContext) backfillRecord {
	if slug, canonical, ok := canonicalNameForLocalID(ch.Identifier, mctx.LocalID); ok {
		rec.Classification = classificationCanonicalManaged
		rec.InstanceSlug = slug
		rec.CanonicalName = canonical
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusNoop, Note: "already canonical; full host ownership predicate not required for no-op"})
		return rec
	}
	if legacyNameMatchesLocalID(ch.Identifier, mctx.LocalID) {
		rec.Classification = classificationAmbiguous
		rec.Issues = appendUniqueSlice(rec.Issues, mctx.Issues)
		rec.Issues = appendUnique(rec.Issues, "legacy_bare_name_requires_full_managed_predicate")
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusBlocked, Note: "legacy bare rewrite refused without host/instance ownership proof"})
		return rec
	}
	rec.Classification = classifyUnmanagedName(ch.Identifier, mctx.Issues)
	rec.Issues = appendUniqueSlice(rec.Issues, mctx.Issues)
	rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusSkipped, Note: "managed predicate not proven"})
	return rec
}

func planLegacyManagedChannelRecord(ctx context.Context, client dynamoClient, cfg backfillConfig, state *backfillState, ch channelRow, mctx managedContext, rec backfillRecord, now time.Time) (backfillRecord, []rollbackEntry, error) {
	legacyRes, legacyFound := state.resolutionsByName[mctx.LegacyName]
	canonicalRes, canonicalFound := state.resolutionsByName[mctx.CanonicalName]
	legacyOwned := legacyFound && resolutionOwnedBy(legacyRes, ch.AgentID)
	canonicalOwned := canonicalFound && resolutionOwnedBy(canonicalRes, ch.AgentID)
	rec.PairedResolution = legacyOwned || canonicalOwned
	blockers := legacyChannelBlockers(ch, legacyFound, legacyOwned, canonicalFound, canonicalOwned)
	if len(blockers) > 0 {
		rec.Classification = classificationAmbiguous
		rec.Issues = appendUniqueSlice(rec.Issues, blockers)
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusBlocked, Note: "manual repair required before backfill"})
		return rec, nil, nil
	}
	return appendLegacyChannelActions(ctx, client, cfg, ch, mctx, rec, legacyRes, legacyOwned, canonicalOwned, now)
}

func legacyChannelBlockers(ch channelRow, legacyFound bool, legacyOwned bool, canonicalFound bool, canonicalOwned bool) []string {
	var blockers []string
	if strings.ToLower(strings.TrimSpace(ch.Status)) != models.SoulChannelStatusActive {
		blockers = appendUnique(blockers, "ens_channel_not_active")
	}
	if legacyFound && !legacyOwned {
		blockers = appendUnique(blockers, "legacy_resolution_agent_mismatch")
	}
	if canonicalFound && !canonicalOwned {
		blockers = appendUnique(blockers, "canonical_resolution_conflict")
	}
	if !legacyOwned && !canonicalOwned {
		blockers = appendUnique(blockers, "missing_legacy_or_canonical_resolution")
	}
	return blockers
}

func appendLegacyChannelActions(ctx context.Context, client dynamoClient, cfg backfillConfig, ch channelRow, mctx managedContext, rec backfillRecord, legacyRes resolutionRow, legacyOwned bool, canonicalOwned bool, now time.Time) (backfillRecord, []rollbackEntry, error) {
	var rollback []rollbackEntry
	updateAction := backfillAction{Kind: actionUpdateChannelIdentifier, Status: actionStatusPlanned, From: mctx.LegacyName, To: mctx.CanonicalName}
	if cfg.Apply {
		if err := applyChannelUpdate(ctx, client, cfg.TableName, ch, mctx.CanonicalName, now); err != nil {
			rec.Issues = appendUnique(rec.Issues, "apply_channel_update_failed")
			rec.Actions = append(rec.Actions, withStatus(updateAction, actionStatusBlocked))
			return rec, rollback, err
		}
		updateAction.Status = actionStatusApplied
	}
	rec.Actions = append(rec.Actions, updateAction)
	rollback = append(rollback, rollbackEntry{Kind: actionUpdateChannelIdentifier, AgentID: ch.AgentID, FromENSName: mctx.LegacyName, ToENSName: mctx.CanonicalName, ChannelPK: ch.PK, ChannelSK: ch.SK, OldIdentifier: mctx.LegacyName, NewIdentifier: mctx.CanonicalName})

	if legacyOwned && !canonicalOwned {
		createAction := backfillAction{Kind: actionCreateCanonicalResolution, Status: actionStatusPlanned, From: mctx.LegacyName, To: mctx.CanonicalName}
		if cfg.Apply {
			if err := applyCreateCanonicalResolution(ctx, client, cfg.TableName, legacyRes, mctx.CanonicalName, now); err != nil {
				rec.Issues = appendUnique(rec.Issues, "apply_create_canonical_resolution_failed")
				rec.Actions = append(rec.Actions, withStatus(createAction, actionStatusBlocked))
				return rec, rollback, err
			}
			createAction.Status = actionStatusApplied
		}
		rec.Actions = append(rec.Actions, createAction)
		rollback = append(rollback, rollbackEntry{Kind: actionCreateCanonicalResolution, AgentID: ch.AgentID, FromENSName: mctx.LegacyName, ToENSName: mctx.CanonicalName, LegacyResolution: plainItem(legacyRes.Item), CanonicalKey: keyRef{PK: resolutionPK(mctx.CanonicalName), SK: "RESOLUTION"}})
	}
	if legacyOwned {
		deleteAction := backfillAction{Kind: actionDeleteLegacyResolution, Status: actionStatusPlanned, From: mctx.LegacyName}
		if cfg.Apply {
			if err := applyDeleteLegacyResolution(ctx, client, cfg.TableName, legacyRes); err != nil {
				rec.Issues = appendUnique(rec.Issues, "apply_delete_legacy_resolution_failed")
				rec.Actions = append(rec.Actions, withStatus(deleteAction, actionStatusBlocked))
				return rec, rollback, err
			}
			deleteAction.Status = actionStatusApplied
		}
		rec.Actions = append(rec.Actions, deleteAction)
		rollback = append(rollback, rollbackEntry{Kind: actionDeleteLegacyResolution, AgentID: ch.AgentID, FromENSName: mctx.LegacyName, LegacyResolution: plainItem(legacyRes.Item)})
	}
	return rec, rollback, nil
}

func planResolutionRecord(ctx context.Context, client dynamoClient, cfg backfillConfig, state *backfillState, res resolutionRow, now time.Time) (backfillRecord, []rollbackEntry, error) {
	_ = now
	rec := baseRecord(recordTypeResolution, res.PK, res.SK, res.AgentID, res.ENSName)
	mctx, err := state.managedContext(ctx, client, cfg, res.AgentID)
	if err != nil {
		return rec, nil, err
	}
	applyManagedContext(&rec, mctx)
	if !mctx.Managed {
		if slug, canonical, ok := canonicalNameForLocalID(res.ENSName, mctx.LocalID); ok {
			rec.Classification = classificationCanonicalManaged
			rec.InstanceSlug = slug
			rec.CanonicalName = canonical
			rec.PairedChannel = channelNameForAgent(state.channelsByAgent[res.AgentID]) == canonical
			rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusNoop, Note: "already canonical; full host ownership predicate not required for no-op"})
			return rec, nil, nil
		}
		if legacyNameMatchesLocalID(res.ENSName, mctx.LocalID) {
			rec.Classification = classificationAmbiguous
			rec.Issues = appendUniqueSlice(rec.Issues, mctx.Issues)
			rec.Issues = appendUnique(rec.Issues, "legacy_bare_name_requires_full_managed_predicate")
			rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusBlocked, Note: "legacy bare rewrite refused without host/instance ownership proof"})
			return rec, nil, nil
		}
		rec.Classification = classifyUnmanagedName(res.ENSName, mctx.Issues)
		rec.Issues = appendUniqueSlice(rec.Issues, mctx.Issues)
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusSkipped, Note: "managed predicate not proven"})
		return rec, nil, nil
	}

	name := normalizeENSName(res.ENSName)
	if name == mctx.CanonicalName {
		rec.Classification = classificationCanonicalManaged
		rec.PairedChannel = channelNameForAgent(state.channelsByAgent[res.AgentID]) == mctx.CanonicalName
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusNoop, Note: "already canonical"})
		return rec, nil, nil
	}
	if name != mctx.LegacyName {
		rec.Classification = classifyManagedMismatchedName(name)
		rec.Issues = appendUnique(rec.Issues, "ens_resolution_name_not_expected_for_managed_agent")
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusSkipped, Note: "not the derived legacy managed name"})
		return rec, nil, nil
	}

	ch, hasChannel := state.channelsByAgent[res.AgentID]
	channelName := channelNameForAgent(ch)
	rec.LegacyName = mctx.LegacyName
	rec.PairedChannel = hasChannel
	if hasChannel && channelName == mctx.LegacyName {
		// The paired channel record owns the create/delete plan so operations are not duplicated.
		rec.Classification = classificationLegacyManagedBare
		rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusSkipped, Note: "paired legacy channel record carries the backfill actions"})
		return rec, nil, nil
	}
	if hasChannel && channelName == mctx.CanonicalName {
		canonicalRes, canonicalFound := state.resolutionsByName[mctx.CanonicalName]
		if canonicalFound && resolutionOwnedBy(canonicalRes, res.AgentID) {
			rec.Classification = classificationLegacyRollbackMaterial
			rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusSkipped, Note: "legacy bare record is rollback material only; runtime already fails closed"})
			return rec, []rollbackEntry{{Kind: actionDeleteLegacyResolution, AgentID: res.AgentID, FromENSName: mctx.LegacyName, LegacyResolution: plainItem(res.Item)}}, nil
		}
	}
	rec.Classification = classificationMissingOrStale
	rec.Issues = appendUnique(rec.Issues, "legacy_resolution_without_current_canonical_pair")
	rec.Actions = append(rec.Actions, backfillAction{Kind: actionNone, Status: actionStatusBlocked, Note: "not promoted because it could create gateway material without a current ENS channel"})
	return rec, nil, nil
}

func baseRecord(recordType, pk, sk, agentID, ensName string) backfillRecord {
	return backfillRecord{RecordType: recordType, PK: pk, SK: sk, AgentID: strings.ToLower(strings.TrimSpace(agentID)), CurrentENSName: normalizeENSName(ensName), Actions: []backfillAction{}}
}

func applyManagedContext(rec *backfillRecord, mctx managedContext) {
	rec.InstanceSlug = mctx.InstanceSlug
	rec.CanonicalName = mctx.CanonicalName
	rec.LegacyName = mctx.LegacyName
}

func (s *backfillState) managedContext(ctx context.Context, client dynamoClient, cfg backfillConfig, agentID string) (managedContext, error) {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if cached, ok := s.ctxCache[agentID]; ok {
		return cached, nil
	}
	mctx := managedContext{AgentID: agentID}
	ident, found, err := s.loadIdentity(ctx, client, cfg.TableName, agentID)
	if err != nil {
		return mctx, err
	}
	if !found {
		mctx.Issues = appendUnique(mctx.Issues, "missing_identity")
		s.ctxCache[agentID] = mctx
		return mctx, nil
	}
	localID := applyIdentityContext(&mctx, ident)
	if err := s.applyDomainAndInstanceContext(ctx, client, cfg, ident, &mctx); err != nil {
		return mctx, err
	}
	applyManagedENSNames(&mctx, localID)
	s.ctxCache[agentID] = mctx
	return mctx, nil
}

func applyIdentityContext(mctx *managedContext, ident identityRow) string {
	mctx.LocalID = ident.LocalID
	mctx.Domain = ident.Domain
	localID, err := soul.ValidateManagedHandle(ident.LocalID)
	if err != nil {
		mctx.Issues = appendUnique(mctx.Issues, "invalid_managed_local_id")
	}
	return localID
}

func (s *backfillState) applyDomainAndInstanceContext(ctx context.Context, client dynamoClient, cfg backfillConfig, ident identityRow, mctx *managedContext) error {
	domain, found, err := s.loadDomainForIdentity(ctx, client, cfg.TableName, cfg.Stage, ident.Domain)
	if err != nil {
		return err
	}
	if !found {
		mctx.Issues = appendUnique(mctx.Issues, "missing_domain_record")
		return nil
	}
	applyDomainContext(mctx, domain)
	if mctx.InstanceSlug == "" {
		return nil
	}
	return s.applyInstanceContext(ctx, client, cfg, ident, domain, mctx)
}

func applyDomainContext(mctx *managedContext, domain domainRow) {
	mctx.InstanceSlug = strings.ToLower(strings.TrimSpace(domain.InstanceSlug))
	if !domainStatusActive(domain.Status) {
		mctx.Issues = appendUnique(mctx.Issues, "domain_not_active")
	}
	if mctx.InstanceSlug == "" {
		mctx.Issues = appendUnique(mctx.Issues, "missing_instance_slug")
	}
}

func (s *backfillState) applyInstanceContext(ctx context.Context, client dynamoClient, cfg backfillConfig, ident identityRow, domain domainRow, mctx *managedContext) error {
	slug, slugErr := soul.ValidateManagedInstanceSlug(mctx.InstanceSlug)
	if slugErr != nil {
		mctx.Issues = appendUnique(mctx.Issues, "invalid_instance_slug")
		return nil
	}
	mctx.InstanceSlug = slug
	inst, found, err := s.loadInstance(ctx, client, cfg.TableName, mctx.InstanceSlug)
	if err != nil {
		return err
	}
	if !found {
		mctx.Issues = appendUnique(mctx.Issues, "missing_instance")
		return nil
	}
	if strings.TrimSpace(inst.Status) != "" && !strings.EqualFold(inst.Status, models.InstanceStatusActive) {
		mctx.Issues = appendUnique(mctx.Issues, "instance_not_active")
	}
	if !hostedBaseDomainMatchesIdentity(cfg.Stage, inst.HostedBaseDomain, ident.Domain, domain.Domain) {
		mctx.Issues = appendUnique(mctx.Issues, "instance_hosted_base_domain_mismatch")
	}
	return nil
}

func applyManagedENSNames(mctx *managedContext, localID string) {
	if len(mctx.Issues) != 0 {
		return
	}
	canonical, canonicalErr := soul.ManagedENSName(localID, mctx.InstanceSlug)
	legacy, legacyErr := soul.LegacyBareManagedENSNameForMigration(localID)
	if canonicalErr != nil || legacyErr != nil {
		mctx.Issues = appendUnique(mctx.Issues, "managed_ens_derivation_failed")
		return
	}
	mctx.CanonicalName = canonical
	mctx.LegacyName = legacy
	mctx.Managed = true
}

func (s *backfillState) loadIdentity(ctx context.Context, client dynamoClient, tableName, agentID string) (identityRow, bool, error) {
	if row, ok := s.identityCache[agentID]; ok || s.identityFoundCache[agentID] {
		return row, s.identityFoundCache[agentID], nil
	}
	item, found, err := getItem(ctx, client, tableName, "SOUL#AGENT#"+agentID, "IDENTITY")
	if err != nil || !found {
		s.identityFoundCache[agentID] = false
		return identityRow{}, false, err
	}
	row := identityRow{AgentID: strings.ToLower(strings.TrimSpace(itemString(item, "agentId"))), Domain: normalizeDomain(itemString(item, "domain")), LocalID: strings.ToLower(strings.TrimSpace(itemString(item, "localId"))), Status: strings.ToLower(strings.TrimSpace(itemString(item, "status"))), LifecycleStatus: strings.ToLower(strings.TrimSpace(itemString(item, "lifecycleStatus")))}
	if row.AgentID == "" {
		row.AgentID = agentID
	}
	s.identityCache[agentID] = row
	s.identityFoundCache[agentID] = true
	return row, true, nil
}

func (s *backfillState) loadDomainForIdentity(ctx context.Context, client dynamoClient, tableName, stage, rawDomain string) (domainRow, bool, error) {
	domain := normalizeDomain(rawDomain)
	if domain == "" {
		return domainRow{}, false, nil
	}
	if rec, found, err := s.loadDomain(ctx, client, tableName, domain); err != nil || found {
		return rec, found, err
	}
	baseDomain, ok := manageddomain.BaseDomainFromStageDomain(stage, domain)
	if !ok {
		return domainRow{}, false, nil
	}
	rec, found, err := s.loadDomain(ctx, client, tableName, baseDomain)
	if err != nil || !found {
		return rec, found, err
	}
	if rec.Type != models.DomainTypePrimary || rec.VerificationMethod != "managed" || !domainStatusActive(rec.Status) {
		return domainRow{}, false, nil
	}
	inst, instFound, err := s.loadInstance(ctx, client, tableName, rec.InstanceSlug)
	if err != nil || !instFound {
		return domainRow{}, instFound, err
	}
	if !strings.EqualFold(normalizeDomain(inst.HostedBaseDomain), rec.Domain) {
		return domainRow{}, false, nil
	}
	return rec, true, nil
}

func (s *backfillState) loadDomain(ctx context.Context, client dynamoClient, tableName, domain string) (domainRow, bool, error) {
	domain = normalizeDomain(domain)
	if row, ok := s.domainCache[domain]; ok || s.domainFoundCache[domain] {
		return row, s.domainFoundCache[domain], nil
	}
	item, found, err := getItem(ctx, client, tableName, "DOMAIN#"+domain, models.SKMetadata)
	if err != nil || !found {
		s.domainFoundCache[domain] = false
		return domainRow{}, false, err
	}
	row := domainRow{Domain: normalizeDomain(itemString(item, "domain")), InstanceSlug: strings.ToLower(strings.TrimSpace(itemString(item, "instanceSlug"))), Status: strings.ToLower(strings.TrimSpace(itemString(item, "status"))), Type: strings.ToLower(strings.TrimSpace(itemString(item, "type"))), VerificationMethod: strings.ToLower(strings.TrimSpace(itemString(item, "verificationMethod")))}
	s.domainCache[domain] = row
	s.domainFoundCache[domain] = true
	return row, true, nil
}

func (s *backfillState) loadInstance(ctx context.Context, client dynamoClient, tableName, slug string) (instanceRow, bool, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if row, ok := s.instanceCache[slug]; ok || s.instanceFoundCache[slug] {
		return row, s.instanceFoundCache[slug], nil
	}
	item, found, err := getItem(ctx, client, tableName, "INSTANCE#"+slug, models.SKMetadata)
	if err != nil || !found {
		s.instanceFoundCache[slug] = false
		return instanceRow{}, false, err
	}
	row := instanceRow{Slug: strings.ToLower(strings.TrimSpace(itemString(item, "slug"))), HostedBaseDomain: normalizeDomain(itemString(item, "hostedBaseDomain")), Status: strings.ToLower(strings.TrimSpace(itemString(item, "status")))}
	s.instanceCache[slug] = row
	s.instanceFoundCache[slug] = true
	return row, true, nil
}

func scanENSChannels(ctx context.Context, client dynamoClient, cfg backfillConfig) ([]channelRow, error) {
	var out []channelRow
	input := &dynamodb.ScanInput{
		TableName:                aws.String(cfg.TableName),
		ConsistentRead:           aws.Bool(true),
		Limit:                    aws.Int32(cfg.PageSize),
		FilterExpression:         aws.String("#sk = :sk"),
		ExpressionAttributeNames: map[string]string{"#sk": "SK"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":sk": &types.AttributeValueMemberS{Value: "CHANNEL#ens"},
		},
	}
	for {
		page, err := client.Scan(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			ch := channelFromItem(item)
			if ch.AgentID == "" && strings.HasPrefix(ch.PK, "SOUL#AGENT#") {
				ch.AgentID = strings.TrimPrefix(ch.PK, "SOUL#AGENT#")
			}
			out = append(out, ch)
		}
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		input.ExclusiveStartKey = page.LastEvaluatedKey
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AgentID == out[j].AgentID {
			return out[i].Identifier < out[j].Identifier
		}
		return out[i].AgentID < out[j].AgentID
	})
	return out, nil
}

func scanENSResolutions(ctx context.Context, client dynamoClient, cfg backfillConfig) (map[string]resolutionRow, error) {
	out := map[string]resolutionRow{}
	input := &dynamodb.ScanInput{
		TableName:                aws.String(cfg.TableName),
		ConsistentRead:           aws.Bool(true),
		Limit:                    aws.Int32(cfg.PageSize),
		FilterExpression:         aws.String("begins_with(#pk, :pk) AND #sk = :sk"),
		ExpressionAttributeNames: map[string]string{"#pk": "PK", "#sk": "SK"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: "ENS#NAME#"},
			":sk": &types.AttributeValueMemberS{Value: "RESOLUTION"},
		},
	}
	for {
		page, err := client.Scan(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			res := resolutionFromItem(item)
			if res.ENSName == "" && strings.HasPrefix(res.PK, "ENS#NAME#") {
				res.ENSName = strings.TrimPrefix(res.PK, "ENS#NAME#")
			}
			out[normalizeENSName(res.ENSName)] = res
		}
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		input.ExclusiveStartKey = page.LastEvaluatedKey
	}
	return out, nil
}

func getItem(ctx context.Context, client dynamoClient, tableName, pk, sk string) (map[string]types.AttributeValue, bool, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(tableName),
		ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
	})
	if err != nil {
		return nil, false, err
	}
	if len(out.Item) == 0 {
		return nil, false, nil
	}
	return cloneItem(out.Item), true, nil
}

func channelFromItem(item map[string]types.AttributeValue) channelRow {
	return channelRow{PK: itemString(item, "PK"), SK: itemString(item, "SK"), AgentID: strings.ToLower(strings.TrimSpace(itemString(item, "agentId"))), Identifier: normalizeENSName(itemString(item, "identifier")), ChannelType: strings.ToLower(strings.TrimSpace(itemString(item, "channelType"))), Status: strings.ToLower(strings.TrimSpace(itemString(item, "status"))), ENSResolverAddress: strings.ToLower(strings.TrimSpace(itemString(item, "ensResolverAddress"))), ENSChain: strings.ToLower(strings.TrimSpace(itemString(item, "ensChain"))), Item: cloneItem(item)}
}

func resolutionFromItem(item map[string]types.AttributeValue) resolutionRow {
	return resolutionRow{PK: itemString(item, "PK"), SK: itemString(item, "SK"), ENSName: normalizeENSName(itemString(item, "ensName")), AgentID: strings.ToLower(strings.TrimSpace(itemString(item, "agentId"))), Status: strings.ToLower(strings.TrimSpace(itemString(item, "status"))), Item: cloneItem(item)}
}

func applyChannelUpdate(ctx context.Context, client dynamoClient, tableName string, ch channelRow, canonicalName string, now time.Time) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: ch.PK},
			"SK": &types.AttributeValueMemberS{Value: ch.SK},
		},
		UpdateExpression:    aws.String("SET #identifier = :newIdentifier, #updatedAt = :updatedAt"),
		ConditionExpression: aws.String("attribute_exists(PK) AND attribute_exists(SK) AND #agentId = :agentId AND #channelType = :channelType AND #identifier = :oldIdentifier"),
		ExpressionAttributeNames: map[string]string{
			"#agentId":     "agentId",
			"#channelType": "channelType",
			"#identifier":  "identifier",
			"#updatedAt":   "updatedAt",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":agentId":       &types.AttributeValueMemberS{Value: ch.AgentID},
			":channelType":   &types.AttributeValueMemberS{Value: models.SoulChannelTypeENS},
			":oldIdentifier": &types.AttributeValueMemberS{Value: ch.Identifier},
			":newIdentifier": &types.AttributeValueMemberS{Value: canonicalName},
			":updatedAt":     &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339)},
		},
	})
	return err
}

func applyCreateCanonicalResolution(ctx context.Context, client dynamoClient, tableName string, legacy resolutionRow, canonicalName string, now time.Time) error {
	item := cloneItem(legacy.Item)
	item["PK"] = &types.AttributeValueMemberS{Value: resolutionPK(canonicalName)}
	item["SK"] = &types.AttributeValueMemberS{Value: "RESOLUTION"}
	item["ensName"] = &types.AttributeValueMemberS{Value: canonicalName}
	if itemString(item, "createdAt") == "" {
		item["createdAt"] = &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339)}
	}
	item["updatedAt"] = &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339)}
	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(PK) AND attribute_not_exists(SK)"),
	})
	if conditionalFailed(err) {
		return nil
	}
	return err
}

func applyDeleteLegacyResolution(ctx context.Context, client dynamoClient, tableName string, legacy resolutionRow) error {
	_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: legacy.PK},
			"SK": &types.AttributeValueMemberS{Value: legacy.SK},
		},
		ConditionExpression: aws.String("attribute_exists(PK) AND attribute_exists(SK) AND #agentId = :agentId AND #ensName = :ensName"),
		ExpressionAttributeNames: map[string]string{
			"#agentId": "agentId",
			"#ensName": "ensName",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":agentId": &types.AttributeValueMemberS{Value: legacy.AgentID},
			":ensName": &types.AttributeValueMemberS{Value: legacy.ENSName},
		},
	})
	if conditionalFailed(err) {
		return nil
	}
	return err
}

func conditionalFailed(err error) bool {
	var cfe *types.ConditionalCheckFailedException
	return errors.As(err, &cfe)
}

func updateSummaryForRecord(summary *backfillSummary, rec backfillRecord) {
	updateClassificationSummary(summary, rec)
	for _, action := range rec.Actions {
		updateActionSummary(summary, rec, action)
	}
}

func updateClassificationSummary(summary *backfillSummary, rec backfillRecord) {
	switch rec.RecordType {
	case recordTypeChannel:
		updateChannelClassificationSummary(summary, rec.Classification)
	case recordTypeResolution:
		updateResolutionClassificationSummary(summary, rec.Classification)
	}
}

func updateChannelClassificationSummary(summary *backfillSummary, classification string) {
	switch classification {
	case classificationCanonicalManaged:
		summary.CanonicalManagedChannels++
	case classificationLegacyManagedBare:
		summary.LegacyManagedBareChannels++
	case classificationExternalENS, classificationExternalLessersoulENS, classificationUnmanagedAgentContext:
		summary.ExternalChannels++
	case classificationMissingOrStale:
		summary.MissingOrStaleChannels++
	case classificationAmbiguous:
		summary.AmbiguousChannels++
	}
}

func updateResolutionClassificationSummary(summary *backfillSummary, classification string) {
	switch classification {
	case classificationCanonicalManaged:
		summary.CanonicalManagedResolutions++
	case classificationLegacyManagedBare:
		summary.LegacyManagedBareResolutions++
	case classificationLegacyRollbackMaterial:
		summary.LegacyRollbackMaterialResolutions++
	case classificationExternalENS, classificationExternalLessersoulENS, classificationUnmanagedAgentContext:
		summary.ExternalResolutions++
	case classificationMissingOrStale:
		summary.MissingOrStaleResolutions++
	case classificationAmbiguous:
		summary.AmbiguousResolutions++
	}
}

func updateActionSummary(summary *backfillSummary, rec backfillRecord, action backfillAction) {
	switch action.Kind {
	case actionUpdateChannelIdentifier:
		summary.ProposedChannelUpdates++
		if action.Status == actionStatusApplied {
			summary.AppliedChannelUpdates++
		}
	case actionCreateCanonicalResolution:
		summary.ProposedCanonicalResolutionCreates++
		if action.Status == actionStatusApplied {
			summary.AppliedCanonicalResolutionCreates++
		}
	case actionDeleteLegacyResolution:
		summary.ProposedLegacyResolutionDeletes++
		if action.Status == actionStatusApplied {
			summary.AppliedLegacyResolutionDeletes++
		}
	}
	if action.Status == actionStatusBlocked {
		summary.BlockedRecords++
	}
	if action.Status == actionStatusSkipped && isExternalClassification(rec.Classification) {
		summary.SkippedExternalRecords++
	}
}

func isExternalClassification(classification string) bool {
	return classification == classificationExternalENS ||
		classification == classificationExternalLessersoulENS ||
		classification == classificationUnmanagedAgentContext
}

func finalizeReport(report *backfillReport) {
	sort.Slice(report.Records, func(i, j int) bool {
		if report.Records[i].RecordType == report.Records[j].RecordType {
			if report.Records[i].AgentID == report.Records[j].AgentID {
				return report.Records[i].CurrentENSName < report.Records[j].CurrentENSName
			}
			return report.Records[i].AgentID < report.Records[j].AgentID
		}
		return report.Records[i].RecordType < report.Records[j].RecordType
	})
	if report.Summary.AmbiguousChannels > 0 || report.Summary.AmbiguousResolutions > 0 {
		report.Issues = appendUnique(report.Issues, "ambiguous_records_present")
	}
	if report.Summary.MissingOrStaleChannels > 0 || report.Summary.MissingOrStaleResolutions > 0 {
		report.Issues = appendUnique(report.Issues, "missing_or_stale_records_present")
	}
}

func writeBackfillReport(report backfillReport, path string, stdout io.Writer) error {
	raw, err := jsonMarshalIndent(report)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		_, err = stdout.Write(append(raw, '\n'))
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600) //nolint:gosec // Operator-selected local evidence path.
}

func writeRollbackReport(report rollbackReport, path string) error {
	raw, err := jsonMarshalIndent(report)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600) //nolint:gosec // Operator-selected local rollback path; do not commit.
}

func jsonMarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

func classifyUnmanagedName(name string, issues []string) string {
	name = normalizeENSName(name)
	if len(issues) > 0 && strings.HasSuffix(name, "."+soul.ManagedENSRootName) {
		return classificationUnmanagedAgentContext
	}
	if strings.HasSuffix(name, "."+soul.ManagedENSRootName) || name == soul.ManagedENSRootName {
		return classificationExternalLessersoulENS
	}
	return classificationExternalENS
}

func classifyManagedMismatchedName(name string) string {
	if strings.HasSuffix(normalizeENSName(name), "."+soul.ManagedENSRootName) {
		return classificationAmbiguous
	}
	return classificationExternalENS
}

func canonicalNameForLocalID(ensName string, localID string) (string, string, bool) {
	name := normalizeENSName(ensName)
	labels := strings.Split(name, ".")
	if len(labels) != 4 || labels[2] != "lessersoul" || labels[3] != "eth" {
		return "", "", false
	}
	handle, err := soul.ValidateManagedHandle(strings.ToLower(strings.TrimSpace(localID)))
	if err != nil || labels[0] != handle {
		return "", "", false
	}
	slug, err := soul.ValidateManagedInstanceSlug(labels[1])
	if err != nil {
		return "", "", false
	}
	canonical, err := soul.ManagedENSName(handle, slug)
	if err != nil || canonical != name {
		return "", "", false
	}
	return slug, canonical, true
}

func legacyNameMatchesLocalID(ensName string, localID string) bool {
	handle, err := soul.ValidateManagedHandle(strings.ToLower(strings.TrimSpace(localID)))
	if err != nil {
		return false
	}
	legacy, err := soul.LegacyBareManagedENSNameForMigration(handle)
	return err == nil && legacy == normalizeENSName(ensName)
}

func resolutionOwnedBy(res resolutionRow, agentID string) bool {
	return res.ENSName != "" && strings.EqualFold(res.AgentID, agentID)
}

func channelNameForAgent(ch channelRow) string { return normalizeENSName(ch.Identifier) }

func sortedResolutionNames(resolutions map[string]resolutionRow) []string {
	names := make([]string, 0, len(resolutions))
	for name := range resolutions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func stopForMaxRecords(cfg backfillConfig, count int) bool {
	return cfg.MaxRecords > 0 && count >= cfg.MaxRecords
}

func withStatus(action backfillAction, status string) backfillAction {
	action.Status = status
	return action
}

func resolutionPK(name string) string { return "ENS#NAME#" + normalizeENSName(name) }

func normalizeENSName(raw string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
}

func normalizeDomain(raw string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
}

func domainStatusActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.DomainStatusVerified, models.DomainStatusActive:
		return true
	default:
		return false
	}
}

func hostedBaseDomainMatchesIdentity(stage, hostedBaseDomain, identityDomain, domainRecordDomain string) bool {
	hosted := normalizeDomain(hostedBaseDomain)
	if hosted == "" {
		return false
	}
	for _, candidate := range []string{identityDomain, domainRecordDomain} {
		candidate = normalizeDomain(candidate)
		if candidate != "" && hosted == candidate {
			return true
		}
		if base, ok := manageddomain.BaseDomainFromStageDomain(stage, candidate); ok && hosted == base {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueSlice(values []string, more []string) []string {
	for _, value := range more {
		values = appendUnique(values, value)
	}
	return values
}

func itemString(item map[string]types.AttributeValue, name string) string {
	if item == nil {
		return ""
	}
	switch v := item[name].(type) {
	case *types.AttributeValueMemberS:
		return v.Value
	case *types.AttributeValueMemberN:
		return v.Value
	default:
		return ""
	}
}

func cloneItem(in map[string]types.AttributeValue) map[string]types.AttributeValue {
	out := make(map[string]types.AttributeValue, len(in))
	for key, value := range in {
		out[key] = cloneAttributeValue(value)
	}
	return out
}

func cloneAttributeValue(value types.AttributeValue) types.AttributeValue {
	switch v := value.(type) {
	case *types.AttributeValueMemberS:
		return &types.AttributeValueMemberS{Value: v.Value}
	case *types.AttributeValueMemberN:
		return &types.AttributeValueMemberN{Value: v.Value}
	case *types.AttributeValueMemberBOOL:
		return &types.AttributeValueMemberBOOL{Value: v.Value}
	case *types.AttributeValueMemberSS:
		return &types.AttributeValueMemberSS{Value: append([]string(nil), v.Value...)}
	case *types.AttributeValueMemberNS:
		return &types.AttributeValueMemberNS{Value: append([]string(nil), v.Value...)}
	case *types.AttributeValueMemberL:
		items := make([]types.AttributeValue, 0, len(v.Value))
		for _, it := range v.Value {
			items = append(items, cloneAttributeValue(it))
		}
		return &types.AttributeValueMemberL{Value: items}
	case *types.AttributeValueMemberM:
		return &types.AttributeValueMemberM{Value: cloneItem(v.Value)}
	case *types.AttributeValueMemberNULL:
		return &types.AttributeValueMemberNULL{Value: v.Value}
	default:
		return value
	}
}

func plainItem(item map[string]types.AttributeValue) map[string]any {
	out := make(map[string]any, len(item))
	keys := make([]string, 0, len(item))
	for key := range item {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out[key] = plainAttributeValue(item[key])
	}
	return out
}

func plainAttributeValue(value types.AttributeValue) any {
	switch v := value.(type) {
	case *types.AttributeValueMemberS:
		return v.Value
	case *types.AttributeValueMemberN:
		return v.Value
	case *types.AttributeValueMemberBOOL:
		return v.Value
	case *types.AttributeValueMemberSS:
		return append([]string(nil), v.Value...)
	case *types.AttributeValueMemberNS:
		return append([]string(nil), v.Value...)
	case *types.AttributeValueMemberL:
		out := make([]any, 0, len(v.Value))
		for _, it := range v.Value {
			out = append(out, plainAttributeValue(it))
		}
		return out
	case *types.AttributeValueMemberM:
		return plainItem(v.Value)
	case *types.AttributeValueMemberNULL:
		return nil
	default:
		return nil
	}
}
