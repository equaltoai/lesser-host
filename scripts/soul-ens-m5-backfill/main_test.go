package main

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	testTable        = "lesser-host-lab-state"
	testAgentID      = "0xagent"
	testOtherAgentID = "0xother"
	testLocalID      = "agent-alice"
	testSlug         = "simulacrum"
	testDomain       = "simulacrum.greater.website"
	testLegacyENS    = "agent-alice.lessersoul.eth"
	testCanonicalENS = "agent-alice.simulacrum.lessersoul.eth"
)

type fakeDynamo struct {
	items map[string]map[string]types.AttributeValue
}

func newFakeDynamo() *fakeDynamo {
	return &fakeDynamo{items: map[string]map[string]types.AttributeValue{}}
}

func (f *fakeDynamo) Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	_ = ctx
	_ = opts
	wantSK := avString(in.ExpressionAttributeValues[":sk"])
	pkPrefix := avString(in.ExpressionAttributeValues[":pk"])
	out := &dynamodb.ScanOutput{}
	for _, item := range f.items {
		if itemString(item, "SK") != wantSK {
			continue
		}
		if pkPrefix != "" && !hasPrefix(itemString(item, "PK"), pkPrefix) {
			continue
		}
		out.Items = append(out.Items, cloneItem(item))
	}
	return out, nil
}

func (f *fakeDynamo) GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	_ = ctx
	_ = opts
	item := f.items[itemKey(itemString(in.Key, "PK"), itemString(in.Key, "SK"))]
	return &dynamodb.GetItemOutput{Item: cloneItem(item)}, nil
}

func (f *fakeDynamo) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	_ = ctx
	_ = opts
	key := itemKey(itemString(in.Key, "PK"), itemString(in.Key, "SK"))
	item := f.items[key]
	if item == nil || itemString(item, "agentId") != avString(in.ExpressionAttributeValues[":agentId"]) || itemString(item, "identifier") != avString(in.ExpressionAttributeValues[":oldIdentifier"]) {
		return nil, &types.ConditionalCheckFailedException{Message: aws.String("condition failed")}
	}
	item["identifier"] = avs(avString(in.ExpressionAttributeValues[":newIdentifier"]))
	item["updatedAt"] = avs(avString(in.ExpressionAttributeValues[":updatedAt"]))
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeDynamo) PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	_ = ctx
	_ = opts
	key := itemKey(itemString(in.Item, "PK"), itemString(in.Item, "SK"))
	if f.items[key] != nil {
		return nil, &types.ConditionalCheckFailedException{Message: aws.String("condition failed")}
	}
	f.items[key] = cloneItem(in.Item)
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDynamo) DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	_ = ctx
	_ = opts
	key := itemKey(itemString(in.Key, "PK"), itemString(in.Key, "SK"))
	item := f.items[key]
	if item == nil || itemString(item, "agentId") != avString(in.ExpressionAttributeValues[":agentId"]) || itemString(item, "ensName") != avString(in.ExpressionAttributeValues[":ensName"]) {
		return nil, &types.ConditionalCheckFailedException{Message: aws.String("condition failed")}
	}
	delete(f.items, key)
	return &dynamodb.DeleteItemOutput{}, nil
}

func TestRunBackfill_DryRunClassifiesAndPlansManagedOnly(t *testing.T) {
	t.Parallel()

	client := seededLegacyManagedDynamo()
	seedManagedIdentity(client, testOtherAgentID, "external", "external.greater.website")
	putItem(client, "SOUL#AGENT#"+testOtherAgentID, "CHANNEL#ens", map[string]types.AttributeValue{
		"agentId":     avs(testOtherAgentID),
		"channelType": avs(models.SoulChannelTypeENS),
		"identifier":  avs("alice.eth"),
		"status":      avs(models.SoulChannelStatusActive),
	})

	report, rollback, err := runBackfill(context.Background(), client, testConfig(false), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("runBackfill: %v", err)
	}
	if report.Summary.LegacyManagedBareChannels != 1 || report.Summary.ExternalChannels != 1 {
		t.Fatalf("unexpected classification summary: %#v", report.Summary)
	}
	if report.Summary.ProposedChannelUpdates != 1 || report.Summary.ProposedCanonicalResolutionCreates != 1 || report.Summary.ProposedLegacyResolutionDeletes != 1 {
		t.Fatalf("expected one planned channel/create/delete, got %#v", report.Summary)
	}
	if report.Summary.SkippedExternalRecords == 0 {
		t.Fatalf("expected external record to be skipped, got %#v", report.Summary)
	}
	if len(rollback.Entries) != 3 {
		t.Fatalf("expected rollback entries for planned mutations, got %#v", rollback.Entries)
	}
	if got := itemString(client.items[itemKey("SOUL#AGENT#"+testAgentID, "CHANNEL#ens")], "identifier"); got != testLegacyENS {
		t.Fatalf("dry-run mutated channel identifier: %q", got)
	}
}

func TestParseConfigStageFlagDerivesStageTable(t *testing.T) {
	t.Parallel()

	cfg, err := parseConfig([]string{"--stage", "live"}, func(key string) string {
		switch key {
		case "STATE_TABLE_NAME":
			return "lesser-host-lab-state"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.TableName != "lesser-host-live-state" {
		t.Fatalf("expected live table when --stage is explicit, got %q", cfg.TableName)
	}

	explicit, err := parseConfig([]string{"--stage", "live", "--table-name", "custom"}, func(string) string { return "lesser-host-lab-state" })
	if err != nil {
		t.Fatalf("parseConfig explicit: %v", err)
	}
	if explicit.TableName != "custom" {
		t.Fatalf("expected explicit table override, got %q", explicit.TableName)
	}
}

func TestRunBackfill_ApplyIsIdempotent(t *testing.T) {
	t.Parallel()

	client := seededLegacyManagedDynamo()
	cfg := testConfig(true)
	report, rollback, err := runBackfill(context.Background(), client, cfg, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("apply backfill: %v", err)
	}
	if report.Summary.AppliedChannelUpdates != 1 || report.Summary.AppliedCanonicalResolutionCreates != 1 || report.Summary.AppliedLegacyResolutionDeletes != 1 {
		t.Fatalf("unexpected applied summary: %#v", report.Summary)
	}
	if len(rollback.Entries) != 3 {
		t.Fatalf("expected rollback entries, got %#v", rollback.Entries)
	}
	if got := itemString(client.items[itemKey("SOUL#AGENT#"+testAgentID, "CHANNEL#ens")], "identifier"); got != testCanonicalENS {
		t.Fatalf("expected canonical channel identifier, got %q", got)
	}
	if client.items[itemKey("ENS#NAME#"+testLegacyENS, "RESOLUTION")] != nil {
		t.Fatalf("expected legacy resolution to be deleted")
	}
	if got := itemString(client.items[itemKey("ENS#NAME#"+testCanonicalENS, "RESOLUTION")], "ensName"); got != testCanonicalENS {
		t.Fatalf("expected canonical resolution, got %q", got)
	}

	again, _, err := runBackfill(context.Background(), client, cfg, time.Unix(10, 0).UTC())
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if again.Summary.ProposedChannelUpdates != 0 || again.Summary.ProposedCanonicalResolutionCreates != 0 || again.Summary.ProposedLegacyResolutionDeletes != 0 {
		t.Fatalf("expected idempotent no-op, got %#v", again.Summary)
	}
}

func TestRunBackfill_BlocksCanonicalResolutionConflict(t *testing.T) {
	t.Parallel()

	client := seededLegacyManagedDynamo()
	putItem(client, "ENS#NAME#"+testCanonicalENS, "RESOLUTION", map[string]types.AttributeValue{
		"ensName": avs(testCanonicalENS),
		"agentId": avs(testOtherAgentID),
	})
	report, _, err := runBackfill(context.Background(), client, testConfig(true), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("runBackfill conflict: %v", err)
	}
	if report.Summary.BlockedRecords == 0 || report.Summary.AppliedChannelUpdates != 0 {
		t.Fatalf("expected blocked conflict with no apply, got %#v", report.Summary)
	}
	if got := itemString(client.items[itemKey("SOUL#AGENT#"+testAgentID, "CHANNEL#ens")], "identifier"); got != testLegacyENS {
		t.Fatalf("conflict should not mutate channel, got %q", got)
	}
}

func TestHostedBaseDomainMatchesIdentityStageAlias(t *testing.T) {
	t.Parallel()

	if !hostedBaseDomainMatchesIdentity("lab", "simulacrum.greater.website", "dev.simulacrum.greater.website", "dev.simulacrum.greater.website") {
		t.Fatal("expected lab stage-domain alias to match hosted base domain")
	}
	if hostedBaseDomainMatchesIdentity("live", "simulacrum.greater.website", "dev.simulacrum.greater.website", "dev.simulacrum.greater.website") {
		t.Fatal("live must not treat dev-prefixed domain as a stage alias")
	}
}

func TestCanonicalNameForLocalIDClassifiesNoopWithoutDomain(t *testing.T) {
	t.Parallel()

	client := newFakeDynamo()
	putItem(client, "SOUL#AGENT#"+testAgentID, "IDENTITY", map[string]types.AttributeValue{
		"agentId":         avs(testAgentID),
		"domain":          avs("missing.example"),
		"localId":         avs(testLocalID),
		"status":          avs(models.SoulAgentStatusActive),
		"lifecycleStatus": avs(models.SoulAgentStatusActive),
	})
	putItem(client, "SOUL#AGENT#"+testAgentID, "CHANNEL#ens", map[string]types.AttributeValue{
		"agentId":     avs(testAgentID),
		"channelType": avs(models.SoulChannelTypeENS),
		"identifier":  avs(testCanonicalENS),
		"status":      avs(models.SoulChannelStatusActive),
	})
	putItem(client, "ENS#NAME#"+testCanonicalENS, "RESOLUTION", map[string]types.AttributeValue{
		"ensName": avs(testCanonicalENS),
		"agentId": avs(testAgentID),
	})

	report, _, err := runBackfill(context.Background(), client, testConfig(false), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("runBackfill: %v", err)
	}
	if report.Summary.CanonicalManagedChannels != 1 || report.Summary.CanonicalManagedResolutions != 1 || report.Summary.ProposedChannelUpdates != 0 {
		t.Fatalf("expected canonical no-op classification, got %#v", report.Summary)
	}
}

func seededLegacyManagedDynamo() *fakeDynamo {
	client := newFakeDynamo()
	seedManagedIdentity(client, testAgentID, testLocalID, testDomain)
	putItem(client, "SOUL#AGENT#"+testAgentID, "CHANNEL#ens", map[string]types.AttributeValue{
		"agentId":     avs(testAgentID),
		"channelType": avs(models.SoulChannelTypeENS),
		"identifier":  avs(testLegacyENS),
		"status":      avs(models.SoulChannelStatusActive),
	})
	putItem(client, "ENS#NAME#"+testLegacyENS, "RESOLUTION", map[string]types.AttributeValue{
		"ensName": avs(testLegacyENS),
		"agentId": avs(testAgentID),
		"wallet":  avs("0x0000000000000000000000000000000000000001"),
		"localId": avs(testLocalID),
		"domain":  avs(testDomain),
	})
	return client
}

func seedManagedIdentity(client *fakeDynamo, agentID, localID, domain string) {
	putItem(client, "SOUL#AGENT#"+agentID, "IDENTITY", map[string]types.AttributeValue{
		"agentId":         avs(agentID),
		"domain":          avs(domain),
		"localId":         avs(localID),
		"status":          avs(models.SoulAgentStatusActive),
		"lifecycleStatus": avs(models.SoulAgentStatusActive),
	})
	slug := localID
	if domain == testDomain {
		slug = testSlug
	}
	putItem(client, "DOMAIN#"+domain, models.SKMetadata, map[string]types.AttributeValue{
		"domain":       avs(domain),
		"instanceSlug": avs(slug),
		"status":       avs(models.DomainStatusActive),
		"type":         avs(models.DomainTypePrimary),
	})
	putItem(client, "INSTANCE#"+slug, models.SKMetadata, map[string]types.AttributeValue{
		"slug":             avs(slug),
		"hostedBaseDomain": avs(domain),
		"status":           avs(models.InstanceStatusActive),
	})
}

func testConfig(apply bool) backfillConfig {
	cfg := backfillConfig{Stage: "lab", TableName: testTable, Apply: apply, PageSize: 100, RollbackOut: "/tmp/rollback.json"}
	if !apply {
		cfg.RollbackOut = ""
	}
	return cfg
}

func putItem(client *fakeDynamo, pk, sk string, attrs map[string]types.AttributeValue) {
	item := map[string]types.AttributeValue{"PK": avs(pk), "SK": avs(sk)}
	for key, value := range attrs {
		item[key] = value
	}
	client.items[itemKey(pk, sk)] = item
}

func itemKey(pk, sk string) string { return pk + "|" + sk }

func avs(value string) *types.AttributeValueMemberS {
	return &types.AttributeValueMemberS{Value: value}
}

func avString(value types.AttributeValue) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(*types.AttributeValueMemberS); ok {
		return s.Value
	}
	return ""
}

func hasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
