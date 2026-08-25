package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/v4/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

func TestHostedGenesisMicroVMExecutionModelKeysAndRoundTrip(t *testing.T) {
	t.Parallel()

	record := validHostedGenesisMicroVMExecutionSessionRecord()
	item, err := NewHostedGenesisMicroVMExecutionFromSessionRecord(record)
	require.NoError(t, err)

	require.Equal(t, "demo", item.InstanceSlug)
	require.Equal(t, "slug:demo", item.TenantID)
	require.Equal(t, hostedgenesis.MicroVMNamespace, item.Namespace)
	require.Equal(t, MainTableName(), item.TableName())
	require.Equal(t, "HOSTED_GENESIS_MICROVM#INSTANCE#demo#NAMESPACE#hosted-genesis", item.PK)
	require.Equal(t, "SESSION#conv_123", item.SK)
	require.Equal(t, record.ExpiresAt.Unix(), item.TTL)

	got, err := item.ToSessionRecord()
	require.NoError(t, err)
	require.Equal(t, record.TenantID, got.TenantID)
	require.Equal(t, record.Namespace, got.Namespace)
	require.Equal(t, record.SessionID, got.SessionID)
	require.Equal(t, record.ProviderMicroVMID, got.ProviderMicroVMID)
	require.Equal(t, record.Metadata["source_of_truth"], got.Metadata["source_of_truth"])
	require.Equal(t, record.TokenMetadata[0].TokenID, got.TokenMetadata[0].TokenID)

	payload, err := json.Marshal(item)
	require.NoError(t, err)
	jsonText := string(payload)
	require.Contains(t, jsonText, `"instance_slug":"demo"`)
	require.Contains(t, jsonText, `"tenant_id":"slug:demo"`)
	require.NotContains(t, jsonText, "HOSTED_GENESIS_MICROVM#INSTANCE#demo")
	require.NotContains(t, jsonText, `"PK"`)
	require.NotContains(t, jsonText, `"SK"`)
	require.NotContains(t, jsonText, `"ttl"`)
}

func TestHostedGenesisMicroVMExecutionRejectsUnboundTenant(t *testing.T) {
	t.Parallel()

	record := validHostedGenesisMicroVMExecutionSessionRecord()
	record.TenantID = "account:demo"
	_, err := NewHostedGenesisMicroVMExecutionFromSessionRecord(record)
	require.Error(t, err)

	record = validHostedGenesisMicroVMExecutionSessionRecord()
	record.Namespace = "other"
	_, err = NewHostedGenesisMicroVMExecutionFromSessionRecord(record)
	require.Error(t, err)
}

func TestHostedGenesisMicroVMExecutionUsesCamelCaseTheoryDBTags(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(HostedGenesisMicroVMExecution{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("theorydb")
		require.NotContains(t, tag, "tenant_id", "field %s must not use AppTheory's generic snake_case table tag", field.Name)
		require.NotContains(t, tag, "session_id", "field %s must not use AppTheory's generic snake_case table tag", field.Name)
		require.NotContains(t, tag, "desired_state", "field %s must not use AppTheory's generic snake_case table tag", field.Name)
		require.NotContains(t, tag, "provider_microvm_id", "field %s must not use AppTheory's generic snake_case table tag", field.Name)
	}
}

func TestHostedGenesisMicroVMExecutionModelDoesNotPersistRawSecretValues(t *testing.T) {
	t.Parallel()

	record := validHostedGenesisMicroVMExecutionSessionRecord()
	item, err := NewHostedGenesisMicroVMExecutionFromSessionRecord(record)
	require.NoError(t, err)
	payload, err := json.Marshal(item)
	require.NoError(t, err)
	jsonText := strings.ToLower(string(payload))
	for _, forbiddenValue := range []string{
		"bearer lab-token",
		"x-aws-proxy-auth",
		"test-proxy-token",
		"sk-live-provider-secret",
		"instance-api-key",
		"wallet-signature",
		"aws_access_key_id",
		"raw transcript",
	} {
		require.NotContains(t, jsonText, forbiddenValue)
	}
}

func TestHostedGenesisMicroVMExecutionUpdateKeysNormalizesSemanticFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 14, 0, 0, 0, time.FixedZone("offset", -4*60*60))
	item := &HostedGenesisMicroVMExecution{
		InstanceSlug:                " Demo ",
		Namespace:                   " hosted-genesis ",
		SessionID:                   " conv_123 ",
		State:                       runtimemicrovm.LifecycleState(" running "),
		DesiredState:                runtimemicrovm.LifecycleState(" running "),
		Endpoint:                    " https://microvm.example.internal/session/conv_123 ",
		MicroVMID:                   " microvm-000001 ",
		ProviderID:                  " aws-lambda-microvm ",
		ProviderMicroVMID:           " microvm-000001 ",
		ProviderState:               " running ",
		AWSLifecycleState:           " running ",
		ImageRef:                    " image-ref ",
		ImageVersion:                " 1 ",
		NetworkConnectorRef:         " egress-ref ",
		IngressNetworkConnectorRefs: []string{" ingress-ref ", "", "  "},
		EgressNetworkConnectorRefs:  []string{" egress-ref "},
		ControllerID:                " controller ",
		CreatedAt:                   now,
		UpdatedAt:                   now.Add(time.Minute),
		LastObservedAt:              now.Add(2 * time.Minute),
		ProviderStartedAt:           now.Add(3 * time.Minute),
		ProviderTerminatedAt:        now.Add(4 * time.Minute),
		ExpiresAt:                   now.Add(time.Hour),
		LastAction:                  runtimemicrovm.Command(" run "),
		LastCommandID:               " req_123 ",
		AuthSubject:                 " subject ",
		ReasonMetadata:              map[string]string{" reason ": " hosted-genesis ", "empty": ""},
		StatusMetadata:              map[string]string{" status ": " running "},
		TokenMetadata: []runtimemicrovm.SessionTokenMetadata{
			{},
			{
				TokenID:   " grant_123 ",
				TokenType: " scope ",
				ExpiresAt: now.Add(5 * time.Minute),
				Scope:     []string{" port:443 ", ""},
			},
		},
		Metadata: map[string]string{" source_of_truth ": " hosted_genesis_session ", "drop": " "},
	}

	require.NoError(t, item.UpdateKeys())
	require.Equal(t, "demo", item.InstanceSlug)
	require.Equal(t, "slug:demo", item.TenantID)
	require.Equal(t, hostedgenesis.MicroVMNamespace, item.Namespace)
	require.Equal(t, "conv_123", item.SessionID)
	require.Equal(t, "HOSTED_GENESIS_MICROVM#INSTANCE#demo#NAMESPACE#hosted-genesis", item.PK)
	require.Equal(t, "SESSION#conv_123", item.SK)
	require.Equal(t, item.ExpiresAt.Unix(), item.TTL)
	require.Equal(t, []string{"ingress-ref"}, item.IngressNetworkConnectorRefs)
	require.Equal(t, map[string]string{"reason": "hosted-genesis"}, item.ReasonMetadata)
	require.Equal(t, map[string]string{"source_of_truth": "hosted_genesis_session"}, item.Metadata)
	require.Len(t, item.TokenMetadata, 1)
	require.Equal(t, "grant_123", item.TokenMetadata[0].TokenID)
	require.Equal(t, []string{"port:443"}, item.TokenMetadata[0].Scope)
	require.Equal(t, time.UTC, item.CreatedAt.Location())
	require.Equal(t, time.UTC, item.ProviderTerminatedAt.Location())
}

func TestHostedGenesisMicroVMExecutionValidationBranches(t *testing.T) {
	t.Parallel()

	var item *HostedGenesisMicroVMExecution
	require.Error(t, item.UpdateKeys())
	_, err := item.ToSessionRecord()
	require.Error(t, err)

	item = &HostedGenesisMicroVMExecution{InstanceSlug: "demo", TenantID: "slug:other", Namespace: hostedgenesis.MicroVMNamespace, SessionID: "conv_123"}
	require.Error(t, item.BeforeCreate())
	require.Error(t, item.BeforeUpdate())

	item = &HostedGenesisMicroVMExecution{InstanceSlug: "demo", Namespace: hostedgenesis.MicroVMNamespace, SessionID: "conv_123"}
	require.Error(t, item.BeforeCreate())

	record := validHostedGenesisMicroVMExecutionSessionRecord()
	record.SessionID = " "
	_, err = NewHostedGenesisMicroVMExecutionFromSessionRecord(record)
	require.Error(t, err)

	require.Equal(t, "", HostedGenesisMicroVMExecutionSlugFromTenantID("account:demo"))
	require.Equal(t, "demo", HostedGenesisMicroVMExecutionSlugFromTenantID(" slug:Demo "))
	require.Equal(t, "HOSTED_GENESIS_MICROVM#INSTANCE#demo#NAMESPACE#hosted-genesis", HostedGenesisMicroVMExecutionPK(" Demo ", " hosted-genesis "))
	require.Equal(t, "SESSION#conv_123", HostedGenesisMicroVMExecutionSK(" conv_123 "))
}

func validHostedGenesisMicroVMExecutionSessionRecord() runtimemicrovm.SessionRecord {
	now := time.Date(2026, 7, 7, 14, 0, 0, 0, time.UTC)
	return runtimemicrovm.SessionRecord{
		TenantID:                    "slug:demo",
		Namespace:                   hostedgenesis.MicroVMNamespace,
		SessionID:                   "conv_123",
		State:                       runtimemicrovm.StateRunning,
		DesiredState:                runtimemicrovm.StateRunning,
		Endpoint:                    "https://microvm.example.internal/session/conv_123",
		MicroVMID:                   "microvm-000001",
		ProviderID:                  runtimemicrovm.AWSLambdaMicroVMProviderID,
		ProviderMicroVMID:           "microvm-000001",
		ProviderState:               string(runtimemicrovm.StateRunning),
		AWSLifecycleState:           string(runtimemicrovm.StateRunning),
		ImageRef:                    "arn:aws:lambda:us-east-1:123456789012:microvm-image:hosted-genesis",
		ImageVersion:                "1",
		NetworkConnectorRef:         "egress-ref",
		IngressNetworkConnectorRefs: []string{"ingress-ref"},
		EgressNetworkConnectorRefs:  []string{"egress-ref"},
		ControllerID:                hostedgenesis.MicroVMControllerID,
		CreatedAt:                   now,
		UpdatedAt:                   now.Add(time.Minute),
		LastObservedAt:              now.Add(time.Minute),
		ProviderStartedAt:           now.Add(10 * time.Second),
		ExpiresAt:                   now.Add(time.Hour),
		Generation:                  3,
		LastAction:                  runtimemicrovm.CommandRun,
		LastCommandID:               "req_123",
		AuthSubject:                 hostedgenesis.MicroVMAuthSubject,
		ReasonMetadata:              map[string]string{"reason": "hosted-genesis"},
		StatusMetadata:              map[string]string{"status": "running"},
		TokenMetadata: []runtimemicrovm.SessionTokenMetadata{{
			TokenID:   "grant_123",
			TokenType: "microvm-port-scope",
			ExpiresAt: now.Add(5 * time.Minute),
			Scope:     []string{"port:443"},
		}},
		Metadata: map[string]string{
			"source_of_truth": hostedgenesis.MicroVMSourceOfTruth,
			"registration_id": "reg_123",
			"agent_id":        "agent_123",
			"conversation_id": "conv_123",
			"turn_id":         "turn_123",
		},
	}
}
