# M5 Fleet Data — DTO Field Documentation

**Branch**: `aron/portal-m5-fleet-data`
**Concern**: Backend/data only. Adds Fleet data fields to `instanceResponse` DTO.
**M4 Fleet UI** owns rendering.

## Added JSON Fields

All fields are additive (`omitempty`). Existing `portalListInstances` consumers
decode without error because standard JSON decoders ignore unknown fields.

| JSON field          | Go type   | Zero value      | Source                                                 |
|---------------------|-----------|-----------------|--------------------------------------------------------|
| `active_users_30d`  | `int64`   | `0`             | Managed Lesser `/api/v1/instance/metrics/daily` (max daily `unique_users` over 30-day window) |
| `posts_24h`         | `int64`   | `0`             | Not yet counterized                                    |
| `sig_fails_24h`     | `int64`   | `0`             | Not yet counterized                                    |
| `spark_activity`    | `[]int64` | `null` (omitted)| Managed Lesser `/api/v1/instance/metrics/daily` (`total_requests` per day, last 7 days) |
| `spark_cost`        | `[]float64`|`null` (omitted)| Managed Lesser `/api/v1/instance/metrics/daily` (`cost_dollars` per day, last 7 days; cents fallback when dollars=0) |
| `peers`             | `int64`   | `0`             | Not yet counterized                                    |
| `severed`           | `int64`   | `0`             | Not yet counterized                                    |

## Redaction Guarantees

The following are **never** present in marshalled `instanceResponse`:

- `PK`, `SK`, `TTL`, `ttl` — DynamoDB internal keys
- `account_id`, `AccountID` — AWS account identifiers
- `EntriesJSON`, `entries_json`, `entriesJson` — raw telemetry JSON blob
- `SecretString`, `SecretBinary` — AWS Secrets Manager values
- `raw_key`, `plaintext` — raw API key material
- `PAN`, `CVV` — payment card data
- `gsipk`, `gsi1pk`, `gsi1PK` — GSI partition keys (cross-tenant patterns)
- `OWNER#<username>` — cross-tenant PK pattern

Verified by `TestFleetDataDTORedactionProof` in `handlers_portal_fleet_internal_test.go`.

## Data Source and Semantics

### managed Lesser metrics endpoint

All computed Fleet fields (`active_users_30d`, `spark_activity`, `spark_cost`)
are sourced from the managed Lesser instance metrics endpoint
(`/api/v1/instance/metrics/daily`). This path has been available since
M0.4/#529 and is the same path used by the per-instance cost endpoint
(`GET /api/v1/portal/instances/{slug}/cost`).

The enrichment flow:
1. `handlePortalListInstances` scopes instances to the authenticated owner via
   GSI1 (`OWNER#<username>`) — this is the ownership gate.
2. For each owned instance, `fleetEnrichFromManagedMetrics` resolves the
   instance API key via `resolvePortalCostInstanceKey` (same no-log/no-browser
   secret handling used by the cost endpoint).
3. `fetchManagedInstanceMetrics` calls the managed Lesser with a 30-day window
   using the resolved key as a Bearer token.
4. Fleet fields are computed from the daily metrics response.

### active_users_30d

- **Computation**: `max(daily.unique_users)` over the 30-day window.
- **Rationale**: `unique_users` is a per-day count. Summing would double-count
  users who are active on multiple days. Taking the maximum is the
  least-misleading single-value summary available from the daily metrics
  endpoint. This is documented in the code and in this evidence file.
- **When unavailable**: Returns `0`. This happens when the managed instance
  has no metrics yet (new instance), when the instance key has not been
  provisioned, or when the metrics endpoint is unreachable.

### spark_activity (7-day activity sparkline)

- **Source field**: `daily[].total_requests` from the managed Lesser metrics.
- **Computation**: Last 7 days of `total_requests`, ordered oldest→newest.
  Missing days (where the metrics response does not contain a row for that
  date) are zero.
- **When unavailable**: Returns `null` (omitted from JSON). Happens on key
  resolution failure or upstream errors.

### spark_cost (7-day cost sparkline)

- **Source field**: `daily[].cost_dollars` from the managed Lesser metrics.
  When `cost_dollars` is zero and `cost_cents` is non-zero, the cents value
  divided by 100 is used as a fallback.
- **Computation**: Last 7 days of cost, ordered oldest→newest. Missing days
  are zero.
- **When unavailable**: Returns `null` (omitted from JSON). Happens on key
  resolution failure or upstream errors.

### posts_24h, sig_fails_24h, peers, severed

- **Status**: Returns `0`. No existing counter in the managed Lesser metrics
  endpoint tracks these metrics.
- **Future**: Require new instrumented counters on the managed Lesser side
  before these fields can be populated.

## Failure Posture

If key resolution or the managed metrics HTTP call fails for any instance:

- The `handlePortalListInstances` endpoint does **not** return HTTP 500.
- That instance's Fleet fields (`active_users_30d`, `spark_activity`,
  `spark_cost`) remain at their zero values.
- The remaining instances in the list are unaffected.

This is verified by `TestHandlePortalListInstancesMetricsFailureIsSilent`
and `TestFleetEnrichFromManagedMetricsUpstreamFailure`.

## Cross-Tenant Isolation

- `handlePortalListInstances` queries GSI1 with PK = `OWNER#<username>`,
  scoping all results to the authenticated customer **before** any
  enrichment.
- Instance key resolution and managed Lesser HTTP calls happen only for
  the owner-scoped instances.
- No cross-tenant aggregation or identifier leakage is possible.

Verified by `TestHandlePortalListInstancesCrossTenantIsolation`.

## Backward Compatibility

- All new fields use `omitempty` JSON tags.
- When zero/nil, fields are omitted from the serialized response.
- Existing consumers that `JSON.parse` the list response see the same fields
  they always have.
- Verified by `TestFleetDataDTOBackwardCompatibility`.

## Test Coverage

| Test                                                       | What it verifies                                      |
|------------------------------------------------------------|-------------------------------------------------------|
| `TestFleetDataDTOBackwardCompatibility`                    | Zero fields omitted, existing fields present          |
| `TestFleetDataDTORedactionProof`                           | No PK/SK/account_id/secrets in output                 |
| `TestFleetDataEmptyMetricsReturnsZeroValues`               | All fields zero when no data                          |
| `TestFleetEnrichFromManagedMetricsPopulatesFields`         | All three computed fields populated from managed metrics |
| `TestFleetEnrichFromManagedMetricsMaxUsersSemantics`       | active_users_30d uses max, not sum                    |
| `TestFleetEnrichFromManagedMetricsCostCentsFallback`       | CostCents fallback when CostDollars is zero           |
| `TestFleetEnrichFromManagedMetricsKeyResolutionFailure`    | Fields zero on key resolution failure (no panic)      |
| `TestFleetEnrichFromManagedMetricsUpstreamFailure`         | Fields zero on upstream HTTP 503 (no panic)           |
| `TestFleetEnrichFromManagedMetricsNilServer`               | No panic on nil server/instance/response              |
| `TestHandlePortalListInstancesFleetFieldsPresent`          | End-to-end: Fleet fields populated in list response   |
| `TestHandlePortalListInstancesMetricsFailureIsSilent`      | List endpoint returns 200 when metrics upstream fails |
| `TestHandlePortalListInstancesCrossTenantIsolation`        | No cross-owner leak, ownership scoped before enrichment |
| `TestHandlePortalListInstancesUnauthenticated`             | Auth required                                         |
| `TestHandlePortalListInstancesStoreNotInitialized`         | Error on nil store                                    |
| `TestHandlePortalListInstancesQueryFailure`                | Error on DB failure                                   |
