# API contracts (lesser-host)

This directory contains **pinned, machine-readable contract artifacts** for downstream consumers that generate clients
from `lesser-host` APIs (for example, `greater-components`).

- `openapi.yaml` — OpenAPI snapshot for build-time client/type generation. Do not serve this file at runtime.
- `soul-mint-conversation-sse.json` — machine-readable SSE companion contract for the soul mint-conversation stream.
- `../spec/v3/` — JSON Schema + fixtures for lesser-soul v3 protocol surfaces implemented by `lesser-host`.

## Canonical generation process

The checked-in REST adapter for `lesser-host` lives at:

- `web/src/lib/greater/adapters/rest/generated/lesser-host-api.ts`

It is generated from `openapi.yaml` with the pinned `openapi-typescript` tool in `web/package.json`.

Canonical commands:

```bash
cd web
npm ci
npm run generate:lesser-host-api
npm run verify:lesser-host-contracts
```

`verify:lesser-host-contracts` is the anti-drift gate. It fails if:

- required soul mint-conversation routes or schemas are missing from `docs/contracts/openapi.yaml`
- the SSE companion contract is missing required events or routes
- the checked-in generated adapter does not match a fresh regeneration

CI and the governance rubric both run this verification so contract drift fails closed.

Release automation packages these artifacts as GitHub Release assets on every `v*` tag.

## Soul comm mailbox contract notes

The mailbox schemas under `../spec/v3/schemas/soul-comm-mailbox-*.schema.json` are the body-facing canonical contract.
They are instance-key authenticated and keep list/get metadata separate from explicit content fetches.

The portal compatibility schemas `soul-agent-comm-activity-*` and `soul-agent-comm-queue-*` are derived from canonical
mailbox state for operator/customer portal views. Queue list items intentionally do not include full message bodies; they
return preview/content metadata and mailbox state only.

Cross-repo migration guidance for body and lesser is in `../soul-comm-mailbox-migration.md`.
