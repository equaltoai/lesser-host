package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	testPilotEmail        = "pilot@lessersoul.ai"
	testPilotInboundEmail = "pilot@inbound.lessersoul.ai"
	testPilotScopedEmail  = "pilot.simulacrum@lessersoul.ai"
	testScoutScopedEmail  = "scout.simulacrum@lessersoul.ai"
	testAttrAgentID       = "agentId"
	testAttrStatus        = "status"
)

func TestBuildProposedManagedAddress(t *testing.T) {
	t.Parallel()

	got, err := buildProposedManagedAddress(" Agent.With.Dot ", "Simulacrum")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LocalPart != "agent.with.dot.simulacrum" || got.Address != "agent.with.dot.simulacrum@lessersoul.ai" {
		t.Fatalf("unexpected proposed address: %#v", got)
	}
	if got.LocalPartLength != len("agent.with.dot.simulacrum") || got.Overflow {
		t.Fatalf("unexpected proposed length/overflow: %#v", got)
	}
}

func TestBuildProposedManagedAddressRejectsInvalidSlugAndOverflow(t *testing.T) {
	t.Parallel()

	if _, err := buildProposedManagedAddress("agent", "bad_slug"); err == nil || err.Error() != "invalid_instance_slug" {
		t.Fatalf("expected invalid_instance_slug, got %v", err)
	}

	got, err := buildProposedManagedAddress("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "slug")
	if err == nil || err.Error() != "proposed_local_part_overflow" {
		t.Fatalf("expected overflow, got proposed=%#v err=%v", got, err)
	}
	if !got.Overflow || got.LocalPartLength <= smtpLocalPartLimit {
		t.Fatalf("expected overflow metadata, got %#v", got)
	}
}

func TestAnnotateProposedDuplicates(t *testing.T) {
	t.Parallel()

	records := []managedEmailInventory{
		{AgentID: "a", Proposed: proposedEmailInventory{Address: "Pilot.Simulacrum@lessersoul.ai"}},
		{AgentID: "b", Proposed: proposedEmailInventory{Address: "pilot.simulacrum@lessersoul.ai"}},
		{AgentID: "c", Proposed: proposedEmailInventory{Address: testScoutScopedEmail}},
	}
	annotateProposedDuplicates(records)
	if !records[0].Proposed.Duplicate || !records[1].Proposed.Duplicate || records[2].Proposed.Duplicate {
		t.Fatalf("unexpected duplicate annotations: %#v", records)
	}
	if len(records[0].Issues) != 1 || records[0].Issues[0] != "duplicate_proposed_address" {
		t.Fatalf("expected duplicate issue, got %#v", records[0].Issues)
	}
}

func TestLoadProviderSnapshotNormalizesRedactedState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "provider.json")
	if err := os.WriteFile(path, []byte(`{
  "source":"operator-redacted-migadu-export",
  "addresses":[
    {"local_part":" Pilot ","mailbox_exists":true,"forwardings":["Pilot@Inbound.LesserSoul.ai"],"aliases":[" PILOT "]},
    {"address":"`+testScoutScopedEmail+`","mailbox_exists":false}
  ]
}`), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	states, source, err := loadProviderSnapshot(path)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if source != "operator-redacted-migadu-export" {
		t.Fatalf("unexpected source %q", source)
	}
	pilot := states["pilot"]
	if pilot.Address != testPilotEmail || len(pilot.Forwardings) != 1 || pilot.Forwardings[0] != testPilotInboundEmail || len(pilot.Aliases) != 1 || pilot.Aliases[0] != "pilot" {
		t.Fatalf("unexpected normalized pilot state: %#v", pilot)
	}
	if got := providerStateForEmail(states, "Scout.Simulacrum@LesserSoul.ai"); got == nil || got.Address != testScoutScopedEmail {
		t.Fatalf("expected scout provider state, got %#v", got)
	}
}

func TestParseConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := parseConfig(nil, func(key string) string {
		switch key {
		case "STAGE":
			return "live"
		case "STATE_TABLE_NAME":
			return ""
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Stage != "live" || cfg.TableName != "lesser-host-live-state" || cfg.PageSize != 100 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadRegistrationSnapshotNormalizesSigningState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "registration.json")
	if err := os.WriteFile(path, []byte(`{
  "source":"redacted-registration-export",
  "agents":[
    {"agent_id":" 0xABC ","email_channel":" Pilot@LesserSoul.ai ","can_self_attest":"true"},
    {"agent_id":"0xdef","email_channel":"`+testScoutScopedEmail+`","can_self_attest":"no","source":"agent-note"}
  ]
}`), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	states, err := loadRegistrationSnapshot(path)
	if err != nil {
		t.Fatalf("load registration snapshot: %v", err)
	}
	if got := states["0xabc"]; got.EmailChannel != "pilot@lessersoul.ai" || got.CanSelfAttest != signingStateYes || got.Source != "redacted-registration-export" {
		t.Fatalf("unexpected first state: %#v", got)
	}
	if got := states["0xdef"]; got.EmailChannel != testScoutScopedEmail || got.CanSelfAttest != signingStateNo || got.Source != "agent-note" {
		t.Fatalf("unexpected second state: %#v", got)
	}
}

type fakeDynamoClient struct {
	items map[string]map[string]types.AttributeValue
}

func (f fakeDynamoClient) Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	_ = ctx
	_ = in
	_ = opts
	out := &dynamodb.ScanOutput{}
	for _, item := range f.items {
		if itemString(item, "SK") == "IDENTITY" {
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func (f fakeDynamoClient) GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	_ = ctx
	_ = opts
	pk := itemString(in.Key, "PK")
	sk := itemString(in.Key, "SK")
	item := f.items[pk+"|"+sk]
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func TestBuildInventoryReportHappyPath(t *testing.T) {
	t.Parallel()

	report, err := buildHappyInventoryReport(t)
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	assertHappyInventoryReport(t, report)
}

func buildHappyInventoryReport(t *testing.T) (inventoryReport, error) {
	t.Helper()
	dir := t.TempDir()
	providerPath := filepath.Join(dir, "provider.json")
	registrationPath := filepath.Join(dir, "registration.json")
	writeHappyInventorySnapshots(t, providerPath, registrationPath)
	client := fakeDynamoClient{items: map[string]map[string]types.AttributeValue{}}
	seedHappyInventoryDynamo(client.items)
	cfg := inventoryConfig{Stage: defaultInventoryStage, TableName: "table", ProviderStatePath: providerPath, RegistrationStatePath: registrationPath, PageSize: 100}
	return buildInventoryReport(context.Background(), client, cfg, time.Unix(0, 0).UTC())
}

func writeHappyInventorySnapshots(t *testing.T, providerPath string, registrationPath string) {
	t.Helper()
	mustWriteTestFile(t, providerPath, `{
  "source":"provider-redacted",
  "addresses":[
    {"local_part":"pilot","mailbox_exists":true,"forwardings":["pilot@inbound.lessersoul.ai"]},
    {"local_part":"pilot.simulacrum","mailbox_exists":false}
  ]
}`)
	mustWriteTestFile(t, registrationPath, `{
  "source":"registration-redacted",
  "agents":[
    {"agent_id":"0xagent","email_channel":"pilot@lessersoul.ai","can_self_attest":"yes"}
  ]
}`)
}

func seedHappyInventoryDynamo(items map[string]map[string]types.AttributeValue) {
	putFakeItem(items, "SOUL#AGENT#0xagent", "IDENTITY", map[string]types.AttributeValue{
		testAttrAgentID:          avs("0xagent"),
		"domain":                 avs("dev.simulacrum.greater.website"),
		"localId":                avs("pilot"),
		testAttrStatus:           avs("active"),
		"lifecycleStatus":        avs("active"),
		"selfDescriptionVersion": avn("3"),
	})
	putFakeItem(items, "SOUL#AGENT#0xagent", "CHANNEL#email", map[string]types.AttributeValue{
		testAttrAgentID: avs("0xagent"),
		"identifier":    avs(testPilotEmail),
		"provider":      avs("migadu"),
		testAttrStatus:  avs("active"),
		"verified":      avb(true),
		"secretRef":     avs("/redacted"),
	})
	putFakeItem(items, "SOUL#EMAIL#pilot@lessersoul.ai", "AGENT", map[string]types.AttributeValue{
		"email":         avs(testPilotEmail),
		testAttrAgentID: avs("0xagent"),
	})
	putFakeItem(items, "DOMAIN#simulacrum.greater.website", "METADATA", map[string]types.AttributeValue{
		"domain":             avs("simulacrum.greater.website"),
		"instanceSlug":       avs("simulacrum"),
		"type":               avs("primary"),
		testAttrStatus:       avs("verified"),
		"verificationMethod": avs("managed"),
	})
	putFakeItem(items, "INSTANCE#simulacrum", "METADATA", map[string]types.AttributeValue{
		"slug":             avs("simulacrum"),
		"hostedBaseDomain": avs("simulacrum.greater.website"),
	})
	putFakeItem(items, "SOUL#AGENT#0xagent", "VERSION#3", map[string]types.AttributeValue{
		"versionNumber":      avn("3"),
		"registrationUri":    avs("s3://bucket/registry/v1/agents/0xagent/registration.json"),
		"registrationSha256": avs("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		"changeSummary":      avs("seed"),
		"selfAttestation":    avs("0xsig"),
	})
}

func assertHappyInventoryReport(t *testing.T, report inventoryReport) {
	t.Helper()
	if report.Summary.EligibleManagedEmail != 1 || len(report.Issues) != 0 || len(report.Agents) != 1 {
		t.Fatalf("unexpected report summary/issues: summary=%#v issues=%#v agents=%d", report.Summary, report.Issues, len(report.Agents))
	}
	agent := report.Agents[0]
	assertHappyInventoryDomain(t, agent)
	assertHappyInventoryRegistration(t, agent)
	if agent.Proposed.Address != testPilotScopedEmail || agent.Proposed.Overflow || agent.Proposed.Duplicate {
		t.Fatalf("unexpected proposed address: %#v", agent.Proposed)
	}
	if agent.MigrationReadiness.CanSelfAttest != signingStateYes || agent.MigrationReadiness.HostMigrationPathNeeded {
		t.Fatalf("unexpected readiness: %#v", agent.MigrationReadiness)
	}
	if agent.ProviderState.Current == nil || agent.ProviderState.Proposed == nil {
		t.Fatalf("expected provider state on current and proposed: %#v", agent.ProviderState)
	}
}

func assertHappyInventoryDomain(t *testing.T, agent managedEmailInventory) {
	t.Helper()
	if agent.DomainRecord == nil || agent.DomainRecord.Resolution != "managed_stage_primary_alias" || agent.DomainRecord.InstanceSlug != "simulacrum" {
		t.Fatalf("unexpected domain record: %#v", agent.DomainRecord)
	}
}

func assertHappyInventoryRegistration(t *testing.T, agent managedEmailInventory) {
	t.Helper()
	if agent.RegistrationVersion == nil || agent.RegistrationVersion.EmailChannel != testPilotEmail || !agent.RegistrationVersion.SelfAttestationPresent {
		t.Fatalf("unexpected registration version: %#v", agent.RegistrationVersion)
	}
}

func TestAppendReportIssuesAndAttributeHelpers(t *testing.T) {
	t.Parallel()

	issues := appendReportIssues(nil, inventorySummary{MissingDomainRecord: 1, InactiveDomainRecord: 1, MissingInstanceSlug: 1, InvalidInstanceSlug: 1, MissingCurrentEmailIndex: 1, CurrentEmailIndexMismatch: 1})
	if len(issues) != 6 {
		t.Fatalf("expected 6 report issues, got %#v", issues)
	}
	item := map[string]types.AttributeValue{"s": avs(" value "), "n": avn("42"), "b": avb(true)}
	if itemString(item, "s") != "value" || itemInt(item, "n") != 42 || !itemBool(item, "b") {
		t.Fatalf("unexpected helper output")
	}
}

func mustWriteTestFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func putFakeItem(items map[string]map[string]types.AttributeValue, pk string, sk string, attrs map[string]types.AttributeValue) {
	attrs["PK"] = avs(pk)
	attrs["SK"] = avs(sk)
	items[pk+"|"+sk] = attrs
}

func avs(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func avn(v string) types.AttributeValue { return &types.AttributeValueMemberN{Value: v} }
func avb(v bool) types.AttributeValue   { return &types.AttributeValueMemberBOOL{Value: v} }

func TestWriteReportStdoutAndFile(t *testing.T) {
	t.Parallel()

	report := newInventoryReport(inventoryConfig{Stage: defaultInventoryStage, TableName: "table"}, time.Unix(0, 0).UTC(), 0)
	var stdout bytes.Buffer
	if err := writeReport(report, "-", &stdout); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"schema_version": 1`)) {
		t.Fatalf("stdout report missing schema version: %s", stdout.String())
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeReport(report, path, &bytes.Buffer{}); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("expected report file, info=%#v err=%v", info, err)
	}
}
