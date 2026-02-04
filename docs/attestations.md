# Attestations (public, cacheable)

`lesser.host` publishes **signed attestations** that can be cached publicly and verified offline.

## Endpoints

- Public keys (JWKS): `GET /.well-known/jwks.json`
  - Returns a JWKS containing one or more `RS256` keys (`kid` is the KMS key id).
  - Response is cacheable (`Cache-Control: public, max-age=3600`).

- Fetch by id: `GET /attestations/{id}`

- Lookup by tuple: `GET /attestations?actor_uri=...&object_uri=...&content_hash=...&module=...&policy_version=...`

## Response format

Attestation responses return:

- `id`: attestation id (sha256 hex)
- `jws`: compact JWS string (`RS256`)
- `header`: decoded JWS header JSON
- `payload`: decoded JWS payload JSON (exact bytes from the JWS)

## Payload schema (v1)

The signed payload is `type=lesser.host/attestation/v1` and binds:

- `actor_uri`, `object_uri`, `content_hash`
- `module`, `policy_version`, `model_set`
- `created_at`, `expires_at`
- module-specific `result` (and optional `evidence`)

This binding is why attestations **do not apply to quote posts** unless `(actor_uri, object_uri, content_hash)` matches
exactly.

## Caching behavior

`GET /attestations/{id}` is served with `Cache-Control: public, max-age=<seconds-until-expires_at>, immutable`.

