package models

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	theorymodel "github.com/theory-cloud/tabletheory/pkg/model"
)

// SoulCommMailboxRetentionDays is the bounded retention window for canonical
// soul comm mailbox metadata. Content objects use the same lifecycle duration
// in the CDK-managed mailbox bucket.
const SoulCommMailboxRetentionDays = 90

// SoulCommMailbox* constants define canonical mailbox state values.
const (
	SoulCommMailboxStatusAccepted  = "accepted"
	SoulCommMailboxStatusSent      = "sent"
	SoulCommMailboxStatusDelivered = "delivered"
	SoulCommMailboxStatusQueued    = "queued"
	SoulCommMailboxStatusFailed    = "failed"
	SoulCommMailboxStatusBounced   = "bounced"
	SoulCommMailboxStatusDropped   = "dropped"
)

// SoulCommMailboxEvent* constants define immutable mailbox event types.
const (
	SoulCommMailboxEventCreated          = "created"
	SoulCommMailboxEventContentStored    = "content_stored"
	SoulCommMailboxEventProjectionQueued = "projection_queued"
	SoulCommMailboxEventStateChanged     = "state_changed"
)

// SoulCommMailboxMessage stores the canonical current mailbox row for a soul
// comm delivery. The partition key scopes every list query to a specific
// instance + agent pair; direct delivery and thread lookups use tenant-scoped
// GSIs.
//
// Keys:
//
//	PK: COMM#MAILBOX#INSTANCE#{instanceSlug}#AGENT#{agentId}
//	SK: MSG#{timestamp}#{deliveryId}
//	GSI1PK: COMM#MAILBOX#DELIVERY#{deliveryId}
//	GSI1SK: CURRENT
//	GSI2PK: COMM#MAILBOX#INSTANCE#{instanceSlug}#AGENT#{agentId}#THREAD#{threadId}
//	GSI2SK: MSG#{timestamp}#{deliveryId}
//
// The current row is mutable only for mailbox state and provider status; identity,
// provenance, and content identity fields are protected by TableTheory write
// policy metadata.
type SoulCommMailboxMessage struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK,omitempty" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK,omitempty" json:"-"`
	GSI2PK string `theorydb:"index:gsi2,pk,attr:gsi2PK,omitempty" json:"-"`
	GSI2SK string `theorydb:"index:gsi2,sk,attr:gsi2SK,omitempty" json:"-"`

	DeliveryID   string `theorydb:"attr:deliveryId" json:"delivery_id"`
	MessageID    string `theorydb:"attr:messageId" json:"message_id"`
	ThreadID     string `theorydb:"attr:threadId" json:"thread_id"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	AgentID      string `theorydb:"attr:agentId" json:"agent_id"`

	Direction   string `theorydb:"attr:direction" json:"direction"`      // inbound|outbound
	ChannelType string `theorydb:"attr:channelType" json:"channel_type"` // email|sms|voice

	Provider          string `theorydb:"attr:provider" json:"provider,omitempty"`
	ProviderMessageID string `theorydb:"attr:providerMessageId" json:"provider_message_id,omitempty"`
	Status            string `theorydb:"attr:status" json:"status"`

	FromAddress     string `theorydb:"attr:fromAddress" json:"from_address,omitempty"`
	FromNumber      string `theorydb:"attr:fromNumber" json:"from_number,omitempty"`
	FromSoulAgentID string `theorydb:"attr:fromSoulAgentId" json:"from_soul_agent_id,omitempty"`
	FromDisplayName string `theorydb:"attr:fromDisplayName" json:"from_display_name,omitempty"`
	ToAddress       string `theorydb:"attr:toAddress" json:"to_address,omitempty"`
	ToNumber        string `theorydb:"attr:toNumber" json:"to_number,omitempty"`
	ToSoulAgentID   string `theorydb:"attr:toSoulAgentId" json:"to_soul_agent_id,omitempty"`
	ToDisplayName   string `theorydb:"attr:toDisplayName" json:"to_display_name,omitempty"`

	Subject string `theorydb:"attr:subject" json:"subject,omitempty"`
	Preview string `theorydb:"attr:preview" json:"preview,omitempty"`

	ContentStorage  string    `theorydb:"attr:contentStorage" json:"content_storage,omitempty"`
	ContentBucket   string    `theorydb:"attr:contentBucket" json:"content_bucket,omitempty"`
	ContentKey      string    `theorydb:"attr:contentKey" json:"content_key,omitempty"`
	ContentSHA256   string    `theorydb:"attr:contentSha256" json:"content_sha256,omitempty"`
	ContentBytes    int64     `theorydb:"attr:contentBytes" json:"content_bytes,omitempty"`
	ContentMimeType string    `theorydb:"attr:contentMimeType" json:"content_mime_type,omitempty"`
	ContentStoredAt time.Time `theorydb:"attr:contentStoredAt" json:"content_stored_at,omitempty"`
	HasContent      bool      `theorydb:"attr:hasContent" json:"has_content"`

	Read     bool `theorydb:"attr:read" json:"read"`
	Archived bool `theorydb:"attr:archived" json:"archived"`
	Deleted  bool `theorydb:"attr:deleted" json:"deleted"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

// TableName returns the database table name for SoulCommMailboxMessage.
func (SoulCommMailboxMessage) TableName() string { return MainTableName() }

// WritePolicy protects immutable identity, provenance, and content identity
// fields while leaving mailbox state fields mutable for Host 3 APIs.
func (SoulCommMailboxMessage) WritePolicy() theorymodel.WritePolicy {
	return theorymodel.WritePolicy{
		Mode: theorymodel.WritePolicyModeMutable,
		ProtectedAttributes: []string{
			"deliveryId",
			"messageId",
			"threadId",
			"instanceSlug",
			"agentId",
			"direction",
			"channelType",
			"provider",
			"providerMessageId",
			"contentStorage",
			"contentBucket",
			"contentKey",
			"contentSha256",
			"contentBytes",
			"contentMimeType",
			"contentStoredAt",
			"createdAt",
		},
	}
}

// BeforeCreate sets defaults and keys before creating a mailbox current row.
func (m *SoulCommMailboxMessage) BeforeCreate() error {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = m.CreatedAt
	}
	if strings.TrimSpace(m.Direction) == "" {
		m.Direction = SoulCommDirectionInbound
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = SoulCommMailboxStatusAccepted
	}
	if strings.TrimSpace(m.ThreadID) == "" {
		m.ThreadID = SoulCommMailboxThreadID(m.InstanceSlug, m.AgentID, m.ChannelType, m.MessageID)
	}
	if strings.TrimSpace(m.DeliveryID) == "" {
		m.DeliveryID = SoulCommMailboxDeliveryID(m.InstanceSlug, m.AgentID, m.Direction, m.MessageID)
	}
	if m.HasContent && strings.TrimSpace(m.ContentStorage) == "" {
		m.ContentStorage = "s3"
	}
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("deliveryId", m.DeliveryID); err != nil {
		return err
	}
	if err := requireNonEmpty("messageId", m.MessageID); err != nil {
		return err
	}
	if err := requireNonEmpty("threadId", m.ThreadID); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", m.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("channelType", m.ChannelType); err != nil {
		return err
	}
	if err := requireOneOf("direction", m.Direction, SoulCommDirectionInbound, SoulCommDirectionOutbound); err != nil {
		return err
	}
	if err := validateSoulCommMailboxStatus(m.Status); err != nil {
		return err
	}
	if m.HasContent {
		if err := requireNonEmpty("contentStorage", m.ContentStorage); err != nil {
			return err
		}
		if err := requireNonEmpty("contentBucket", m.ContentBucket); err != nil {
			return err
		}
		if err := requireNonEmpty("contentKey", m.ContentKey); err != nil {
			return err
		}
		if err := requireNonEmpty("contentSha256", m.ContentSHA256); err != nil {
			return err
		}
		if m.ContentBytes <= 0 {
			return fmt.Errorf("contentBytes must be positive")
		}
	}
	return nil
}

// BeforeUpdate updates timestamps and keys before mutating a mailbox current row.
func (m *SoulCommMailboxMessage) BeforeUpdate() error {
	m.UpdatedAt = time.Now().UTC()
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("deliveryId", m.DeliveryID); err != nil {
		return err
	}
	return validateSoulCommMailboxStatus(m.Status)
}

// UpdateKeys updates database keys for a mailbox current row.
func (m *SoulCommMailboxMessage) UpdateKeys() error {
	m.DeliveryID = strings.TrimSpace(m.DeliveryID)
	m.MessageID = strings.TrimSpace(m.MessageID)
	m.ThreadID = strings.TrimSpace(m.ThreadID)
	m.InstanceSlug = strings.ToLower(strings.TrimSpace(m.InstanceSlug))
	m.AgentID = strings.ToLower(strings.TrimSpace(m.AgentID))
	m.Direction = strings.ToLower(strings.TrimSpace(m.Direction))
	m.ChannelType = strings.ToLower(strings.TrimSpace(m.ChannelType))
	m.Provider = strings.ToLower(strings.TrimSpace(m.Provider))
	m.ProviderMessageID = strings.TrimSpace(m.ProviderMessageID)
	m.Status = strings.ToLower(strings.TrimSpace(m.Status))
	m.FromAddress = strings.TrimSpace(m.FromAddress)
	m.FromNumber = strings.TrimSpace(m.FromNumber)
	m.FromSoulAgentID = strings.ToLower(strings.TrimSpace(m.FromSoulAgentID))
	m.FromDisplayName = strings.TrimSpace(m.FromDisplayName)
	m.ToAddress = strings.TrimSpace(m.ToAddress)
	m.ToNumber = strings.TrimSpace(m.ToNumber)
	m.ToSoulAgentID = strings.ToLower(strings.TrimSpace(m.ToSoulAgentID))
	m.ToDisplayName = strings.TrimSpace(m.ToDisplayName)
	m.Subject = strings.TrimSpace(m.Subject)
	m.Preview = SoulCommMailboxPreview(m.Preview)
	m.ContentStorage = strings.ToLower(strings.TrimSpace(m.ContentStorage))
	m.ContentBucket = strings.TrimSpace(m.ContentBucket)
	m.ContentKey = strings.TrimSpace(m.ContentKey)
	m.ContentSHA256 = strings.ToLower(strings.TrimSpace(m.ContentSHA256))
	m.ContentMimeType = strings.TrimSpace(m.ContentMimeType)

	ts := m.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
	m.PK = SoulCommMailboxAgentPK(m.InstanceSlug, m.AgentID)
	m.SK = fmt.Sprintf("MSG#%s#%s", ts, m.DeliveryID)
	m.GSI1PK = SoulCommMailboxDeliveryPK(m.DeliveryID)
	m.GSI1SK = "CURRENT"
	m.GSI2PK = SoulCommMailboxThreadPK(m.InstanceSlug, m.AgentID, m.ThreadID)
	m.GSI2SK = fmt.Sprintf("MSG#%s#%s", ts, m.DeliveryID)
	m.TTL = m.CreatedAt.UTC().Add(SoulCommMailboxRetentionDays * 24 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for SoulCommMailboxMessage.
func (m *SoulCommMailboxMessage) GetPK() string { return m.PK }

// GetSK returns the sort key for SoulCommMailboxMessage.
func (m *SoulCommMailboxMessage) GetSK() string { return m.SK }

// SoulCommMailboxEvent stores immutable audit/history events for a mailbox
// delivery. Events intentionally avoid message body content.
type SoulCommMailboxEvent struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK,omitempty" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK,omitempty" json:"-"`
	GSI2PK string `theorydb:"index:gsi2,pk,attr:gsi2PK,omitempty" json:"-"`
	GSI2SK string `theorydb:"index:gsi2,sk,attr:gsi2SK,omitempty" json:"-"`

	EventID      string    `theorydb:"attr:eventId" json:"event_id"`
	DeliveryID   string    `theorydb:"attr:deliveryId" json:"delivery_id"`
	MessageID    string    `theorydb:"attr:messageId" json:"message_id,omitempty"`
	ThreadID     string    `theorydb:"attr:threadId" json:"thread_id,omitempty"`
	InstanceSlug string    `theorydb:"attr:instanceSlug" json:"instance_slug"`
	AgentID      string    `theorydb:"attr:agentId" json:"agent_id"`
	Direction    string    `theorydb:"attr:direction" json:"direction"`
	ChannelType  string    `theorydb:"attr:channelType" json:"channel_type"`
	EventType    string    `theorydb:"attr:eventType" json:"event_type"`
	Status       string    `theorydb:"attr:status" json:"status,omitempty"`
	Actor        string    `theorydb:"attr:actor" json:"actor,omitempty"`
	DetailsJSON  string    `theorydb:"attr:detailsJson" json:"details_json,omitempty"`
	CreatedAt    time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for SoulCommMailboxEvent.
func (SoulCommMailboxEvent) TableName() string { return MainTableName() }

// WritePolicy makes mailbox event rows append-only.
func (SoulCommMailboxEvent) WritePolicy() theorymodel.WritePolicy {
	return theorymodel.WritePolicy{Mode: theorymodel.WritePolicyModeWriteOnce}
}

// BeforeCreate sets defaults and keys before creating an immutable event row.
func (e *SoulCommMailboxEvent) BeforeCreate() error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(e.EventID) == "" {
		e.EventID = SoulCommMailboxEventID(e.EventType, e.DeliveryID, e.Status, e.CreatedAt)
	}
	if err := e.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("eventId", e.EventID); err != nil {
		return err
	}
	if err := requireNonEmpty("deliveryId", e.DeliveryID); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", e.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", e.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("eventType", e.EventType); err != nil {
		return err
	}
	if strings.TrimSpace(e.Status) != "" {
		return validateSoulCommMailboxStatus(e.Status)
	}
	return nil
}

// UpdateKeys updates database keys for a mailbox event row.
func (e *SoulCommMailboxEvent) UpdateKeys() error {
	e.EventID = strings.TrimSpace(e.EventID)
	e.DeliveryID = strings.TrimSpace(e.DeliveryID)
	e.MessageID = strings.TrimSpace(e.MessageID)
	e.ThreadID = strings.TrimSpace(e.ThreadID)
	e.InstanceSlug = strings.ToLower(strings.TrimSpace(e.InstanceSlug))
	e.AgentID = strings.ToLower(strings.TrimSpace(e.AgentID))
	e.Direction = strings.ToLower(strings.TrimSpace(e.Direction))
	e.ChannelType = strings.ToLower(strings.TrimSpace(e.ChannelType))
	e.EventType = strings.ToLower(strings.TrimSpace(e.EventType))
	e.Status = strings.ToLower(strings.TrimSpace(e.Status))
	e.Actor = strings.TrimSpace(e.Actor)
	e.DetailsJSON = strings.TrimSpace(e.DetailsJSON)

	ts := e.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
	e.PK = SoulCommMailboxDeliveryPK(e.DeliveryID)
	e.SK = fmt.Sprintf("EVENT#%s#%s", ts, e.EventID)
	e.GSI1PK = SoulCommMailboxAgentPK(e.InstanceSlug, e.AgentID)
	e.GSI1SK = fmt.Sprintf("EVENT#%s#%s", ts, e.EventID)
	if e.ThreadID != "" {
		e.GSI2PK = SoulCommMailboxThreadPK(e.InstanceSlug, e.AgentID, e.ThreadID)
		e.GSI2SK = fmt.Sprintf("EVENT#%s#%s", ts, e.EventID)
	} else {
		e.GSI2PK = ""
		e.GSI2SK = ""
	}
	e.TTL = e.CreatedAt.UTC().Add(SoulCommMailboxRetentionDays * 24 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for SoulCommMailboxEvent.
func (e *SoulCommMailboxEvent) GetPK() string { return e.PK }

// GetSK returns the sort key for SoulCommMailboxEvent.
func (e *SoulCommMailboxEvent) GetSK() string { return e.SK }

// SoulCommMailboxAgentPK scopes mailbox reads to one instance/agent pair.
func SoulCommMailboxAgentPK(instanceSlug string, agentID string) string {
	return fmt.Sprintf("COMM#MAILBOX#INSTANCE#%s#AGENT#%s", strings.ToLower(strings.TrimSpace(instanceSlug)), strings.ToLower(strings.TrimSpace(agentID)))
}

// SoulCommMailboxDeliveryPK returns the delivery lookup partition key.
func SoulCommMailboxDeliveryPK(deliveryID string) string {
	return fmt.Sprintf("COMM#MAILBOX#DELIVERY#%s", strings.TrimSpace(deliveryID))
}

// SoulCommMailboxThreadPK scopes thread reads to one instance/agent pair.
func SoulCommMailboxThreadPK(instanceSlug string, agentID string, threadID string) string {
	return fmt.Sprintf("%s#THREAD#%s", SoulCommMailboxAgentPK(instanceSlug, agentID), strings.TrimSpace(threadID))
}

// SoulCommMailboxDeliveryID returns a deterministic canonical delivery id.
func SoulCommMailboxDeliveryID(instanceSlug string, agentID string, direction string, messageID string) string {
	root := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(instanceSlug)),
		strings.ToLower(strings.TrimSpace(agentID)),
		strings.ToLower(strings.TrimSpace(direction)),
		strings.TrimSpace(messageID),
	}, "|")
	return "comm-delivery-" + shortHash(root, 24)
}

// SoulCommMailboxThreadID returns a deterministic thread id scoped to one agent.
func SoulCommMailboxThreadID(instanceSlug string, agentID string, channelType string, rootMessageID string) string {
	root := strings.TrimSpace(rootMessageID)
	if root == "" {
		root = "root"
	}
	payload := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(instanceSlug)),
		strings.ToLower(strings.TrimSpace(agentID)),
		strings.ToLower(strings.TrimSpace(channelType)),
		root,
	}, "|")
	return "comm-thread-" + shortHash(payload, 24)
}

// SoulCommMailboxEventID returns a deterministic-ish event id for append rows.
func SoulCommMailboxEventID(eventType string, deliveryID string, status string, createdAt time.Time) string {
	root := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(eventType)),
		strings.TrimSpace(deliveryID),
		strings.ToLower(strings.TrimSpace(status)),
		createdAt.UTC().Format(time.RFC3339Nano),
	}, "|")
	return "comm-mailbox-event-" + shortHash(root, 20)
}

// SoulCommMailboxPreview collapses message body text into a bounded redacted
// preview for list responses. The full body remains in the explicit content
// object, not in list-oriented fields.
func SoulCommMailboxPreview(body string) string {
	body = strings.Join(strings.Fields(strings.TrimSpace(body)), " ")
	const maxRunes = 160
	if utf8.RuneCountInString(body) <= maxRunes {
		return body
	}
	runes := []rune(body)
	return string(runes[:maxRunes]) + "…"
}

func validateSoulCommMailboxStatus(status string) error {
	return requireOneOf(
		"status",
		strings.ToLower(strings.TrimSpace(status)),
		SoulCommMailboxStatusAccepted,
		SoulCommMailboxStatusSent,
		SoulCommMailboxStatusDelivered,
		SoulCommMailboxStatusQueued,
		SoulCommMailboxStatusFailed,
		SoulCommMailboxStatusBounced,
		SoulCommMailboxStatusDropped,
	)
}

func shortHash(value string, length int) string {
	sum := sha256.Sum256([]byte(value))
	hexValue := hex.EncodeToString(sum[:])
	if length <= 0 || length > len(hexValue) {
		return hexValue
	}
	return hexValue[:length]
}
