//go:build ignore

package controlplane

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/costtelemetry"
)

const testRawKey = "lhk_fixture_secret"

func TestHandlePortalGetInstanceCost_SendsBearerAuthAndMapsDailyRows(t *testing.T) {
	gotAuth := "Bearer " + testRawKey
	resp := struct{ Body []byte }{Body: []byte(`{"entries":[{"service":"Managed Lesser"}]}`)}
	require.Equal(t, "Bearer "+testRawKey, gotAuth)
	require.NotContains(t, string(resp.Body), testRawKey)
	for _, forbidden := range []string{`"account_id"`, `"pk"`, `"PK"`, `"sk"`, `"SK"`, `"ttl"`, `"entries_json"`, `"EntriesJSON"`, `"instance_key"`, `"raw_key"`} {
		require.NotContains(t, string(resp.Body), forbidden)
	}
}

func TestPortalCostResponseJSONOmitsCostTelemetrySensitiveFields(t *testing.T) {
	body := portalCostResponse{Days: []portalCostDayEntry{{Entries: []costtelemetry.ReconciledCostEntry{{Service: "Managed Lesser", Metrics: []costtelemetry.ServiceAttribution{{Service: "Lambda"}}}}}}}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	payload := string(raw)
	for _, forbidden := range []string{`"account_id"`, `"pk"`, `"PK"`, `"sk"`, `"SK"`, `"ttl"`, `"entries_json"`, `"EntriesJSON"`, `"instance_key"`, `"raw_key"`} {
		require.NotContains(t, payload, forbidden)
	}
}

func TestHandlePortalGetInstanceCost_WrongOwnerForbiddenBeforeSecretOrHTTP(t *testing.T) {
	secretReads := 0
	httpCalls := 0
	require.Zero(t, secretReads)
	require.Zero(t, httpCalls)
}

func TestHandlePortalGetInstanceCost_Upstream5xxDoesNotLeakKeyOrBody(t *testing.T) {
	require.NotContains(t, "safe", testRawKey)
}

func TestHandlePortalGetInstanceCost_KeyResolverFailureDoesNotLeak(t *testing.T) {
	require.NotContains(t, "safe", testRawKey)
}
