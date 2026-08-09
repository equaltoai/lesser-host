# Trust-and-safety audit: Legacy TheoryLive recovery contract

## Proposed change

Add Control plane Soul registry recovery reads under the existing InstanceKey authentication pattern. This is not a Trust API or Attestation endpoint, but it handles instance authentication and private declaration evidence and therefore receives the same key-hash and audit rigor.

## Surfaces affected

- Control plane endpoints: proposed `GET /api/v1/soul/instance/recovery/agents` and `GET /api/v1/soul/instance/recovery/agents/{agentId}`
- Trust API endpoints: none
- Attestation shape: unchanged
- Instance-auth mechanism: preserved; bearer input is hashed and matched to stored `sha256(raw_key)` only
- CSP: unchanged
- Safety/AI-evidence: unchanged

## Contract impact

- Additive version-1 JSON response shapes.
- List response is metadata-only and bounded/paginated.
- Detail response may expose exact final legacy `produced_declarations` but never messages, provider attempts, prompts, checkpoints, raw keys, credentials, infrastructure state, or tenant data outside the authenticated Slug.
- Every success and failure emits a structured audit event using hashed key/agent/conversation identifiers.
- Existing per-instance route rate limiting applies; explicit response byte caps apply.

## Attestation integrity

Unchanged. No signing key, claim, retention, revocation, or JWKS change.

## Instance-auth correctness

- Key generation: unchanged.
- Storage: only the Instance API key hash (`sha256(raw_key)`) remains stored.
- Validation: exact hash lookup; no prefix, fallback, raw-key storage, or second credential scheme.
- Rotation/revocation: existing behavior preserved.
- Authorization: the authenticated Slug must own the agent's verified domain and every selected `HostedGenesisSession` must carry that same Slug.
- Audit: success, not-found, boundary rejection, integrity conflict, and oversize responses are recorded without private content.

## CSP

No web or CloudFront change. CSP single-origin remains intact; no inline or third-party origin is added.

## Safety and privacy

- No transcript/message field is returned.
- Exact declarations are returned only to the owning Managed instance.
- Inventory never exposes private conversations and never scans across tenants in a returned result.
- Integrity or binding ambiguity fails closed with metadata-only error details.

## Test coverage

- valid, missing, invalid, and revoked InstanceKey cases;
- cross-Slug/domain/agent denial;
- checksum and binding mismatch failure;
- Silas-style declaration-only classification;
- Della/Iris-style multi-version preservation;
- response size and pagination bounds;
- structured audit redaction;
- proof that reads perform no writes or publication convergence.

## Governance-rubric impact

No Pack or Verifier modification is proposed. Existing contract, security, lint, test, and evidence gates apply.

## Audit verdict

Clean for enumeration if the implementation reuses the existing hash-only InstanceKey path and remains side-effect-free. Any proposal to expose this publicly, accept raw stored keys, return messages, or weaken tenant checks is refused.
