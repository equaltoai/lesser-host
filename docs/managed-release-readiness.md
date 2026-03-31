# Managed Release Readiness

This document defines how project `17` distinguishes **implementation complete** from **managed-release certified**.

## Canonical readiness inputs

Readiness is derived from the certification bundle produced by:

- `docs/managed-release-certification.md`
- `gov-infra/evidence/managed-release-certification/managed-release-certification.json`
- `gov-infra/evidence/managed-release-certification/managed-release-certification-lesser-body.json` when `run_lesser_body=true`

The derived readiness bundle is written alongside it:

- `gov-infra/evidence/managed-release-certification/managed-release-readiness.json`
- `gov-infra/evidence/managed-release-certification/managed-release-readiness.md`

## Readiness states

- `certified`
  - the certification report passed
  - rollout readiness is `ready`
- `blocked`
  - one or more certification checks failed
  - rollout readiness is `blocked`

Blocking readiness is driven by explicit failed certification checks, not by whether implementation issues have merged.

## Project 17 issue surface

Project `17` readiness is surfaced on the parent milestone issues through labels and a marker comment:

- `managed-release-certified`
- `managed-release-blocked`

The readiness sync updates those labels on the configured parent issues and maintains a `<!-- managed-release-readiness -->`
comment with:

- project number
- requested Lesser and `lesser-body` versions
- `lesser-body` certification status from the canonical body evidence bundle
- current certification status
- rollout readiness
- blocking certification checks

That makes the project distinguish:

- merged implementation work
- readiness blocked by failed managed certification
- readiness cleared for rollout

## Cross-repo rollout blocking

The managed release canary workflow can target multiple parent issues, for example:

- `equaltoai/lesser#658`
- `equaltoai/lesser-body#91`
- `equaltoai/lesser-host#96`

When certification is blocked:

- `managed-release-blocked` is applied
- `managed-release-certified` is removed
- the readiness comment is updated with the failed checks

When certification passes:

- `managed-release-certified` is applied
- `managed-release-blocked` is removed
- the readiness comment is updated to show rollout readiness is `ready`

This is the canonical feedback loop for project `17`. Manual project interpretation should follow the readiness labels and
comment, not just whether implementation PRs have merged.
