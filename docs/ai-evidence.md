# AI evidence endpoints

The trust API exposes instance-authenticated AI evidence endpoints for managed instances.

## `POST /api/v1/ai/evidence/image`

The image evidence endpoint runs bounded Rekognition evidence extraction for an image already present in the host artifact bucket.

Request shape:

```json
{
  "object_key": "evidence/<instanceSlug>/<object-id>"
}
```

Ownership requirements:

- The bearer token authenticates the caller as an instance slug.
- `object_key` **must** be under an instance-owned prefix:
  - `evidence/<instanceSlug>/...`, or
  - `moderation/<instanceSlug>/...` when reusing a moderation image object.
- Keys from other instances, render artifacts (`renders/...`), provisioning receipts, or arbitrary shared-bucket locations are rejected before any S3 `HeadObject` or provider call.

This keeps the shared artifact bucket from becoming a cross-tenant processing/read boundary: one instance cannot cause host AI tooling to process another instance's image object and then read derived labels/text/face counts through its own AI job.

Render artifacts are not accepted by raw `object_key`; use a dedicated `render_id` flow only after host verifies `RenderArtifact.RequestedBy == <instanceSlug>`.
