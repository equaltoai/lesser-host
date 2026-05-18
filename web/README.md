# lesser.host web UI

This is the frontend for `lesser.host` (repo: `lesser-host`).

## Stack

- Vite + Svelte 5 + TypeScript
- `greater-components` (vendored via `greater` CLI, config target `greater-v0.8.14` / commit `74ce64890bab7c85b83847a1ff72dc882b66ebc5`; primitives refreshed to that tag)
  - Host keeps local safety hardening on `MarkdownRenderer`: sanitization is mandatory before `{@html}` output.
  - Host marks `content` and `adapters` as locally modified in `components.json`; do not overwrite them during Greater refreshes without preserving strict-CSP markdown sanitization and current host soul/x402 adapter contracts.

## Local dev

The UI calls APIs via same-origin paths (e.g. `GET /setup/status`). In dev, Vite proxies those paths to the
configured API origins.

1. Install deps:

```bash
npm ci
```

2. Configure env (optional):

```bash
cp .env.example .env
```

3. Run:

```bash
npm run dev
```

## Scripts

- `npm run lint`
- `npm run typecheck`
- `npm run build`

## Recommended IDE Setup

[VS Code](https://code.visualstudio.com/) + [Svelte](https://marketplace.visualstudio.com/items?itemName=svelte.svelte-vscode).
