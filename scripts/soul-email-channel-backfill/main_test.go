package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const (
	testManagedDomain = "theory.greater.website"
	testLocalID       = "alice"
	testManagedEmail  = "alice.theory@lessersoul.ai"
)

type fakeDynamo struct {
	items        map[string]map[string]dynamotypes.AttributeValue
	identities   []map[string]dynamotypes.AttributeValue
	transactions [][]dynamotypes.TransactWriteItem
	err          error
}

func (f *fakeDynamo) Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.ScanOutput{Items: f.identities}, nil
}
func (f *fakeDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	pk := avString(in.Key["PK"])
	sk := avString(in.Key["SK"])
	if pk == "" || sk == "" {
		return nil, errors.New("invalid key")
	}
	item := f.items[aws.ToString(in.TableName)+"\x00"+pk+"\x00"+sk]
	if item == nil {
		item = f.items[pk+"\x00"+sk]
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}
func (f *fakeDynamo) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.transactions = append(f.transactions, in.TransactItems)
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

type fakeSSM struct {
	values map[string]string
	err    error
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	value, ok := f.values[aws.ToString(in.Name)]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Name: in.Name, Value: aws.String(value)}}, nil
}

func TestParseConfigRequiresApplyConfirmation(t *testing.T) {
	t.Parallel()
	getenv := func(string) string { return "" }
	if _, err := parseConfig([]string{"--stage", "live", "--domain", testManagedDomain, "--apply"}, getenv); err == nil {
		t.Fatal("expected apply safety error")
	}
	cfg, err := parseConfig([]string{"--stage", "live", "--domain", testManagedDomain, "--apply", "--confirm-stage", "live", "--rollback-out", "/tmp/rollback.json", "--source-table", "restored"}, getenv)
	if err != nil || !cfg.Apply || cfg.Stage != "live" || cfg.TableName != "lesser-host-live-state" {
		t.Fatalf("unexpected config: %#v err=%v", cfg, err)
	}
	if _, err := parseConfig([]string{"--stage", "live", "--agent-id", "0xabc", "--apply", "--confirm-stage", "live", "--source-table", "restored", "--out", "/tmp/same.json", "--rollback-out", "/tmp/same.json"}, getenv); err == nil {
		t.Fatal("expected distinct evidence path safety error")
	}
}

func TestProcessAgentPlansExactPointInTimeRestoration(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC)
	agentID := "0xabc"
	domain := testManagedDomain
	localID := testLocalID
	email := testManagedEmail
	secretRef := "/legacy/original/migadu-password"
	pk := agentPK(agentID)
	channelIndexSK := "DOMAIN#" + domain + "#LOCAL#" + localID + "#AGENT#" + agentID
	db := &fakeDynamo{items: map[string]map[string]dynamotypes.AttributeValue{
		"restored\x00" + pk + "\x00IDENTITY":                  {"PK": avs(pk), "SK": avs("IDENTITY"), "agentId": avs(agentID), "domain": avs(domain), "localId": avs(localID)},
		"restored\x00" + pk + "\x00CHANNEL#email":             {"PK": avs(pk), "SK": avs("CHANNEL#email"), "agentId": avs(agentID), "channelType": avs("email"), "identifier": avs(email), "provider": avs("migadu"), "verified": &dynamotypes.AttributeValueMemberBOOL{Value: true}, "provisionedAt": avs("2026-07-01T12:00:00Z"), "status": avs("active"), "secretRef": avs(secretRef)},
		"restored\x00SOUL#EMAIL#" + email + "\x00AGENT":       {"PK": avs("SOUL#EMAIL#" + email), "SK": avs("AGENT"), "email": avs(email), "agentId": avs(agentID)},
		"restored\x00SOUL#CHANNEL#email\x00" + channelIndexSK: {"PK": avs("SOUL#CHANNEL#email"), "SK": avs(channelIndexSK), "channelType": avs("email"), "domain": avs(domain), "localId": avs(localID), "agentId": avs(agentID)},
		"restored\x00" + pk + "\x00CONTACT_PREFERENCES":       {"PK": avs(pk), "SK": avs("CONTACT_PREFERENCES"), "agentId": avs(agentID), "preferred": avs("email")},
	}}
	cs := clients{db: db, ssm: &fakeSSM{values: map[string]string{secretRef: "never-read"}}}
	cfg := config{Stage: "live", TableName: "current", SourceTable: "restored"}
	id := identity{AgentID: agentID, Domain: domain, LocalID: localID}
	rec, keys, err := processAgent(context.Background(), cs, cfg, id, now)
	if err != nil || rec.Classification != classificationPlanned || len(keys) != 5 {
		t.Fatalf("unexpected source-table plan: rec=%#v keys=%#v err=%v", rec, keys, err)
	}
	cfg.Apply = true
	rec, keys, err = processAgent(context.Background(), cs, cfg, id, now)
	if err != nil || rec.Classification != classificationApplied || len(keys) != 5 || len(db.transactions) != 1 {
		t.Fatalf("unexpected source-table apply: rec=%#v keys=%#v err=%v", rec, keys, err)
	}
	channel := findPut(t, db.transactions[0], pk, "CHANNEL#email")
	if avString(channel["secretRef"]) != secretRef || avString(channel["identifier"]) != email {
		t.Fatalf("source channel was not copied exactly: %#v", channel)
	}

	cfg.Apply = false
	db.items["current\x00SOUL#EMAIL#"+email+"\x00AGENT"] = map[string]dynamotypes.AttributeValue{"email": avs(email), "agentId": avs("0xother")}
	rec, _, err = processAgent(context.Background(), cs, cfg, id, now)
	if err != nil || rec.Classification != classificationBlocked || !contains(rec.Issues, "email_index_owned_by_another_agent") {
		t.Fatalf("foreign current index must block: rec=%#v err=%v", rec, err)
	}
}

func TestRunBackfillFiltersAndSummarizes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 8, 16, 0, 0, 0, time.UTC)
	agentID := "0xabc"
	domain := testManagedDomain
	localID := testLocalID
	email := testManagedEmail
	secretRef := "/legacy/original/migadu-password"
	pk := agentPK(agentID)
	channelIndexSK := "DOMAIN#" + domain + "#LOCAL#" + localID + "#AGENT#" + agentID
	db := &fakeDynamo{
		identities: []map[string]dynamotypes.AttributeValue{
			{"agentId": avs("0xdef"), "domain": avs("other.greater.website"), "localId": avs("other")},
			{"agentId": avs(agentID), "domain": avs(domain), "localId": avs(localID)},
		},
		items: map[string]map[string]dynamotypes.AttributeValue{
			"restored\x00" + pk + "\x00IDENTITY":                               {"agentId": avs(agentID), "domain": avs(domain), "localId": avs(localID)},
			"restored\x00" + pk + "\x00" + soulEmailChannelSortKey:             {"PK": avs(pk), "SK": avs(soulEmailChannelSortKey), "agentId": avs(agentID), "channelType": avs("email"), "identifier": avs(email), "provider": avs("migadu"), "verified": &dynamotypes.AttributeValueMemberBOOL{Value: true}, "provisionedAt": avs("2026-07-01T12:00:00Z"), "status": avs("active"), "secretRef": avs(secretRef)},
			"restored\x00SOUL#EMAIL#" + email + "\x00AGENT":                    {"PK": avs("SOUL#EMAIL#" + email), "SK": avs("AGENT"), "email": avs(email), "agentId": avs(agentID)},
			"restored\x00" + soulEmailChannelIndexPK + "\x00" + channelIndexSK: {"PK": avs(soulEmailChannelIndexPK), "SK": avs(channelIndexSK), "channelType": avs("email"), "domain": avs(domain), "localId": avs(localID), "agentId": avs(agentID)},
		},
	}
	cfg := config{Stage: "live", TableName: "current", SourceTable: "restored", Domains: []string{domain}, PageSize: 100}
	rep, rollback, err := runBackfill(context.Background(), clients{db: db, ssm: &fakeSSM{values: map[string]string{secretRef: "never-read"}}}, cfg, now)
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if rep.Summary.ScannedIdentities != 2 || rep.Summary.MatchedIdentities != 1 || rep.Summary.Planned != 1 || rep.Summary.Eligible != 1 || len(rep.Agents) != 1 {
		t.Fatalf("unexpected summary: %#v", rep.Summary)
	}
	if len(rollback.Entries) != 1 || len(rollback.Entries[0].Keys) != 4 {
		t.Fatalf("unexpected rollback evidence: %#v", rollback)
	}

	updateSummary(&rep.Summary, classificationApplied)
	updateSummary(&rep.Summary, classificationAlreadyHealthy)
	updateSummary(&rep.Summary, classificationBlocked)
	updateSummary(&rep.Summary, "error")
	if rep.Summary.Applied != 1 || rep.Summary.AlreadyHealthy != 1 || rep.Summary.Blocked != 1 || rep.Summary.Errors != 1 {
		t.Fatalf("classification summary: %#v", rep.Summary)
	}
}

func TestProcessAgentRequiresCompleteHealthyCurrentState(t *testing.T) {
	t.Parallel()
	agentID := "0xabc"
	domain := testManagedDomain
	localID := testLocalID
	email := testManagedEmail
	secretRef := "/soul/live/agents/0xabc/email/migadu-password"
	pk := agentPK(agentID)
	channelIndexSK := "DOMAIN#" + domain + "#LOCAL#" + localID + "#AGENT#" + agentID
	channel := map[string]dynamotypes.AttributeValue{
		"PK": avs(pk), "SK": avs("CHANNEL#email"), "agentId": avs(agentID), "channelType": avs("email"),
		"identifier": avs(email), "provider": avs("migadu"), "verified": &dynamotypes.AttributeValueMemberBOOL{Value: true},
		"provisionedAt": avs("2026-07-01T12:00:00Z"), "status": avs("active"), "secretRef": avs(secretRef),
	}
	items := map[string]map[string]dynamotypes.AttributeValue{
		pk + "\x00CHANNEL#email":                  channel,
		"SOUL#EMAIL#" + email + "\x00AGENT":       {"PK": avs("SOUL#EMAIL#" + email), "SK": avs("AGENT"), "agentId": avs(agentID)},
		"SOUL#CHANNEL#email\x00" + channelIndexSK: {"PK": avs("SOUL#CHANNEL#email"), "SK": avs(channelIndexSK), "channelType": avs("email"), "domain": avs(domain), "localId": avs(localID), "agentId": avs(agentID)},
	}
	db := &fakeDynamo{items: items}
	cs := clients{db: db, ssm: &fakeSSM{values: map[string]string{secretRef: "never-read"}}}
	cfg := config{Stage: "live", TableName: "state"}
	id := identity{AgentID: agentID, Domain: domain, LocalID: localID}

	rec, _, err := processAgent(context.Background(), cs, cfg, id, time.Now().UTC())
	if err != nil || rec.Classification != "already_healthy" || !rec.SSMParameterPresent || rec.RecoveredEmailSHA256 != sha256Hex(email) {
		t.Fatalf("complete state should be healthy: rec=%#v err=%v", rec, err)
	}

	delete(items, "SOUL#CHANNEL#email\x00"+channelIndexSK)
	rec, _, err = processAgent(context.Background(), cs, cfg, id, time.Now().UTC())
	if err != nil || rec.Classification != classificationBlocked || !contains(rec.Issues, "existing_channel_index_missing_or_mismatched") {
		t.Fatalf("missing index must block: rec=%#v err=%v", rec, err)
	}

	items["SOUL#CHANNEL#email\x00"+channelIndexSK] = map[string]dynamotypes.AttributeValue{"PK": avs("SOUL#CHANNEL#email"), "SK": avs(channelIndexSK), "channelType": avs("email"), "domain": avs(domain), "localId": avs(localID), "agentId": avs(agentID)}
	channel["verified"] = &dynamotypes.AttributeValueMemberBOOL{Value: false}
	rec, _, err = processAgent(context.Background(), cs, cfg, id, time.Now().UTC())
	if err != nil || rec.Classification != classificationBlocked || !contains(rec.Issues, "existing_email_channel_is_not_a_healthy_managed_channel") {
		t.Fatalf("partial channel must block: rec=%#v err=%v", rec, err)
	}
}

func TestParameterLookupErrorsFailClosed(t *testing.T) {
	t.Parallel()
	_, err := parameterExists(context.Background(), &fakeSSM{err: errors.New("access denied")}, "/secret")
	if err == nil {
		t.Fatal("expected lookup error")
	}
	found, err := parameterExists(context.Background(), &fakeSSM{values: map[string]string{}}, "/missing")
	if err != nil || found {
		t.Fatalf("missing parameter: found=%v err=%v", found, err)
	}
}

func TestReservePrivateFileDoesNotOverwriteEvidence(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rollback.json")
	if err := reservePrivateFile(path); err != nil {
		t.Fatalf("reserve file: %v", err)
	}
	if err := reservePrivateFile(path); err == nil {
		t.Fatal("existing evidence file must not be overwritten")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("reserved evidence permissions: info=%v err=%v", info, err)
	}
}

func TestEvidenceHelpers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")
	rep := report{SchemaVersion: reportSchemaVersion, Stage: "live", Summary: summary{Planned: 1}}
	if err := writeReport(rep, reportPath, io.Discard); err != nil {
		t.Fatalf("write report: %v", err)
	}
	rollbackPath := filepath.Join(dir, "rollback.json")
	if err := writeJSONFile(rollbackPath, rollbackReport{SchemaVersion: reportSchemaVersion}); err != nil {
		t.Fatalf("write rollback: %v", err)
	}
	for _, path := range []string{reportPath, rollbackPath} {
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("evidence file %s: info=%v err=%v", path, info, statErr)
		}
	}

	stdout := &bytes.Buffer{}
	if err := writeReport(rep, "-", stdout); err != nil || !strings.Contains(stdout.String(), `"planned": 1`) {
		t.Fatalf("stdout report: %q err=%v", stdout.String(), err)
	}
}

func findPut(t *testing.T, writes []dynamotypes.TransactWriteItem, pk, sk string) map[string]dynamotypes.AttributeValue {
	t.Helper()
	for _, write := range writes {
		if write.Put != nil && avString(write.Put.Item["PK"]) == pk && avString(write.Put.Item["SK"]) == sk {
			return write.Put.Item
		}
	}
	t.Fatalf("put not found: %s / %s", pk, sk)
	return nil
}
func avs(v string) dynamotypes.AttributeValue { return &dynamotypes.AttributeValueMemberS{Value: v} }
func avString(v dynamotypes.AttributeValue) string {
	if s, ok := v.(*dynamotypes.AttributeValueMemberS); ok {
		return s.Value
	}
	return ""
}
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
