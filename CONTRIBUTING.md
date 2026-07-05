# Contributing

Thanks for contributing to `lesser-host`.

## Branching and releases

Feature branches are opened against `staging`, not `main`. The `staging` branch is the integration gate and requires the existing Gov-infra rubric check plus the seven parallel CI jobs. Promotion from `staging` to `main` is operator-owned; `main` accepts PRs only from `staging` and releases are manual `v*` tags cut from `main`. See `docs/release-branching.md`.

Git branch `staging` is not a deploy stage. Deploy stages remain `lab -> live` through `theory app up/down --stage <lab|live> --execute`.

## Development quickstart

- Go tests: `go test ./...`
- Web: `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build`
- CDK: `cd cdk && npm ci && npm run synth`
- Contracts: `cd contracts && npm ci && npm test`

## Operator-local deploy configuration

Operational deployment values (account IDs, hosted zone IDs, contract addresses, etc.) are intentionally not tracked in
git. The deploy surface has two local files with disjoint ownership:

- `app-theory/deploy.local.json` owns the web custom-domain binding for the active deploy stage (`lab` / `live`).
  Copy `app-theory/deploy.local.json.example` to `app-theory/deploy.local.json`, then fill
  `domain.<stage>.rootDomain`, `domain.<stage>.hostedZoneId`, and `domain.<stage>.hostedZoneName`.
- `cdk/cdk.context.local.json` owns non-domain CDK context overrides such as contract addresses, managed provisioning
  account/bootstrap values, and local operator placeholders. It is not the home for web domain config.

- Copy `cdk/cdk.context.local.json.example` to `cdk/cdk.context.local.json`
- Fill in your real non-domain values locally

The CDK app will automatically merge values from `cdk/cdk.context.local.json` at synth time, and it will fail closed if
`app-theory/deploy.local.json` is missing the active stage's domain config. After both local files are prepared, use the
AppTheory deploy contract:

```bash
theory app up --aws-profile my-profile --stage lab --execute
```

## Secrets

Do not commit secrets (API keys, private keys, webhook secrets, etc). Runtime credentials are loaded from AWS SSM
Parameter Store.
