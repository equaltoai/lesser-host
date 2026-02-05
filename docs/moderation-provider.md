# Moderation scanning provider (M6)

`lesser.host` can act as a private moderation scanning provider for registered instances. Scans are **instance-scoped**
(not published as attestations) and are **async-first** (queued + worker executed) with deterministic fallback when LLM
providers are unavailable.

## Endpoints (trust-api)

All endpoints require **instance auth** (Bearer instance key) and return a stable `status`:
`ok | queued | not_checked_budget | disabled | error`.

### Text moderation

- `POST /api/v1/ai/moderation/text`
- `POST /api/v1/ai/moderation/text/report` (recommended for “on reports” flows)

Request:

```json
{
  "text": "string",
  "context": {
    "has_links": true,
    "has_media": false,
    "virality_score": 0
  }
}
```

### Image moderation

- `POST /api/v1/ai/moderation/image`
- `POST /api/v1/ai/moderation/image/report` (recommended for “on reports” flows)

Request supports either:

1) `url` (preferred): `lesser.host` fetches the image with SSRF protections and stores it under
   `moderation/<instanceSlug>/<sha256>`.
2) `object_key`: must be under `moderation/<instanceSlug>/...` in the artifact bucket.

```json
{
  "url": "https://example.com/image.jpg",
  "object_key": "moderation/my-instance/<sha256>",
  "context": {
    "virality_score": 0
  }
}
```

Limits:
- Max image size: `5 MiB`
- Content-Type must be `image/*` (sniffed if missing)

### Polling for results

- `GET /api/v1/ai/jobs/{jobId}`

Returns the job record and (when available) the cached module result.

## Instance configuration (control-plane-api)

Configure moderation scanning per instance via:
- `PUT /api/v1/instances/{slug}/config`

Fields:
- `moderation_enabled` (bool): master enable/disable.
- `moderation_trigger` (string):
  - `on_reports` (default): `.../text` and `.../image` return `disabled`; use `.../report` endpoints.
  - `always`: allow `.../text` and `.../image`.
  - `links_media_only`: text scans require `context.has_links=true` or `context.has_media=true`.
  - `virality`: requires `context.virality_score >= moderation_virality_min`.
- `moderation_virality_min` (int): minimum `virality_score` threshold for `virality` mode.

## Retention

Moderation input objects stored under `moderation/` are expired automatically by an S3 lifecycle rule (30 days).
Cached moderation results (`AIResult`) are retained for 30 days by TTL.

