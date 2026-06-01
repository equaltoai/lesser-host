package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
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
	inventorySchemaVersion = 1
	canonicalEmailDomain   = "lessersoul.ai"
	smtpLocalPartLimit     = 64
	defaultInventoryStage  = "lab"
	signingStateYes        = "yes"
	signingStateNo         = "no"
	signingStateUnknown    = "unknown"
)

var managedInstanceSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`)

type dynamoClient interface {
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

type inventoryConfig struct {
	Stage                 string
	TableName             string
	ProviderStatePath     string
	RegistrationStatePath string
	OutputPath            string
	PageSize              int32
	MaxAgents             int
	IncludeInactive       bool
}

type inventoryReport struct {
	SchemaVersion int                     `json:"schema_version"`
	GeneratedAt   string                  `json:"generated_at"`
	Mode          string                  `json:"mode"`
	Stage         string                  `json:"stage"`
	TableName     string                  `json:"table_name"`
	Contract      inventoryContract       `json:"contract"`
	Summary       inventorySummary        `json:"summary"`
	Agents        []managedEmailInventory `json:"agents"`
	Issues        []string                `json:"issues,omitempty"`
}

type inventoryContract struct {
	CanonicalAddressFormat string `json:"canonical_address_format"`
	ProviderLocalPart      string `json:"provider_local_part"`
	SMTPLocalPartLimit     int    `json:"smtp_local_part_limit"`
	InstanceSlugSource     string `json:"instance_slug_source"`
	LegacyAliasPolicy      string `json:"legacy_alias_policy"`
}

type inventorySummary struct {
	ScannedIdentities         int `json:"scanned_identities"`
	EligibleManagedEmail      int `json:"eligible_managed_email"`
	SkippedInactive           int `json:"skipped_inactive"`
	MissingEmailChannel       int `json:"missing_email_channel"`
	MissingDomainRecord       int `json:"missing_domain_record"`
	InactiveDomainRecord      int `json:"inactive_domain_record"`
	MissingInstanceSlug       int `json:"missing_instance_slug"`
	InvalidInstanceSlug       int `json:"invalid_instance_slug"`
	ProposedDuplicateCount    int `json:"proposed_duplicate_count"`
	ProposedOverflowCount     int `json:"proposed_overflow_count"`
	MissingCurrentEmailIndex  int `json:"missing_current_email_index"`
	CurrentEmailIndexMismatch int `json:"current_email_index_mismatch"`
	ProviderStateEntries      int `json:"provider_state_entries"`
}

type managedEmailInventory struct {
	AgentID             string                     `json:"agent_id"`
	Domain              string                     `json:"domain"`
	LocalID             string                     `json:"local_id"`
	Status              string                     `json:"status"`
	LifecycleStatus     string                     `json:"lifecycle_status,omitempty"`
	SelfDescription     selfDescriptionInventory   `json:"self_description"`
	DomainRecord        *domainInventory           `json:"domain_record,omitempty"`
	CurrentEmailChannel *emailChannelInventory     `json:"current_email_channel,omitempty"`
	CurrentEmailIndex   *emailIndexInventory       `json:"current_email_index,omitempty"`
	RegistrationVersion *registrationVersionRecord `json:"registration_version,omitempty"`
	Proposed            proposedEmailInventory     `json:"proposed"`
	ProviderState       providerInventory          `json:"provider_state"`
	MigrationReadiness  migrationReadiness         `json:"migration_readiness"`
	Issues              []string                   `json:"issues,omitempty"`
}

type selfDescriptionInventory struct {
	Version int  `json:"version"`
	Active  bool `json:"active"`
}

type domainInventory struct {
	Domain             string `json:"domain"`
	InstanceSlug       string `json:"instance_slug"`
	Type               string `json:"type,omitempty"`
	Status             string `json:"status,omitempty"`
	VerificationMethod string `json:"verification_method,omitempty"`
	Resolution         string `json:"resolution"`
}

type emailChannelInventory struct {
	Identifier string `json:"identifier"`
	Provider   string `json:"provider,omitempty"`
	Status     string `json:"status,omitempty"`
	Verified   bool   `json:"verified"`
	SecretRef  string `json:"secret_ref_present,omitempty"`
}

type emailIndexInventory struct {
	Email          string `json:"email"`
	AgentID        string `json:"agent_id"`
	MatchesAgent   bool   `json:"matches_agent"`
	MatchesChannel bool   `json:"matches_channel"`
}

type registrationVersionRecord struct {
	VersionNumber          int    `json:"version_number"`
	RegistrationURI        string `json:"registration_uri,omitempty"`
	RegistrationSHA256     string `json:"registration_sha256,omitempty"`
	ChangeSummary          string `json:"change_summary,omitempty"`
	SelfAttestationPresent bool   `json:"self_attestation_present"`
	EmailChannel           string `json:"email_channel,omitempty"`
	Source                 string `json:"source,omitempty"`
}

type proposedEmailInventory struct {
	LocalPart       string `json:"local_part,omitempty"`
	Address         string `json:"address,omitempty"`
	LocalPartLength int    `json:"local_part_length"`
	Overflow        bool   `json:"overflow"`
	Duplicate       bool   `json:"duplicate"`
}

type providerInventory struct {
	Current  *providerAddressState `json:"current,omitempty"`
	Proposed *providerAddressState `json:"proposed,omitempty"`
	Source   string                `json:"source"`
}

type migrationReadiness struct {
	RequiresSelfAttestation bool   `json:"requires_self_attestation"`
	CanSelfAttest           string `json:"can_self_attest"`
	HostMigrationPathNeeded bool   `json:"host_migration_path_needed"`
}

type providerSnapshot struct {
	GeneratedAt string                 `json:"generated_at,omitempty"`
	Source      string                 `json:"source,omitempty"`
	Addresses   []providerAddressState `json:"addresses"`
}

type registrationSnapshot struct {
	GeneratedAt string                      `json:"generated_at,omitempty"`
	Source      string                      `json:"source,omitempty"`
	Agents      []registrationSnapshotAgent `json:"agents"`
}

type registrationSnapshotAgent struct {
	AgentID       string `json:"agent_id"`
	EmailChannel  string `json:"email_channel,omitempty"`
	CanSelfAttest string `json:"can_self_attest,omitempty"`
	Source        string `json:"source,omitempty"`
}

type providerAddressState struct {
	LocalPart     string   `json:"local_part"`
	Address       string   `json:"address,omitempty"`
	MailboxExists *bool    `json:"mailbox_exists,omitempty"`
	Forwardings   []string `json:"forwardings,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

type identityRow struct {
	AgentID                string
	Domain                 string
	LocalID                string
	Status                 string
	LifecycleStatus        string
	SelfDescriptionVersion int
}

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		die("%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		die("load aws config: %v", err)
	}

	report, err := buildInventoryReport(ctx, dynamodb.NewFromConfig(awsCfg), cfg, time.Now().UTC())
	if err != nil {
		die("inventory failed: %v", err)
	}
	if err := writeReport(report, cfg.OutputPath, os.Stdout); err != nil {
		die("write report: %v", err)
	}
	if report.Summary.ProposedDuplicateCount > 0 || report.Summary.ProposedOverflowCount > 0 || len(report.Issues) > 0 {
		os.Exit(2)
	}
}

func parseConfig(args []string, getenv func(string) string) (inventoryConfig, error) {
	stageDefault := strings.ToLower(strings.TrimSpace(getenv("STAGE")))
	if stageDefault == "" {
		stageDefault = defaultInventoryStage
	}
	tableDefault := strings.TrimSpace(getenv("STATE_TABLE_NAME"))
	if tableDefault == "" {
		tableDefault = fmt.Sprintf("lesser-host-%s-state", stageDefault)
	}
	cfg := inventoryConfig{Stage: stageDefault, TableName: tableDefault, PageSize: 100}
	fs := flag.NewFlagSet("soul-email-m0-inventory", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Stage, "stage", cfg.Stage, "Control-plane stage used for managed stage-domain alias resolution")
	fs.StringVar(&cfg.TableName, "table-name", cfg.TableName, "DynamoDB state table name")
	fs.StringVar(&cfg.ProviderStatePath, "provider-state", "", "Optional redacted provider-state JSON snapshot")
	fs.StringVar(&cfg.RegistrationStatePath, "registration-state", "", "Optional redacted registration/signing-state JSON snapshot")
	fs.StringVar(&cfg.OutputPath, "out", "", "Output JSON path (default stdout)")
	fs.IntVar(&cfg.MaxAgents, "max-agents", 0, "Maximum eligible agents to include (0 = unlimited)")
	fs.BoolVar(&cfg.IncludeInactive, "include-inactive", false, "Include inactive/suspended/archived identities in the report")
	pageSize := int(cfg.PageSize)
	fs.IntVar(&pageSize, "page-size", pageSize, "DynamoDB scan page size")
	if err := fs.Parse(args); err != nil {
		return inventoryConfig{}, err
	}
	cfg.Stage = strings.ToLower(strings.TrimSpace(cfg.Stage))
	if cfg.Stage == "" {
		cfg.Stage = defaultInventoryStage
	}
	cfg.TableName = strings.TrimSpace(cfg.TableName)
	if cfg.TableName == "" {
		return inventoryConfig{}, errors.New("table name is required")
	}
	if pageSize <= 0 || pageSize > 1000 {
		return inventoryConfig{}, errors.New("page-size must be between 1 and 1000")
	}
	cfg.PageSize = int32(pageSize)
	if cfg.MaxAgents < 0 {
		return inventoryConfig{}, errors.New("max-agents must be non-negative")
	}
	return cfg, nil
}

func buildInventoryReport(ctx context.Context, client dynamoClient, cfg inventoryConfig, now time.Time) (inventoryReport, error) {
	inputs, err := loadInventoryInputs(cfg)
	if err != nil {
		return inventoryReport{}, err
	}
	idents, err := scanIdentityRows(ctx, client, cfg)
	if err != nil {
		return inventoryReport{}, err
	}

	report := newInventoryReport(cfg, now, inputs.providerStateCount)
	for _, ident := range idents {
		report.Summary.ScannedIdentities++
		if !identityIsActive(ident) && !cfg.IncludeInactive {
			report.Summary.SkippedInactive++
			continue
		}

		result, err := buildInventoryRecord(ctx, client, cfg, ident, inputs)
		if err != nil {
			return inventoryReport{}, err
		}
		report.Summary = mergeInventorySummary(report.Summary, result.Summary)
		if result.Include {
			report.Agents = append(report.Agents, result.Record)
		}
		if cfg.MaxAgents > 0 && report.Summary.EligibleManagedEmail >= cfg.MaxAgents {
			break
		}
	}
	finalizeInventoryReport(&report)
	return report, nil
}

type loadedInventoryInputs struct {
	providerStates     map[string]providerAddressState
	providerSource     string
	providerStateCount int
	registrationStates map[string]registrationSnapshotAgent
}

type inventoryRecordResult struct {
	Record  managedEmailInventory
	Summary inventorySummary
	Include bool
}

func loadInventoryInputs(cfg inventoryConfig) (loadedInventoryInputs, error) {
	providerStates, providerSource, err := loadProviderSnapshot(cfg.ProviderStatePath)
	if err != nil {
		return loadedInventoryInputs{}, err
	}
	registrationStates, err := loadRegistrationSnapshot(cfg.RegistrationStatePath)
	if err != nil {
		return loadedInventoryInputs{}, err
	}
	return loadedInventoryInputs{
		providerStates:     providerStates,
		providerSource:     providerSource,
		providerStateCount: len(providerStates),
		registrationStates: registrationStates,
	}, nil
}

func newInventoryReport(cfg inventoryConfig, now time.Time, providerStateCount int) inventoryReport {
	return inventoryReport{
		SchemaVersion: inventorySchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Mode:          "dry-run",
		Stage:         cfg.Stage,
		TableName:     cfg.TableName,
		Contract: inventoryContract{
			CanonicalAddressFormat: "<agent-local-id>.<instance-slug>@lessersoul.ai",
			ProviderLocalPart:      "<agent-local-id>.<instance-slug>",
			SMTPLocalPartLimit:     smtpLocalPartLimit,
			InstanceSlugSource:     "Domain.InstanceSlug",
			LegacyAliasPolicy:      "host-internal legacy alias index canonicalizes existing bare recipients to the current instance-scoped address before comm-worker channel matching",
		},
		Summary: inventorySummary{ProviderStateEntries: providerStateCount},
	}
}

func buildInventoryRecord(ctx context.Context, client dynamoClient, cfg inventoryConfig, ident identityRow, inputs loadedInventoryInputs) (inventoryRecordResult, error) {
	result := inventoryRecordResult{Record: baseInventoryRecord(ident, inputs.providerSource), Include: true}
	_, managed, err := applyEmailChannelInventory(ctx, client, cfg, &result, ident, inputs.providerStates)
	if err != nil || !managed {
		return result, err
	}
	if err := applyDomainInventory(ctx, client, cfg, &result, ident); err != nil {
		return inventoryRecordResult{}, err
	}
	if err := applyRegistrationInventory(ctx, client, cfg, &result, ident, inputs.registrationStates); err != nil {
		return inventoryRecordResult{}, err
	}
	applyProposedInventory(&result, ident, inputs.providerStates)
	result.Summary.EligibleManagedEmail++
	return result, nil
}

func baseInventoryRecord(ident identityRow, providerSource string) managedEmailInventory {
	active := identityIsActive(ident)
	return managedEmailInventory{
		AgentID:         ident.AgentID,
		Domain:          ident.Domain,
		LocalID:         ident.LocalID,
		Status:          ident.Status,
		LifecycleStatus: ident.LifecycleStatus,
		SelfDescription: selfDescriptionInventory{Version: ident.SelfDescriptionVersion, Active: active},
		ProviderState:   providerInventory{Source: providerSource},
		MigrationReadiness: migrationReadiness{
			RequiresSelfAttestation: true,
			CanSelfAttest:           signingStateUnknown,
			HostMigrationPathNeeded: true,
		},
	}
}

func applyEmailChannelInventory(ctx context.Context, client dynamoClient, cfg inventoryConfig, result *inventoryRecordResult, ident identityRow, providerStates map[string]providerAddressState) (*emailChannelInventory, bool, error) {
	channel, found, err := loadEmailChannel(ctx, client, cfg.TableName, ident.AgentID)
	if err != nil || !found {
		if !found {
			result.Summary.MissingEmailChannel++
			result.Record.Issues = append(result.Record.Issues, "missing_email_channel")
		}
		return nil, false, err
	}
	result.Record.CurrentEmailChannel = channel
	if !strings.EqualFold(channel.Provider, "migadu") {
		result.Record.Issues = append(result.Record.Issues, "email_channel_provider_not_migadu")
		return channel, false, nil
	}
	result.Record.ProviderState.Current = providerStateForEmail(providerStates, channel.Identifier)
	if err := applyEmailIndexInventory(ctx, client, cfg.TableName, result, ident, channel.Identifier); err != nil {
		return nil, false, err
	}
	return channel, true, nil
}

func applyEmailIndexInventory(ctx context.Context, client dynamoClient, tableName string, result *inventoryRecordResult, ident identityRow, address string) error {
	idx, found, err := loadEmailIndex(ctx, client, tableName, address)
	if err != nil {
		return err
	}
	if !found {
		result.Summary.MissingCurrentEmailIndex++
		result.Record.Issues = append(result.Record.Issues, "missing_current_email_index")
		return nil
	}
	idx.MatchesAgent = strings.EqualFold(idx.AgentID, ident.AgentID)
	idx.MatchesChannel = strings.EqualFold(idx.Email, address)
	result.Record.CurrentEmailIndex = idx
	if !idx.MatchesAgent || !idx.MatchesChannel {
		result.Summary.CurrentEmailIndexMismatch++
		result.Record.Issues = append(result.Record.Issues, "current_email_index_mismatch")
	}
	return nil
}

func applyDomainInventory(ctx context.Context, client dynamoClient, cfg inventoryConfig, result *inventoryRecordResult, ident identityRow) error {
	domain, found, err := loadDomainForIdentity(ctx, client, cfg.TableName, cfg.Stage, ident.Domain)
	if err != nil {
		return err
	}
	if !found {
		result.Summary.MissingDomainRecord++
		result.Record.Issues = append(result.Record.Issues, "missing_domain_record")
		return nil
	}
	result.Record.DomainRecord = domain
	if !domainStatusActive(domain.Status) {
		result.Summary.InactiveDomainRecord++
		result.Record.Issues = append(result.Record.Issues, "inactive_domain_record")
	}
	if strings.TrimSpace(domain.InstanceSlug) == "" {
		result.Summary.MissingInstanceSlug++
		result.Record.Issues = append(result.Record.Issues, "missing_instance_slug")
	} else if !managedInstanceSlugRE.MatchString(domain.InstanceSlug) {
		result.Summary.InvalidInstanceSlug++
		result.Record.Issues = append(result.Record.Issues, "invalid_instance_slug")
	}
	return nil
}

func applyRegistrationInventory(ctx context.Context, client dynamoClient, cfg inventoryConfig, result *inventoryRecordResult, ident identityRow, states map[string]registrationSnapshotAgent) error {
	version, found, err := loadRegistrationVersion(ctx, client, cfg.TableName, ident.AgentID, ident.SelfDescriptionVersion)
	if err != nil || !found {
		return err
	}
	if regState, ok := states[ident.AgentID]; ok {
		version.EmailChannel = normalizeEmail(regState.EmailChannel)
		version.Source = strings.TrimSpace(regState.Source)
		result.Record.MigrationReadiness.CanSelfAttest = normalizeSigningState(regState.CanSelfAttest)
		result.Record.MigrationReadiness.HostMigrationPathNeeded = result.Record.MigrationReadiness.CanSelfAttest != signingStateYes
	}
	result.Record.RegistrationVersion = version
	return nil
}

func applyProposedInventory(result *inventoryRecordResult, ident identityRow, providerStates map[string]providerAddressState) {
	if result.Record.DomainRecord == nil {
		return
	}
	proposed, err := buildProposedManagedAddress(ident.LocalID, result.Record.DomainRecord.InstanceSlug)
	result.Record.Proposed = proposed
	if err != nil {
		result.Record.Issues = append(result.Record.Issues, err.Error())
	}
	if proposed.Overflow {
		result.Summary.ProposedOverflowCount++
	}
	result.Record.ProviderState.Proposed = providerStateForEmail(providerStates, proposed.Address)
}

func mergeInventorySummary(dst, src inventorySummary) inventorySummary {
	dst.EligibleManagedEmail += src.EligibleManagedEmail
	dst.MissingEmailChannel += src.MissingEmailChannel
	dst.MissingDomainRecord += src.MissingDomainRecord
	dst.InactiveDomainRecord += src.InactiveDomainRecord
	dst.MissingInstanceSlug += src.MissingInstanceSlug
	dst.InvalidInstanceSlug += src.InvalidInstanceSlug
	dst.ProposedOverflowCount += src.ProposedOverflowCount
	dst.MissingCurrentEmailIndex += src.MissingCurrentEmailIndex
	dst.CurrentEmailIndexMismatch += src.CurrentEmailIndexMismatch
	return dst
}

func finalizeInventoryReport(report *inventoryReport) {
	annotateProposedDuplicates(report.Agents)
	for _, rec := range report.Agents {
		if rec.Proposed.Duplicate {
			report.Summary.ProposedDuplicateCount++
		}
	}
	report.Issues = appendReportIssues(report.Issues, report.Summary)
	sort.Slice(report.Agents, func(i, j int) bool {
		if report.Agents[i].Domain == report.Agents[j].Domain {
			return report.Agents[i].LocalID < report.Agents[j].LocalID
		}
		return report.Agents[i].Domain < report.Agents[j].Domain
	})
}

func appendReportIssues(issues []string, summary inventorySummary) []string {
	if summary.MissingDomainRecord > 0 {
		issues = appendUnique(issues, "missing_domain_record")
	}
	if summary.InactiveDomainRecord > 0 {
		issues = appendUnique(issues, "inactive_domain_record")
	}
	if summary.MissingInstanceSlug > 0 {
		issues = appendUnique(issues, "missing_instance_slug")
	}
	if summary.InvalidInstanceSlug > 0 {
		issues = appendUnique(issues, "invalid_instance_slug")
	}
	if summary.MissingCurrentEmailIndex > 0 {
		issues = appendUnique(issues, "missing_current_email_index")
	}
	if summary.CurrentEmailIndexMismatch > 0 {
		issues = appendUnique(issues, "current_email_index_mismatch")
	}
	return issues
}

func scanIdentityRows(ctx context.Context, client dynamoClient, cfg inventoryConfig) ([]identityRow, error) {
	if client == nil {
		return nil, errors.New("dynamodb client is required")
	}
	var rows []identityRow
	input := &dynamodb.ScanInput{
		TableName:                aws.String(cfg.TableName),
		Limit:                    aws.Int32(cfg.PageSize),
		FilterExpression:         aws.String("#sk = :sk"),
		ExpressionAttributeNames: map[string]string{"#sk": "SK"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":sk": &types.AttributeValueMemberS{Value: "IDENTITY"},
		},
	}
	for {
		out, err := client.Scan(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, item := range out.Items {
			row := identityRow{
				AgentID:                strings.ToLower(strings.TrimSpace(itemString(item, "agentId"))),
				Domain:                 normalizeDomain(itemString(item, "domain")),
				LocalID:                strings.TrimSpace(itemString(item, "localId")),
				Status:                 strings.ToLower(strings.TrimSpace(itemString(item, "status"))),
				LifecycleStatus:        strings.ToLower(strings.TrimSpace(itemString(item, "lifecycleStatus"))),
				SelfDescriptionVersion: itemInt(item, "selfDescriptionVersion"),
			}
			if row.AgentID == "" {
				continue
			}
			rows = append(rows, row)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return rows, nil
}

func loadEmailChannel(ctx context.Context, client dynamoClient, tableName, agentID string) (*emailChannelInventory, bool, error) {
	item, found, err := getItem(ctx, client, tableName, "SOUL#AGENT#"+strings.ToLower(strings.TrimSpace(agentID)), "CHANNEL#email")
	if err != nil || !found {
		return nil, found, err
	}
	secretRef := ""
	if strings.TrimSpace(itemString(item, "secretRef")) != "" {
		secretRef = "present"
	}
	return &emailChannelInventory{
		Identifier: normalizeEmail(itemString(item, "identifier")),
		Provider:   strings.ToLower(strings.TrimSpace(itemString(item, "provider"))),
		Status:     strings.ToLower(strings.TrimSpace(itemString(item, "status"))),
		Verified:   itemBool(item, "verified"),
		SecretRef:  secretRef,
	}, true, nil
}

func loadEmailIndex(ctx context.Context, client dynamoClient, tableName, email string) (*emailIndexInventory, bool, error) {
	email = normalizeEmail(email)
	item, found, err := getItem(ctx, client, tableName, "SOUL#EMAIL#"+email, "AGENT")
	if err != nil || !found {
		return nil, found, err
	}
	return &emailIndexInventory{Email: normalizeEmail(itemString(item, "email")), AgentID: strings.ToLower(strings.TrimSpace(itemString(item, "agentId")))}, true, nil
}

func loadRegistrationVersion(ctx context.Context, client dynamoClient, tableName, agentID string, version int) (*registrationVersionRecord, bool, error) {
	if version <= 0 {
		return nil, false, nil
	}
	item, found, err := getItem(ctx, client, tableName, "SOUL#AGENT#"+strings.ToLower(strings.TrimSpace(agentID)), fmt.Sprintf("VERSION#%d", version))
	if err != nil || !found {
		return nil, found, err
	}
	return &registrationVersionRecord{
		VersionNumber:          version,
		RegistrationURI:        strings.TrimSpace(itemString(item, "registrationUri")),
		RegistrationSHA256:     strings.TrimSpace(itemString(item, "registrationSha256")),
		ChangeSummary:          strings.TrimSpace(itemString(item, "changeSummary")),
		SelfAttestationPresent: strings.TrimSpace(itemString(item, "selfAttestation")) != "",
	}, true, nil
}

func loadDomainForIdentity(ctx context.Context, client dynamoClient, tableName, stage, rawDomain string) (*domainInventory, bool, error) {
	domain := normalizeDomain(rawDomain)
	if domain == "" {
		return nil, false, nil
	}
	if rec, found, err := loadDomainRecord(ctx, client, tableName, domain, "exact"); err != nil || found {
		return rec, found, err
	}
	baseDomain, ok := manageddomain.BaseDomainFromStageDomain(stage, domain)
	if !ok {
		return nil, false, nil
	}
	rec, found, err := loadDomainRecord(ctx, client, tableName, baseDomain, "managed_stage_primary_alias")
	if err != nil || !found {
		return rec, found, err
	}
	if rec.Type != models.DomainTypePrimary || rec.VerificationMethod != "managed" || !domainStatusActive(rec.Status) {
		return nil, false, nil
	}
	inst, instFound, err := loadInstance(ctx, client, tableName, rec.InstanceSlug)
	if err != nil || !instFound {
		return nil, false, err
	}
	if !strings.EqualFold(strings.TrimSpace(inst.HostedBaseDomain), rec.Domain) {
		return nil, false, nil
	}
	return rec, true, nil
}

type instanceInventory struct {
	Slug             string
	HostedBaseDomain string
}

func loadInstance(ctx context.Context, client dynamoClient, tableName, slug string) (instanceInventory, bool, error) {
	item, found, err := getItem(ctx, client, tableName, "INSTANCE#"+strings.TrimSpace(slug), models.SKMetadata)
	if err != nil || !found {
		return instanceInventory{}, found, err
	}
	return instanceInventory{Slug: strings.TrimSpace(itemString(item, "slug")), HostedBaseDomain: normalizeDomain(itemString(item, "hostedBaseDomain"))}, true, nil
}

func loadDomainRecord(ctx context.Context, client dynamoClient, tableName, domain, resolution string) (*domainInventory, bool, error) {
	item, found, err := getItem(ctx, client, tableName, "DOMAIN#"+normalizeDomain(domain), models.SKMetadata)
	if err != nil || !found {
		return nil, found, err
	}
	return &domainInventory{
		Domain:             normalizeDomain(itemString(item, "domain")),
		InstanceSlug:       strings.TrimSpace(itemString(item, "instanceSlug")),
		Type:               strings.ToLower(strings.TrimSpace(itemString(item, "type"))),
		Status:             strings.ToLower(strings.TrimSpace(itemString(item, "status"))),
		VerificationMethod: strings.ToLower(strings.TrimSpace(itemString(item, "verificationMethod"))),
		Resolution:         resolution,
	}, true, nil
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
	return out.Item, true, nil
}

func buildProposedManagedAddress(agentLocalID, instanceSlug string) (proposedEmailInventory, error) {
	localID, err := soul.ValidateManagedHandle(agentLocalID)
	if err != nil || localID == "" {
		return proposedEmailInventory{}, errors.New("invalid_agent_local_id")
	}
	instanceSlug, err = soul.ValidateManagedInstanceSlug(instanceSlug)
	if err != nil || !managedInstanceSlugRE.MatchString(instanceSlug) {
		return proposedEmailInventory{}, errors.New("invalid_instance_slug")
	}
	localPart := localID + "." + instanceSlug
	proposed := proposedEmailInventory{
		LocalPart:       localPart,
		Address:         localPart + "@" + canonicalEmailDomain,
		LocalPartLength: len(localPart),
		Overflow:        len(localPart) > smtpLocalPartLimit,
	}
	if proposed.Overflow {
		return proposed, errors.New("proposed_local_part_overflow")
	}
	return proposed, nil
}

func annotateProposedDuplicates(records []managedEmailInventory) {
	counts := map[string]int{}
	for _, rec := range records {
		addr := normalizeEmail(rec.Proposed.Address)
		if addr == "" {
			continue
		}
		counts[addr]++
	}
	for i := range records {
		addr := normalizeEmail(records[i].Proposed.Address)
		if addr != "" && counts[addr] > 1 {
			records[i].Proposed.Duplicate = true
			records[i].Issues = appendUnique(records[i].Issues, "duplicate_proposed_address")
		}
	}
}

func loadProviderSnapshot(path string) (map[string]providerAddressState, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]providerAddressState{}, "not_collected", nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // Operator-provided local snapshot path for dry-run inventory only.
	if err != nil {
		return nil, "", err
	}
	var snap providerSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, "", err
	}
	out := make(map[string]providerAddressState, len(snap.Addresses)*2)
	for _, state := range snap.Addresses {
		state.LocalPart = strings.ToLower(strings.TrimSpace(state.LocalPart))
		state.Address = normalizeEmail(state.Address)
		if state.Address == "" && state.LocalPart != "" {
			state.Address = state.LocalPart + "@" + canonicalEmailDomain
		}
		state.Forwardings = normalizeEmailList(state.Forwardings)
		state.Aliases = normalizeStringList(state.Aliases)
		if state.LocalPart != "" {
			out[state.LocalPart] = state
		}
		if state.Address != "" {
			out[state.Address] = state
		}
	}
	source := strings.TrimSpace(snap.Source)
	if source == "" {
		source = path
	}
	return out, source, nil
}

func loadRegistrationSnapshot(path string) (map[string]registrationSnapshotAgent, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]registrationSnapshotAgent{}, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // Operator-provided local snapshot path for dry-run inventory only.
	if err != nil {
		return nil, err
	}
	var snap registrationSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	out := make(map[string]registrationSnapshotAgent, len(snap.Agents))
	for _, agent := range snap.Agents {
		agent.AgentID = strings.ToLower(strings.TrimSpace(agent.AgentID))
		agent.EmailChannel = normalizeEmail(agent.EmailChannel)
		agent.CanSelfAttest = normalizeSigningState(agent.CanSelfAttest)
		if strings.TrimSpace(agent.Source) == "" {
			agent.Source = strings.TrimSpace(snap.Source)
		}
		if agent.AgentID != "" {
			out[agent.AgentID] = agent
		}
	}
	return out, nil
}

func normalizeSigningState(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "true", "can_self_attest":
		return signingStateYes
	case "no", "false", "cannot_self_attest":
		return signingStateNo
	default:
		return signingStateUnknown
	}
}

func providerStateForEmail(states map[string]providerAddressState, email string) *providerAddressState {
	if len(states) == 0 {
		return nil
	}
	email = normalizeEmail(email)
	localPart, _, ok := strings.Cut(email, "@")
	if state, found := states[email]; found {
		return &state
	}
	if ok {
		if state, found := states[localPart]; found {
			return &state
		}
	}
	return nil
}

func identityIsActive(row identityRow) bool {
	status := strings.ToLower(strings.TrimSpace(row.Status))
	lifecycle := strings.ToLower(strings.TrimSpace(row.LifecycleStatus))
	return status == models.SoulAgentStatusActive && (lifecycle == "" || lifecycle == models.SoulAgentStatusActive)
}

func domainStatusActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.DomainStatusVerified, models.DomainStatusActive:
		return true
	default:
		return false
	}
}

func writeReport(report inventoryReport, outputPath string, stdout io.Writer) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" || outputPath == "-" {
		_, err = stdout.Write(raw)
		return err
	}
	return os.WriteFile(outputPath, raw, 0o600) //nolint:gosec // Inventory evidence is written only to the operator-selected local output path.
}

func itemString(item map[string]types.AttributeValue, name string) string {
	if item == nil {
		return ""
	}
	switch v := item[name].(type) {
	case *types.AttributeValueMemberS:
		return strings.TrimSpace(v.Value)
	case *types.AttributeValueMemberN:
		return strings.TrimSpace(v.Value)
	default:
		return ""
	}
}

func itemInt(item map[string]types.AttributeValue, name string) int {
	raw := itemString(item, name)
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return v
}

func itemBool(item map[string]types.AttributeValue, name string) bool {
	if item == nil {
		return false
	}
	switch v := item[name].(type) {
	case *types.AttributeValueMemberBOOL:
		return v.Value
	case *types.AttributeValueMemberS:
		return strings.EqualFold(strings.TrimSpace(v.Value), "true")
	default:
		return false
	}
}

func normalizeDomain(raw string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
}

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeEmailList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if v := normalizeEmail(value); v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if v := strings.ToLower(strings.TrimSpace(value)); v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
