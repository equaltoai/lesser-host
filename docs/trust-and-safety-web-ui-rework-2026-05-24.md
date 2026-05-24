# Trust-and-safety audit — web/ UI rework on FaceTheory v3.3.0 — 2026-05-24

Output of the `audit-trust-and-safety` walk for the 2026-05-24 web/ UI rework scoped-need (`docs/scoped-need-web-ui-rework-2026-05-24.md`, branch `aron/web-ui-rework-planning`). Run on top of the `coordinate-framework-feedback` output (`docs/framework-feedback-facetheory-v3.3.0-2026-05-24.md`, commit `5579350`). Multi-dimensional walk: CSP shape change, OAC mutating-form posture, public attestation inspector re-skin and ISR decision, confirmation that instance-auth and attestation signing posture are preserved.

This audit does **not** authorize CSP loosening, third-party script origins, raw-key acceptance, attestation signature skipping, or any change to the instance-auth `sha256(raw_key)` contract.

## Surfaces affected

- **Web SPA serving path** — moves from inline-free Vite static build → FaceTheory v3.3.0 Svelte SSR (or SSG for `/attestations/*` if eventually adopted) with same-origin JSON-sidecar hydration at `/_facetheory/data/*`.
- **CloudFront ResponseHeadersPolicy** — `WebSecurityHeaders` and `SafeAppSecurityHeaders` policies (`cdk/lib/lesser-host-stack.ts:1213-1267`); same CSP, applied to new behaviors.
- **CloudFront behaviors** — new behavior for `/_facetheory/data/*` (S3 origin) and any FaceTheory-served HTML behavior (SSR Lambda origin, AppTheorySsrSite-managed). Existing `/api/*`, `/auth/*`, `/setup/*` (control-plane-api), and `/.well-known/*`, `/attestations/*` (trust-api) behaviors unchanged.
- **Trust API surface** — `cmd/trust-api/`, `internal/trust/handlers_attestations.go`, `internal/trust/attestations_issue.go`, `internal/trust/auth_instance.go`. **Contract preserved; no endpoint added, modified, or removed by this rework.**
- **Instance-auth code path** — `internal/trust/auth_instance.go:22-60` (`InstanceAuthHook`). **Preserved unchanged.**
- **Attestation signing** — `internal/attestations/kms_service.go` (`KMSService.SignPayloadJWS`). **Preserved unchanged.**
- **Web SPA UI re-skin** — `web/src/pages/Attestations.svelte` (if it exists; or equivalent route) and any attestation-inspector components re-styled per the Claude Design handoff. Network behavior unchanged.

## Dimension 1: CSP shape change (Vite inline-free → FaceTheory JSON-sidecar)

### Current state (anchored)

`cdk/lib/lesser-host-stack.ts:1186-1198` defines `webCsp`:

```
default-src 'none';
base-uri 'none';
object-src 'none';
frame-ancestors 'none';
form-action 'self';
img-src 'self' data: blob:;
font-src 'self';
style-src 'self';
script-src 'self';
connect-src 'self';
manifest-src 'self'
```

Plus full security header suite at `cdk/lib/lesser-host-stack.ts:1213-1243`:

- `frame-options DENY` (in addition to `frame-ancestors 'none'`)
- `referrer-policy SAME_ORIGIN`
- `strict-transport-security max-age=365d; includeSubdomains; preload`
- `xss-protection 1; mode=block`
- `Permissions-Policy: camera=(), microphone=(), geolocation=(), interest-cohort=()`

`safeAppCsp` (`cdk/lib/lesser-host-stack.ts:1199-1211`) is identical except `frame-ancestors https://safe.global https://*.safe.global` for the Safe-global tip-registry surface.

`web/index.html` is clean — one module loader (`<script type="module" src="/src/main.ts">`), no inline scripts, no inline styles, no inline event handlers. This is exactly what `script-src 'self'` permits.

### Proposed change

The web SPA delivery shifts to FaceTheory v3.3.0's Svelte adapter via `AppTheorySsrSite`, with strict no-inline CSP hydration via JSON sidecars (per FaceTheory's `Migration 7: Move Legacy Inline Hydration To Strict CSP External Hydration` and `Add Strict No-Inline CSP Hydration` patterns). Hydration data moves from inline `__FACETHEORY_DATA__` to same-origin JSON sidecars at `/_facetheory/data/*` served from S3.

The proposed CSP for the FaceTheory-served surface is **byte-identical to the current `webCsp`**:

```
default-src 'none';
base-uri 'none';
object-src 'none';
frame-ancestors 'none';
form-action 'self';
img-src 'self' data: blob:;
font-src 'self';
style-src 'self';
script-src 'self';
connect-src 'self';
manifest-src 'self'
```

No `'unsafe-inline'`. No `'unsafe-eval'`. No third-party script origins. No CDN script loading. No nonces. No `'strict-dynamic'`. The JSON sidecars are same-origin and consumed by `connect-src 'self'`-permitted `fetch` calls; the OAC-safe form transport JS (`startAwsOacFormTransport()`) ships as part of the FaceTheory SSR Lambda's served HTML and runs from a `script-src 'self'` module bundle.

The Safe-global CSP retains its `frame-ancestors https://safe.global https://*.safe.global` exception for the Safe-app tip-registry surface; this is unchanged.

### Verdict

**Audit clean.** The CSP shape change is in the **path the SPA is served from** (Vite static build → FaceTheory SSR), not in the **CSP contract itself**. The contract is byte-identical. FaceTheory v3.3.0's strict-no-inline CSP path is specifically designed so the response CSP does not need to grant `'unsafe-inline'`, nonces, or hashes; hydration data lives in same-origin JSON sidecars served from S3 under the same CloudFront distribution.

### Audit assertions for CI

The `maintain-governance-rubric` walk should add an additive verifier (CSR-022-like) that asserts:

1. `webCsp` and `safeAppCsp` byte-strings in `cdk/lib/lesser-host-stack.ts` do not contain `'unsafe-inline'`, `'unsafe-eval'`, `nonce-`, `sha256-` script directives (hash-allowed only for `style-src` if explicitly governance-authorized), or non-`'self'` script origins. (Today this is asserted by convention only; a verifier eliminates drift.)
2. The CloudFront distribution definition contains a behavior for `/_facetheory/data/*` pointing at S3 (not at a Lambda), with cache headers coordinated to the referencing HTML route.
3. Built `web/dist/**/*.html` files contain no inline `<script>` blocks, no inline `<style>` blocks, no inline event handlers (`onclick=`, `onload=`, etc.), and no `data:` script sources.
4. Built `web/dist/**/*.html` references all script and CSS assets via same-origin URLs (no `https://...` script src, no `https://...` CSS href).

These assertions move the existing CSP-by-convention to CSP-by-CI. The verifier output commits to `gov-infra/evidence/` per the rubric.

### Refusal cases triggered (none yet, but explicit)

- Any FaceTheory adoption shim that requires `'unsafe-inline'` for inline event handlers → **refuse**; rewrite to event-binding-on-mount under the Svelte adapter.
- Any FaceTheory plugin that requires `'unsafe-eval'` → **refuse**; replace plugin.
- Any third-party hydration CDN suggested by upstream docs → **refuse**; FaceTheory v3.3.0 explicitly supports same-origin S3 sidecar.
- Any inline `<style nonce="...">` block in built HTML → **refuse**; rewrite to external stylesheet.

## Dimension 2: OAC posture for mutating-form surfaces

### Current state

Host's current SPA submits mutating requests via JavaScript `fetch` calls to `/api/*` and `/auth/*` Function URLs. Those Function URLs use **`NONE` auth-type** with bearer-token validation in handler code (custom — operator JWT for `/api/v1/operators/*`, customer wallet session for `/api/v1/portal/*`, instance API key for `/.well-known/*` and `/attestations/*` writes). The current architecture does **not** use Lambda Function URL OAC + AWS_IAM signing.

### Proposed change

Adopting `AppTheorySsrSite` introduces a new Lambda Function URL **for the SSR Lambda only**, protected by Lambda OAC + AWS_IAM signing (this is the FaceTheory v3.3.0 + AppTheory v1.7.0 `AppTheorySsrSite` contract per `docs/apptheory/cdk/ssr-site.md`: "omitted `ssrUrlAuthType` now fails closed to `AWS_IAM` + lambda Origin Access Control for **all** CloudFront-to-Lambda traffic"). Mutating same-origin HTML form POSTs to SSR routes must opt into FaceTheory's OAC-safe transport via `data-facetheory-oac-form` + `startAwsOacFormTransport()`.

**Critically: the existing trust-API and control-plane-API Lambdas remain bearer-auth.** They are not migrated to OAC. The CloudFront distribution composes:

- Default origin: SSR Lambda Function URL (OAC + AWS_IAM)
- `/_facetheory/data/*` → S3 (no auth)
- `/api/*`, `/auth/*`, `/setup/*` → control-plane-api Lambda Function URL (bearer auth, unchanged)
- `/.well-known/*`, `/attestations/*` → trust-api Lambda Function URL (bearer auth for writes, public read, unchanged)

This is Signal C from the framework-feedback walk: the `AppTheorySsrSite` contract's documented composition pattern with mixed-auth co-origins is the open framework question. Host's planned posture is hand-wired `addBehavior` calls outside the construct's defaults, with an additive verifier asserting the expected shape.

### Audit assertions for CI

1. The CDK test suite (`cdk/test/`) asserts that the CloudFront distribution behaviors include the bearer-auth control-plane-api and trust-api behaviors at their existing paths after FaceTheory adoption, and that the SSR Lambda is the **only** OAC-protected origin in the distribution.
2. Any HTML form in built `web/dist/**/*.html` that POSTs to an SSR route (default-origin path) carries `data-facetheory-oac-form` and is matched by the OAC form transport activation.
3. No HTML form in built `web/dist/**/*.html` posts to an `/api/*`, `/auth/*`, `/setup/*`, `/.well-known/*`, or `/attestations/*` action — those are bearer-auth fetches via JS, not native form POSTs.
4. The OAC form transport JS bundle (host's `web/` build output) loads from a same-origin URL under `script-src 'self'`. The bundle does not introduce a new third-party origin in `connect-src`.

### Refusal cases triggered (none yet, but explicit)

- Any proposal to migrate `/api/*` or `/attestations/*` to OAC + AWS_IAM at the CloudFront layer → **refuse**; bearer-auth is the API contract for both control-plane and trust-API consumers (instance API keys for trust-API writes; wallet session / operator JWT for control-plane). Migrating to OAC + IAM would break the consumer contract and is not what the rework needs.
- Any proposal to disable `redirect: "error"` on the OAC mutating fetch → **refuse**; the same-origin replay protection is load-bearing.
- Any cross-origin form action (`action="https://other-origin/..."`) on a marked OAC form → **refuse**; FaceTheory fails such requests closed already; verifier asserts no cross-origin actions in built HTML.

## Dimension 3: Public attestation inspector re-skin + ISR adoption decision

### Current state

The public attestation inspector lives in `web/src/pages/` (re-skinned per the Claude Design handoff for visual treatment; the data flow is unchanged): on render, the SPA calls `/attestations/{id}` (public read) and renders the decoded JWS payload plus signing key id and verification instructions. The `/.well-known/jwks.json` endpoint serves the public key set for third-party verification. All of this is **client-rendered** today.

### Proposed change

Per the scoped-need M3, the public attestation inspector is re-skinned per the design (Anthropic Claude Design handoff under `docs/design/web-ui-rework-2026-05-24/`). The re-skin is presentational only — same data flow, same endpoints, same authentication model (public read, instance-auth write).

The framework-feedback walk's Signal B flagged that FaceTheory v3.3.0's marketing surface advertises "tenant-partition-safe ISR" but the canonical pattern for per-tenant ISR partition safety is **not surfaced** in the public theorycloud knowledge base for tenant-scoped routes like `/attestations/{id}` (where attestation issuer is tenant-scoped) or `/.well-known/jwks.json` (which is host-scoped, not tenant-scoped, but still needs partition-safe invalidation on host-key-set rotation).

### ISR adoption decision

**Defer ISR adoption on tenant-scoped trust-API surfaces (`/attestations/{id}`, `/.well-known/jwks.json`) for this rework.** Reasons:

1. The canonical tenant-partition-safe ISR pattern is not documented in the queried theorycloud knowledge base (Signal B). Adopting ISR without the canonical pattern risks the worst trust-and-safety failure mode: serving one tenant's attestation under another tenant's URL scope, or serving a stale attestation after a key rotation.
2. The cost of staying client-rendered for the attestation inspector is small. Third-party readers parse attestations programmatically (not via the inspector UI); the inspector is for humans inspecting attestations interactively. Cold-start latency on the inspector page is acceptable.
3. `/.well-known/jwks.json` is already served by the trust-api Lambda with bounded cache headers; reading the host's published public key set is cheap and already cacheable at the CloudFront layer. ISR would not add value here.
4. If ISR's canonical pattern is documented in a future FaceTheory release, ISR adoption for the attestation inspector is a **separate follow-up scope** governed by `audit-trust-and-safety` rerun + `maintain-governance-rubric` walk for the per-tenant cache-invalidation verifier.

The attestation inspector re-skin is **strictly visual** for this rework: same fetch pattern (`fetch('/attestations/{id}')`), same JWS decoding, same key-id rendering. No data-layer change.

### Audit assertions for CI

1. The attestation inspector page makes no network calls that change data (no POST/PUT/DELETE to trust-API surfaces); only `GET /attestations/{id}` and `GET /.well-known/jwks.json`.
2. The inspector does not surface any internal-only attestation field (no internal correlation IDs, no operator-private fields).
3. The inspector does not cache attestation content client-side beyond the existing browser HTTP cache (no localStorage / IndexedDB persistence of attestation payloads; doing so would risk staleness across rotations).
4. The `/attestations/{id}` response is rendered through the existing `web/src/lib/greater/content/MarkdownRenderer.svelte` mandatory-sanitization path **only** if any attestation field is markdown-rendered (today attestation payloads are JSON-rendered; this assertion is preventive against drift).

### Refusal cases triggered (none yet, but explicit)

- Any proposal to ISR-cache `/attestations/{id}` without a documented per-tenant partition pattern → **refuse**; defer until Signal B resolves.
- Any proposal to client-side-cache attestation content beyond the browser HTTP cache → **refuse**; staleness risk after key rotation.
- Any proposal to surface internal correlation IDs in the inspector for "debug" purposes → **refuse**; the inspector is a public-facing trust surface.

## Dimension 4: Instance-auth + attestation signing posture preservation

### Current state (anchored)

**Instance authentication** (`internal/trust/auth_instance.go:22-60`):

```go
func (s *Server) InstanceAuthHook(ctx *apptheory.Context) (string, error) {
    ...
    raw := strings.TrimSpace(httpx.BearerToken(ctx.Request.Headers))
    if raw == "" {
        return "", nil
    }
    sum := sha256.Sum256([]byte(raw))
    keyID := hex.EncodeToString(sum[:])
    var key models.InstanceKey
    err := s.store.DB.WithContext(ctx.Context()).
        Model(&models.InstanceKey{}).
        Where("PK", "=", fmt.Sprintf("INSTANCE_KEY#%s", keyID)).
        Where("SK", "=", "KEY").
        First(&key)
    ...
    if !key.RevokedAt.IsZero() {
        return "", nil
    }
    slug := strings.TrimSpace(key.InstanceSlug)
    ctx.Set(ctxKeyInstanceSlug, slug)
    ctx.Set(ctxKeyInstanceKey, strings.TrimSpace(key.ID))
    return slug, nil
}
```

This is the canonical sha256(raw_key) contract: raw bearer never logged, raw never stored, only the hex sha256 is the lookup key; revoked keys fail closed; on match, the instance slug + key id bind into request context for downstream authorization. The raw API key is returned to the customer **exactly once at creation** (per the scoped-need's instance-keys UI assertion).

**Attestation signing** (`internal/attestations/kms_service.go:90-91`):

```go
// SignPayloadJWS signs a payload and returns a compact JWS and the signing key ID.
func (s *KMSService) SignPayloadJWS(ctx context.Context, payload []byte) (string, string, error) {
```

Attestations are signed via AWS KMS. The compact JWS (RFC 7515) format is third-party-verifiable using the published JWKS at `/.well-known/jwks.json`. The KMS signing key is never extracted; signing happens in KMS. The signing key id is included in the JWS header.

### Proposed change

**None.** The web/ UI rework re-skins surfaces and replaces the SPA delivery path; it does not touch `internal/trust/auth_instance.go`, `internal/attestations/kms_service.go`, or any attestation issuance flow. All instance-authenticated trust-API writes continue to flow through `InstanceAuthHook` unchanged. All attestation issuances continue to flow through `SignPayloadJWS` unchanged.

### Audit assertions for CI

1. `git diff` between the M0–M3 milestone branches and `main` shows **no changes** to `internal/trust/auth_instance.go`, `internal/attestations/kms_service.go`, `internal/attestations/kms_service_internal_test.go`, or `internal/trust/attestations_issue.go` for any reason except an unrelated change explicitly authorized via a separate scope-need.
2. The `auth_instance_test.go` regression suite continues to pass on every milestone PR.
3. The `kms_service_internal_test.go` regression suite continues to pass on every milestone PR.
4. The `gov-infra` redaction verifier continues to fail closed on any commit that surfaces the string `raw_key`, an unhashed bearer token, or a KMS private key export pattern.

### Refusal cases triggered (none, but explicit guardrails)

- Any UI proposal to display a previously-created raw instance API key → **refuse**; one-time reveal at creation. The instance-keys UI per the Claude Design handoff is correctly designed (the design's portal-pages-2.jsx shows "display plaintext once with explicit copy-to-clipboard + warnings" exactly per the existing contract; the re-skin preserves this).
- Any UI proposal to allow operator-side re-read of an instance key → **refuse**; operators have no privileged access to raw keys either.
- Any UI proposal to skip the rotation grace window → **refuse**; rotation timing belongs to the auth contract.
- Any proposal to log full attestation payloads at debug level → **refuse**; existing structured-log policy redacts payload bodies.

## Test coverage

### Existing (preserved)

- `internal/trust/auth_instance_test.go` — sha256 bearer-token hashing + revoked-key fail-closed behavior.
- `internal/attestations/kms_service_internal_test.go` — JWS signing flow, error paths.
- `internal/trust/handlers_attestations_test.go` — endpoint contract.
- `web/src/lib/greater/content/MarkdownRenderer.svelte.test.ts` — mandatory-sanitizer-before-{@html} (closes the May 16 Signal #1 markdown sanitizer concern locally).

### Added by the rework (deferred to enumerate-changes for filing)

- CDK test asserting `webCsp` and `safeAppCsp` byte-strings contain no forbidden directives.
- CDK test asserting CloudFront behaviors include `/_facetheory/data/*` → S3 (no Lambda) and existing API behaviors unchanged.
- Web build-time test asserting built `web/dist/**/*.html` contains no inline scripts / styles / event handlers, all asset references same-origin.
- Web build-time test asserting any marked OAC form (`data-facetheory-oac-form`) has same-origin action and the form-transport JS is loaded.
- Optional: end-to-end test that a public attestation inspector page can fetch and render an issued attestation in a Playwright run against a built bundle, confirming no CSP violations in the browser.

## Governance-rubric impact

This audit requires **new verifiers** to move convention-only assertions into CI-enforced evidence:

1. **CSR-022-FT-CSP-INTEGRITY**: assert `webCsp` and `safeAppCsp` directives contain no `'unsafe-inline'`, `'unsafe-eval'`, `nonce-`, non-`'self'` script origins, or third-party script CDNs.
2. **CSR-022-FT-HTML-INLINE-ABSENCE**: assert built `web/dist/**/*.html` contains no inline `<script>` or `<style>` blocks, no inline event handlers, no `data:` script sources, all script and CSS asset references same-origin.
3. **CSR-022-FT-OAC-FORM-INTEGRITY**: assert any marked OAC form in built HTML has same-origin action and the OAC form transport JS is loaded; refuse cross-origin actions.
4. **CSR-022-FT-CLOUDFRONT-COMPOSITION**: assert CloudFront distribution behaviors include `/_facetheory/data/*` (S3 origin), existing API and trust behaviors (bearer-auth Lambda origins, unchanged), and exactly one OAC-protected default origin (SSR Lambda).
5. **CSR-022-FT-AUTH-PRESERVATION**: assert no change to `internal/trust/auth_instance.go`, `internal/attestations/kms_service.go`, or related files across the rework's M0–M3 commits.

These verifier asks are consolidated and tracked through `maintain-governance-rubric` in the next walk.

## Consumer impact

- **Managed-instance operators (customers)** — visual re-skin of attestation inspector, instance-keys page, and any trust-data surfaces. No contract change, no behavior change.
- **Third-party attestation readers** — none. `/.well-known/jwks.json` and `/attestations/{id}` continue to serve the same JWS shape and JWKS contents.
- **Portal users (web/)** — improved usability via the design's three-column shell + ⌘K palette; the trust-surface re-skin is part of the broader rework.
- **Operators (Aron + collaborators)** — new operator-side trust dashboards may surface in the Claude Design handoff's operator-console pages (`/operator/...`) but those reads use existing operator-authenticated endpoints; no new auth contract.
- **External vendors** — none affected.

## Multi-tenant isolation impact

**Preserved absolutely.** No new cross-tenant query, no new aggregation across tenant scopes in a public-facing view, no shared cache for tenant-scoped attestation responses (ISR deferred per Dimension 3 specifically to avoid this risk). Operator-side multi-instance views aggregate **only** behind operator-JWT auth, unchanged from today.

## On-chain impact

**None.** Soul-registry contracts, TipSplitter contracts, KMS signing keys, mint-signer keys all unchanged. Attestation signing happens off-chain via KMS (not on-chain).

## AGPL posture

Unchanged. FaceTheory v3.3.0, Greater-components, and any additive components requested per the framework-feedback walk are AGPL-compatible (Theory Cloud and equaltoai stack, AGPL-3.0). OAC form transport library and JSON-sidecar hydration are part of the framework, not vendored.

## Proposed next skill

The audit is **clean for the trust-and-safety dimensions**. The new verifier list above flows into the `maintain-governance-rubric` walk (next-next, after `provision-managed-instance`). The trust-API contract dimensions all pass: the rework is a UI re-skin layered over an unchanged trust-API contract and unchanged instance-auth + attestation-signing posture. The CSP shape change is delivery-path only — the CSP contract is byte-identical.

Handoff:

- **`provision-managed-instance`** runs next (the third specialist walk) for `wire-mcp` job kind + drift + customer-readable stack-state.
- **`maintain-governance-rubric`** consolidates the five new verifier asks (CSP integrity, HTML inline absence, OAC form integrity, CloudFront composition, instance-auth preservation) plus any verifier asks from the provisioning walk.
- **`enumerate-changes`** receives the four-walk output as input.
