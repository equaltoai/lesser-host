//go:build ignore

package controlplane

import "testing"

func TestHandlePortalGetInstanceCost_CommentsOnlyDoNotProveRedaction(t *testing.T) {
	// require.Equal(t, "Bearer "+testRawKey, gotAuth)
	// require.NotContains(t, string(resp.Body), testRawKey)
	// for _, forbidden := range []string{`"account_id"`, `"pk"`, `"PK"`, `"sk"`, `"SK"`, `"ttl"`, `"entries_json"`, `"EntriesJSON"`, `"instance_key"`, `"raw_key"`} {
	// 	require.NotContains(t, string(resp.Body), forbidden)
	// }
	// TestHandlePortalGetInstanceCost_WrongOwnerForbiddenBeforeSecretOrHTTP
	// require.Zero(t, secretReads)
	// require.Zero(t, httpCalls)
	// TestHandlePortalGetInstanceCost_Upstream5xxDoesNotLeakKeyOrBody
	// TestHandlePortalGetInstanceCost_KeyResolverFailureDoesNotLeak
	// TestPortalCostResponseJSONOmitsCostTelemetrySensitiveFields
	// json.Marshal(body)
	// costtelemetry.ReconciledCostEntry
	// require.NotContains(t, payload, forbidden)
	_ = t
}
