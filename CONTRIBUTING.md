# Contributing

Thanks for contributing to `lesser-host`.

## Development quickstart

- Go tests: `go test ./...`
- Web: `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build`
- CDK: `cd cdk && npm ci && npm run synth`
- Contracts: `cd contracts && npm ci && npm test`

## Operational values (CDK context)

Operational deployment values (account IDs, hosted zone IDs, contract addresses, etc) are intentionally not tracked in
git.

- Copy `cdk/cdk.context.local.json.example` to `cdk/cdk.context.local.json`
- Fill in your real values locally

The CDK apps will automatically merge values from `cdk/cdk.context.local.json` at synth time.

## Secrets

Do not commit secrets (API keys, private keys, webhook secrets, etc). Runtime credentials are loaded from AWS SSM
Parameter Store.
