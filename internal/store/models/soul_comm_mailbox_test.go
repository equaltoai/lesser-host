package models

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	theorycore "github.com/theory-cloud/tabletheory/pkg/core"
	theoryerrors "github.com/theory-cloud/tabletheory/pkg/errors"
	theorymodel "github.com/theory-cloud/tabletheory/pkg/model"
	theoryquery "github.com/theory-cloud/tabletheory/pkg/query"
)

func TestSoulCommMailboxMessageKeysAndRetention(t *testing.T) {
	created := time.Date(2026, 4, 25, 12, 30, 0, 0, time.UTC)
	msg := newSoulCommMailboxMessageForTest(created)
	if err := msg.BeforeCreate(); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
	assertMailboxMessageNormalized(t, msg)
	assertMailboxMessageIDs(t, msg)
	assertMailboxMessageKeys(t, msg)
	assertMailboxMessageRetentionAndPreview(t, msg, created)
}

func TestSoulCommMailboxMessageBeforeUpdatePreservesLoadedKeys(t *testing.T) {
	created := time.Date(2026, 4, 25, 12, 30, 0, 0, time.UTC)
	msg := newSoulCommMailboxMessageForTest(created)
	if err := msg.BeforeCreate(); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
	loadedPK := msg.PK
	loadedSK := msg.SK
	loadedGSI1PK := msg.GSI1PK
	loadedGSI1SK := msg.GSI1SK
	loadedGSI2PK := msg.GSI2PK
	loadedGSI2SK := msg.GSI2SK
	loadedTTL := msg.TTL

	// TableTheory owns implicit createdAt/updatedAt timestamps at marshal time;
	// state updates must still target the current row's persisted PK/SK rather
	// than recomputing keys from a later createdAt attribute.
	msg.CreatedAt = created.Add(4 * time.Second)
	msg.Read = true
	if err := msg.BeforeUpdate(); err != nil {
		t.Fatalf("BeforeUpdate: %v", err)
	}

	if msg.PK != loadedPK || msg.SK != loadedSK {
		t.Fatalf("expected loaded primary keys to be preserved, got %q/%q want %q/%q", msg.PK, msg.SK, loadedPK, loadedSK)
	}
	if msg.GSI1PK != loadedGSI1PK || msg.GSI1SK != loadedGSI1SK || msg.GSI2PK != loadedGSI2PK || msg.GSI2SK != loadedGSI2SK {
		t.Fatalf("expected loaded index keys to be preserved, got gsi1=%q/%q gsi2=%q/%q", msg.GSI1PK, msg.GSI1SK, msg.GSI2PK, msg.GSI2SK)
	}
	if msg.TTL != loadedTTL {
		t.Fatalf("expected loaded ttl to be preserved, got %d want %d", msg.TTL, loadedTTL)
	}
}

func TestSoulCommMailboxThreadIDNormalizesHostMessageIDReferences(t *testing.T) {
	bare := SoulCommMailboxThreadID("demo", "0xabc", "email", "comm-msg-abc123")
	hostRef := SoulCommMailboxThreadID("demo", "0xabc", "email", "<comm-msg-abc123@lessersoul.ai>")
	external := SoulCommMailboxThreadID("demo", "0xabc", "email", "<comm-msg-abc123@example.net>")

	if bare != hostRef {
		t.Fatalf("expected host-generated provider Message-ID to normalize into the bare thread root: %s != %s", bare, hostRef)
	}
	if bare == external {
		t.Fatalf("expected external Message-ID domain to stay distinct from host thread root")
	}
}

func newSoulCommMailboxMessageForTest(created time.Time) *SoulCommMailboxMessage {
	return &SoulCommMailboxMessage{
		InstanceSlug:      " Demo ",
		AgentID:           " 0xABC ",
		Direction:         SoulCommDirectionInbound,
		ChannelType:       " Email ",
		MessageID:         "<provider-msg-1>",
		Provider:          "Migadu",
		ProviderMessageID: "provider-msg-1",
		FromAddress:       "sender@example.com",
		ToAddress:         "agent@example.com",
		Subject:           "Hello",
		Preview:           strings.Repeat("word ", 80),
		HasContent:        true,
		ContentStorage:    "s3",
		ContentBucket:     "bucket",
		ContentKey:        "mailbox/v1/agent/0xabc/delivery/content",
		ContentSHA256:     "ABCDEF",
		ContentBytes:      12,
		ContentMimeType:   "text/plain",
		ContentStoredAt:   created,
		CreatedAt:         created,
	}
}

func assertMailboxMessageNormalized(t *testing.T, msg *SoulCommMailboxMessage) {
	t.Helper()
	if msg.InstanceSlug != "demo" {
		t.Fatalf("unexpected instance slug: %q", msg.InstanceSlug)
	}
	if msg.AgentID != "0xabc" {
		t.Fatalf("unexpected agent id: %q", msg.AgentID)
	}
	if msg.ChannelType != "email" || msg.Provider != "migadu" {
		t.Fatalf("unexpected channel/provider: %q/%q", msg.ChannelType, msg.Provider)
	}
}

func assertMailboxMessageIDs(t *testing.T, msg *SoulCommMailboxMessage) {
	t.Helper()
	if msg.DeliveryID == "" || !strings.HasPrefix(msg.DeliveryID, "comm-delivery-") {
		t.Fatalf("unexpected delivery id: %q", msg.DeliveryID)
	}
	if msg.ThreadID == "" || !strings.HasPrefix(msg.ThreadID, "comm-thread-") {
		t.Fatalf("unexpected thread id: %q", msg.ThreadID)
	}
}

func assertMailboxMessageKeys(t *testing.T, msg *SoulCommMailboxMessage) {
	t.Helper()
	wantPK := SoulCommMailboxAgentPK("demo", "0xabc")
	if msg.PK != wantPK {
		t.Fatalf("unexpected pk: %q", msg.PK)
	}
	if !strings.HasPrefix(msg.SK, "MSG#2026-04-25T12:30:00.000000000Z#") {
		t.Fatalf("unexpected sk: %q", msg.SK)
	}
	if msg.GSI1PK != SoulCommMailboxDeliveryPK(msg.DeliveryID) || msg.GSI1SK != "CURRENT" {
		t.Fatalf("unexpected delivery index: %q/%q", msg.GSI1PK, msg.GSI1SK)
	}
	if msg.GSI2PK != SoulCommMailboxThreadPK("demo", "0xabc", msg.ThreadID) {
		t.Fatalf("unexpected thread pk: %q", msg.GSI2PK)
	}
	if !strings.HasPrefix(msg.GSI2SK, "MSG#2026-04-25T12:30:00.000000000Z#") {
		t.Fatalf("unexpected thread sk: %q", msg.GSI2SK)
	}
}

func assertMailboxMessageRetentionAndPreview(t *testing.T, msg *SoulCommMailboxMessage, created time.Time) {
	t.Helper()
	if got, want := msg.TTL, created.Add(SoulCommMailboxRetentionDays*24*time.Hour).Unix(); got != want {
		t.Fatalf("unexpected ttl: got %d want %d", got, want)
	}
	if len([]rune(msg.Preview)) > 161 {
		t.Fatalf("expected bounded preview, got %q", msg.Preview)
	}
	if !strings.HasSuffix(msg.Preview, "…") {
		t.Fatalf("expected truncated preview, got %q", msg.Preview)
	}
}

func TestSoulCommMailboxEventWriteOnceKeys(t *testing.T) {
	created := time.Date(2026, 4, 25, 12, 45, 0, 0, time.UTC)
	evt := &SoulCommMailboxEvent{
		DeliveryID:   "comm-delivery-1",
		MessageID:    "msg-1",
		ThreadID:     "comm-thread-1",
		InstanceSlug: "Demo",
		AgentID:      "0xABC",
		Direction:    SoulCommDirectionOutbound,
		ChannelType:  "email",
		EventType:    SoulCommMailboxEventCreated,
		Status:       SoulCommMailboxStatusSent,
		Actor:        "instance:demo",
		CreatedAt:    created,
	}
	if err := evt.BeforeCreate(); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
	if evt.PK != SoulCommMailboxDeliveryPK("comm-delivery-1") || !strings.HasPrefix(evt.SK, "EVENT#2026-04-25T12:45:00.000000000Z#") {
		t.Fatalf("unexpected event keys: %q/%q", evt.PK, evt.SK)
	}
	if evt.GSI1PK != SoulCommMailboxAgentPK("demo", "0xabc") || evt.GSI2PK != SoulCommMailboxThreadPK("demo", "0xabc", "comm-thread-1") {
		t.Fatalf("unexpected event indexes: %q %q", evt.GSI1PK, evt.GSI2PK)
	}
	if got, want := evt.TTL, created.Add(SoulCommMailboxRetentionDays*24*time.Hour).Unix(); got != want {
		t.Fatalf("unexpected ttl: got %d want %d", got, want)
	}
}

func TestSoulCommMailboxWritePolicies(t *testing.T) {
	msg := &SoulCommMailboxMessage{
		DeliveryID:   "comm-delivery-1",
		MessageID:    "msg-1",
		ThreadID:     "comm-thread-1",
		InstanceSlug: "demo",
		AgentID:      "0xabc",
		Direction:    SoulCommDirectionInbound,
		ChannelType:  "email",
		Status:       SoulCommMailboxStatusAccepted,
		CreatedAt:    time.Now().UTC(),
	}
	if err := msg.BeforeCreate(); err != nil {
		t.Fatalf("message BeforeCreate: %v", err)
	}
	q, _ := newMailboxPolicyQuery(t, msg)
	if err := q.Update("Read"); err != nil {
		t.Fatalf("expected read state update to be allowed: %v", err)
	}
	q, _ = newMailboxPolicyQuery(t, msg)
	if err := q.Update("deliveryId"); !errors.Is(err, theoryerrors.ErrProtectedFieldMutation) {
		t.Fatalf("expected deliveryId protected-field error, got %v", err)
	}
	q, _ = newMailboxPolicyQuery(t, msg)
	if err := q.Update("contentKey"); !errors.Is(err, theoryerrors.ErrProtectedFieldMutation) {
		t.Fatalf("expected contentKey protected-field error, got %v", err)
	}

	evt := &SoulCommMailboxEvent{
		EventID:      "event-1",
		DeliveryID:   "comm-delivery-1",
		InstanceSlug: "demo",
		AgentID:      "0xabc",
		Direction:    SoulCommDirectionInbound,
		ChannelType:  "email",
		EventType:    SoulCommMailboxEventCreated,
		CreatedAt:    time.Now().UTC(),
	}
	if err := evt.BeforeCreate(); err != nil {
		t.Fatalf("event BeforeCreate: %v", err)
	}
	q, _ = newMailboxPolicyQuery(t, evt)
	if err := q.CreateOrUpdate(); !errors.Is(err, theoryerrors.ErrImmutableModelMutation) {
		t.Fatalf("expected immutable upsert error, got %v", err)
	}
}

type mailboxPolicyMetadata struct{ meta *theorymodel.Metadata }

func (m mailboxPolicyMetadata) TableName() string { return m.meta.TableName }

func (m mailboxPolicyMetadata) PrimaryKey() theorycore.KeySchema {
	schema := theorycore.KeySchema{}
	if m.meta.PrimaryKey != nil && m.meta.PrimaryKey.PartitionKey != nil {
		schema.PartitionKey = m.meta.PrimaryKey.PartitionKey.Name
	}
	if m.meta.PrimaryKey != nil && m.meta.PrimaryKey.SortKey != nil {
		schema.SortKey = m.meta.PrimaryKey.SortKey.Name
	}
	return schema
}

func (m mailboxPolicyMetadata) Indexes() []theorycore.IndexSchema { return nil }

func (m mailboxPolicyMetadata) AttributeMetadata(field string) *theorycore.AttributeMetadata {
	if meta := m.meta.Fields[field]; meta != nil {
		return mailboxPolicyAttributeMetadata(meta)
	}
	if meta := m.meta.FieldsByDBName[field]; meta != nil {
		return mailboxPolicyAttributeMetadata(meta)
	}
	return nil
}

func (m mailboxPolicyMetadata) VersionFieldName() string             { return "" }
func (m mailboxPolicyMetadata) RawMetadata() *theorymodel.Metadata   { return m.meta }
func (m mailboxPolicyMetadata) WritePolicy() theorymodel.WritePolicy { return m.meta.WritePolicy }

func mailboxPolicyAttributeMetadata(field *theorymodel.FieldMetadata) *theorycore.AttributeMetadata {
	typeName := ""
	if field.Type != nil {
		typeName = field.Type.String()
	}
	return &theorycore.AttributeMetadata{
		Name:         field.Name,
		Type:         typeName,
		DynamoDBName: field.DBName,
		Tags:         field.Tags,
	}
}

type mailboxPolicyExecutor struct{}

func (mailboxPolicyExecutor) ExecuteQuery(*theorycore.CompiledQuery, any) error { return nil }
func (mailboxPolicyExecutor) ExecuteScan(*theorycore.CompiledQuery, any) error  { return nil }
func (mailboxPolicyExecutor) ExecutePutItem(*theorycore.CompiledQuery, map[string]types.AttributeValue) error {
	return nil
}
func (mailboxPolicyExecutor) ExecuteUpdateItem(*theorycore.CompiledQuery, map[string]types.AttributeValue) error {
	return nil
}
func (mailboxPolicyExecutor) ExecuteDeleteItem(*theorycore.CompiledQuery, map[string]types.AttributeValue) error {
	return nil
}

func newMailboxPolicyQuery(t *testing.T, item any) (*theoryquery.Query, *mailboxPolicyExecutor) {
	t.Helper()
	registry := theorymodel.NewRegistry()
	if err := registry.Register(item); err != nil {
		t.Fatalf("register: %v", err)
	}
	meta, err := registry.GetMetadata(item)
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	exec := &mailboxPolicyExecutor{}
	return theoryquery.New(item, mailboxPolicyMetadata{meta: meta}, exec), exec
}
