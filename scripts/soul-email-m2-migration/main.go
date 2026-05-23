package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/secrets"
)

const (
	migrationSchemaVersion = 1
	defaultStage           = "lab"
	canonicalEmailDomain   = "lessersoul.ai"
	defaultInboundDomain   = "inbound.lessersoul.ai"
	migaduBaseURL          = "https://api.migadu.com/v1"

	migrationStateInventoryDryRun        = "inventory_dry_run"
	migrationStateProviderPreparePending = "provider_prepare_pending"
	migrationStateProviderPrepared       = "provider_prepared"
	migrationStateHostSwitched           = "host_switched"
	migrationStateNeedsRepair            = "needs_repair"

	actionEnsureProviderForwarding = "ensure_provider_mailbox_forwarding"
	actionPreserveLegacyProvider   = "preserve_legacy_provider_delivery"
	actionCollectSelfAttestation   = "collect_self_attestation_or_host_audit_receipt"
	actionPublishRegistration      = "publish_registration_with_instance_scoped_email"
	actionHostSync                 = "host_channel_index_legacy_alias_sync"
	actionVerifyLegacyAlias        = "verify_legacy_alias_canonicalization"

	signingStateYes     = "yes"
	signingStateNo      = "no"
	signingStateUnknown = "unknown"
)

var (
	migaduCredsLoader    = secrets.MigaduCreds
	migaduPasswordLoader = func(ctx context.Context, stage string, agentID string) (string, error) {
		return secrets.GetSSMParameter(ctx, nil, soulEmailPasswordSSMParam(stage, agentID))
	}
	migaduAPIBaseURL = migaduBaseURL
	newHTTPClient    = func() *http.Client { return &http.Client{Timeout: 10 * time.Second} }
)

// errMigaduMailboxAlreadyExists is a sentinel returned by defaultMigaduCreateMailbox
// when the Migadu API responds with HTTP 409 Conflict, meaning a mailbox with the
// same local part already exists. The caller should treat this as a non-fatal
// signal: the mailbox exists (possibly from a prior migration run) and forwarding
// may still be created or already be in place.
var errMigaduMailboxAlreadyExists = errors.New("migadu mailbox already exists")

type migrationConfig struct {
	Stage              string
	TableName          string
	InventoryPath      string
	OutputPath         string
	AgentID            string
	EmailInboundDomain string
	Apply              bool
	MaxAgents          int
}

type migrationReport struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at"`
	Mode          string                 `json:"mode"`
	Stage         string                 `json:"stage"`
	TableName     string                 `json:"table_name"`
	Inventory     migrationInventoryRef  `json:"inventory"`
	Contract      migrationContract      `json:"contract"`
	Summary       migrationSummary       `json:"summary"`
	Agents        []migrationAgentReport `json:"agents"`
	Issues        []string               `json:"issues,omitempty"`
}

type migrationInventoryRef struct {
	Path          string `json:"path"`
	GeneratedAt   string `json:"generated_at,omitempty"`
	SchemaVersion int    `json:"schema_version,omitempty"`
}

type migrationContract struct {
	CanonicalAddressFormat string `json:"canonical_address_format"`
	LegacyAliasPolicy      string `json:"legacy_alias_policy"`
	RegistrationAuthority  string `json:"registration_authority"`
	ProviderPolicy         string `json:"provider_policy"`
	LiveExecutionIssue     string `json:"live_execution_issue"`
}

type migrationSummary struct {
	SelectedAgents         int `json:"selected_agents"`
	AlreadyHostSwitched    int `json:"already_host_switched"`
	ProviderPrepared       int `json:"provider_prepared"`
	ProviderActionsPlanned int `json:"provider_actions_planned"`
	ProviderActionsApplied int `json:"provider_actions_applied"`
	RegistrationActions    int `json:"registration_actions"`
	HostSyncActions        int `json:"host_sync_actions"`
	NeedsRepair            int `json:"needs_repair"`
	Skipped                int `json:"skipped"`
	Errors                 int `json:"errors"`
}

type migrationAgentReport struct {
	AgentID             string                   `json:"agent_id"`
	Domain              string                   `json:"domain"`
	InstanceSlug        string                   `json:"instance_slug,omitempty"`
	LocalID             string                   `json:"local_id"`
	OldAddress          string                   `json:"old_address,omitempty"`
	NewAddress          string                   `json:"new_address,omitempty"`
	ProviderLocalPart   string                   `json:"provider_local_part,omitempty"`
	CanSelfAttest       string                   `json:"can_self_attest"`
	State               string                   `json:"state"`
	Actions             []migrationAction        `json:"actions,omitempty"`
	Rollback            []string                 `json:"rollback,omitempty"`
	ProviderState       providerMigrationState   `json:"provider_state"`
	RegistrationVersion *registrationVersionInfo `json:"registration_version,omitempty"`
	Issues              []string                 `json:"issues,omitempty"`
}

type migrationAction struct {
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

type providerMigrationState struct {
	CurrentKnown      bool `json:"current_known"`
	CurrentReachable  bool `json:"current_reachable"`
	ProposedKnown     bool `json:"proposed_known"`
	ProposedReachable bool `json:"proposed_reachable"`
}

type migaduCreateForwardingRequest struct {
	Address string `json:"address"`
}

type migaduCreateMailboxRequest struct {
	Name      string `json:"name"`
	LocalPart string `json:"local_part"`
	//nolint:gosec // Required by Migadu's mailbox-create API payload; loaded at runtime from SSM.
	Credential            string `json:"password"`
	PasswordRecoveryEmail any    `json:"password_recovery_email"`
}

type providerPrepareRequest struct {
	Stage             string
	AgentID           string
	DisplayName       string
	LocalPart         string
	ForwardingAddress string
}

type providerClient interface {
	EnsureMailboxAndForwarding(ctx context.Context, req providerPrepareRequest) error
}

type defaultMigaduClient struct{}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, defaultMigaduClient{}, time.Now().UTC()))
}

func runCLI(args []string, getenv func(string) string, stdout io.Writer, stderr io.Writer, provider providerClient, now time.Time) int {
	cfg, err := parseConfig(args, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	report, err := runMigration(ctx, provider, cfg, now)
	if err != nil {
		fmt.Fprintf(stderr, "migration planning failed: %v\n", err)
		return 1
	}
	if err := writeMigrationReport(report, cfg.OutputPath, stdout); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	if report.Summary.Errors > 0 {
		return 1
	}
	if len(report.Issues) > 0 || report.Summary.NeedsRepair > 0 {
		return 2
	}
	return 0
}

func parseConfig(args []string, getenv func(string) string) (migrationConfig, error) {
	stageDefault := strings.ToLower(strings.TrimSpace(getenv("STAGE")))
	if stageDefault == "" {
		stageDefault = defaultStage
	}
	tableDefault := strings.TrimSpace(getenv("STATE_TABLE_NAME"))
	if tableDefault == "" {
		tableDefault = fmt.Sprintf("lesser-host-%s-state", stageDefault)
	}
	inboundDefault := strings.ToLower(strings.TrimSpace(getenv("SOUL_EMAIL_INBOUND_DOMAIN")))
	if inboundDefault == "" {
		inboundDefault = defaultInboundDomain
	}

	cfg := migrationConfig{Stage: stageDefault, TableName: tableDefault, EmailInboundDomain: inboundDefault}
	fs := flag.NewFlagSet("soul-email-m2-migration", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Stage, "stage", cfg.Stage, "Control-plane stage")
	fs.StringVar(&cfg.TableName, "table-name", cfg.TableName, "DynamoDB state table name recorded in evidence")
	fs.StringVar(&cfg.InventoryPath, "inventory", "", "M0 inventory report JSON path")
	fs.StringVar(&cfg.OutputPath, "out", "", "Output migration evidence JSON path (default stdout)")
	fs.StringVar(&cfg.AgentID, "agent-id", "", "Optional target agent id")
	fs.StringVar(&cfg.EmailInboundDomain, "email-inbound-domain", cfg.EmailInboundDomain, "Inbound bridge email domain")
	fs.BoolVar(&cfg.Apply, "apply", false, "Apply provider-prepare mutations only; registration/channel switch still requires self-attested publish path")
	fs.IntVar(&cfg.MaxAgents, "max-agents", 0, "Maximum selected agents to plan/apply (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return migrationConfig{}, err
	}
	cfg.Stage = strings.ToLower(strings.TrimSpace(cfg.Stage))
	if cfg.Stage == "" {
		cfg.Stage = defaultStage
	}
	cfg.TableName = strings.TrimSpace(cfg.TableName)
	cfg.InventoryPath = strings.TrimSpace(cfg.InventoryPath)
	cfg.OutputPath = strings.TrimSpace(cfg.OutputPath)
	cfg.AgentID = strings.ToLower(strings.TrimSpace(cfg.AgentID))
	cfg.EmailInboundDomain = strings.ToLower(strings.TrimSpace(cfg.EmailInboundDomain))
	if cfg.InventoryPath == "" {
		return migrationConfig{}, errors.New("--inventory is required")
	}
	if cfg.TableName == "" {
		return migrationConfig{}, errors.New("--table-name or STATE_TABLE_NAME is required")
	}
	if cfg.EmailInboundDomain == "" {
		return migrationConfig{}, errors.New("--email-inbound-domain or SOUL_EMAIL_INBOUND_DOMAIN is required")
	}
	if cfg.MaxAgents < 0 {
		return migrationConfig{}, errors.New("--max-agents must be non-negative")
	}
	return cfg, nil
}

func runMigration(ctx context.Context, provider providerClient, cfg migrationConfig, now time.Time) (migrationReport, error) {
	inventory, err := loadInventoryReport(cfg.InventoryPath)
	if err != nil {
		return migrationReport{}, err
	}
	if err := validateInventoryMetadata(inventory, cfg); err != nil {
		return migrationReport{}, err
	}
	report := newMigrationReport(cfg, inventory, now)
	report.Issues = append(report.Issues, inventoryBlockingIssues(inventory)...)
	selected := selectInventoryAgents(inventory.Agents, cfg.AgentID, cfg.MaxAgents)
	if len(selected) == 0 && cfg.AgentID != "" {
		report.Issues = appendUnique(report.Issues, "target_agent_not_found")
	}

	for _, rec := range selected {
		agent := planAgentMigration(rec, cfg.EmailInboundDomain)
		report.Agents = append(report.Agents, agent)
		mergeMigrationSummary(&report.Summary, agent)
	}
	if len(report.Issues) > 0 {
		for i := range report.Agents {
			report.Agents[i].State = migrationStateNeedsRepair
			report.Agents[i].Issues = appendUnique(report.Agents[i].Issues, "report_blocking_issue")
		}
		report.Summary.NeedsRepair = len(report.Agents)
		return report, nil
	}
	if cfg.Apply {
		applyProviderPreparedActions(ctx, provider, cfg, &report)
		applied := report.Summary.ProviderActionsApplied
		errorsCount := report.Summary.Errors
		report.Summary = summarizeMigrationAgents(report.Agents)
		report.Summary.ProviderActionsApplied = applied
		report.Summary.Errors = errorsCount
	}
	sort.SliceStable(report.Agents, func(i, j int) bool {
		if report.Agents[i].Domain == report.Agents[j].Domain {
			return report.Agents[i].LocalID < report.Agents[j].LocalID
		}
		return report.Agents[i].Domain < report.Agents[j].Domain
	})
	return report, nil
}

func validateInventoryMetadata(inventory inventoryReport, cfg migrationConfig) error {
	var mismatches []string
	if inventory.SchemaVersion != migrationSchemaVersion {
		mismatches = append(mismatches, fmt.Sprintf("schema_version=%d expected=%d", inventory.SchemaVersion, migrationSchemaVersion))
	}
	inventoryStage := strings.ToLower(strings.TrimSpace(inventory.Stage))
	if inventoryStage != cfg.Stage {
		mismatches = append(mismatches, fmt.Sprintf("stage=%q expected=%q", inventoryStage, cfg.Stage))
	}
	inventoryTable := strings.TrimSpace(inventory.TableName)
	if inventoryTable != cfg.TableName {
		mismatches = append(mismatches, fmt.Sprintf("table_name=%q expected=%q", inventoryTable, cfg.TableName))
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("inventory metadata mismatch: %s", strings.Join(mismatches, "; "))
	}
	return nil
}

func newMigrationReport(cfg migrationConfig, inventory inventoryReport, now time.Time) migrationReport {
	mode := "dry-run"
	if cfg.Apply {
		mode = "apply-provider-prepare"
	}
	return migrationReport{
		SchemaVersion: migrationSchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Mode:          mode,
		Stage:         cfg.Stage,
		TableName:     cfg.TableName,
		Inventory: migrationInventoryRef{
			Path:          cfg.InventoryPath,
			GeneratedAt:   strings.TrimSpace(inventory.GeneratedAt),
			SchemaVersion: inventory.SchemaVersion,
		},
		Contract: migrationContract{
			CanonicalAddressFormat: "<agent-local-id>.<instance-slug>@lessersoul.ai",
			LegacyAliasPolicy:      "host-internal SoulEmailLegacyAliasIndex canonicalizes known legacy recipients before comm-worker channel matching; aliases are never public channels",
			RegistrationAuthority:  "self-attested registration publish preferred; host audit path must be separately approved and disclosed",
			ProviderPolicy:         "prepare new Migadu mailbox/forwarding before any public channel switch; preserve old bare inbound delivery",
			LiveExecutionIssue:     "https://github.com/equaltoai/lesser-host/issues/339",
		},
	}
}

func inventoryBlockingIssues(report inventoryReport) []string {
	var issues []string
	issues = append(issues, report.Issues...)
	if report.Summary.ProposedDuplicateCount > 0 {
		issues = appendUnique(issues, "duplicate_proposed_address")
	}
	if report.Summary.ProposedOverflowCount > 0 {
		issues = appendUnique(issues, "proposed_local_part_overflow")
	}
	if report.Summary.MissingCurrentEmailIndex > 0 {
		issues = appendUnique(issues, "missing_current_email_index")
	}
	if report.Summary.CurrentEmailIndexMismatch > 0 {
		issues = appendUnique(issues, "current_email_index_mismatch")
	}
	if report.Summary.MissingDomainRecord > 0 {
		issues = appendUnique(issues, "missing_domain_record")
	}
	if report.Summary.MissingInstanceSlug > 0 {
		issues = appendUnique(issues, "missing_instance_slug")
	}
	if report.Summary.InvalidInstanceSlug > 0 {
		issues = appendUnique(issues, "invalid_instance_slug")
	}
	return issues
}

func selectInventoryAgents(records []managedEmailInventory, agentID string, maxAgents int) []managedEmailInventory {
	selected := make([]managedEmailInventory, 0, len(records))
	for _, rec := range records {
		if agentID != "" && !strings.EqualFold(strings.TrimSpace(rec.AgentID), agentID) {
			continue
		}
		selected = append(selected, rec)
		if maxAgents > 0 && len(selected) >= maxAgents {
			break
		}
	}
	return selected
}

func planAgentMigration(rec managedEmailInventory, emailInboundDomain string) migrationAgentReport {
	agent := baseMigrationAgentReport(rec, emailInboundDomain)
	if rec.DomainRecord != nil {
		agent.InstanceSlug = strings.ToLower(strings.TrimSpace(rec.DomainRecord.InstanceSlug))
	}
	agent.Issues = appendBaseMigrationIssues(agent.Issues, rec, agent.NewAddress, agent.CanSelfAttest)

	if agent.OldAddress == "" || agent.NewAddress == "" || len(agent.Issues) > 0 {
		agent.State = migrationStateNeedsRepair
		return agent
	}
	if strings.EqualFold(agent.OldAddress, agent.NewAddress) {
		return planAlreadyHostSwitchedAgent(agent, rec)
	}
	return planPendingAgentMigration(agent, rec, emailInboundDomain)
}

func baseMigrationAgentReport(rec managedEmailInventory, emailInboundDomain string) migrationAgentReport {
	return migrationAgentReport{
		AgentID:             strings.ToLower(strings.TrimSpace(rec.AgentID)),
		Domain:              strings.ToLower(strings.TrimSpace(rec.Domain)),
		LocalID:             strings.TrimSpace(rec.LocalID),
		OldAddress:          currentEmailAddress(rec.CurrentEmailChannel),
		NewAddress:          normalizeEmail(rec.Proposed.Address),
		ProviderLocalPart:   strings.ToLower(strings.TrimSpace(rec.Proposed.LocalPart)),
		CanSelfAttest:       normalizeSigningState(rec.MigrationReadiness.CanSelfAttest),
		State:               migrationStateInventoryDryRun,
		ProviderState:       providerMigrationStateFor(rec, emailInboundDomain),
		RegistrationVersion: rec.RegistrationVersion,
		Rollback: []string{
			"preserve old bare provider delivery during rollback",
			"if host switch has occurred but registration publish fails, restore previous SoulAgentChannel/SoulEmailAgentIndex and disable the legacy alias index",
			"leave newly prepared provider forwarding intact until canaries confirm safe cleanup",
		},
	}
}

func currentEmailAddress(ch *emailChannelInventory) string {
	if ch == nil {
		return ""
	}
	return normalizeEmail(ch.Identifier)
}

func appendBaseMigrationIssues(issues []string, rec managedEmailInventory, newAddress string, canSelfAttest string) []string {
	issues = append(issues, rec.Issues...)
	if rec.CurrentEmailChannel == nil {
		issues = appendUnique(issues, "missing_old_email_channel")
	}
	if newAddress == "" {
		issues = appendUnique(issues, "missing_proposed_address")
	}
	if rec.Proposed.Overflow {
		issues = appendUnique(issues, "proposed_local_part_overflow")
	}
	if rec.Proposed.Duplicate {
		issues = appendUnique(issues, "duplicate_proposed_address")
	}
	if canSelfAttest == signingStateUnknown {
		issues = appendUnique(issues, "unknown_registration_signing_state")
	}
	return issues
}

func planAlreadyHostSwitchedAgent(agent migrationAgentReport, rec managedEmailInventory) migrationAgentReport {
	agent.State = migrationStateHostSwitched
	if rec.CurrentEmailIndex == nil || !rec.CurrentEmailIndex.MatchesAgent || !rec.CurrentEmailIndex.MatchesChannel {
		agent.Issues = appendUnique(agent.Issues, "current_email_index_not_canonical")
		agent.State = migrationStateNeedsRepair
	}
	if rec.RegistrationVersion == nil || !strings.EqualFold(strings.TrimSpace(rec.RegistrationVersion.EmailChannel), agent.NewAddress) {
		agent.Issues = appendUnique(agent.Issues, "registration_email_not_instance_scoped")
		agent.State = migrationStateNeedsRepair
	}
	agent.Actions = append(agent.Actions, migrationAction{
		Kind:        actionVerifyLegacyAlias,
		Status:      "pending_canary",
		Description: "verify public channels advertise only the instance-scoped address and any known legacy alias canonicalizes internally",
	})
	return agent
}

func planPendingAgentMigration(agent migrationAgentReport, rec managedEmailInventory, emailInboundDomain string) migrationAgentReport {
	agent.Actions = append(agent.Actions, migrationAction{
		Kind:        actionPreserveLegacyProvider,
		Status:      actionStatus(agent.ProviderState.CurrentReachable),
		Description: "preserve existing bare provider mailbox/alias/forwarding for inbound-only legacy delivery",
	})
	if !agent.ProviderState.CurrentKnown {
		agent.Issues = appendUnique(agent.Issues, "current_provider_state_unknown")
	}
	if !agent.ProviderState.CurrentReachable {
		agent.Issues = appendUnique(agent.Issues, "current_provider_delivery_not_confirmed")
	}
	if rec.CurrentEmailChannel != nil && strings.TrimSpace(rec.CurrentEmailChannel.SecretRef) != "present" {
		agent.Issues = appendUnique(agent.Issues, "current_email_secret_ref_missing")
	}
	if agent.ProviderState.ProposedReachable {
		agent.State = migrationStateProviderPrepared
	} else {
		agent.State = migrationStateProviderPreparePending
		agent.Actions = append(agent.Actions, ensureProviderForwardingAction(agent, emailInboundDomain))
	}
	agent.Actions = append(agent.Actions, registrationAndHostSyncActions(agent)...)
	if agent.CanSelfAttest == signingStateNo {
		agent.Issues = appendUnique(agent.Issues, "host_audit_path_required")
	}
	if agent.CanSelfAttest == signingStateUnknown {
		agent.Issues = appendUnique(agent.Issues, "unknown_registration_signing_state")
	}
	if len(agent.Issues) > 0 {
		agent.State = migrationStateNeedsRepair
	}
	return agent
}

func ensureProviderForwardingAction(agent migrationAgentReport, emailInboundDomain string) migrationAction {
	return migrationAction{
		Kind:        actionEnsureProviderForwarding,
		Status:      "pending",
		Description: "ensure new Migadu mailbox/forwarding delivers to " + agent.ProviderLocalPart + "@" + strings.ToLower(strings.TrimSpace(emailInboundDomain)) + " before public channel switch",
	}
}

func registrationAndHostSyncActions(agent migrationAgentReport) []migrationAction {
	return []migrationAction{
		{
			Kind:        actionCollectSelfAttestation,
			Status:      registrationActionStatus(agent.CanSelfAttest),
			Description: "collect agent self-attestation for registration document that advertises only the instance-scoped email address, or attach approved host audit receipt",
		},
		{
			Kind:        actionPublishRegistration,
			Status:      "pending_after_provider_prepared",
			Description: "publish signed self-description version with " + agent.NewAddress + " as the only public email channel",
		},
		{
			Kind:        actionHostSync,
			Status:      "implemented_in_control_plane",
			Description: "registration sync writes SoulAgentChannel/SoulEmailAgentIndex for the new address and SoulEmailLegacyAliasIndex for the old address",
		},
	}
}

func providerMigrationStateFor(rec managedEmailInventory, emailInboundDomain string) providerMigrationState {
	current := rec.ProviderState.Current
	proposed := rec.ProviderState.Proposed
	return providerMigrationState{
		CurrentKnown:      current != nil,
		CurrentReachable:  providerReachable(current),
		ProposedKnown:     proposed != nil,
		ProposedReachable: providerForwardsTo(proposed, expectedProviderForwardingTarget(rec.Proposed.LocalPart, emailInboundDomain)),
	}
}

func providerReachable(state *providerAddressState) bool {
	if state == nil {
		return false
	}
	if state.MailboxExists != nil && *state.MailboxExists {
		return true
	}
	return len(state.Forwardings) > 0 || len(state.Aliases) > 0
}

func providerForwardsTo(state *providerAddressState, target string) bool {
	if state == nil {
		return false
	}
	target = normalizeEmail(target)
	if target == "" {
		return false
	}
	for _, forwarding := range state.Forwardings {
		if normalizeEmail(forwarding) == target {
			return true
		}
	}
	return false
}

func expectedProviderForwardingTarget(localPart string, emailInboundDomain string) string {
	localPart = strings.ToLower(strings.TrimSpace(localPart))
	emailInboundDomain = strings.ToLower(strings.TrimSpace(emailInboundDomain))
	if localPart == "" || emailInboundDomain == "" {
		return ""
	}
	return localPart + "@" + emailInboundDomain
}

func mergeMigrationSummary(summary *migrationSummary, agent migrationAgentReport) {
	summary.SelectedAgents++
	switch agent.State {
	case migrationStateHostSwitched:
		summary.AlreadyHostSwitched++
	case migrationStateProviderPrepared:
		summary.ProviderPrepared++
	case migrationStateNeedsRepair:
		summary.NeedsRepair++
	}
	for _, action := range agent.Actions {
		switch action.Kind {
		case actionEnsureProviderForwarding:
			summary.ProviderActionsPlanned++
		case actionCollectSelfAttestation, actionPublishRegistration:
			summary.RegistrationActions++
		case actionHostSync:
			summary.HostSyncActions++
		}
	}
}

func summarizeMigrationAgents(agents []migrationAgentReport) migrationSummary {
	var summary migrationSummary
	for _, agent := range agents {
		mergeMigrationSummary(&summary, agent)
	}
	return summary
}

func applyProviderPreparedActions(ctx context.Context, provider providerClient, cfg migrationConfig, report *migrationReport) {
	if provider == nil {
		report.Issues = appendUnique(report.Issues, "provider_client_not_configured")
		report.Summary.Errors++
		return
	}
	for i := range report.Agents {
		if report.Agents[i].State != migrationStateProviderPreparePending {
			continue
		}
		if len(report.Agents[i].Issues) > 0 {
			report.Agents[i].State = migrationStateNeedsRepair
			continue
		}
		localPart := strings.TrimSpace(report.Agents[i].ProviderLocalPart)
		forwardingAddress := localPart + "@" + strings.TrimSpace(cfg.EmailInboundDomain)
		if err := provider.EnsureMailboxAndForwarding(ctx, providerPrepareRequest{
			Stage:             cfg.Stage,
			AgentID:           report.Agents[i].AgentID,
			DisplayName:       report.Agents[i].LocalID,
			LocalPart:         localPart,
			ForwardingAddress: forwardingAddress,
		}); err != nil {
			report.Agents[i].Issues = appendUnique(report.Agents[i].Issues, "provider_prepare_failed")
			report.Agents[i].State = migrationStateNeedsRepair
			report.Summary.Errors++
			continue
		}
		markActionApplied(report.Agents[i].Actions, actionEnsureProviderForwarding)
		report.Agents[i].State = migrationStateProviderPrepared
		report.Agents[i].ProviderState.ProposedKnown = true
		report.Agents[i].ProviderState.ProposedReachable = true
		report.Summary.ProviderActionsApplied++
	}
}

func markActionApplied(actions []migrationAction, kind string) {
	for i := range actions {
		if actions[i].Kind == kind {
			actions[i].Status = "applied"
		}
	}
}

func actionStatus(ok bool) string {
	if ok {
		return "verified"
	}
	return "needs_repair"
}

func registrationActionStatus(canSelfAttest string) string {
	switch canSelfAttest {
	case signingStateYes:
		return "ready"
	case signingStateNo:
		return "requires_approved_host_audit_path"
	default:
		return "blocked_unknown"
	}
}

func (defaultMigaduClient) EnsureMailboxAndForwarding(ctx context.Context, input providerPrepareRequest) error {
	input.Stage = strings.ToLower(strings.TrimSpace(input.Stage))
	input.AgentID = strings.ToLower(strings.TrimSpace(input.AgentID))
	input.LocalPart = strings.ToLower(strings.TrimSpace(input.LocalPart))
	input.ForwardingAddress = strings.ToLower(strings.TrimSpace(input.ForwardingAddress))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.LocalPart == "" || input.ForwardingAddress == "" || input.AgentID == "" {
		return errors.New("migadu provider prepare requires agentID, localPart, and forwarding address")
	}
	password, err := migaduPasswordLoader(ctx, input.Stage, input.AgentID)
	if err != nil {
		return err
	}
	password = strings.TrimSpace(password)
	if password == "" {
		return errors.New("migadu mailbox password is empty")
	}
	creds, err := migaduCredsLoader(ctx, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(creds.Username) == "" || strings.TrimSpace(creds.APIToken) == "" {
		return errors.New("migadu credentials are incomplete")
	}
	if input.DisplayName == "" {
		input.DisplayName = input.LocalPart
	}
	if err := defaultMigaduCreateMailbox(ctx, creds, input.LocalPart, input.DisplayName, password); err != nil && !errors.Is(err, errMigaduMailboxAlreadyExists) {
		return err
	}
	return defaultMigaduCreateForwarding(ctx, creds, input.LocalPart, input.ForwardingAddress)
}

func defaultMigaduCreateMailbox(ctx context.Context, creds secrets.MigaduCredentials, localPart string, displayName string, password string) error {
	body, err := json.Marshal(migaduCreateMailboxRequest{
		Name:                  strings.TrimSpace(displayName),
		LocalPart:             strings.TrimSpace(localPart),
		Credential:            strings.TrimSpace(password),
		PasswordRecoveryEmail: nil,
	})
	if err != nil {
		return fmt.Errorf("migadu mailbox encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, migaduAPIBaseURL+"/domains/"+canonicalEmailDomain+"/mailboxes", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("migadu mailbox build: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.SetBasicAuth(strings.TrimSpace(creds.Username), strings.TrimSpace(creds.APIToken))
	client := newHTTPClient()
	//nolint:gosec // Request target is the fixed Migadu HTTPS API host by default; tests override migaduAPIBaseURL.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("migadu mailbox: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusConflict:
		return errMigaduMailboxAlreadyExists
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("migadu mailbox: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(msg)))
}

func defaultMigaduCreateForwarding(ctx context.Context, creds secrets.MigaduCredentials, localPart string, forwardingAddress string) error {
	body, err := json.Marshal(migaduCreateForwardingRequest{Address: strings.TrimSpace(forwardingAddress)})
	if err != nil {
		return fmt.Errorf("migadu forwarding encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, migaduAPIBaseURL+"/domains/"+canonicalEmailDomain+"/mailboxes/"+strings.TrimSpace(localPart)+"/forwardings", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("migadu forwarding build: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.SetBasicAuth(strings.TrimSpace(creds.Username), strings.TrimSpace(creds.APIToken))
	client := newHTTPClient()
	//nolint:gosec // Request target is the fixed Migadu HTTPS API host by default; tests override migaduAPIBaseURL.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("migadu forwarding: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("migadu forwarding: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(msg)))
}

func soulEmailPasswordSSMParam(stage string, agentID string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage == "" {
		stage = defaultStage
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	// #nosec G101 -- SSM parameter path, not a hardcoded credential.
	return fmt.Sprintf("/lesser-host/soul/%s/agents/%s/channels/email/migadu_password", stage, agentID)
}

func loadInventoryReport(path string) (inventoryReport, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // Operator-provided local M0 evidence path.
	if err != nil {
		return inventoryReport{}, err
	}
	var report inventoryReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return inventoryReport{}, err
	}
	return report, nil
}

func writeMigrationReport(report migrationReport, outputPath string, stdout io.Writer) error {
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
	return os.WriteFile(outputPath, raw, 0o600) //nolint:gosec // Evidence is written only to the operator-selected local output path.
}

type inventoryReport struct {
	SchemaVersion int                     `json:"schema_version"`
	GeneratedAt   string                  `json:"generated_at"`
	Stage         string                  `json:"stage"`
	TableName     string                  `json:"table_name"`
	Summary       inventorySummary        `json:"summary"`
	Agents        []managedEmailInventory `json:"agents"`
	Issues        []string                `json:"issues,omitempty"`
}

type inventorySummary struct {
	MissingDomainRecord       int `json:"missing_domain_record"`
	MissingInstanceSlug       int `json:"missing_instance_slug"`
	InvalidInstanceSlug       int `json:"invalid_instance_slug"`
	ProposedDuplicateCount    int `json:"proposed_duplicate_count"`
	ProposedOverflowCount     int `json:"proposed_overflow_count"`
	MissingCurrentEmailIndex  int `json:"missing_current_email_index"`
	CurrentEmailIndexMismatch int `json:"current_email_index_mismatch"`
}

type managedEmailInventory struct {
	AgentID             string                   `json:"agent_id"`
	Domain              string                   `json:"domain"`
	LocalID             string                   `json:"local_id"`
	DomainRecord        *domainInventory         `json:"domain_record,omitempty"`
	CurrentEmailChannel *emailChannelInventory   `json:"current_email_channel,omitempty"`
	CurrentEmailIndex   *emailIndexInventory     `json:"current_email_index,omitempty"`
	RegistrationVersion *registrationVersionInfo `json:"registration_version,omitempty"`
	Proposed            proposedEmailInventory   `json:"proposed"`
	ProviderState       providerInventory        `json:"provider_state"`
	MigrationReadiness  migrationReadiness       `json:"migration_readiness"`
	Issues              []string                 `json:"issues,omitempty"`
}

type domainInventory struct {
	InstanceSlug string `json:"instance_slug"`
}

type emailChannelInventory struct {
	Identifier string `json:"identifier"`
	SecretRef  string `json:"secret_ref_present,omitempty"`
}

type emailIndexInventory struct {
	MatchesAgent   bool `json:"matches_agent"`
	MatchesChannel bool `json:"matches_channel"`
}

type registrationVersionInfo struct {
	VersionNumber          int    `json:"version_number"`
	RegistrationURI        string `json:"registration_uri,omitempty"`
	RegistrationSHA256     string `json:"registration_sha256,omitempty"`
	ChangeSummary          string `json:"change_summary,omitempty"`
	SelfAttestationPresent bool   `json:"self_attestation_present"`
	EmailChannel           string `json:"email_channel,omitempty"`
	Source                 string `json:"source,omitempty"`
}

type proposedEmailInventory struct {
	LocalPart string `json:"local_part,omitempty"`
	Address   string `json:"address,omitempty"`
	Overflow  bool   `json:"overflow"`
	Duplicate bool   `json:"duplicate"`
}

type providerInventory struct {
	Current  *providerAddressState `json:"current,omitempty"`
	Proposed *providerAddressState `json:"proposed,omitempty"`
}

type migrationReadiness struct {
	CanSelfAttest string `json:"can_self_attest"`
}

type providerAddressState struct {
	LocalPart     string   `json:"local_part"`
	MailboxExists *bool    `json:"mailbox_exists,omitempty"`
	Forwardings   []string `json:"forwardings,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
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

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
