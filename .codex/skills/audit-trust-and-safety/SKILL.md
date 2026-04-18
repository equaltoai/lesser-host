---
name: audit-trust-and-safety
description: Use when a change touches the trust API surface (`/.well-known/*`, `/attestations/*`), instance authentication (sha256(raw_key) matching), attestation shape or integrity, CSP (single-origin enforcement on `web/`), or safety / AI-evidence services. Walks trust posture with rigor; loosening these surfaces is refused without explicit governance event.
---

# Audit trust and safety

host's trust API is the public surface that third parties read to evaluate a managed instance's trust posture. Instance authentication (via sha256 hash of a raw key) gates who can write to it. Attestation integrity is what makes the evidence credible. CSP (strict single-origin on `web/`) is the defense-in-depth for the operator portal.

This skill walks every trust-API / CSP / instance-auth change with the rigor the surface demands.

## The trust-and-safety surfaces (memorize)

- **`cmd/trust-api/`** — public attestation + instance-auth Lambda
- **`internal/trust/`** — attestations, previews, instance auth
- **Public trust endpoints** (`/.well-known/*`) — discovery, attestation lookup
- **`/attestations/*`** — read (public) + write (instance-authenticated)
- **Instance-auth mechanism** — bearer token = `sha256(raw_key)` matching; raw keys never stored
- **Safety / AI-evidence services** — `cmd/ai-worker/`, `internal/<safety-pkg>/`, outputs that feed attestations
- **`web/` SPA** — CSP strict single-origin (`script-src 'self'`, `style-src 'self'`, no inline, no third-party origins)
- **CloudFront distribution** — serves `lesser.host` with CSP enforced at response-header level

## When this skill runs

Invoke when:

- A change modifies the trust API surface (new endpoint, modified shape, changed authentication requirement)
- A change modifies attestation shape, signing, retention, or integrity
- A change modifies instance-auth (how raw keys are generated, hashed, stored, validated)
- A change modifies CSP (new origin, new inline need, new directive)
- A change modifies safety / AI-evidence collection, emission, or consumption
- A change modifies the trust-api Lambda or its IAM permissions
- `scope-need` or `investigate-issue` flags a change as trust-API / CSP / instance-auth-touching

## Preconditions

- **The change is described concretely.** "Harden instance-auth" is too vague; "add a rate-limit per instance-key-hash of 100 requests/minute on `POST /attestations/*`, returning 429 with `Retry-After` header on breach, evidence logged to CloudWatch" is concrete.
- **MCP tools healthy**, `memory_recent` first — trust-and-safety evolves with audit findings and security posture iterations.

## The five-dimension walk

### Dimension 1: Trust API contract

For changes to trust-API surface:

- **Endpoint enumeration** — which `/.well-known/*` or `/attestations/*` surfaces are affected
- **Authentication model per endpoint** — public read / instance-authenticated write / operator-authenticated admin
- **Shape and versioning** — request / response shapes; backward compatibility; versioning if breaking
- **Evidence emission** — what audit events emit on call; retention; structured-log format
- **Rate limiting** — per-key, per-IP, per-endpoint rate limits
- **Third-party consumer impact** — external auditors, trust-evaluation tools, operator-facing tools may consume these surfaces; breaking changes require coordination

### Dimension 2: Attestation integrity

- **Attestation shape** — the signed-claim format (JSON + signature envelope)
- **Signing key** — which key signs attestations; how the key is stored and rotated
- **Signature verification** — third parties reading attestations can verify the signature against a published public key
- **Attestation content** — what claims are made (instance identity, lesser version, body version, trust posture, safety evidence summary, AI-evidence reference)
- **Retention** — attestations persist for a defined policy; immutable history
- **Revocation** — if an attestation is issued in error, what's the revocation mechanism (publish a revocation record; clients consuming attestations respect revocations)
- **Cross-attestation consistency** — newer attestations for the same instance supersede older; the "current" attestation is well-defined

### Dimension 3: Instance authentication (key hash)

- **Key generation** — at instance provisioning, a raw API key is generated (cryptographically random)
- **One-time reveal** — the raw key is returned to the customer exactly once at creation; never returned again
- **Storage** — only `sha256(raw_key)` stores in host's DynamoDB
- **Validation** — every trust-API call with a bearer token computes `sha256(bearer)` and compares against the stored hash; mismatch rejects with 401
- **Rotation** — customers can rotate their instance keys via the portal; the previous hash is retained for a grace window then removed
- **Revocation** — customers can revoke keys; revocation removes the hash immediately
- **Audit** — every authentication attempt audits (success + failure); failure rate is monitored

### Dimension 4: CSP strict single-origin

host's `web/` SPA is served with **strict CSP**:

- `default-src 'self'`
- `script-src 'self'`
- `style-src 'self'`
- `img-src 'self' data:` (where needed)
- `connect-src 'self'` + explicit API origins (e.g. eth_rpc, Stripe callbacks where required)
- `frame-ancestors 'none'`
- No `'unsafe-inline'` for scripts or styles
- No `'unsafe-eval'`
- No third-party CDN origins for scripts
- No inline event handlers (onclick, etc.)
- No inline `<script>` or `<style>` blocks

Changes that relax any of this are refused without explicit governance-change process. Specific refusals:

- **"Add a third-party analytics script."** Use server-side analytics or a self-hosted alternative.
- **"Add `'unsafe-eval'` for a dependency."** Replace the dependency.
- **"Add `'unsafe-inline'` for a specific button."** Refactor to use CSP-compliant handlers.
- **"Embed a third-party widget via iframe."** Evaluate carefully; frame-src additions are possible but require explicit reasoning.

### Dimension 5: Safety and AI-evidence services

- **AI workers** (`cmd/ai-worker/`) — process safety / moderation / AI-evidence jobs
- **Evidence collection** — what data is gathered for attestation claims (moderation statistics, safety-preview outputs, AI-risk assessments)
- **Provider integration** — external AI providers have their own compliance obligations; credentials handled via SSM, usage audit-logged
- **Privacy boundary** — AI workers may process tenant content (via their own AI providers); output evidence is aggregate, not content-leaking
- **Preview services** — safety-preview endpoints return sanitized evaluations without leaking input content

## The audit output

```markdown
## Trust-and-safety audit: <change name>

### Proposed change
<concrete description>

### Surfaces affected
- Trust API endpoints: <list>
- Attestation shape: <...>
- Instance-auth mechanism: <...>
- CSP: <...>
- Safety / AI-evidence services: <...>

### Trust API contract impact
- Endpoint additions / modifications / removals: <...>
- Authentication model: <preserved>
- Shape / versioning: <additive / semantic / breaking>
- Evidence emission: <preserved / enhanced>
- Rate limiting: <preserved / added>
- Third-party consumer impact: <none / coordination plan>

### Attestation integrity
- Shape: <preserved / versioned>
- Signing key: <unchanged / rotation coordinated>
- Signature verifiability: <preserved>
- Content: <...>
- Retention: <policy preserved>
- Revocation: <supported>

### Instance-auth correctness
- Key generation: <unchanged>
- Storage (sha256 only): <preserved>
- Validation (hash-match): <preserved>
- Rotation flow: <...>
- Revocation flow: <...>
- Audit: <every attempt>

### CSP (if web/ touched)
- Headers: <enumerated>
- Changes: <none / additive 'self' origin / explicit governance-authorized loosening>
- Inline-script / inline-style / unsafe-eval / third-party-origin: <none — preserved>

### Safety / AI-evidence
- Provider changes: <none / coordinated with privacy review>
- Evidence collection shape: <preserved / enhanced>
- Privacy boundary: <preserved>
- Preview services: <preserved>

### Test coverage
- Unit tests: <added / existing>
- Integration tests (trust-API auth flow with valid / invalid / expired keys): <added / existing>
- CSP validation test: <automated check in `web/` build pipeline>
- Attestation-signing round-trip test: <added / existing>

### Governance-rubric impact
- New verifier(s) needed: <no / yes — route to maintain-governance-rubric>
- Evidence-policy impact: <none / enhanced>

### Consumer impact
- Managed-instance operators: <...>
- Third-party attestation readers: <...>
- Portal users (web/): <...>

### Proposed next skill
<enumerate-changes if audit clean; maintain-governance-rubric if new verifier needed; evolve-soul-registry if attestation signing intersects with soul-registry signer; provision-managed-instance if instance-auth seeding is affected; scope-need if audit surfaces scope growth>
```

## Refusal cases

- **"Accept raw instance API keys for a legacy endpoint."** Refuse. Never.
- **"Store the raw key in SSM for convenience."** Refuse.
- **"Relax the hash comparison to allow prefix matching."** Refuse.
- **"Return the raw key on re-read endpoints."** Refuse. One-time reveal at creation.
- **"Log the raw key once for debugging."** Refuse.
- **"Skip authentication on a specific trust-API endpoint for performance."** Refuse.
- **"Add `'unsafe-inline'` for a specific view."** Refuse without explicit governance event and documented refactor plan.
- **"Add `'unsafe-eval'`."** Refuse.
- **"Add a third-party script origin."** Refuse without explicit governance event.
- **"Embed a third-party iframe."** Evaluate carefully; default refuse.
- **"Skip signature on a specific attestation type."** Refuse.
- **"Share attestation signing keys across instances."** Refuse.
- **"Log the full content of safety-evidence inputs."** Refuse; evidence is aggregate, content-sanitized.
- **"Skip audit events for failed auth attempts."** Refuse. Failure events are high-signal for attack detection.

## Persist

Append when the walk surfaces something worth remembering — a trust-API evolution decision, an attestation-shape versioning choice, a CSP refactor pattern, an instance-auth rotation timing subtlety, a safety-evidence privacy-boundary finding. Routine audits aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **Audit clean, additive change** — invoke `enumerate-changes`.
- **Audit clean, with new verifier needed** — invoke `maintain-governance-rubric` first.
- **Audit overlaps with soul-registry** (shared signers, attestation-as-on-chain-proof) — invoke `evolve-soul-registry` as well.
- **Audit overlaps with provisioning** (instance-auth seeding at provisioning) — invoke `provision-managed-instance` as well.
- **Audit surfaces scope growth** — revisit `scope-need`.
- **Audit reveals an existing bug** — route through `investigate-issue`, then back here.
- **Audit surfaces framework awkwardness** (AppTheory middleware for auth patterns) — `coordinate-framework-feedback`.
- **Audit surfaces CSP loosening request** — refuse or escalate to Aron for explicit governance-change authorization.
