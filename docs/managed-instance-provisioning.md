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

3) **Create delegated hosted zone**
   - in the instance account, create a public hosted zone for `slug.greater.website`.
   - capture the returned name servers.

4) **Delegate from `greater.website`**
   - in the central account, create an `NS` record in the parent zone delegating `slug.greater.website` to the child zone.

5) **Deploy Lesser + seed admin**
   - select a Lesser release (see `docs/lesser-release-contract.md`).
   - build a managed provisioning input file that includes:
     - `slug`
     - `stage`
     - `admin_wallet_address`
     - `admin_username`
     - `admin_wallet_chain_id`
     - `consent_message` + `consent_signature`
   - run `lesser up` in the instance account with:
     - `--base-domain <slug.greater.website>`
     - `--aws-profile <temp-profile>` (static session creds)
     - `--provisioning-input <path>`
   - immediately seed the admin wallet and unlock the instance:
     - `lesser init-admin --base-domain <slug.greater.website> --aws-profile <temp-profile> --provisioning-input <path>`
   - read the deployment receipt `~/.lesser/<app>/<base-domain>/state.json`.

6) **Register with lesser.host**
   - store instance endpoints from the receipt.
   - mint an instance API key for `lesser.host` calls (future: inject into Lesser at deploy time).

7) **Observability + recovery**
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

## Required config (control plane)

The control plane needs (at minimum):
- `MANAGED_PROVISIONING_ENABLED=true` to allow the provisioning worker to run.
- `MANAGED_ORG_VENDING_ROLE_ARN` (optional) role ARN in the Organizations management/delegated admin account that the
  control plane can assume for `organizations:*` and cross-account `sts:AssumeRole` into instance accounts.
- `MANAGED_PARENT_DOMAIN` (default: `greater.website`)
- `MANAGED_PARENT_HOSTED_ZONE_ID` (central account Route53 hosted zone id for `greater.website`)
- `MANAGED_INSTANCE_ROLE_NAME` (default: `OrganizationAccountAccessRole`)
- `MANAGED_TARGET_OU_ID` (optional; move instance accounts into this OU)
- `MANAGED_ACCOUNT_EMAIL_TEMPLATE` (required for account vending; example: `lesser+{slug}@example.com`)
- `MANAGED_ACCOUNT_NAME_PREFIX` (default: `lesser-`)
- `MANAGED_DEFAULT_REGION` (default: `AWS_REGION` or `us-east-1`)
- `MANAGED_LESSER_DEFAULT_VERSION` (optional semver tag; used when the request doesn’t specify one)
- `MANAGED_PROVISION_RUNNER_PROJECT_NAME` (CodeBuild project name used to run `lesser up`)
- `ARTIFACT_BUCKET_NAME` (S3 bucket where the runner writes receipts)
- `MANAGED_LESSER_GITHUB_OWNER` (default: `equaltoai`)
- `MANAGED_LESSER_GITHUB_REPO` (default: `lesser`)
- `MANAGED_LESSER_GITHUB_TOKEN_SSM_PARAM` (optional; SecureString SSM parameter containing a GitHub token)

Infra is expected to provide:
- `PROVISION_QUEUE_URL` (SQS queue that drives the async pipeline)

## Receipt + bootstrap outputs

The deploy runner writes the Lesser receipt to S3 so the provisioning worker can ingest it and update instance state:

- Receipt: `s3://$ARTIFACT_BUCKET_NAME/managed/provisioning/<slug>/<jobId>/state.json`
- Bootstrap key material (legacy only): `s3://$ARTIFACT_BUCKET_NAME/managed/provisioning/<slug>/bootstrap.json`

Notes:
- Managed provisioning now seeds the admin wallet via `init-admin`, so a bootstrap mnemonic should not be generated.
- If a legacy bootstrap file exists, treat it as sensitive and rotate/delete it after migration.
