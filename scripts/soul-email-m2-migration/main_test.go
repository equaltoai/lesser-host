package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soulemail"
)

const (
	testAgentID         = "0xagent"
	testOldEmail        = "pilot@lessersoul.ai"
	testNewEmail        = "pilot.simulacrum@lessersoul.ai"
	testNewLocalPart    = "pilot.simulacrum"
	testForwardingAddr  = "pilot.simulacrum@lab.lessersoul.ai"
	testDisplayName     = "pilot"
	testMailboxPassword = "mailbox-password"
	testLiveStage       = "live"
	testStateTableName  = "state"
	testInventoryFile   = "inventory.json"
	testInventoryFlag   = "--inventory"
	testMigaduUser      = "migadu-user"
	testMigaduToken     = "migadu-token"
)
const testMailboxesPath = "/domains/lessersoul.ai/mailboxes"

type fakeProvider struct {
	calls []string
	err   error
}

func (f *fakeProvider) EnsureMailboxAndForwarding(_ context.Context, input providerPrepareRequest) error {
	f.calls = append(f.calls, input.AgentID+":"+input.LocalPart+"->"+input.ForwardingAddress)
	return f.err
}

func TestPlanAgentMigration_DryRunListsRequiredActions(t *testing.T) {
	t.Parallel()

	report := runMigrationForTest(t, false, happyInventoryReport(func(rec *managedEmailInventory) {
		falseValue := false
		rec.ProviderState.Proposed = &providerAddressState{LocalPart: testNewLocalPart, MailboxExists: &falseValue}
	}))
	if len(report.Issues) != 0 || report.Summary.NeedsRepair != 0 {
		t.Fatalf("unexpected issues: summary=%#v issues=%#v", report.Summary, report.Issues)
	}
	if report.Summary.ProviderActionsPlanned != 1 || report.Summary.RegistrationActions != 2 || report.Summary.HostSyncActions != 1 {
		t.Fatalf("unexpected action summary: %#v", report.Summary)
	}
	agent := report.Agents[0]
	if agent.OldAddress != testOldEmail || agent.NewAddress != testNewEmail || agent.InstanceSlug != "simulacrum" || agent.State != migrationStateProviderPreparePending {
		t.Fatalf("unexpected agent plan: %#v", agent)
	}
}

func TestRunMigration_ApplyProviderPrepareIsIdempotent(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{}
	report := runMigrationForTestWithProvider(t, provider, true, happyInventoryReport(func(rec *managedEmailInventory) {
		rec.ProviderState.Proposed = nil
	}))
	if report.Summary.ProviderActionsApplied != 1 || len(provider.calls) != 1 || provider.calls[0] != testAgentID+":"+testNewLocalPart+"->"+testForwardingAddr {
		t.Fatalf("expected provider apply once, summary=%#v calls=%#v", report.Summary, provider.calls)
	}
	if report.Agents[0].State != migrationStateProviderPrepared {
		t.Fatalf("expected provider_prepared after apply, got %#v", report.Agents[0])
	}

	ready := runMigrationForTestWithProvider(t, &fakeProvider{}, true, happyInventoryReport(nil))
	if ready.Summary.ProviderActionsApplied != 0 || ready.Summary.ProviderActionsPlanned != 0 || ready.Agents[0].State != migrationStateProviderPrepared {
		t.Fatalf("expected existing provider state to be a no-op, summary=%#v agent=%#v", ready.Summary, ready.Agents[0])
	}
}

func TestPlanAgentMigration_ExistingNewChannelRequiresRegistrationParity(t *testing.T) {
	t.Parallel()

	report := runMigrationForTest(t, false, happyInventoryReport(func(rec *managedEmailInventory) {
		rec.CurrentEmailChannel.Identifier = testNewEmail
		rec.CurrentEmailIndex = &emailIndexInventory{MatchesAgent: true, MatchesChannel: true}
		rec.RegistrationVersion.EmailChannel = testNewEmail
	}))
	if report.Summary.AlreadyHostSwitched != 1 || report.Summary.NeedsRepair != 0 || report.Agents[0].State != migrationStateHostSwitched {
		t.Fatalf("expected host_switched no-op, summary=%#v agent=%#v", report.Summary, report.Agents[0])
	}
}

func TestPlanAgentMigration_MissingOldChannelNeedsRepair(t *testing.T) {
	t.Parallel()

	report := runMigrationForTest(t, false, happyInventoryReport(func(rec *managedEmailInventory) {
		rec.CurrentEmailChannel = nil
	}))
	if report.Summary.NeedsRepair != 1 || report.Agents[0].State != migrationStateNeedsRepair {
		t.Fatalf("expected needs_repair, summary=%#v agent=%#v", report.Summary, report.Agents[0])
	}
	if !contains(report.Agents[0].Issues, "missing_old_email_channel") {
		t.Fatalf("expected missing_old_email_channel issue, got %#v", report.Agents[0].Issues)
	}
}

func TestRunMigration_RefusesInventoryDuplicatesAndOverflow(t *testing.T) {
	t.Parallel()

	inv := happyInventoryReport(nil)
	inv.Summary.ProposedDuplicateCount = 1
	inv.Summary.ProposedOverflowCount = 1
	report := runMigrationForTest(t, false, inv)
	if len(report.Issues) == 0 || report.Summary.NeedsRepair != 1 || report.Agents[0].State != migrationStateNeedsRepair {
		t.Fatalf("expected report-level refusal, summary=%#v issues=%#v agent=%#v", report.Summary, report.Issues, report.Agents[0])
	}
	if !contains(report.Issues, "duplicate_proposed_address") || !contains(report.Issues, "proposed_local_part_overflow") {
		t.Fatalf("expected duplicate/overflow issues, got %#v", report.Issues)
	}
}

func TestRunMigration_RejectsInventoryMetadataMismatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mut  func(*inventoryReport)
		want string
	}{
		{
			name: "schema",
			mut:  func(inv *inventoryReport) { inv.SchemaVersion = migrationSchemaVersion + 1 },
			want: "schema_version",
		},
		{
			name: "stage",
			mut:  func(inv *inventoryReport) { inv.Stage = testLiveStage },
			want: "stage",
		},
		{
			name: "table",
			mut:  func(inv *inventoryReport) { inv.TableName = "lesser-host-live-state" },
			want: "table_name",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inv := happyInventoryReport(nil)
			tc.mut(&inv)
			dir := t.TempDir()
			path := filepath.Join(dir, testInventoryFile)
			raw, err := jsonMarshalIndent(inv)
			if err != nil {
				t.Fatalf("marshal inventory: %v", err)
			}
			writeErr := os.WriteFile(path, raw, 0o600)
			if writeErr != nil {
				t.Fatalf("write inventory: %v", writeErr)
			}
			provider := &fakeProvider{}
			_, err = runMigration(context.Background(), provider, migrationConfig{
				Stage:              defaultStage,
				TableName:          testStateTableName,
				InventoryPath:      path,
				EmailInboundDomain: defaultInboundDomain,
				Apply:              true,
			}, time.Unix(0, 0).UTC())
			if err == nil || !strings.Contains(err.Error(), "inventory metadata mismatch") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected metadata mismatch containing %q, got %v", tc.want, err)
			}
			if len(provider.calls) != 0 {
				t.Fatalf("provider should not be called on metadata mismatch, got %#v", provider.calls)
			}
		})
	}
}

func TestPlanAgentMigration_ProposedProviderRequiresExactInboundForwarding(t *testing.T) {
	t.Parallel()

	report := runMigrationForTest(t, false, happyInventoryReport(func(rec *managedEmailInventory) {
		trueValue := true
		rec.ProviderState.Proposed = &providerAddressState{
			LocalPart:     testNewLocalPart,
			MailboxExists: &trueValue,
			Forwardings:   []string{"pilot.simulacrum@wrong.example"},
			Aliases:       []string{testNewEmail},
		}
	}))
	if report.Summary.ProviderPrepared != 0 || report.Summary.ProviderActionsPlanned != 1 {
		t.Fatalf("expected exact forwarding to be required, summary=%#v agent=%#v", report.Summary, report.Agents[0])
	}
	if report.Agents[0].State != migrationStateProviderPreparePending || report.Agents[0].ProviderState.ProposedReachable {
		t.Fatalf("expected provider_prepare_pending with unreachable proposed state, got %#v", report.Agents[0])
	}
}

func TestRunMigration_TargetAndProviderFailureIssues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, testInventoryFile)
	raw, err := jsonMarshalIndent(happyInventoryReport(nil))
	if err != nil {
		t.Fatalf("marshal inventory: %v", err)
	}
	writeErr := os.WriteFile(path, raw, 0o600)
	if writeErr != nil {
		t.Fatalf("write inventory: %v", writeErr)
	}

	targetReport, err := runMigration(context.Background(), &fakeProvider{}, migrationConfig{
		Stage:              defaultStage,
		TableName:          testStateTableName,
		InventoryPath:      path,
		AgentID:            "0xmissing",
		EmailInboundDomain: defaultInboundDomain,
	}, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("run target migration: %v", err)
	}
	if !contains(targetReport.Issues, "target_agent_not_found") {
		t.Fatalf("expected target_agent_not_found, got %#v", targetReport.Issues)
	}

	providerReport := runMigrationForTestWithProvider(t, &fakeProvider{err: errors.New("provider down")}, true, happyInventoryReport(func(rec *managedEmailInventory) {
		rec.ProviderState.Proposed = nil
	}))
	if providerReport.Summary.Errors != 1 || providerReport.Agents[0].State != migrationStateNeedsRepair || !contains(providerReport.Agents[0].Issues, "provider_prepare_failed") {
		t.Fatalf("expected provider failure repair, summary=%#v agent=%#v", providerReport.Summary, providerReport.Agents[0])
	}
}

func TestPlanAgentMigration_SigningAndAlreadySwitchedRepairBranches(t *testing.T) {
	t.Parallel()

	needsAudit := runMigrationForTest(t, false, happyInventoryReport(func(rec *managedEmailInventory) {
		rec.MigrationReadiness.CanSelfAttest = signingStateNo
	}))
	if needsAudit.Agents[0].State != migrationStateNeedsRepair || !contains(needsAudit.Agents[0].Issues, "host_audit_path_required") {
		t.Fatalf("expected host audit repair, got %#v", needsAudit.Agents[0])
	}

	mismatch := runMigrationForTest(t, false, happyInventoryReport(func(rec *managedEmailInventory) {
		rec.CurrentEmailChannel.Identifier = testNewEmail
		rec.CurrentEmailIndex = &emailIndexInventory{MatchesAgent: false, MatchesChannel: true}
		rec.RegistrationVersion.EmailChannel = testOldEmail
	}))
	if mismatch.Agents[0].State != migrationStateNeedsRepair ||
		!contains(mismatch.Agents[0].Issues, "current_email_index_not_canonical") ||
		!contains(mismatch.Agents[0].Issues, "registration_email_not_instance_scoped") {
		t.Fatalf("expected already-switched repair issues, got %#v", mismatch.Agents[0])
	}
}

func TestInventoryAndHelperErrorBranches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte(`{"agents":`), 0o600); err != nil {
		t.Fatalf("write bad inventory: %v", err)
	}
	if _, err := loadInventoryReport(badPath); err == nil {
		t.Fatalf("expected invalid inventory error")
	}
	if got := appendUnique([]string{"x"}, "x"); len(got) != 1 {
		t.Fatalf("expected duplicate append to be ignored, got %#v", got)
	}
	if got := appendUnique(nil, " "); len(got) != 0 {
		t.Fatalf("expected blank append to be ignored, got %#v", got)
	}
	if normalizeSigningState("cannot_self_attest") != signingStateNo || normalizeSigningState("can_self_attest") != signingStateYes {
		t.Fatalf("unexpected signing normalization")
	}

	reportPath := filepath.Join(dir, "report.json")
	if err := writeMigrationReport(newMigrationReport(migrationConfig{Stage: defaultStage, TableName: testStateTableName, InventoryPath: testInventoryFile}, inventoryReport{}, time.Unix(0, 0).UTC()), reportPath, &bytes.Buffer{}); err != nil {
		t.Fatalf("write report file: %v", err)
	}
	if info, err := os.Stat(reportPath); err != nil || info.Size() == 0 {
		t.Fatalf("expected report file, info=%#v err=%v", info, err)
	}

	issues := inventoryBlockingIssues(inventoryReport{Summary: inventorySummary{
		MissingDomainRecord:       1,
		MissingInstanceSlug:       1,
		InvalidInstanceSlug:       1,
		MissingCurrentEmailIndex:  1,
		CurrentEmailIndexMismatch: 1,
	}})
	for _, issue := range []string{"missing_domain_record", "missing_instance_slug", "invalid_instance_slug", "missing_current_email_index", "current_email_index_mismatch"} {
		if !contains(issues, issue) {
			t.Fatalf("expected inventory issue %q in %#v", issue, issues)
		}
	}
}

func TestApplyProviderPreparedActionsNilProviderAndSelection(t *testing.T) {
	t.Parallel()

	records := []managedEmailInventory{
		{AgentID: "0x1"},
		{AgentID: "0x2"},
	}
	if got := selectInventoryAgents(records, "", 1); len(got) != 1 || got[0].AgentID != "0x1" {
		t.Fatalf("unexpected max selection: %#v", got)
	}
	if got := selectInventoryAgents(records, "0x2", 0); len(got) != 1 || got[0].AgentID != "0x2" {
		t.Fatalf("unexpected target selection: %#v", got)
	}

	report := migrationReport{Agents: []migrationAgentReport{{State: migrationStateProviderPreparePending}}}
	applyProviderPreparedActions(context.Background(), nil, migrationConfig{}, &report)
	if report.Summary.Errors != 1 || !contains(report.Issues, "provider_client_not_configured") {
		t.Fatalf("expected provider client error, summary=%#v issues=%#v", report.Summary, report.Issues)
	}
}

func TestParseConfigAndWriteReport(t *testing.T) {
	t.Parallel()

	cfg, err := parseConfig([]string{testInventoryFlag, testInventoryFile, "--agent-id", " 0xAGENT ", "--apply", "--max-agents", "2"}, func(key string) string {
		switch key {
		case "STAGE":
			return testLiveStage
		case "SOUL_EMAIL_INBOUND_DOMAIN":
			return "Inbound.LesserSoul.ai"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if !cfg.Apply || cfg.Stage != testLiveStage || cfg.TableName != "lesser-host-live-state" || cfg.AgentID != testAgentID || cfg.EmailInboundDomain != soulemail.LiveInboundDomain || cfg.MaxAgents != 2 {
		t.Fatalf("unexpected config: %#v", cfg)
	}

	var stdout bytes.Buffer
	if err := writeMigrationReport(newMigrationReport(cfg, inventoryReport{}, time.Unix(0, 0).UTC()), "-", &stdout); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"schema_version": 1`)) {
		t.Fatalf("stdout missing schema version: %s", stdout.String())
	}
}

func TestParseConfigDefaultsStageInboundDomain(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		args  []string
		want  string
		stage string
	}{
		{name: "lab flag", args: []string{"--stage", "lab", testInventoryFlag, testInventoryFile}, want: soulemail.LabInboundDomain, stage: defaultStage},
		{name: "live flag", args: []string{"--stage", "live", testInventoryFlag, testInventoryFile}, want: soulemail.LiveInboundDomain, stage: testLiveStage},
		{name: "blank env defaults lab", args: []string{testInventoryFlag, testInventoryFile}, want: soulemail.LabInboundDomain, stage: defaultStage},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parsedCfg, err := parseConfig(tc.args, func(string) string { return "" })
			if err != nil {
				t.Fatalf("parse config: %v", err)
			}
			if parsedCfg.Stage != tc.stage || parsedCfg.EmailInboundDomain != tc.want {
				t.Fatalf("unexpected config: %#v, want stage=%q inbound=%q", parsedCfg, tc.stage, tc.want)
			}
		})
	}
}

func TestRunCLI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	inventoryPath := filepath.Join(dir, testInventoryFile)
	raw, err := jsonMarshalIndent(happyInventoryReport(nil))
	if err != nil {
		t.Fatalf("marshal inventory: %v", err)
	}
	writeErr := os.WriteFile(inventoryPath, raw, 0o600)
	if writeErr != nil {
		t.Fatalf("write inventory: %v", writeErr)
	}
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{testInventoryFlag, inventoryPath}, func(key string) string {
		if key == "STATE_TABLE_NAME" {
			return testStateTableName
		}
		return ""
	}, &stdout, &stderr, &fakeProvider{}, time.Unix(0, 0).UTC())
	if code != 0 || stderr.Len() != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"mode": "dry-run"`)) {
		t.Fatalf("unexpected CLI result code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runCLI(nil, func(string) string { return "" }, &stdout, &stderr, &fakeProvider{}, time.Unix(0, 0).UTC())
	if code != 1 || !strings.Contains(stderr.String(), "--inventory is required") {
		t.Fatalf("expected config failure, code=%d stderr=%s", code, stderr.String())
	}
}

func TestDefaultMigaduClientEnsuresMailboxAndForwarding(t *testing.T) {
	var paths []string
	server := newMigaduProviderTestServer(t, &paths)
	defer server.Close()

	oldCredsLoader := migaduCredsLoader
	oldPasswordLoader := migaduPasswordLoader
	oldBaseURL := migaduAPIBaseURL
	migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
		return secrets.MigaduCredentials{Username: testMigaduUser, APIToken: testMigaduToken}, nil
	}
	migaduPasswordLoader = func(_ context.Context, stage string, agentID string) (string, error) {
		if stage != defaultStage || agentID != testAgentID {
			t.Fatalf("unexpected password lookup stage=%q agent=%q", stage, agentID)
		}
		return testMailboxPassword, nil
	}
	migaduAPIBaseURL = server.URL
	t.Cleanup(func() {
		migaduCredsLoader = oldCredsLoader
		migaduPasswordLoader = oldPasswordLoader
		migaduAPIBaseURL = oldBaseURL
	})

	err := defaultMigaduClient{}.EnsureMailboxAndForwarding(context.Background(), providerPrepareRequest{
		Stage:             defaultStage,
		AgentID:           testAgentID,
		DisplayName:       testDisplayName,
		LocalPart:         testNewLocalPart,
		ForwardingAddress: testForwardingAddr,
	})
	if err != nil {
		t.Fatalf("EnsureMailboxAndForwarding: %v", err)
	}
	got := strings.Join(paths, ",")
	if got != "POST "+(testMailboxesPath)+",POST "+(testMailboxesPath)+"/"+testNewLocalPart+"/forwardings" {
		t.Fatalf("unexpected request order: %s", got)
	}
}

func TestDefaultMigaduClientMailboxConflictFailsClosedWithoutForwarding(t *testing.T) {
	var paths []string
	server := newMigaduConflictMailboxTestServer(t, &paths)
	defer server.Close()

	oldCredsLoader := migaduCredsLoader
	oldPasswordLoader := migaduPasswordLoader
	oldBaseURL := migaduAPIBaseURL
	migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
		return secrets.MigaduCredentials{Username: testMigaduUser, APIToken: testMigaduToken}, nil
	}
	migaduPasswordLoader = func(context.Context, string, string) (string, error) {
		return testMailboxPassword, nil
	}
	migaduAPIBaseURL = server.URL
	t.Cleanup(func() {
		migaduCredsLoader = oldCredsLoader
		migaduPasswordLoader = oldPasswordLoader
		migaduAPIBaseURL = oldBaseURL
	})

	err := defaultMigaduClient{}.EnsureMailboxAndForwarding(context.Background(), providerPrepareRequest{
		Stage:             defaultStage,
		AgentID:           testAgentID,
		DisplayName:       testDisplayName,
		LocalPart:         testNewLocalPart,
		ForwardingAddress: testForwardingAddr,
	})
	if !errors.Is(err, errMigaduMailboxAlreadyExists) {
		t.Fatalf("expected mailbox conflict to fail closed, got: %v", err)
	}
	got := strings.Join(paths, ",")
	if got != "POST "+testMailboxesPath {
		t.Fatalf("expected mailbox POST only and no forwarding POST on 409, got %q", got)
	}
}

func newMigaduConflictMailboxTestServer(t *testing.T, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.Method+" "+r.URL.Path)
		assertMigaduTestAuth(t, r)
		switch r.URL.Path {
		case testMailboxesPath:
			assertMigaduMailboxRequest(t, r)
			w.WriteHeader(http.StatusConflict)
		case "/domains/lessersoul.ai/mailboxes/" + testNewLocalPart + "/forwardings":
			t.Fatalf("forwarding POST must not be issued after unverified mailbox conflict")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

func newMigaduProviderTestServer(t *testing.T, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.Method+" "+r.URL.Path)
		assertMigaduTestAuth(t, r)
		switch r.URL.Path {
		case testMailboxesPath:
			assertMigaduMailboxRequest(t, r)
			w.WriteHeader(http.StatusCreated)
		case "/domains/lessersoul.ai/mailboxes/" + testNewLocalPart + "/forwardings":
			assertMigaduForwardingRequest(t, r)
			w.WriteHeader(http.StatusConflict)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

func assertMigaduTestAuth(t *testing.T, r *http.Request) {
	t.Helper()
	user, pass, ok := r.BasicAuth()
	if !ok || user != testMigaduUser || pass != testMigaduToken {
		t.Fatalf("unexpected auth user=%q pass=%q ok=%v", user, pass, ok)
	}
}

func assertMigaduMailboxRequest(t *testing.T, r *http.Request) {
	t.Helper()
	var req migaduCreateMailboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode mailbox: %v", err)
	}
	if req.LocalPart != testNewLocalPart || req.Credential != testMailboxPassword || req.Name != testDisplayName {
		t.Fatalf("unexpected mailbox request: %#v", req)
	}
}

func assertMigaduForwardingRequest(t *testing.T, r *http.Request) {
	t.Helper()
	var req migaduCreateForwardingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode forwarding: %v", err)
	}
	if req.Address != testForwardingAddr {
		t.Fatalf("unexpected forwarding request: %#v", req)
	}
}

func TestDefaultMigaduClientRejectsMissingInputs(t *testing.T) {
	t.Parallel()

	if err := (defaultMigaduClient{}).EnsureMailboxAndForwarding(context.Background(), providerPrepareRequest{}); err == nil {
		t.Fatalf("expected missing input error")
	}
	if got := soulEmailPasswordSSMParam(" LIVE ", " 0xABC "); got != "/lesser-host/soul/live/agents/0xabc/channels/email/migadu_password" {
		t.Fatalf("unexpected password param %q", got)
	}
}

func TestDefaultMigaduClientProviderFailures(t *testing.T) {
	oldCredsLoader := migaduCredsLoader
	oldPasswordLoader := migaduPasswordLoader
	oldBaseURL := migaduAPIBaseURL
	migaduPasswordLoader = func(context.Context, string, string) (string, error) { return testMailboxPassword, nil }
	t.Cleanup(func() {
		migaduCredsLoader = oldCredsLoader
		migaduPasswordLoader = oldPasswordLoader
		migaduAPIBaseURL = oldBaseURL
	})

	migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
		return secrets.MigaduCredentials{}, nil
	}
	if err := validDefaultMigaduPrepare().EnsureMailboxAndForwarding(context.Background(), validProviderPrepareRequest()); err == nil {
		t.Fatalf("expected incomplete credentials error")
	}

	migaduCredsLoader = func(context.Context, secrets.SSMAPI) (secrets.MigaduCredentials, error) {
		return secrets.MigaduCredentials{Username: testMigaduUser, APIToken: testMigaduToken}, nil
	}
	migaduAPIBaseURL = newFailingMigaduTestServer(t, http.StatusInternalServerError, http.StatusCreated).URL
	if err := validDefaultMigaduPrepare().EnsureMailboxAndForwarding(context.Background(), validProviderPrepareRequest()); err == nil || !strings.Contains(err.Error(), "migadu mailbox") {
		t.Fatalf("expected mailbox provider error, got %v", err)
	}

	migaduAPIBaseURL = newFailingMigaduTestServer(t, http.StatusCreated, http.StatusBadGateway).URL
	if err := validDefaultMigaduPrepare().EnsureMailboxAndForwarding(context.Background(), validProviderPrepareRequest()); err == nil || !strings.Contains(err.Error(), "migadu forwarding") {
		t.Fatalf("expected forwarding provider error, got %v", err)
	}
}

func validDefaultMigaduPrepare() defaultMigaduClient {
	return defaultMigaduClient{}
}

func validProviderPrepareRequest() providerPrepareRequest {
	return providerPrepareRequest{
		Stage:             defaultStage,
		AgentID:           testAgentID,
		DisplayName:       testDisplayName,
		LocalPart:         testNewLocalPart,
		ForwardingAddress: testForwardingAddr,
	}
}

func newFailingMigaduTestServer(t *testing.T, mailboxStatus int, forwardingStatus int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testMailboxesPath:
			http.Error(w, "mailbox failed", mailboxStatus)
		case "/domains/lessersoul.ai/mailboxes/" + testNewLocalPart + "/forwardings":
			http.Error(w, "forwarding failed", forwardingStatus)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runMigrationForTest(t *testing.T, apply bool, inv inventoryReport) migrationReport {
	t.Helper()
	return runMigrationForTestWithProvider(t, &fakeProvider{}, apply, inv)
}

func runMigrationForTestWithProvider(t *testing.T, provider providerClient, apply bool, inv inventoryReport) migrationReport {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, testInventoryFile)
	raw, err := jsonMarshalIndent(inv)
	if err != nil {
		t.Fatalf("marshal inventory: %v", err)
	}
	writeErr := os.WriteFile(path, raw, 0o600)
	if writeErr != nil {
		t.Fatalf("write inventory: %v", writeErr)
	}
	report, err := runMigration(context.Background(), provider, migrationConfig{
		Stage:              defaultStage,
		TableName:          testStateTableName,
		InventoryPath:      path,
		EmailInboundDomain: defaultInboundDomain,
		Apply:              apply,
	}, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("run migration: %v", err)
	}
	if len(report.Agents) != 1 {
		t.Fatalf("expected one agent, got %#v", report.Agents)
	}
	return report
}

func happyInventoryReport(mut func(*managedEmailInventory)) inventoryReport {
	trueValue := true
	rec := managedEmailInventory{
		AgentID: testAgentID,
		Domain:  "dev.simulacrum.greater.website",
		LocalID: "pilot",
		DomainRecord: &domainInventory{
			InstanceSlug: "simulacrum",
		},
		CurrentEmailChannel: &emailChannelInventory{Identifier: testOldEmail, SecretRef: "present"},
		CurrentEmailIndex:   &emailIndexInventory{MatchesAgent: true, MatchesChannel: true},
		RegistrationVersion: &registrationVersionInfo{VersionNumber: 3, EmailChannel: testOldEmail, SelfAttestationPresent: true},
		Proposed:            proposedEmailInventory{LocalPart: testNewLocalPart, Address: testNewEmail},
		ProviderState: providerInventory{
			Current:  &providerAddressState{LocalPart: "pilot", MailboxExists: &trueValue},
			Proposed: &providerAddressState{LocalPart: testNewLocalPart, Forwardings: []string{testForwardingAddr}},
		},
		MigrationReadiness: migrationReadiness{CanSelfAttest: signingStateYes},
	}
	if mut != nil {
		mut(&rec)
	}
	return inventoryReport{SchemaVersion: 1, GeneratedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), Stage: defaultStage, TableName: testStateTableName, Agents: []managedEmailInventory{rec}}
}

func jsonMarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}
