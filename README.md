# lesser-host

`lesser-host` is the control plane for `lesser.host`: hosted Lesser instance provisioning, host registration governance
(tips), central trust/safety services, and the soul registry.

## Repo map

- Control plane API (AppTheory HTTP): `cmd/control-plane-api`
- Trust API (AppTheory HTTP): `cmd/trust-api`
- Managed provisioning worker (SQS): `cmd/provision-worker`
- Other workers: `cmd/*-worker`
- Web app (portal + operator console): `web/`
- Infrastructure (AWS CDK): `cdk/`
- Contracts (TipSplitter + Soul registry): `contracts/`

## Key surfaces

- Portal + operator APIs: `docs/portal.md`
- Managed instance provisioning (incl. lesser-body + `POST /mcp/{actor}`): `docs/managed-instance-provisioning.md`
- Soul registry surface + instance integration: `docs/soul-surface.md`
- Soul registry contracts: `docs/soul-registry.md`

## Local verification

- Go: `go test ./...`
- CDK: `cd cdk && npm ci && npm run synth`
- Web: `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build`
- Contracts: `cd contracts && npm ci && npm test`

## Roadmap

- `docs/roadmap.md`
