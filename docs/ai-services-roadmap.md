# lesser.host AI services subroadmap

This subroadmap covers the missing “AI services layer” needed for `lesser.host` to provide higher-level analysis and
summarization by combining:

- **Deterministic tool outputs** (AWS primitives and other bounded, cheap signals)
- **Stronger LLM reasoning/summarization** (e.g., ChatGPT/OpenAI and Anthropic)

It is designed to plug into the main roadmap milestones:

- **M4**: improve render-based summaries (upgrade from deterministic summaries to LLM-backed summaries)
- **M6**: moderation scanning provider (instance-private, admin-only)
- **M7**: claim verification (optional, expensive)

## Principles

- **Non-blocking:** AI work must never block publish; async-first with stable `queued|ok|not_checked_budget|error` states.
- **Cache-first:** only charge on cache miss; results are reusable across requests where inputs match.
- **Tool-first:** use deterministic, cheap tool signals first; use LLMs to synthesize/interpret outputs.
- **Provider-agnostic:** avoid leaking provider-specific types beyond adapters.
- **Instance-level policy:** instances choose cost/latency tradeoffs (including batching mode).
- **Strict limits:** bounded bytes, links, timeouts, retries; prompt-injection resistant by treating fetched content as data.

## Core concepts

### AI module contract

Every AI capability is a **module** with:

- `module`: stable name (e.g., `render_summary_llm`, `moderation_text_llm`)
- `policyVersion`: version of prompts/toolchain/schema (changes whenever logic or templates change)
- `modelSet`: named model bundle (e.g., `openai:gpt-5-mini-2025-08-07`, `anthropic:claude-sonnet-4-5-20250929`, `deterministic`)
- `inputsHash`: deterministic hash of normalized inputs + evidence hashes/refs
- `createdAt` / `expiresAt`: validity window for caching and distribution

Outputs:

- `result`: schema-valid JSON (module-specific)
- `usage`: normalized usage record (provider tokens, durations, tool calls, etc.)
- `errors`: structured failure info (provider errors never leak secrets)

### Evidence refs (tool outputs)

Modules should avoid embedding large raw content in prompts or storage. Instead they consume:

- render artifact refs: `renderId`, snapshot hash, snapshot object key (bounded reads)
- URL normalization + redirect chain + content-type
- extracted/normalized text (bounded; optionally hashed)
- tool outputs (e.g., Comprehend, Rekognition) as structured JSON

Evidence is always treated as **untrusted data**.

### TableTheory models (state + cache)

Use TableTheory for all persistence:

- `AIJob`: job state for async execution (idempotent keys; retry counters; timestamps; TTL)
- `AIResult`: cached output keyed by `(module, policyVersion, modelSet, inputsHash)` (TTL; audit metadata)

Budget + billing:

- charge only on cache miss
- record audit log entries for all requests and job executions
- record usage ledger entries for debits (credits remain the primary billing unit)

## Instance-level configuration: batching and pricing policy

Batching is a high-leverage cost reducer (amortizes instruction/policy tokens and provider overhead). It must be
configurable per instance so instances can choose their cost/latency tradeoffs and pricing policies.

### Proposed config fields (instance-level defaults)

- `ai_enabled` (bool)
- `ai_model_set` (string): default model set for LLM modules (provider + model)
- `ai_batching_mode` (string):
  - `none`: one item per call (lowest latency, highest cost)
  - `in_request`: batch within a single API request (balanced)
  - `worker`: batch in async workers (best cost/throughput, higher latency)
  - `hybrid`: allow both (module-dependent)
- `ai_batch_max_items` (int): maximum items per LLM call (shard if exceeded)
- `ai_batch_max_total_bytes` (int): maximum evidence bytes per batch (shard if exceeded)

Pricing policy hooks:

- `ai_pricing_multiplier_bps` (int): optional per-instance multiplier applied to debited credits for AI modules.
  - instances that enable batching can be given a cheaper multiplier (or separate “batch price”) if desired.

Notes:

- Module requests may optionally override instance defaults (operator/admin only); otherwise instance defaults apply.
- Cache is still **per item** even if executed in batch.

## Provider support

Planned LLM provider adapters:

- OpenAI: `github.com/openai/openai-go`
- Anthropic: `github.com/anthropics/anthropic-sdk-go`
- (Optional later) AWS Bedrock adapter for parity with environments that require it.

Provider responsibilities:

- request/response mapping to internal types
- retries/backoff + timeouts
- usage extraction (tokens/seconds)
- structured outputs validation (JSON schema)

Secrets:

- store provider keys in AWS Secrets Manager (or SSM) and inject into Lambda env at deploy.
- OpenAI: env `OPENAI_API_KEY` or SSM `/lesser-host/api/openai/service`
- Anthropic: env `ANTHROPIC_API_KEY` (or `CLAUDE_API_KEY`) or SSM `/lesser-host/api/claude`

Model sets (examples):

- OpenAI: `openai:gpt-5.2-2025-12-11`, `openai:gpt-5-2025-08-07`, `openai:gpt-5-mini-2025-08-07`
- Anthropic: `anthropic:claude-sonnet-4-5-20250929`, `anthropic:claude-haiku-4-5-20251001`, `anthropic:claude-opus-4-5-20251101`

## Batching design

Batch at two layers:

1) **In-request batching**: a single request processes N items and returns an array keyed by `item_id`.
2) **Worker batching**: an async worker receives up to `batchSize` jobs per invocation and makes one LLM call per batch.

Batch grouping rules (for stable caching and outputs):

- only batch jobs with identical `module`, `policyVersion`, `modelSet`, and schema version
- each item includes `item_id`, `inputsHash`, and compact evidence refs/hashes

Guardrails:

- shard batches by `ai_batch_max_items` and `ai_batch_max_total_bytes`
- prefer smaller batches on retries (reduce blast radius)
- never batch across instances unless explicitly allowed by policy (default: no cross-instance batching)

## Milestones

### AI0 — Foundations (provider-agnostic)

Deliverables:

- Define internal module contract + schemas.
- Implement TableTheory models `AIJob` and `AIResult`.
- Add budget/usage ledger integration for AI modules (cache-miss only).
- Add instance-level config fields for AI + batching + pricing multiplier.

Acceptance criteria:

- Idempotent job IDs and cache keys.
- Cache-hit path returns without provider/tool calls.
- Budget exceeded yields `not_checked_budget` without blocking callers.

---

### AI1 — Tool outputs layer (cheap, deterministic evidence)

Deliverables:

- Implement tool modules that produce bounded evidence (AWS-first where useful):
  - language/PII/entity detection (Comprehend)
  - image moderation labels / OCR / face signals (Rekognition)
  - (optional) transcription when needed (Transcribe)
- Normalize all outputs into stable, versioned JSON evidence objects.

Acceptance criteria:

- Tool outputs are schema-valid and cacheable.
- Evidence refs are small enough to safely include in LLM prompts.

---

### AI2 — LLM summarization upgrade (ties into M4)

Deliverables:

- New module: `render_summary_llm` that consumes render snapshot/text evidence and produces:
  - short summary
  - key bullets
  - notable risks/red flags (if any)
- Deterministic fallback when LLM is disabled/unavailable.
- Batching supported for multi-link publish jobs (per instance `ai_batching_mode`).

Acceptance criteria:

- Summaries are schema-valid, cached, and budgeted.
- Batch execution produces per-item cached `AIResult` entries.

---

### AI3 — Moderation scanning provider (implements M6)

Deliverables:

- Instance-private moderation scan endpoints:
  - text scan
  - media scan (image/video as supported)
- Async workflow for “on reports” triggers (queue + worker).
- Combine tool evidence + LLM synthesis into structured moderation outputs.
- Retention + audit policies (no federation-wide publication by default).

Acceptance criteria:

- A registered instance can request scans and receive deterministic, schema-valid responses.
- No scan blocks publish; out-of-budget returns `not_checked_budget`.
- Every scan is auditable and tied to a policy version and model set.

---

### AI4 — Claim verification (implements M7)

Deliverables:

- Evidence policy v1 (retrieval constraints, citation requirements).
- Claim extraction + classification + per-claim outputs with citations.
- High-cost gating: cost estimate and cache-miss-only charging.

Acceptance criteria:

- Claims return citations or are marked `inconclusive` with a reason.
- Cache-hit path returns without re-running.

---

### AI5 — Evaluation harness + ops

Deliverables:

- Golden fixtures for prompts/schemas and provider-adapter unit tests (CI-safe; no real LLM calls).
- Metrics: cache hit rate, latency, per-module spend, error rates.
- Per-instance rate limits and concurrency caps.

Acceptance criteria:

- Regression tests catch prompt/schema drift.
- Operational dashboards show spend and failure modes per instance/module.
