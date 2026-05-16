# Theory Cloud framework feedback signals — 2026-05-16

This record closes Phase 6 of the lesser-host Theory Cloud adoption roadmap. It
captures framework-level feedback surfaced while adopting AppTheory `v1.6.0`,
TableTheory `v1.8.3`, and Greater Components `greater-v0.8.11`.

These are feedback signals only. They do **not** authorize local framework
patches, CSP loosening, trust-API auth changes, provisioning changes, or managed
release-verification changes.

## Routing summary

| Signal | Owner path | Routing status |
| --- | --- | --- |
| AppTheory SES event-source dispatch | AppTheory framework steward via Aron / Arch review | Prepared below; this host endpoint has no contactable AppTheory mailbox, so it is routed through Aron with this PR and the Arch review handoff. |
| Greater/FaceTheory fail-closed markdown rendering | Greater Components steward; FaceTheory pattern owner via Greater/Aron | Prepared below and sent to `greater.equaltoai@theorymcp.ai`; delivery ID recorded after send. |
| Greater/FaceTheory LightningCSS-safe global selectors | Greater Components steward; FaceTheory pattern owner via Greater/Aron | Prepared below and sent to `greater.equaltoai@theorymcp.ai`; delivery ID recorded after send. |

Related TableTheory release-state feedback is already recorded in
`docs/tabletheory-release-state-assessment.md`; it should be routed as a
separate TableTheory framework follow-up if Aron exposes a TheoryCloud steward
contact path.

## Framework-feedback signal: AppTheory SES event-source dispatch

### Target framework

AppTheory Lambda runtime / event workload dispatcher.

### Framework version in use

`github.com/theory-cloud/apptheory v1.6.0`.

### The concern (under host's constraints)

Host's inbound email bridge is a production communication ingress surface. It
must preserve sanitized logging, deterministic observability, and Lambda
entrypoint consistency, but AppTheory's documented event workload entrypoints
cover SQS, EventBridge, Kinesis, SNS, DynamoDB Streams, and AppSync rather than
SES receipt events. As a result, `cmd/email-ingress` keeps a direct
`lambda.Start` adapter and hand-wires the AppTheory observability record.

### The idiomatic code host would write if the framework supported it

```go
app := apptheory.New(
    apptheory.WithObservability(observability.New(emailingress.ServiceName)),
)
app.SES("email-ingress", func(ctx context.Context, event apptheory.SESEvent) error {
    workload := apptheory.NormalizeSESWorkload(ctx, event)
    return srv.HandleSESEvent(workload.Context, workload.Event)
})
lambda.Start(app.HandleLambda)
```

The concrete API shape is AppTheory-owned. The host-side requirement is that SES
receipt events receive the same request/workload correlation, Lambda request ID,
structured completion logging, and testkit ergonomics as the rest of host's
AppTheory-backed workers.

### The current workaround in host

```go
obsHooks := observability.New(emailingress.ServiceName)
obsApp := apptheory.New(
    apptheory.WithObservability(observability.New(emailingress.ServiceName)),
)
_ = obsApp

srv := emailingress.NewServer()
lambda.Start(func(ctx context.Context, event events.SimpleEmailEvent) error {
    return handleSESEvent(ctx, srv, obsHooks, event)
})
```

The workaround lives in `cmd/email-ingress/main.go`. `internal/emailingress`
then performs SES verdict checks, S3 raw-mail fetch, MIME parsing, recipient
normalization, and SQS enqueue into the comm-worker queue.

### Cost of the workaround

- Code complexity: one Lambda entrypoint uses a different dispatch shape from
  host's other AppTheory workers.
- Test burden: entrypoint behavior and completion logging need host-local tests
  rather than AppTheory event-workload testkit coverage.
- Performance impact: none known; the concern is correctness and consistency.
- Maintenance drag: AppTheory observability shape changes must be mirrored
  manually in the SES adapter.
- Governance-rubric impact: deterministic evidence still emits, but the
  hand-wired path is easier to drift from other worker evidence conventions.

### Scope of the gap

- Specific to host's constraints: communication ingress, structured evidence,
  multi-worker operational reliability.
- Likely broader: any AppTheory application consuming AWS SES receipt Lambda
  events would benefit.
- Other known consumers affected: none confirmed from host's local scope.

### Host's workaround posture

- Continue workaround while framework evolves: yes.
- Workaround is temporary / awaits framework: yes.
- Governance-rubric allows the workaround: yes; no verifier weakening needed.

### Proposed next step

AppTheory steward should scope whether SES receipt events belong in the event
workload dispatcher and testkit. Host should not patch AppTheory locally and
should revisit `cmd/email-ingress` on the next AppTheory adoption pass.

## Framework-feedback signal: Greater/FaceTheory fail-closed markdown rendering

### Target framework

Greater Components content package and FaceTheory strict-CSP rendering patterns.

### Framework version in use

Greater Components `greater-v0.8.11` / commit
`0abcb00d4466b425473476dd1b38ba628118091c`. Host does not directly pin
FaceTheory; this is a FaceTheory-adjacent UI pattern surfaced through Greater.

### The concern (under host's constraints)

Host renders AI- and user-provided markdown in the portal under strict
single-origin CSP. Because rendered markdown flows through Svelte `{@html}`,
markdown rendering must be fail-closed: sanitization must be mandatory before
HTML insertion, and any bypass must be isolated into an explicitly dangerous API
rather than a convenient component prop.

### The idiomatic code host would write if the framework supported it

```svelte
<script lang="ts">
  import { SafeMarkdownRenderer } from '@equaltoai/greater-components/content';
</script>

<SafeMarkdownRenderer
  content={message.content}
  policy="strict-csp"
  allowLinks
  openLinksInNewTab
/>
```

A helper for non-component rendering would similarly be fail-closed:

```ts
const html = renderSafeMarkdownToHtml(content, { policy: 'strict-csp' });
```

### The current workaround in host

Host keeps a local hardening on the vendored Greater markdown component:

```ts
processor.use(rehypeSanitize, buildSanitizeSchema())
```

The component still renders with `{@html renderedHtml}`, so host has a test that
asserts there is no optional sanitizer bypass and that `rehype-sanitize` is
always wired before HTML rendering (`web/src/lib/greater/content/MarkdownRenderer.svelte.test.ts`).

### Cost of the workaround

- Code complexity: host carries a modified vendored content component instead of
  consuming a first-class strict-CSP renderer unchanged.
- Test burden: host needs safety-contract tests around the vendored component.
- Performance impact: sanitizer cost is acceptable and required for the threat
  model.
- Maintenance drag: every Greater content refresh must preserve the local
  fail-closed sanitizer contract.
- Governance-rubric impact: this is CSP/security-sensitive; losing the local
  hardening would be a portal XSS regression even if CSP remains strict.

### Scope of the gap

- Specific to host's constraints: strict CSP, operator/customer portal,
  AI-generated assistant text, no inline script/style tolerance.
- Likely broader: yes; any Greater/FaceTheory consumer rendering untrusted
  markdown under strict CSP benefits from a fail-closed renderer.
- Other known consumers affected: `lesser`/`sim` may have similar UI needs, but
  no cross-repo claim is made here.

### Host's workaround posture

- Continue workaround while framework evolves: yes.
- Workaround is temporary / awaits framework: yes.
- Governance-rubric allows the workaround: yes, because tests preserve the
  sanitizer contract and CSP is not loosened.

### Proposed next step

Greater / FaceTheory stewards should scope a first-class strict-CSP markdown
renderer (or a documented strict policy mode) whose default path sanitizes before
`{@html}` and does not expose a casual bypass prop. Host should keep its local
hardening until that API exists and is adopted through a normal Greater refresh.

## Framework-feedback signal: Greater/FaceTheory LightningCSS-safe global selectors

### Target framework

Greater Components CSS distribution and FaceTheory strict-CSP stylesheet
patterns.

### Framework version in use

Greater Components `greater-v0.8.11` / commit
`0abcb00d4466b425473476dd1b38ba628118091c`.

### The concern (under host's constraints)

Host builds a static Svelte 5 SPA with Vite and strict CSP. Some vendored
Greater stylesheet selectors use Svelte-style `:global(...)` in plain CSS
(`web/src/lib/greater/primitives/theme.css`), which LightningCSS treats as a
non-standard selector form and warns about during stylesheet processing. The
warnings do not require host to loosen CSP, but they add build noise around the
same CSS layer that enforces strict-CSP-safe component APIs.

### The idiomatic code host would write if the framework supported it

```css
/* Plain distributable CSS should be standards-compatible. */
.gr-badge--sm .gr-badge__icon svg { ... }
.gr-list > li { ... }
[data-theme='high-contrast'] .gr-action-bar .gr-action-bar__button:hover:not(:disabled) { ... }
```

Svelte-only `:global(...)` selectors would stay in component-scoped Svelte style
blocks, not in plain distributed CSS consumed by Vite/LightningCSS.

### The current workaround in host

Host tolerates the warnings during Greater adoption and keeps the strict CSP
stylesheet posture intact. No inline styles, `unsafe-inline`, `unsafe-eval`, or
third-party stylesheet origins are added.

### Cost of the workaround

- Code complexity: none in host code, but the vendored CSS carries toolchain
  ambiguity.
- Test burden: build logs need human review to distinguish expected selector
  warnings from real CSP or CSS regressions.
- Performance impact: none known.
- Maintenance drag: warning noise can hide future stylesheet regressions.
- Governance-rubric impact: strict-CSP verification remains intact, but noisy
  CSS builds reduce the signal quality of UI validation evidence.

### Scope of the gap

- Specific to host's constraints: strict-CSP evidence and static Vite build.
- Likely broader: yes; any Greater consumer using LightningCSS or strict CSS
  validation benefits from standards-compatible distributed CSS.
- Other known consumers affected: not confirmed from host's local scope.

### Host's workaround posture

- Continue workaround while framework evolves: yes.
- Workaround is temporary / awaits framework: yes.
- Governance-rubric allows the workaround: yes, as long as warnings do not mask
  failing validation and CSP remains strict.

### Proposed next step

Greater / FaceTheory stewards should scope a CSS packaging cleanup that removes
Svelte-only `:global(...)` selector syntax from plain distributed CSS while
preserving component semantics and strict-CSP compatibility. Host should adopt it
through a pinned Greater refresh, not by patching Greater locally.

## Outbound coordination record

- `greater.equaltoai@theorymcp.ai`: sent 2026-05-16, delivery ID `delivery-a90022e3e4787c87`.
- AppTheory / FaceTheory framework stewards: no contactable TheoryCloud mailbox
  is exposed to this host endpoint on 2026-05-16. The AppTheory signal and
  FaceTheory pattern concerns are therefore routed through this PR, Arch review,
  and Aron handoff for manual framework-steward delivery.
