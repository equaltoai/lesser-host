# Managed release certification

This document defines the certification gate for **release-driven managed deploys** in `lesser-host`.

It exists because implementation-complete does not mean rollout-ready. A Lesser release is only considered safe for
managed rollout once the release has produced the evidence described here through the hosted `lesser-host` path.

## Scope

Managed release certification covers the real hosted workflow:

1. trigger a managed Lesser update from a published Lesser release tag
2. observe the update through `lesser-host`
3. record runner visibility, receipts, and terminal status
4. optionally extend the same run through `lesser-body` deploy and MCP follow-on wiring
5. derive a rollout-readiness result from the recorded evidence

This is the boundary that `M9` uses for project and rollout decisions.

## Certification checklist

The managed release is only certified when every required check passes.

### Core checks

- `compatibility_contract_valid`
  - the requested Lesser release matches the published `lesser-host` compatibility contract before rollout
- `hosted_update_started`
  - `lesser-host` accepted the update request and returned a concrete job id
- `hosted_update_completed`
  - the managed Lesser update reached terminal `status=ok`
- `runner_visibility_present`
  - the update preserved at least one operator-visible deep link (`run_url` or phase-specific run URLs)
- `receipt_key_defined`
  - the certification report recorded the canonical managed receipt key for the Lesser phase
- `retry_visibility_present`
  - if the run fails, the report still preserves `error_code`, `error_message`, `failed_phase`, and run-link evidence

### Optional follow-on checks

- `lesser_body_completed`
  - required when the certification run includes `lesser-body`
- `lesser_body_receipt_key_defined`
  - required when the certification run includes `lesser-body`
- `mcp_wiring_completed`
  - required when the certification run includes MCP follow-on wiring
- `mcp_receipt_key_defined`
  - required when the certification run includes MCP follow-on wiring

## Canonical evidence surface

Certification evidence is stored as a bundle under `gov-infra/evidence/managed-release-certification/`.

Required files:

- `managed-release-certification.json`
  - machine-readable certification report
- `managed-release-certification.md`
  - operator-readable summary for release and project decisions

Recommended companion artifacts:

- raw API request/response captures if needed for debugging
- workflow step summaries
- GitHub Actions artifact uploads for the full evidence directory

## Machine-readable certification report

The canonical report schema lives at:

- `docs/spec/managed-release-certification.schema.json`

An example report lives at:

- `docs/spec/managed-release-certification.example.json`

The report records:

- requested Lesser and `lesser-body` versions
- target `lesser-host` base URL and managed instance slug
- every certification check and its pass/fail status
- job-level evidence for Lesser, `lesser-body`, and MCP phases
- canonical managed receipt keys
- rollout summary (`overall_status`)

## Canonical managed receipt keys

Certification reports record canonical receipt keys under the managed update prefix:

- Lesser: `managed/updates/<slug>/<jobId>/state.json`
- `lesser-body`: `managed/updates/<slug>/<jobId>/body-state.json`
- MCP: `managed/updates/<slug>/<jobId>/mcp-state.json`

If a phase fails before its receipt exists, the report must still preserve the missing-receipt outcome together with the
job’s failure details and deep links.

## Rollout rule

A release is considered **managed-rollout ready** only when:

- the compatibility contract passes
- the requested hosted phases complete successfully
- the report’s `overall_status` is `pass`

Anything else is rollout-blocking evidence, not an informal “probably okay”.
