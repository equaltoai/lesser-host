# lesser.host — Pricing & Services Outline

## Overview

lesser.host provides managed hosting for Lesser (ActivityPub) instances, trust/safety services, and on-chain governance
— as a tiered service with both hosted and self-hosted consumption models.

---

## Service Tiers

| Tier | Monthly | Hosting | Trust Services | Domains | On-Chain |
|------|---------|---------|---------------|---------|----------|
| **External** | $0 | Self-hosted | Pay-per-use credits | N/A | Optional |
| **Starter** | $5/mo | Hosted on `slug.greater.website` | 500 included credits | 1 subdomain | Included |
| **Standard** | $15/mo | Hosted | 2,000 credits + attestations | + 1 vanity domain | Included |
| **Pro** | $35/mo | Hosted | 10,000 credits + all AI services | + unlimited domains | Included |

---

## Tier Details

### External (Free)

**Target:** Self-hosted Lesser operators who want trust services without managed hosting.

| Feature | Included |
|---------|----------|
| Hosting | None — bring your own infrastructure |
| Trust services | Pay-per-use only (no included credits) |
| Attestations | Available per-credit |
| Domains | N/A (self-managed) |
| On-chain host registration | Self-service via tip registry |
| Support | Community only |

**Why it exists:** Creates a funnel from self-hosted operators to hosted plans. Any Lesser instance can register
with lesser.host and consume trust services without committing to hosting. The operator pays only for what they use.

**Registration flow:**
1. Operator submits external registration request (wallet + domain + DNS proof).
2. lesser.host operator approves (or auto-approves if proofs pass).
3. Instance receives API credentials for trust service calls.
4. Usage is metered; credits purchased on demand.

---

### Starter ($5/mo)

**Target:** Individuals, small communities, and hobby instances.

| Feature | Included |
|---------|----------|
| Hosting | Dedicated AWS account, `slug.greater.website` |
| Trust credits | 500/month |
| Link previews | ✓ (fetched/rendered by lesser.host) |
| Link safety (basic) | ✓ |
| Attestations | — (upgrade to Standard) |
| AI moderation | — (upgrade to Pro) |
| Domains | 1 subdomain (`slug.greater.website`) |
| On-chain tip registry | Auto-registered for `slug.greater.website` |
| Data retention | Standard (30 days benign, 180 days flagged) |
| Support | Email |

**Credit budget:**
- 500 credits ≈ 500 preview fetches or 250 basic safety scans per month.
- Overage: $0.005/credit (can set hard cap or auto-purchase).
- Cache hits do not consume credits.

**Cost basis:**
- ~$4/mo infrastructure cost (after SSM optimization).
- ~$1/mo margin at idle.
- Credit usage and overage provide additional margin with activity.

---

### Standard ($15/mo)

**Target:** Organizations, public-facing communities, and instances that want trust attestations.

| Feature | Included |
|---------|----------|
| Hosting | Dedicated AWS account |
| Trust credits | 2,000/month |
| Link previews | ✓ |
| Link safety (basic + render) | ✓ |
| Attestations | ✓ (KMS-signed, publicly verifiable) |
| AI moderation | — (upgrade to Pro) |
| Domains | `slug.greater.website` + 1 vanity domain |
| On-chain tip registry | Both subdomain + vanity domain registered |
| Data retention | Standard |
| Support | Email + priority |

**What Standard adds:**
- **Attestations:** Publicly cacheable, offline-verifiable trust signals. Other instances and clients can
  check `GET /attestations/{id}` against published JWKS keys without trusting lesser.host at runtime.
- **Render pipeline:** Headless browser rendering for richer preview cards and enhanced safety analysis.
- **Vanity domain:** `yourcommunity.org` with DNS proof + optional Route53-managed DNS.

**Credit budget:**
- 2,000 credits covers a moderately active instance (~50-100 posts/day with links).
- Render jobs cost 5 credits each, but are only triggered on suspicious links (auto-escalation policy).
- Overage: $0.004/credit.

---

### Pro ($35/mo)

**Target:** Large communities, organizations with compliance needs, and instances that want full AI services.

| Feature | Included |
|---------|----------|
| Hosting | Dedicated AWS account |
| Trust credits | 10,000/month |
| Link previews | ✓ |
| Link safety (basic + render) | ✓ |
| Attestations | ✓ |
| AI moderation (text + image) | ✓ |
| AI claim verification | ✓ |
| AI evidence extraction | ✓ |
| Domains | `slug.greater.website` + unlimited vanity domains |
| On-chain tip registry | All domains registered |
| Data retention | Extended (configurable) |
| Support | Priority + dedicated onboarding |

**What Pro adds:**
- **AI moderation:** Text and image scanning via AWS Comprehend, Rekognition, and LLM providers (OpenAI/Claude).
  Triggered on reports (default), or configurable for always-on, media-only, or virality thresholds.
- **Claim verification:** Multi-model evidence extraction with citations and confidence scores. Produces structured
  attestations with per-claim verdicts.
- **Unlimited vanity domains:** Each with DNS proof requirements and on-chain host registration.

**Credit budget:**
- 10,000 credits supports a highly active instance (~500+ posts/day) with full AI pipeline.
- Moderation scans: 10 credits/text, 15 credits/image.
- Claim verification: 50 credits/job.
- Cache deduplication across all services significantly reduces effective cost.
- Overage: $0.003/credit.

---

## Credit System

### Credit Costs by Service

| Service | Credits | Notes |
|---------|---------|-------|
| Link preview (fetch) | 1 | HTML fetch + OG metadata extraction |
| Link safety basic | 2 | Deterministic URL analysis (no network fetch) |
| Link safety render | 5 | Headless browser screenshot + text snapshot |
| Attestation (sign) | 1 | KMS signing of result |
| AI moderation (text) | 10 | Language detection + entity extraction + PII scan |
| AI moderation (image) | 15 | Moderation labels + text detection + face detection |
| AI moderation (report) | 10 | Full report with structured categories + highlights |
| AI evidence (text) | 20 | LLM-based evidence extraction with citations |
| AI evidence (image) | 25 | Multi-modal evidence extraction |
| AI claim verification | 50 | Multi-model, multi-source claim checking |

### Credit Rules

1. **Cache hits are free.** If a result exists for `(normalizedURL, policyVersion)` or `(actorUri, objectUri, contentHash, module, policyVersion)`, no credit is charged.
2. **Author discount.** When an author publishes from a registered instance, the "initial publish" flow receives discounted pricing (implementation-specific, surfaced in portal UI).
3. **Budget enforcement is non-blocking.** If credits are exhausted and no overage method is set, the response is `"status": "not_checked", "reason": "budget_exceeded"` — the publish is never blocked.
4. **Monthly rollover:** Unused credits do not roll over. This keeps the pricing simple.

### Purchasing Credits

- **Included credits** come with the tier and reset monthly.
- **Additional credits** can be purchased via Stripe checkout in the portal.
- **Overage billing** can be enabled with a payment method on file — usage beyond included credits is billed at the tier's overage rate.
- **Hard cap option:** Set a maximum monthly spend to prevent unexpected overage charges.

---

## Domain Management

### Subdomain (`slug.greater.website`)

- Automatically provisioned when the instance is created.
- DNS is managed by lesser.host (NS delegation under `greater.website`).
- On-chain `hostId` is `keccak256(normalize("slug.greater.website"))`.
- No additional cost.

### Vanity Domain

- Requires DNS proof before activation:
  - **User-managed DNS:** Copy/paste TXT record + CNAME/A routing records.
  - **Route53-managed DNS (optional):** lesser.host can UPSERT proof + routing records automatically.
- DNS proof must be re-verified periodically (or on wallet/fee changes).
- On-chain `hostId` registered separately for the vanity domain.
- Domain can be rotated or disabled without affecting the instance.

---

## On-Chain Integration (Tip Registry)

All hosted instances are automatically registered in the TipSplitter contract:

| Action | Who | When |
|--------|-----|------|
| `registerHost(hostId, wallet, feeBps)` | lesser.host (via Safe multisig) | Instance creation |
| `updateHost(hostId, wallet, feeBps)` | lesser.host (via Safe) | Wallet or fee change |
| `setHostActive(hostId, active)` | lesser.host (via Safe) | Instance enable/disable |
| Token allowlist management | lesser.host (via Safe) | New token support |

**External instances** go through the tip registry registration flow:
1. Operator initiates registration with wallet signature + DNS proof.
2. lesser.host operator approves (higher assurance: DNS + HTTPS proof for wallet/fee increases).
3. lesser.host proposes Safe transaction; operators execute.
4. On-chain state is reconciled and audited.

**Fee structure:**
- Lesser org fee: 1% (LESSER_FEE_BPS = 100, hardcoded in contract).
- Host fee: configurable per-host, capped at 5% (MAX_HOST_FEE_BPS = 500).
- Actor receives remainder.
- Supported tokens: ETH, USDC, USDT, EURC, XAUt (via allowlist).

---

## Infrastructure Per Instance

Each hosted instance receives:

| Resource | Details |
|----------|---------|
| **AWS Account** | Dedicated account in lesser.host's AWS Organization |
| **DynamoDB table** | Single-table design, pay-per-request, field-level KMS encryption |
| **S3 bucket** | Media storage, block-all-public-access, enforce-SSL |
| **Lambda functions** | Lesser engine (43 functions), serverless, ARM64 |
| **CloudFront** | CDN with security headers, SPA rewrite, API routing |
| **Route53 hosted zone** | DNS for `slug.greater.website` (delegated) |
| **KMS key** | Encryption + signing |
| **SQS queues** | Async processing (federation, media, notifications) |
| **CloudWatch** | Structured logging + metrics |

**Idle cost per instance:** ~$4/month (after SSM optimization).

---

## Portal Experience

### Self-Serve Portal (all tiers)

- Create/claim instance slug
- View instance status and configuration
- Configure trust services (previews, safety, attestations, moderation)
- Manage domains (add, verify, rotate, disable)
- View usage dashboard (credits consumed, cache hit rate, budget remaining)
- Purchase credits / manage payment method
- View invoices and receipts

### Operator Console (lesser.host staff)

- Review/approve external instance registrations
- Review/approve vanity domain requests
- Propose on-chain transactions (Safe payloads)
- Record transaction execution results
- View complete audit trail
- Monitor provision jobs (status, logs, retry, notes)

---

## Upgrade/Downgrade Paths

| From → To | What Happens |
|-----------|-------------|
| External → Starter | Instance provisioned, data migrated (if applicable), subdomain assigned |
| Starter → Standard | Attestation signing enabled, render pipeline activated, vanity domain slot opened |
| Standard → Pro | AI moderation + claim verification enabled, domain limit removed |
| Pro → Standard | AI services disabled at next billing cycle, excess domains must be removed |
| Standard → Starter | Attestations and vanity domain disabled, render pipeline disabled |
| Any → External | Hosting terminated (with data export window), trust services remain on pay-per-use |

---

## Future Considerations

- **Team plans:** Multiple operators per instance with RBAC.
- **Volume discounts:** Reduced per-credit pricing at higher tiers or with annual commitment.
- **Fedipack integration:** Package registry instances as a specialized face with registry-specific credit types.
- **SLA tiers:** Uptime guarantees for Standard ($15) and Pro ($35) plans.
- **Custom deployments:** Enterprise instances with custom AWS account configuration, dedicated support, and custom AI model sets.
