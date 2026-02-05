# Lesser release contract (consumed by lesser.host)

This document defines the **release contract** that `lesser.host` uses to deploy **managed** Lesser instances, and that
operators use for **independent** (self-managed) Lesser installs.

This repo (`lesser-host`) **does not publish Lesser releases**. It consumes releases produced by the `lesser` repository
via **GitHub Releases** (no NPM).

## Goals

- Define what “a Lesser release” contains (assets + metadata) so automation can deploy deterministically.
- Define stable, machine-readable **deployment outputs** so `lesser.host` can register and manage instances.
- Support two install modes:
  - **Independent install:** user runs `lesser up` in their own AWS account(s).
  - **Managed install:** `lesser.host` provisions AWS accounts and deploys Lesser on the user’s behalf.

## Non-goals (v1)

- Defining a full UI or portal flow (tracked separately in the `lesser.host` roadmap).
- Defining payment/billing integration.
- Requiring container images (allowed, but not required in v1).

## Versioning

- **Tag format:** `vMAJOR.MINOR.PATCH` (semver).
- **GitHub Release:** MUST be created for the tag (draft or prerelease is allowed, but must include required assets).

## Required release assets

Every GitHub Release MUST publish:

1) **CLI binaries**
   - `lesser-linux-amd64`
   - `lesser-linux-arm64`
   - (optional) `lesser-darwin-amd64`, `lesser-darwin-arm64`

2) **Checksums**
   - `checksums.txt` containing `sha256` for each published binary asset.

3) **Release manifest**
   - `lesser-release.json` with this shape:

```json
{
  "schema": 1,
  "name": "lesser",
  "version": "v1.2.3",
  "git_sha": "abcdef123...",
  "go_version": "1.25.6",
  "cdk": {
    "major": 2
  },
  "artifacts": {
    "receipt_schema_version": 1
  }
}
```

Notes:
- `cdk.major` is informational; the deploy runner may still use whatever CDK v2 is installed.
- `receipt_schema_version` refers to the JSON schema written by `lesser up` (see “Deployment outputs”).

## Deployment inputs (CLI contract)

For a release to be “deployable”, the CLI MUST support:

- `lesser up --app <slug> --base-domain <domain> --aws-profile <profile> [--with-staging] [--out <path>] [--rebuild-lambdas]`

Constraints:
- `--app` MUST be a lowercase slug (e.g., `my-lesser`).
- `--base-domain` MUST have an existing **public Route53 hosted zone** in the *target account*.
- `--aws-profile` MUST select AWS credentials and region.

### Managed provisioning notes (AWS credentials)

`lesser up` currently expects an AWS CLI profile. In managed mode, `lesser.host` SHOULD:

- assume a role in the target account (STS), then
- write a **temporary** AWS CLI profile with the session credentials, and
- run `lesser up` against that profile.

This avoids requiring user-owned AWS credentials.

## Deployment outputs (receipt contract)

After a successful `lesser up`, the CLI MUST write a local receipt at:

- `~/.lesser/<app>/<base-domain>/state.json`

The receipt MUST be JSON with:
- top-level `version` (currently `1`)
- `account_id`, `region`
- `hosted_zone: { id, name }`
- `shared_stack`, and stage stacks under `stages.{dev,live,staging?}` including:
  - `stack_name`
  - `domain`
  - `table_name`
  - `bootstrap_address`
  - `urls` (service URLs)

`lesser.host` treats this receipt as the source of truth for registering the deployed instance endpoint(s).

## DNS model for managed installs (greater.website)

Managed installs MUST NOT require the user to own an AWS account, but **may** require the platform (EqualTo AI) to manage
DNS under `greater.website`.

For v1 managed installs, the recommended DNS model is:

- Central account owns the Route53 hosted zone for `greater.website`.
- Each managed instance is deployed into a **dedicated AWS account** with its own public hosted zone:
  - `slug.greater.website`
- The central `greater.website` zone delegates `slug.greater.website` via an **NS record** created during provisioning.

This allows Lesser’s existing CDK stacks to create certificates and records **within the instance account** without
cross-account Route53 writes (other than the one-time NS delegation).

## Known gaps / follow-ups (may require Lesser changes)

These are tracked as contract gaps for managed deployments:

1) **Destroy / deprovision command**
   - The CLI does not currently expose `lesser down` / `lesser destroy`.
   - Managed hosting needs deterministic teardown for failed provisions and account recycling.

2) **Bootstrap wallet UX**
   - On first deploy, Lesser can generate a bootstrap mnemonic which is awkward for true self-serve.
   - Managed hosting likely needs a way to set the bootstrap wallet to the requester’s wallet at provision time.

3) **Non-profile credential support**
   - Native support for ambient AWS credentials (no AWS CLI profile) would simplify managed automation.

