# ADR 0006 — FaceTheory v3.3.0 adoption

**Date:** 2026-05-24  
**Status:** Accepted

## Context

`lesser-host` has maintained a strict single-origin CSP (`script-src 'self'`, `style-src 'self'`,
no inline) since inception — a defense-in-depth posture for the operator portal and public trust
surfaces. The frontend roadmap (`docs/frontend-roadmap.md`) originally targeted a Vite static SPA
deployment behind CloudFront.

A 2026-05-16 deferral verdict was recorded for FaceTheory v3.3.0 adoption: the framework was deferred
pending resolution of upstream gaps around client-side hydration sidecar delivery and CDK-side
construct support. The following upstream signals resolved those gaps:

- **Signal A (client hydration):** FaceTheory v3.4.0 shipped `loadFaceHydrationData<T>()` in the
  `@theory-cloud/facetheory/client` subpath, retiring the bespoke 38-line `fetchSidecarPayload`
  interim that `lesser-host` carried (`216fb96`). The framework enforces same-origin pre/post fetch;
  host adds `redirect:'error'` for stricter posture.
- **Signal D (CDK constructs):** `@theory-cloud/apptheory-cdk` v1.9.0 shipped `AppTheorySsrSite`
  with `SSR_ONLY` mode, JSON-sidecar S3 behavior, and CloudFront distribution composition.

These signals confirmed that FaceTheory adoption could proceed without weakening CSP, trust-auth,
multi-tenant isolation, or supply-chain integrity.

## Decision

`lesser-host` adopts FaceTheory v3.3.0 (upgraded to v3.4.0) as its web rendering framework:

- Server-side rendering (SSR) via FaceTheory's AppTheory Lambda integration.
- CSP delivery via JSON hydration sidecar served from a dedicated S3 bucket (htmlStoreBucket), with
  the SSR Lambda writing sidecar payloads and the CloudFront distribution serving them under
  `/_facetheory/data/*`.
- Client-side hydration via `loadFaceHydrationData<T>()` (FaceTheory v3.4.0 client subpath), replacing
  the interim `fetchSidecarPayload` workaround (`216fb96`).

The 2026-05-16 deferral verdict is explicitly superseded by this ADR.

## CSP delivery-path analysis

The CSP byte-string contract is preserved exactly — same directives, same order, same values, same
join separator (`; `). The delivery mechanism changed:

- **Before (static SPA):** CSP header applied by CloudFront response headers policy on every response.
- **After (FaceTheory SSR):** CSP delivered via the same CloudFront response headers policy; the
  JSON hydration sidecar carries SSR hydration payloads, not CSP directives. The CSP header itself
  remains a CloudFront-level invariant enforced by SEC-5 (byte-string integrity verifier).

**Conclusion:** delivery-path only change. Security contract preserved. The SEC-5 verifier locks the
CSP byte-string, making any drift a CI-failing condition.

## Four-walk governance

This ADR was prepared under the four-walk governance review:
1. **CSP/Security walk** — SEC-5 (CSP byte-string), SEC-6 (inline absence), SEC-7 (OAC integrity),
   SEC-8 (CloudFront composition) verifiers enforce the security contract.
2. **Supply-chain walk** — SEC-9 (release-verification change-lock), SEC-10 (trust-auth change-lock)
   verifiers prevent regression on the provisioning and trust-auth pipelines.
3. **Framework-consumption walk** — CON-4 (MCP route ownership) verifier ensures the framework
   integration surfaces preserve host-owned routes.
4. **Docs/metadata walk** — This ADR, threat-model additions (T-CSP-001, T-OAC-001, T-COMP-001,
   T-AUTH-DRIFT-001, T-SUPPLY-001, T-MCP-ROUTE-001), and controls-matrix additions (C-CSP-FT,
   C-OAC-MUT, C-CDN-COMP, C-AUTH-LOCK, C-SUPPLY-LOCK, C-MCP-ROUTE) close the documentation
   traceability loop.

## Consequences

- **Positive:** FaceTheory SSR enables server-rendered pages with preserved CSP posture, retiring
  the client-side workaround (`216fb96`). The JSON sidecar architecture separates hydration data from
  CSP delivery, maintaining the single-origin posture.
- **Negative:** Adds a FaceTheory v3.3.0+ dependency (SSR Lambda runtime, CDK construct,
  client subpath). The SSR Lambda has access to write to htmlStoreBucket (for hydration sidecars)
  but not to the web bucket (static assets). This is a one-direction trust relationship.
- **Risks:** Framework version bumps require coordinated CDK + web + CSP validation. The SEC-5
  through SEC-10 verifier suite provides deterministic CI enforcement against drift.

## Related

- Signal A resolution: `theory-cloud/FaceTheory#250` (sidecar gap), `theory-cloud/FaceTheory#248`
  (Signal C FaceTheory side), `theory-cloud/AppTheory#593` (Signal C AppTheory side)
- Upstream adoption commits: `80e8de5` (FaceTheory v3.4.0 bump), `2d2ee83` (retire `216fb96`),
  `8bf29e8` (AppTheorySsrSite adoption), `1c8d81a` (CDK deps)
- Verifiers: `gov-infra/verifiers/sec/web-csp-integrity.sh` (SEC-5), `gov-infra/verifiers/sec/web-html-inline-absence.sh` (SEC-6)
- Threat model: `gov-infra/planning/lesser-host-threat-model.md`
- Controls matrix: `gov-infra/planning/lesser-host-controls-matrix.md`
