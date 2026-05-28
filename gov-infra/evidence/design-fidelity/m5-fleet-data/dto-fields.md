# M5 Fleet Data — DTO Field Documentation

**Branch**: `aron/portal-m5-fleet-data`
**Concern**: Backend/data only. Adds Fleet data fields to `instanceResponse` DTO.
**M4 Fleet UI** owns rendering.

## Added JSON Fields

All fields are additive (`omitempty`). Existing `portalListInstances` consumers
decode without error because standard JSON decoders ignore unknown fields.

| JSON field          | Go type   | Zero value      | Source                          |
|---------------------|-----------|-----------------|---------------------------------|
| `active_users_30d`  | `int64`   | `0`             | Managed Lesser metrics endpoint |
| `posts_24h`         | `int64`   | `0`             | Not yet counterized             |
| `sig_fails_24h`     | `int64`   | `0`             | Not yet counterized             |
| `spark_activity`    | `[]int64` | `null` (omitted)| UsageLedgerEntry aggregation    |
| `spark_cost`        | `[]float64`|`null` (omitted)| CostTelemetry table             |
| `peers`             | `int64`   | `0`             | Not yet counterized             |
| `severed`           | `int64`   | `0`             | Not yet counterized             |

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

## Data Availability Notes

### spark_cost (7-day cost sparkline)
- **Source**: `CostTelemetry` table (host-local DynamoDB)
- **Computed by**: `fleetEnrichSparklines` in `handlers_portal_fleet.go`
- **Behavior**: Queries `ListCostTelemetryByInstance` for last 7 days. Dates with
  no data are zero. Failures are silent (field stays `null`).

### spark_activity (7-day activity sparkline)
- **Source**: `UsageLedgerEntry` table aggregation (host-local DynamoDB)
- **Computed by**: `fleetAggregateDailyActivity` in `handlers_portal_fleet.go`
- **Behavior**: Counts `UsageLedgerEntry` rows per day for the last 7 days. A
  7-day window spans at most 2 months (at most 2 DynamoDB queries per instance).
  Failures are silent.

### active_users_30d
- **Status**: Returns `0` in list endpoint. Requires the managed Lesser instance
  metrics endpoint (`/api/v1/instance/metrics/daily` → `UniqueUsers` field).
- **Future**: Can be populated when the per-instance metrics endpoint is called.

### posts_24h, sig_fails_24h, peers, severed
- **Status**: Returns `0`. No existing counter tracks these metrics.
- **Future**: Require new data pipelines or managed instance instrumentation.

## Cross-Tenant Isolation

- `handlePortalListInstances` queries GSI1 with PK = `OWNER#<username>`, scoping
  all results to the authenticated customer.
- `fleetEnrichSparklines` queries CostTelemetry by exact slug match
  (`COST_TELEMETRY#<slug>`), not by owner or cross-tenant index.
- `fleetAggregateDailyActivity` queries UsageLedgerEntry by exact slug in PK
  (`USAGE#<slug>#<month>`).

No cross-tenant aggregation or identifier leakage is possible.

## Backward Compatibility

- All new fields use `omitempty` JSON tags.
- When zero/nil, fields are omitted from the serialized response.
- Existing consumers that `JSON.parse` the list response see the same fields
  they always have.
- Verified by `TestFleetDataDTOBackwardCompatibility`.

## Test Coverage

| Test                                    | What it verifies                           |
|-----------------------------------------|--------------------------------------------|
| `TestFleetDataDTOBackwardCompatibility` | Zero fields omitted, existing fields present|
| `TestFleetDataDTORedactionProof`        | No PK/SK/account_id/secrets in output      |
| `TestFleetDataEmptyMetricsReturnsZeroValues` | All fields zero when no data          |
| `TestFleetEnrichSparklinesWithCostTelemetry` | sparkCost populated from CostTelemetry|
| `TestFleetEnrichSparklinesNilServer`    | No panic on nil server/store/response      |
| `TestFleetAggregateDailyActivity`       | Activity counts from UsageLedgerEntry      |
| `TestFleetAggregateDailyActivityStoreNotAvailable` | Error on nil store           |
| `TestHandlePortalListInstancesFleetFieldsPresent` | Additive DTO in list response |
| `TestHandlePortalListInstancesCrossTenantIsolation` | No cross-owner leak          |
| `TestHandlePortalListInstancesUnauthenticated` | Auth required                  |
| `TestHandlePortalListInstancesStoreNotInitialized` | Error on nil store          |
| `TestHandlePortalListInstancesQueryFailure` | Error on DB failure               |
