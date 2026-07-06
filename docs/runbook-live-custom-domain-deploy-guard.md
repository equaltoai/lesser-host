# Live custom-domain deploy guard runbook

## Purpose

The live `lesser.host` stack must always preserve the canonical first-party Host domain. A live deploy is unsafe if the
synthesized CloudFormation artifact would remove the `lesser.host` CloudFront alias, ACM viewer certificate, Route53 A /
AAAA records, or runtime URLs.

## AppTheory app.json domain resolution

Normal synth/deploy reads the active stage's web domain binding from `app-theory/app.json` under
`lesserHost.webDomain.<stage>.{rootDomain,hostedZoneId,hostedZoneName}`. That makes the AppTheory app-up contract/config
itself the source of truth for host's web custom-domain values while preserving the theory-cli schema-1 deploy path:
`theory app up/down` still reads `app-theory/app.json`, substitutes only `{{AWS_PROFILE}}` and `{{STAGE}}` in
`cdk.up/down`, and executes the declared command from `cdk.dir`.

Do not put web domain values in `cdk/cdk.json`, `cdk/cdk.context.local.json`, environment variables, CLI context
overrides, or sidecar files. The CDK stack uses `HostedZone.fromHostedZoneAttributes` with the stage config parsed from
`app-theory/app.json`; it does not perform a Route53 lookup. If the file, active stage entry, root domain, hosted-zone
name, or hosted-zone id is missing/invalid/placeholder-like, synth fails closed before any CloudFormation mutation.

Lab and live use the same AppTheory app.json mechanism. The live validator still skips non-live stages because only live
must preserve the canonical `lesser.host` apex, but lab synth also requires a valid app.json domain entry.

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
resolution is broken or the AppTheory app.json web-domain config is invalid or unavailable, then fix the resolution path before any live deploy.

## Non-mutating proof commands

These commands synthesize and validate artifacts only. They do not deploy.

```bash
# Preferred live proof under the real deploy profile; do not set a CDK timeout.
LESSER_HOST_CDK_DRY_RUN=1 AWS_PROFILE=Lesser scripts/app-theory-cdk.sh up live
```

Expected result: success. The wrapper runs the hosted genesis guard, deploy-template placeholder guard, live custom-domain
guard, and CloudFormation dependency-cycle guard, then stops before `cdk deploy`.

For deterministic local proof without AWS lookup, run the CDK tests. They inject a temporary `app-theory/app.json`
fixture and then validate the synthesized template artifact with the same guard:

```bash
cd cdk
npm test
```

For one-off synth proof, use the committed `app-theory/app.json` stage entry and synthesize without deploying:

```bash
cd cdk
npm run build
npx cdk synth \
  -c stage=live \
  --output .build/live-domain \
  --quiet
node ../scripts/validate-live-domain-template.mjs live .build/live-domain/lesser-host-live.template.json
```

Expected result: success. Do not substitute web domain values through CDK context or environment variables.

Actual live deploys remain operator-authorized only. Never set a timeout on a CDK deploy.

## Incident encoded by this guard

On 2026-06-23, a live deploy was run after the then-current gitignored operator-local hosted-zone context was missing.
Because that hosted-zone binding was absent, the live template omitted the custom-domain resources and CloudFormation deleted `WebAliasA`,
`WebAliasAAAA`, and `WebCertificate`. Route53 apex had only NS/SOA records, CloudFront had `Aliases.Quantity = 0` with
`CloudFrontDefaultCertificate = true`, and Sim SSR failed DNS lookup for `lesser.host`. The corrected requirement is that
normal live synth fails closed unless `app-theory/app.json` provides the live hosted-zone attributes, with this
guard retained as a backstop.
