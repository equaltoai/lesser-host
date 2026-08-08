package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const (
	reportSchemaVersion          = 1
	classificationAlreadyHealthy = "already_healthy"
	classificationApplied        = "applied"
	classificationBlocked        = "blocked"
	classificationNotProvisioned = "not_previously_provisioned"
	classificationPlanned        = "planned"
	soulEmailChannelIndexPK      = "SOUL#CHANNEL#email"
	soulEmailChannelSortKey      = "CHANNEL#email"
)

type dynamoAPI interface {
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

type ssmAPI interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

type config struct {
	Stage       string
	TableName   string
	SourceTable string
	AgentID     string
	Domains     []string
	OutputPath  string
	RollbackOut string
	Apply       bool
	Confirm     string
	PageSize    int32
	MaxAgents   int
}

type identity struct {
	AgentID string
	Domain  string
	LocalID string
}

type agentReport struct {
	AgentID              string   `json:"agent_id"`
	Domain               string   `json:"domain,omitempty"`
	LocalID              string   `json:"local_id,omitempty"`
	Classification       string   `json:"classification"`
	RecoveredEmailSHA256 string   `json:"recovered_email_sha256,omitempty"`
	SSMParameterPresent  bool     `json:"ssm_parameter_present"`
	Actions              []string `json:"actions,omitempty"`
	Issues               []string `json:"issues,omitempty"`
}

type summary struct {
	ScannedIdentities int `json:"scanned_identities"`
	MatchedIdentities int `json:"matched_identities"`
	Eligible          int `json:"eligible"`
	AlreadyHealthy    int `json:"already_healthy"`
	Planned           int `json:"planned"`
	Applied           int `json:"applied"`
	Blocked           int `json:"blocked"`
	Errors            int `json:"errors"`
}

type report struct {
	SchemaVersion int            `json:"schema_version"`
	GeneratedAt   string         `json:"generated_at"`
	Mode          string         `json:"mode"`
	Stage         string         `json:"stage"`
	TableName     string         `json:"table_name"`
	SourceTable   string         `json:"source_table,omitempty"`
	Contract      reportContract `json:"contract"`
	Summary       summary        `json:"summary"`
	Agents        []agentReport  `json:"agents"`
}

type reportContract struct {
	Eligibility string `json:"eligibility"`
	Mutation    string `json:"mutation"`
	Secrets     string `json:"secrets"`
}

type rollbackReport struct {
	SchemaVersion int             `json:"schema_version"`
	GeneratedAt   string          `json:"generated_at"`
	Stage         string          `json:"stage"`
	TableName     string          `json:"table_name"`
	Entries       []rollbackEntry `json:"entries"`
}

type rollbackEntry struct {
	AgentID string   `json:"agent_id"`
	Keys    []keyRef `json:"created_keys"`
}

type keyRef struct {
	PK string `json:"pk"`
	SK string `json:"sk"`
}

type clients struct {
	db  dynamoAPI
	ssm ssmAPI
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, time.Now().UTC()))
}

func runCLI(args []string, getenv func(string) string, stdout, stderr io.Writer, now time.Time) int {
	cfg, err := parseConfig(args, getenv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if cfg.Apply {
		reserveErr := reservePrivateFile(cfg.RollbackOut)
		if reserveErr != nil {
			fmt.Fprintf(stderr, "reserve rollback evidence: %v\n", reserveErr)
			return 1
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "load aws config: %v\n", err)
		return 1
	}
	cs := clients{db: dynamodb.NewFromConfig(awsCfg), ssm: ssm.NewFromConfig(awsCfg)}
	rep, rollback, err := runBackfill(ctx, cs, cfg, now)
	if err != nil {
		fmt.Fprintf(stderr, "email channel backfill failed: %v\n", err)
		return 1
	}
	if cfg.Apply {
		if err := writeJSONFile(cfg.RollbackOut, rollback); err != nil {
			fmt.Fprintf(stderr, "write rollback evidence: %v\n", err)
			return 1
		}
	}
	if err := writeReport(rep, cfg.OutputPath, stdout); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	if rep.Summary.Errors > 0 {
		return 1
	}
	if rep.Summary.Blocked > 0 {
		return 2
	}
	return 0
}

type stringListFlag []string

func (f *stringListFlag) String() string { return strings.Join(*f, ",") }
func (f *stringListFlag) Set(value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return errors.New("domain must not be empty")
	}
	*f = append(*f, value)
	return nil
}

func parseConfig(args []string, getenv func(string) string) (config, error) {
	stage := strings.ToLower(strings.TrimSpace(getenv("STAGE")))
	if stage == "" {
		stage = "lab"
	}
	tableEnv := strings.TrimSpace(getenv("STATE_TABLE_NAME"))
	table := tableEnv
	if table == "" {
		table = fmt.Sprintf("lesser-host-%s-state", stage)
	}
	cfg := config{Stage: stage, TableName: table, PageSize: 100}
	fs := flag.NewFlagSet("soul-email-channel-backfill", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Stage, "stage", cfg.Stage, "Control plane stage")
	fs.StringVar(&cfg.TableName, "table-name", cfg.TableName, "DynamoDB state table")
	fs.StringVar(&cfg.SourceTable, "source-table", "", "Point-in-time-restored source table containing the last good channel state")
	fs.StringVar(&cfg.AgentID, "agent-id", "", "Optional canary agent id")
	fs.Var((*stringListFlag)(&cfg.Domains), "domain", "Limit to one managed-instance domain; repeat for multiple domains (required unless --agent-id is set)")
	fs.StringVar(&cfg.OutputPath, "out", "", "Evidence JSON path (default stdout)")
	fs.StringVar(&cfg.RollbackOut, "rollback-out", "", "Required local created-key evidence path in apply mode")
	fs.BoolVar(&cfg.Apply, "apply", false, "Apply conditional restorative writes (default dry-run)")
	fs.StringVar(&cfg.Confirm, "confirm-stage", "", "Required in apply mode; must exactly equal --stage")
	fs.IntVar(&cfg.MaxAgents, "max-agents", 0, "Maximum matching agents to inspect (0 unlimited)")
	pageSize := int(cfg.PageSize)
	fs.IntVar(&pageSize, "page-size", pageSize, "DynamoDB scan/query page size")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return normalizeAndValidateConfig(cfg, args, tableEnv, pageSize)
}

func normalizeAndValidateConfig(cfg config, args []string, tableEnv string, pageSize int) (config, error) {
	if tableEnv == "" && !flagPresent(args, "table-name") {
		cfg.TableName = fmt.Sprintf("lesser-host-%s-state", strings.ToLower(strings.TrimSpace(cfg.Stage)))
	}
	cfg.Stage = strings.ToLower(strings.TrimSpace(cfg.Stage))
	cfg.TableName = strings.TrimSpace(cfg.TableName)
	cfg.SourceTable = strings.TrimSpace(cfg.SourceTable)
	cfg.AgentID = strings.ToLower(strings.TrimSpace(cfg.AgentID))
	for i := range cfg.Domains {
		cfg.Domains[i] = strings.ToLower(strings.TrimSpace(cfg.Domains[i]))
	}
	cfg.Confirm = strings.ToLower(strings.TrimSpace(cfg.Confirm))
	cfg.RollbackOut = strings.TrimSpace(cfg.RollbackOut)
	if cfg.Stage == "" || cfg.TableName == "" {
		return config{}, errors.New("--stage and --table-name are required")
	}
	if pageSize <= 0 || pageSize > 1000 {
		return config{}, errors.New("--page-size must be between 1 and 1000")
	}
	cfg.PageSize = int32(pageSize) //nolint:gosec // pageSize is bounded above.
	if cfg.AgentID == "" && len(cfg.Domains) == 0 {
		return config{}, errors.New("at least one --domain is required unless --agent-id is set")
	}
	if cfg.MaxAgents < 0 {
		return config{}, errors.New("--max-agents must be non-negative")
	}
	if cfg.SourceTable == "" {
		return config{}, errors.New("--source-table is required")
	}
	if err := validateApplyConfig(cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func validateApplyConfig(cfg config) error {
	if !cfg.Apply {
		return nil
	}
	if cfg.Confirm != cfg.Stage || cfg.RollbackOut == "" {
		return errors.New("--apply requires --source-table, --confirm-stage equal to --stage, and --rollback-out")
	}
	if cfg.OutputPath != "" && cfg.OutputPath != "-" && cfg.OutputPath == cfg.RollbackOut {
		return errors.New("--out and --rollback-out must be different files")
	}
	return nil
}

func flagPresent(args []string, name string) bool {
	prefix := "--" + name
	for _, arg := range args {
		if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
			return true
		}
	}
	return false
}

func runBackfill(ctx context.Context, cs clients, cfg config, now time.Time) (report, rollbackReport, error) {
	identities, err := scanIdentities(ctx, cs.db, cfg)
	if err != nil {
		return report{}, rollbackReport{}, err
	}
	mode := "dry-run"
	if cfg.Apply {
		mode = "apply"
	}
	rep := report{SchemaVersion: reportSchemaVersion, GeneratedAt: now.Format(time.RFC3339), Mode: mode, Stage: cfg.Stage, TableName: cfg.TableName, SourceTable: cfg.SourceTable, Contract: reportContract{
		Eligibility: "missing CHANNEL#email plus a complete managed channel in the operator-selected point-in-time-restored source table",
		Mutation:    "conditional per-agent transaction creates only missing channel/index/preferences records and an audit event; existing conflicting state blocks",
		Secrets:     "the tool checks SSM parameter existence without decryption and never reads, emits, or rewrites the password",
	}}
	rb := rollbackReport{SchemaVersion: reportSchemaVersion, GeneratedAt: now.Format(time.RFC3339), Stage: cfg.Stage, TableName: cfg.TableName}
	rep.Summary.ScannedIdentities = len(identities)
	for _, id := range identities {
		if !identityMatchesConfig(id, cfg) {
			continue
		}
		if cfg.MaxAgents > 0 && len(rep.Agents) >= cfg.MaxAgents {
			break
		}
		rep.Summary.MatchedIdentities++
		rec, keys, planErr := processAgent(ctx, cs, cfg, id, now)
		if planErr != nil {
			rec.Classification = "error"
			rec.Issues = append(rec.Issues, "processing_error")
		}
		updateSummary(&rep.Summary, rec.Classification)
		rep.Agents = append(rep.Agents, rec)
		if len(keys) > 0 {
			rb.Entries = append(rb.Entries, rollbackEntry{AgentID: id.AgentID, Keys: keys})
		}
	}
	return rep, rb, nil
}

func identityMatchesConfig(id identity, cfg config) bool {
	if cfg.AgentID != "" && id.AgentID != cfg.AgentID {
		return false
	}
	return len(cfg.Domains) == 0 || containsFold(cfg.Domains, id.Domain)
}

func updateSummary(s *summary, classification string) {
	switch classification {
	case classificationAlreadyHealthy:
		s.AlreadyHealthy++
	case classificationPlanned:
		s.Eligible++
		s.Planned++
	case classificationApplied:
		s.Eligible++
		s.Applied++
	case classificationBlocked:
		s.Blocked++
	case "error":
		s.Errors++
	}
}

func processAgent(ctx context.Context, cs clients, cfg config, id identity, now time.Time) (agentReport, []keyRef, error) {
	rec := agentReport{AgentID: id.AgentID, Domain: id.Domain}
	channel, channelFound, err := getItem(ctx, cs.db, cfg.TableName, agentPK(id.AgentID), soulEmailChannelSortKey)
	if err != nil {
		return rec, nil, err
	}
	if channelFound {
		return processExistingCurrentChannel(ctx, cs, cfg, id, channel, rec)
	}
	return processAgentFromSourceTable(ctx, cs, cfg, id, now, rec)
}

func processExistingCurrentChannel(ctx context.Context, cs clients, cfg config, id identity, channel map[string]types.AttributeValue, rec agentReport) (agentReport, []keyRef, error) {
	if issue := validateSourceManagedEmailChannel(channel, id.AgentID); issue != "" {
		rec.Classification = classificationBlocked
		rec.Issues = append(rec.Issues, "existing_email_channel_is_not_a_healthy_managed_channel")
		return rec, nil, nil
	}
	secretExists, lookupErr := parameterExists(ctx, cs.ssm, itemString(channel, "secretRef"))
	if lookupErr != nil {
		return rec, nil, lookupErr
	}
	if !secretExists {
		rec.Classification = classificationBlocked
		rec.Issues = append(rec.Issues, "existing_email_channel_secret_parameter_missing")
		return rec, nil, nil
	}
	rec.SSMParameterPresent = true
	rec.RecoveredEmailSHA256 = sha256Hex(normalizeEmail(itemString(channel, "identifier")))
	issues, indexErr := validateCurrentManagedEmailIndexes(ctx, cs.db, cfg, id, channel)
	if indexErr != nil {
		return rec, nil, indexErr
	}
	if len(issues) > 0 {
		rec.Classification = classificationBlocked
		rec.Issues = append(rec.Issues, issues...)
		return rec, nil, nil
	}
	rec.Classification = classificationAlreadyHealthy
	return rec, nil, nil
}

func processAgentFromSourceTable(ctx context.Context, cs clients, cfg config, id identity, now time.Time, rec agentReport) (agentReport, []keyRef, error) {
	sourceIdentity, identityFound, err := getItem(ctx, cs.db, cfg.SourceTable, agentPK(id.AgentID), "IDENTITY")
	if err != nil {
		return rec, nil, err
	}
	if !identityFound || !strings.EqualFold(itemString(sourceIdentity, "agentId"), id.AgentID) ||
		!strings.EqualFold(itemString(sourceIdentity, "domain"), id.Domain) ||
		!strings.EqualFold(itemString(sourceIdentity, "localId"), id.LocalID) {
		rec.Classification = classificationBlocked
		rec.Issues = append(rec.Issues, "source_identity_boundary_mismatch")
		return rec, nil, nil
	}
	sourceChannel, found, err := getItem(ctx, cs.db, cfg.SourceTable, agentPK(id.AgentID), soulEmailChannelSortKey)
	if err != nil {
		return rec, nil, err
	}
	if !found {
		rec.Classification = classificationNotProvisioned
		return rec, nil, nil
	}
	if issue := validateSourceManagedEmailChannel(sourceChannel, id.AgentID); issue != "" {
		rec.Classification = classificationBlocked
		rec.Issues = append(rec.Issues, issue)
		return rec, nil, nil
	}
	secretRef := itemString(sourceChannel, "secretRef")
	secretExists, err := parameterExists(ctx, cs.ssm, secretRef)
	if err != nil {
		return rec, nil, err
	}
	if !secretExists {
		rec.Classification = classificationBlocked
		rec.Issues = append(rec.Issues, "source_channel_secret_parameter_missing")
		return rec, nil, nil
	}
	rec.SSMParameterPresent = true
	email := normalizeEmail(itemString(sourceChannel, "identifier"))
	rec.RecoveredEmailSHA256 = sha256Hex(email)

	writes, keys, issues, err := planSourceWrites(ctx, cs.db, cfg, id, sourceChannel, email, now)
	if err != nil {
		return rec, nil, err
	}
	return finishAgentPlan(ctx, cs.db, cfg, id, 0, rec, writes, keys, issues)
}

func finishAgentPlan(ctx context.Context, client dynamoAPI, cfg config, id identity, tokenVersion int, rec agentReport, writes []types.TransactWriteItem, keys []keyRef, issues []string) (agentReport, []keyRef, error) {
	if len(issues) > 0 {
		rec.Classification = classificationBlocked
		rec.Issues = append(rec.Issues, issues...)
		return rec, nil, nil
	}
	for _, key := range keys {
		rec.Actions = append(rec.Actions, actionName(key))
	}
	if !cfg.Apply {
		rec.Classification = classificationPlanned
		return rec, keys, nil
	}
	_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes, ClientRequestToken: aws.String(backfillToken(cfg.Stage, id.AgentID, tokenVersion))})
	if err != nil {
		return rec, nil, err
	}
	rec.Classification = classificationApplied
	return rec, keys, nil
}

func validateSourceManagedEmailChannel(item map[string]types.AttributeValue, agentID string) string {
	if !strings.EqualFold(itemString(item, "agentId"), agentID) {
		return "source_channel_agent_mismatch"
	}
	if !strings.EqualFold(itemString(item, "channelType"), "email") || !strings.EqualFold(itemString(item, "provider"), "migadu") {
		return "source_channel_not_managed_email"
	}
	if normalizeEmail(itemString(item, "identifier")) == "" || itemString(item, "secretRef") == "" {
		return "source_channel_missing_identity_or_secret_ref"
	}
	if !itemBool(item, "verified") || !strings.EqualFold(itemString(item, "status"), "active") || itemString(item, "provisionedAt") == "" || itemString(item, "deprovisionedAt") != "" {
		return "source_channel_not_active_provisioned"
	}
	return ""
}

func validateCurrentManagedEmailIndexes(ctx context.Context, client dynamoAPI, cfg config, id identity, channel map[string]types.AttributeValue) ([]string, error) {
	var issues []string
	emailPK := "SOUL#EMAIL#" + normalizeEmail(itemString(channel, "identifier"))
	emailIndex, found, err := getItem(ctx, client, cfg.TableName, emailPK, "AGENT")
	if err != nil {
		return nil, err
	}
	if !found || !strings.EqualFold(itemString(emailIndex, "agentId"), id.AgentID) {
		issues = append(issues, "existing_email_index_missing_or_mismatched")
	}

	channelIndexPK := soulEmailChannelIndexPK
	channelIndexSK := fmt.Sprintf("DOMAIN#%s#LOCAL#%s#AGENT#%s", strings.ToLower(strings.TrimSpace(id.Domain)), strings.ToLower(strings.TrimSpace(id.LocalID)), id.AgentID)
	channelIndex, found, err := getItem(ctx, client, cfg.TableName, channelIndexPK, channelIndexSK)
	if err != nil {
		return nil, err
	}
	if !found || !strings.EqualFold(itemString(channelIndex, "agentId"), id.AgentID) ||
		!strings.EqualFold(itemString(channelIndex, "channelType"), "email") ||
		!strings.EqualFold(itemString(channelIndex, "domain"), id.Domain) ||
		!strings.EqualFold(itemString(channelIndex, "localId"), id.LocalID) {
		issues = append(issues, "existing_channel_index_missing_or_mismatched")
	}
	return issues, nil
}

func planSourceWrites(ctx context.Context, client dynamoAPI, cfg config, id identity, sourceChannel map[string]types.AttributeValue, email string, now time.Time) ([]types.TransactWriteItem, []keyRef, []string, error) {
	plan := sourceWritePlan{}
	plan.writes, plan.keys = appendConditionalRawPut(plan.writes, plan.keys, cfg.TableName, sourceChannel)
	if err := planSourceEmailIndex(ctx, client, cfg, id, email, &plan); err != nil {
		return nil, nil, nil, err
	}
	if err := planSourceChannelIndex(ctx, client, cfg, id, &plan); err != nil {
		return nil, nil, nil, err
	}
	if err := planSourcePreferences(ctx, client, cfg, id, &plan); err != nil {
		return nil, nil, nil, err
	}
	if len(plan.issues) > 0 {
		return nil, nil, plan.issues, nil
	}
	auditItem := map[string]types.AttributeValue{
		"PK":        &types.AttributeValueMemberS{Value: "AUDIT#soul_agent:" + id.AgentID + ":channel:email"},
		"SK":        &types.AttributeValueMemberS{Value: fmt.Sprintf("EVENT#%s#email-regression-backfill-v1", now.Format(time.RFC3339Nano))},
		"id":        &types.AttributeValueMemberS{Value: "email-regression-backfill-v1"},
		"actor":     &types.AttributeValueMemberS{Value: "operator-backfill"},
		"action":    &types.AttributeValueMemberS{Value: "soul.channel.email.backfill"},
		"target":    &types.AttributeValueMemberS{Value: "soul_agent:" + id.AgentID + ":channel:email"},
		"details":   &types.AttributeValueMemberS{Value: "source=point-in-time-restored-table"},
		"requestID": &types.AttributeValueMemberS{Value: "email-regression-backfill-v1"},
		"createdAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
	}
	plan.writes, plan.keys = appendConditionalRawPut(plan.writes, plan.keys, cfg.TableName, auditItem)
	return plan.writes, plan.keys, nil, nil
}

type sourceWritePlan struct {
	writes []types.TransactWriteItem
	keys   []keyRef
	issues []string
}

func planSourceEmailIndex(ctx context.Context, client dynamoAPI, cfg config, id identity, email string, plan *sourceWritePlan) error {
	emailPK := "SOUL#EMAIL#" + email
	sourceEmailIndex, found, err := getItem(ctx, client, cfg.SourceTable, emailPK, "AGENT")
	if err != nil {
		return err
	}
	if !found || !strings.EqualFold(itemString(sourceEmailIndex, "agentId"), id.AgentID) || normalizeEmail(itemString(sourceEmailIndex, "email")) != email {
		plan.issues = append(plan.issues, "source_email_index_missing_or_mismatched")
	}
	current, currentFound, getErr := getItem(ctx, client, cfg.TableName, emailPK, "AGENT")
	if getErr != nil {
		return getErr
	} else if currentFound && !strings.EqualFold(itemString(current, "agentId"), id.AgentID) {
		plan.issues = append(plan.issues, "email_index_owned_by_another_agent")
	} else if currentFound && normalizeEmail(itemString(current, "email")) != email {
		plan.issues = append(plan.issues, "existing_email_index_mismatched")
	} else if !currentFound && found {
		plan.writes, plan.keys = appendConditionalRawPut(plan.writes, plan.keys, cfg.TableName, sourceEmailIndex)
	}
	return nil
}

func planSourceChannelIndex(ctx context.Context, client dynamoAPI, cfg config, id identity, plan *sourceWritePlan) error {
	channelIndexPK := soulEmailChannelIndexPK
	channelIndexSK := fmt.Sprintf("DOMAIN#%s#LOCAL#%s#AGENT#%s", strings.ToLower(strings.TrimSpace(id.Domain)), strings.ToLower(strings.TrimSpace(id.LocalID)), id.AgentID)
	sourceChannelIndex, found, err := getItem(ctx, client, cfg.SourceTable, channelIndexPK, channelIndexSK)
	if err != nil {
		return err
	}
	if !found || !validChannelIndex(sourceChannelIndex, id) {
		plan.issues = append(plan.issues, "source_channel_index_missing_or_mismatched")
	}
	current, currentFound, getErr := getItem(ctx, client, cfg.TableName, channelIndexPK, channelIndexSK)
	if getErr != nil {
		return getErr
	} else if currentFound && !validChannelIndex(current, id) {
		plan.issues = append(plan.issues, "existing_channel_index_mismatched")
	} else if !currentFound && found {
		plan.writes, plan.keys = appendConditionalRawPut(plan.writes, plan.keys, cfg.TableName, sourceChannelIndex)
	}
	return nil
}

func planSourcePreferences(ctx context.Context, client dynamoAPI, cfg config, id identity, plan *sourceWritePlan) error {
	sourcePrefs, prefsFound, err := getItem(ctx, client, cfg.SourceTable, agentPK(id.AgentID), "CONTACT_PREFERENCES")
	if err != nil {
		return err
	}
	if prefsFound && !strings.EqualFold(itemString(sourcePrefs, "agentId"), id.AgentID) {
		plan.issues = append(plan.issues, "source_contact_preferences_agent_mismatch")
	}
	current, currentFound, getErr := getItem(ctx, client, cfg.TableName, agentPK(id.AgentID), "CONTACT_PREFERENCES")
	if getErr != nil {
		return getErr
	} else if currentFound && !strings.EqualFold(itemString(current, "agentId"), id.AgentID) {
		plan.issues = append(plan.issues, "existing_contact_preferences_agent_mismatch")
	} else if !currentFound && prefsFound {
		plan.writes, plan.keys = appendConditionalRawPut(plan.writes, plan.keys, cfg.TableName, sourcePrefs)
	}
	return nil
}

func appendConditionalRawPut(writes []types.TransactWriteItem, keys []keyRef, table string, item map[string]types.AttributeValue) ([]types.TransactWriteItem, []keyRef) {
	pk, sk := itemString(item, "PK"), itemString(item, "SK")
	copyItem := make(map[string]types.AttributeValue, len(item))
	for key, value := range item {
		copyItem[key] = value
	}
	writes = append(writes, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(table), Item: copyItem, ConditionExpression: aws.String("attribute_not_exists(PK) AND attribute_not_exists(SK)")}})
	keys = append(keys, keyRef{PK: pk, SK: sk})
	return writes, keys
}

func validChannelIndex(item map[string]types.AttributeValue, id identity) bool {
	return strings.EqualFold(itemString(item, "agentId"), id.AgentID) &&
		strings.EqualFold(itemString(item, "channelType"), "email") &&
		strings.EqualFold(itemString(item, "domain"), id.Domain) &&
		strings.EqualFold(itemString(item, "localId"), id.LocalID)
}

func scanIdentities(ctx context.Context, client dynamoAPI, cfg config) ([]identity, error) {
	input := &dynamodb.ScanInput{TableName: aws.String(cfg.TableName), Limit: aws.Int32(cfg.PageSize), FilterExpression: aws.String("#sk = :identity"), ExpressionAttributeNames: map[string]string{"#sk": "SK"}, ExpressionAttributeValues: map[string]types.AttributeValue{":identity": &types.AttributeValueMemberS{Value: "IDENTITY"}}}
	var rows []identity
	for {
		out, err := client.Scan(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, item := range out.Items {
			id := identity{AgentID: strings.ToLower(itemString(item, "agentId")), Domain: strings.ToLower(itemString(item, "domain")), LocalID: strings.ToLower(itemString(item, "localId"))}
			if id.AgentID != "" {
				rows = append(rows, id)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].AgentID < rows[j].AgentID })
	return rows, nil
}

func parameterExists(ctx context.Context, client ssmAPI, name string) (bool, error) {
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(false)})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, err
	}
	return out.Parameter != nil, nil
}

func getItem(ctx context.Context, client dynamoAPI, table, pk, sk string) (map[string]types.AttributeValue, bool, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: pk}, "SK": &types.AttributeValueMemberS{Value: sk}}})
	if err != nil {
		return nil, false, err
	}
	return out.Item, len(out.Item) > 0, nil
}

func itemBool(item map[string]types.AttributeValue, key string) bool {
	if v, ok := item[key].(*types.AttributeValueMemberBOOL); ok {
		return v.Value
	}
	return false
}

func itemString(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key].(*types.AttributeValueMemberS); ok {
		return strings.TrimSpace(v.Value)
	}
	if v, ok := item[key].(*types.AttributeValueMemberN); ok {
		return strings.TrimSpace(v.Value)
	}
	return ""
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func agentPK(agentID string) string {
	return "SOUL#AGENT#" + strings.ToLower(strings.TrimSpace(agentID))
}
func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func actionName(key keyRef) string {
	switch {
	case key.SK == soulEmailChannelSortKey:
		return "create_email_channel"
	case key.SK == "AGENT" && strings.HasPrefix(key.PK, "SOUL#EMAIL#"):
		return "create_email_index"
	case key.SK == "CONTACT_PREFERENCES":
		return "create_contact_preferences"
	case strings.HasPrefix(key.PK, soulEmailChannelIndexPK):
		return "create_channel_index"
	case strings.HasPrefix(key.PK, "AUDIT#"):
		return "create_audit_event"
	default:
		return "create_recovery_record"
	}
}

func backfillToken(stage, agentID string, version int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("email-regression-backfill-v1|%s|%s|%d", stage, agentID, version)))
	return hex.EncodeToString(sum[:])
}

func writeReport(rep report, path string, stdout io.Writer) error {
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if strings.TrimSpace(path) == "" || path == "-" {
		_, err = stdout.Write(raw)
		return err
	}
	return os.WriteFile(path, raw, 0o600) // #nosec G703 -- operator-selected local evidence path; no file content is used as a path.
}

func writeJSONFile(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600) // #nosec G703 -- operator-selected local evidence path; no file content is used as a path.
}

func reservePrivateFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G703 -- operator-selected local evidence path is intentionally created without overwrite.
	if err != nil {
		return err
	}
	return f.Close()
}
