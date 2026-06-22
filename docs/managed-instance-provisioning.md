# Managed instance provisioning (M9)

This document describes how `lesser.host` provisions a **managed** Lesser instance for a user with no user-owned AWS
account.

It assumes:
- `lesser.host` runs in (or can act with permissions of) an AWS Organizations management/delegated admin account.
- `greater.website` is operated by EqualTo AI and has a parent Route53 hosted zone in a central account.
- Each managed instance is deployed into a **dedicated AWS account** for quota isolation and blast-radius reduction.

## Terminology

- **control plane**: this repo (`lesser-host`) powering `lesser.host`.
- **instance account**: the AWS account dedicated to a single managed Lesser instance.
- **parent zone**: the Route53 hosted zone for `greater.website` in the central account.
- **child zone**: a Route53 hosted zone for `slug.greater.website` created in the instance account.

## High-level flow

1) **Request**
   - user/operator requests a new instance slug (e.g., `alice`) from `lesser.host`.

2) **Allocate account**
   - create a new AWS account in the org (or allocate from an account pool).
   - ensure `lesser.host` can assume a provisioning role into the new account.
   - for manually adopted accounts, validate the account ID, expected account email/name, and active Organizations
     membership before persisting it as the instance's hosted account boundary.

3) **Create delegated hosted zone**
   - in the instance account, create a public hosted zone for `slug.greater.website`.
   - capture the returned name servers.

4) **Delegate from `greater.website`**
   - in the central account, create an `NS` record in the parent zone delegating `slug.greater.website` to the child zone.

5) **Deploy Lesser + seed admin**
   - select a Lesser release (see `docs/lesser-release-contract.md`).
   - download and verify the managed Lesser release assets:
     - `checksums.txt`
     - `lesser-release.json`
     - `lesser-lambda-bundle.tar.gz`
     - `lesser-lambda-bundle.json`
   - build a managed provisioning input file that includes:
     - `slug`
     - `stage`
     - `admin_wallet_address`
     - `admin_username`
     - `admin_wallet_chain_id`
     - `consent_message` + `consent_signature` (see [Structured init-admin consent](#structured-init-admin-consent-lesser-m9))
   - run `lesser up` in the instance account with:
     - `--base-domain <slug.greater.website>`
     - `--aws-profile <temp-profile>` (static session creds)
     - `--provisioning-input <path>`
     - `--release-dir <verified-release-dir>`
   - before CodeBuild starts, the provisioning worker revalidates that the job's slug, account ID, role, region, and
     base domain match the instance metadata; the runner also verifies that its assumed `managed` AWS profile resolves
     to the requested account ID.
   - immediately seed the admin wallet and unlock the instance:
     - `lesser init-admin --base-domain <slug.greater.website> --aws-profile <temp-profile> --provisioning-input <path>`
   - read the deployment receipt `~/.lesser/<app>/<base-domain>/state.json`.

6) **(Optional, default-on) Deploy lesser-body (AgentCore MCP) + wire `POST /mcp/{actor}`**
   - see `docs/lesser-body-release-contract.md` for the canonical managed consumer contract
   - download and verify the managed `lesser-body` release assets:
     - `checksums.txt`
     - `lesser-body-release.json`
     - `lesser-body-deploy.json`
     - `lesser-body.zip`
     - `deploy-lesser-body-from-release.sh`
     - `lesser-body-managed-<stage>.template.json`
   - deploy `lesser-body` (AgentCore-compatible MCP runtime) into the instance account/stage using the
     release-produced helper script and the target account's bootstrapped CDK asset bucket.
   - ensure `lesser-body` writes SSM exports in the instance account:
     - `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`
   - re-run the Lesser stage deploy with the MCP wiring feature enabled, using the verified Lesser Lambda bundle as the
     `lambdaAssetRoot`, so the instance’s API Gateway exposes:
     - `POST https://api.<stageDomain>/mcp/{actor}`
     - `GET  https://api.<stageDomain>/.well-known/mcp.json`
   - for soul comm mailbox tools, `lesser-body` calls host's instance-authenticated mailbox APIs and treats host as the
     source of truth for delivery metadata, bounded content, and read/archive/delete state.

7) **Register with lesser.host**
   - store instance endpoints from the receipt.
   - mint an instance API key for `lesser.host` calls (future: inject into Lesser at deploy time).

8) **Observability + recovery**
   - persist provisioning job status and step-level errors.
   - allow safe retry (idempotent per slug) and clean rollback where possible.

## DNS details (delegation)

Only a single record is required in the parent zone:

- Record name: `slug.greater.website`
- Type: `NS`
- Values: the 4 authoritative name servers returned when creating the child zone.

All other records (A/AAAA/CNAME validation, etc.) are created by the Lesser CDK stacks in the instance account’s child
zone.

## Idempotency rules

- Provisioning is keyed by `slug` and MUST be retry-safe.
- Hosted zone creation:
  - if `slug.greater.website` zone already exists in the instance account, re-use it.
- NS delegation:
  - if the parent zone already delegates `slug.greater.website` to the same name servers, treat as OK.
- Lesser deployment:
  - `lesser up` is expected to be idempotent for an existing deployment (updates stacks).

## Structured init-admin consent (lesser M9)

Managed provisioning seeds the initial administrator through Lesser's `init-admin` command. For Lesser M9 and later,
host signs an exact, compact JSON consent payload and passes that exact string through `provision.json`:

```json
{
  "kind": "lesser.init_admin_consent.v1",
  "instance": "dev.<slug>.<parent-domain>",
  "username": "<admin username>",
  "nonce": "<16-256 printable non-space ASCII>",
  "expires_at": "<RFC3339/RFC3339Nano, future, <=1h>"
}
```

The real signed message is emitted as a single-line JSON string with exactly those fields:

- `kind` is exactly `lesser.init_admin_consent.v1`.
- `instance` is the managed stage domain derived from the control-plane stage and base domain:
  - `lab` / `dev`: `dev.<slug>.<parent-domain>`
  - `staging` / `stage`: `staging.<slug>.<parent-domain>`
  - `live` / `prod` / `production`: `<slug>.<parent-domain>`
- `username` is the normalized requested admin username.
- `nonce` is generated by host and satisfies Lesser's printable, non-space, 16-256 character nonce requirement.
- `expires_at` is generated by host, in UTC, within the one-hour Lesser M9 limit.

The browser wallet signs the exact JSON bytes with EIP-191 `personal_sign`. host stores the same string on the
`ProvisionConsentChallenge`, copies it without whitespace trimming into `ProvisionJob.ConsentMessage`, base64-encodes it
for the CodeBuild runner, writes it into `provision.json`, and invokes:

```bash
lesser init-admin --base-domain <slug.greater.website> --aws-profile <temp-profile> --provisioning-input <path>
```

Do not canonicalize, pretty-print, trim, or reserialize the consent message after signing. Any byte drift invalidates the
wallet signature and can cause Lesser M9 replay protection to reject the admin seed. Treat the raw message and signature
as replayable artifacts while they exist: the provisioning worker fails the job and clears them if `expires_at` is
missing or has passed before the deploy runner starts, and clears them as soon as CodeBuild accepts the deploy runner.

### CORS sequencing for Lesser M9

Lesser M9's browser API defaults should remain instance-origin-first. `API_CORS_ALLOWED_ORIGINS` is only needed if host
provisions browser clients served from origins other than the managed instance origin. The current managed provisioning
path signs the consent message in the `lesser.host` portal and submits the signed artifact through host's control plane;
it does not require loosening the managed Lesser API CORS posture.

### M9 rollout gate

Host M4.5 must merge before host consumes or certifies a Lesser M9 release. After Lesser PR #901 is merged and published,
operators must certify the exact published M9 assets through the normal managed-release certification/readiness path:

1. download the exact GitHub Release assets host will consume;
2. verify `checksums.txt`, `lesser-release.json`, `lesser-lambda-bundle.tar.gz`, and `lesser-lambda-bundle.json`;
3. run the real managed provisioning or managed-update path against those verified assets;
4. prove `lesser init-admin` accepts the structured consent message in lab; and
5. canary a managed instance before live/default-version rollout.

No step in this M9 handoff weakens consumer release verification: checksum mismatches still abort provisioning, and
uncertified assets must not be promoted to default managed rollout.

## Required config (control plane)

The control plane needs (at minimum):
- `MANAGED_PROVISIONING_ENABLED=true` to allow the provisioning worker to run.
- `MANAGED_ORG_VENDING_ROLE_ARN` (optional) role ARN in the Organizations management/delegated admin account that the
  control plane can assume for `organizations:*` and cross-account `sts:AssumeRole` into instance accounts.
  Leave this blank until the org-bootstrap stack outputs a real ARN; example placeholder values are treated as absent
  and omitted from deployable IAM policies.
- `MANAGED_PARENT_DOMAIN` (default: `greater.website`)
- `MANAGED_PARENT_HOSTED_ZONE_ID` (central account Route53 hosted zone id for `greater.website`)
- `MANAGED_INSTANCE_ROLE_NAME` (default: `OrganizationAccountAccessRole`)
- `MANAGED_TARGET_OU_ID` (optional; move instance accounts into this OU)
- `MANAGED_ACCOUNT_EMAIL_TEMPLATE` (required for account vending; example: `lesser+{slug}@example.com`)
- `MANAGED_ACCOUNT_NAME_PREFIX` (default: `lesser-`)
- `MANAGED_DEFAULT_REGION` (default: `AWS_REGION` or `us-east-1`)
- `MANAGED_LESSER_DEFAULT_VERSION` (optional release tag or `latest`; used when the request doesn’t specify one)
- `MANAGED_PROVISION_RUNNER_PROJECT_NAME` (CodeBuild project name used to run `lesser up`)
- `MANAGED_PROVISION_RUNNER_ROLE_ARN` (CodeBuild service role ARN; provision worker ensures the per-instance role trust
  policy allows this principal before invoking the runner)
- `ARTIFACT_BUCKET_NAME` (S3 bucket where the runner writes receipts)
- `MANAGED_LESSER_GITHUB_OWNER` (default: `equaltoai`)
- `MANAGED_LESSER_GITHUB_REPO` (default: `lesser`)
- `MANAGED_LESSER_GITHUB_TOKEN_SSM_PARAM` (optional; SecureString SSM parameter containing a GitHub token)
- `MANAGED_LESSER_BODY_DEFAULT_VERSION` (optional release tag or `latest` for `lesser-body`; used when the request doesn’t specify one)
- `MANAGED_LESSER_BODY_GITHUB_OWNER` (default: `equaltoai`)
- `MANAGED_LESSER_BODY_GITHUB_REPO` (default: `lesser-body`)

Infra is expected to provide:
- `PROVISION_QUEUE_URL` (SQS queue that drives the async pipeline)

## Receipt + bootstrap outputs

The deploy runner writes the Lesser receipt to S3 so the provisioning worker can ingest it and update instance state:

- Receipt: `s3://$ARTIFACT_BUCKET_NAME/managed/provisioning/<slug>/<jobId>/state.json`
- Lesser-body receipt (optional): `s3://$ARTIFACT_BUCKET_NAME/managed/provisioning/<slug>/<jobId>/body-state.json`
- MCP wiring receipt (optional): `s3://$ARTIFACT_BUCKET_NAME/managed/provisioning/<slug>/<jobId>/mcp-state.json`
- Bootstrap key material (legacy only): `s3://$ARTIFACT_BUCKET_NAME/managed/provisioning/<slug>/bootstrap.json`

Notes:
- Managed provisioning now seeds the admin wallet via `init-admin`, so a bootstrap mnemonic should not be generated.
- If a legacy bootstrap file exists, treat it as sensitive and rotate/delete it after migration.
- The S3-managed receipts preserve the native Lesser/body/MCP fields and add host-side artifact provenance at
  `managed_deploy_artifacts`.

### Managed receipt provenance

Artifact-driven managed receipts add:

- `managed_deploy_artifacts.mode`
- `managed_deploy_artifacts.checksums_path`
- `managed_deploy_artifacts.release_manifest_path`
- `managed_deploy_artifacts.release.{name,version,git_sha}`
- `managed_deploy_artifacts.deploy_artifact.kind`
- `managed_deploy_artifacts.deploy_artifact.path`
- `managed_deploy_artifacts.deploy_artifact.manifest_path`

Mode-specific additions:

- Lesser + MCP receipts:
  - `managed_deploy_artifacts.deploy_artifact.files`
  - `managed_deploy_artifacts.deploy_artifact.prepared_at`
- lesser-body receipts:
  - `managed_deploy_artifacts.release.source_checkout_required=false`
  - `managed_deploy_artifacts.release.npm_install_required=false`
  - `managed_deploy_artifacts.deploy_artifact.script_path`
  - `managed_deploy_artifacts.deploy_artifact.template_path`

## SSM contract (instance account)

Managed provisioning relies on **SSM Parameter Store** for cross-stack wiring inside the instance account (no CloudFormation
exports/imports).

Required parameters (well-known names; `${app}` = instance slug/app name, `${stage}` = `dev|staging|live`):

From Lesser (inputs for `lesser-body`):
- `/${app}/${stage}/lesser/exports/v1/table_name`
- `/${app}/${stage}/lesser/exports/v1/domain`

From `lesser-body` (inputs for Lesser API Gateway `/mcp/{actor}` wiring):
- `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`

## Instance key secret stage aliases

The instance-account Secrets Manager secret for host instance-auth keys is named by the normalized control-plane stage
(`lab/<slug>/instance-key`, `live/<slug>/instance-key`, etc.) and must carry host-managed tags for slug, key ID,
managed status, and control-plane stage. The live fail-closed rule treats `live`, `prod`, and `production` as the same
live stage: legacy untagged ARNs are refused for all three aliases instead of being ignored and replaced.

## Soul comm mailbox migration

Managed provisioning does not move mailbox authority into the tenant account. Host mints and stores the instance API key
hash during registration; body uses the raw instance-side token at runtime to call host's mailbox APIs. The boundary is:

- host owns canonical mailbox rows, bounded encrypted content, provider status, and read/archive/delete state
- lesser receives notification projections for UX/activity only
- body exposes MCP tools over host and must not store durable mailbox truth locally

See `docs/soul-comm-mailbox-migration.md` for backward compatibility, rate-limit, auth, audit, and projection details.

## Smoke test (MCP)

Once provisioning completes with `body_enabled=true`, validate the instance MCP endpoint:

1) Initialize (JSON-RPC):
```bash
curl -sSfL -X POST "https://api.<stageDomain>/mcp/<actor>" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
```

2) Expect:
- HTTP `200`
- a valid JSON-RPC response containing `result`
- an `mcp-session-id` header

## Org bootstrap (required)

Managed provisioning requires a dedicated **org vending role** in the AWS Organizations management/delegated admin
account. This repo now ships a small **org bootstrap CDK app** that creates/updates that role and its required policy.

**Deploy once in the org account:**

```bash
	cd cdk
	AWS_PROFILE=<org-admin-profile> npx cdk deploy --app "npx ts-node --prefer-ts-exts bin/org-bootstrap.ts" \
	  -c orgBootstrapControlPlaneAccountId=<CONTROL_PLANE_ACCOUNT_ID> \
	  -c managedOrgVendingRoleName=lesser-host-org-vending \
	  -c orgBootstrapStackName=lesser-host-org-bootstrap
	```

This stack creates the `lesser-host-org-vending` role with permissions for:
- `organizations:CreateAccount`
- `organizations:DescribeCreateAccountStatus`
- `organizations:ListAccounts`
- `organizations:ListParents`
- `organizations:MoveAccount`

The control plane assumes this role when performing org-level operations.

## Recovery: adopt existing account

If AWS account creation succeeds but the provisioning job fails before the account is attached, operators can recover
without creating a second account:

1) Open the provisioning job in the operator console.
2) Use **Adopt existing account** with the 12-digit AWS account id (and optional email).
3) The job is reset to `account.move` and requeued to resume the workflow.

This is the preferred recovery path for `EMAIL_ALREADY_EXISTS`, org permission failures, or other partial failures.
