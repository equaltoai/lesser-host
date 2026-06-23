# Live custom-domain deploy guard runbook

## Purpose

The live `lesser.host` stack must always preserve the canonical first-party Host domain. A live deploy is unsafe if the
synthesized CloudFormation artifact would remove the `lesser.host` CloudFront alias, ACM viewer certificate, Route53 A /
AAAA records, or runtime URLs.

## Default live domain resolution

Normal live synth/deploy must resolve the canonical hosted zone by domain and synthesize `lesser.host` by construction.
Operators should not need `cdk/cdk.context.local.json` or an explicit `-c webHostedZoneId=...` override for the standard
live path. With the real deploy AWS profile available, `scripts/app-theory-cdk.sh up live` performs a non-mutating
Route53 hosted-zone lookup for `lesser.host` during synth and then validates the resulting template before deploy.

Explicit `webHostedZoneId` / `webHostedZoneName` context remains available only for diagnostics and deterministic tests.
Do not commit operational Route53 values. A hosted-zone id is not a secret, but the preferred live behavior is domain
lookup under the deploy profile rather than hidden operator-local context.

Lab/open-source behavior remains intentional: non-live stages do not default to the live apex domain, and the validator
skips non-live stages so a lab template without hosted-zone context can continue to use a CloudFront distribution URL.

## What the guard checks

`scripts/app-theory-cdk.sh up live` synthesizes a fresh Cloud Assembly and then runs
`scripts/validate-live-domain-template.mjs live <template>` before `cdk deploy`. The validator inspects the synthesized
template artifact and fails before CloudFormation mutation unless all of the following are true:

- the CloudFront distribution has alias `lesser.host`;
- the viewer certificate is ACM-backed, not the CloudFront default certificate;
- Route53 apex A and AAAA records for `lesser.host` are present;
- every live Lambda `PUBLIC_BASE_URL` value in the template is `https://lesser.host`;
- the `WebUrl` output is `https://lesser.host`.

If the guard fails, do not work around it by creating hidden local context. Treat the failure as evidence that CDK domain
resolution is broken or the AWS profile/lookup path is unavailable, then fix the resolution path before any live deploy.

## Non-mutating proof commands

These commands synthesize and validate artifacts only. They do not deploy.

```bash
# Preferred live proof under the real deploy profile; do not set a CDK timeout.
LESSER_HOST_CDK_DRY_RUN=1 AWS_PROFILE=Lesser scripts/app-theory-cdk.sh up live
```

Expected result: success. The wrapper runs the hosted genesis guard, deploy-template placeholder guard, live custom-domain
guard, and CloudFormation dependency-cycle guard, then stops before `cdk deploy`.

For deterministic local proof without AWS lookup, run the CDK tests. They inject a mocked CDK hosted-zone lookup result
for `lesser.host` and then validate the synthesized template artifact with the same guard:

```bash
cd cdk
npm test
```

For one-off diagnostics only, an explicit context override can still be used with a fake test hosted-zone id:

```bash
cd cdk
npm run build
npx cdk synth \
  -c stage=live \
  -c webHostedZoneId=ZEXAMPLELESSERHOST \
  -c webHostedZoneName=lesser.host \
  --output .build/live-domain
node ../scripts/validate-live-domain-template.mjs live .build/live-domain/lesser-host-live.template.json
```

Expected result: success. `ZEXAMPLELESSERHOST` is a fake test value for local proof only; do not put operational values in
git.

Actual live deploys remain operator-authorized only. Never set a timeout on a CDK deploy.

## Incident encoded by this guard

On 2026-06-23, a live deploy was run after the gitignored operator-local context was missing. Because `webHostedZoneId`
was absent, the live template omitted the custom-domain resources and CloudFormation deleted `WebAliasA`,
`WebAliasAAAA`, and `WebCertificate`. Route53 apex had only NS/SOA records, CloudFront had `Aliases.Quantity = 0` with
`CloudFrontDefaultCertificate = true`, and Sim SSR failed DNS lookup for `lesser.host`. The corrected requirement is that
normal live synth resolves and uses `lesser.host` without hidden local context, with this guard retained as a backstop.
