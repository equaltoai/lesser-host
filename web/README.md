# lesser.host web UI

This is the frontend for `lesser.host` (repo: `lesser-host`).

## Stack

- Vite + Svelte 5 + TypeScript
- `greater-components` (vendored via `greater` CLI, pinned to `greater-v0.8.11` / commit `0abcb00d4466b425473476dd1b38ba628118091c`)
  - Host keeps a local safety hardening on `MarkdownRenderer`: sanitization is mandatory before `{@html}` output.

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
