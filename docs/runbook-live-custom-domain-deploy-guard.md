# Live custom-domain deploy guard runbook

## Purpose

The live `lesser.host` stack must always preserve the canonical first-party Host domain. A live deploy is unsafe if the
synthesized CloudFormation artifact would remove the `lesser.host` CloudFront alias, ACM viewer certificate, Route53 A /
AAAA records, or runtime URLs.

## Required operator-local context

Live deploys require operator-local CDK context in `cdk/cdk.context.local.json`. Do not commit that file. At minimum, the
file must provide a real `webHostedZoneId` for the `lesser.host` hosted zone, with `webHostedZoneName` left as or set to
`lesser.host`.

Safe shape:

```json
{
  "context": {
    "webHostedZoneId": "<operator-local-lesser-host-zone-id>",
    "webHostedZoneName": "lesser.host"
  }
}
```

The repository intentionally keeps `webHostedZoneId` empty in `cdk/cdk.json` and
`cdk/cdk.context.local.json.example` so private Route53 values do not enter git.

## What the guard checks

`scripts/app-theory-cdk.sh up live` synthesizes a fresh Cloud Assembly and then runs
`scripts/validate-live-domain-template.mjs live <template>` before `cdk deploy`. The validator inspects the synthesized
template artifact and fails before CloudFormation mutation unless all of the following are true:

- the CloudFront distribution has alias `lesser.host`;
- the viewer certificate is ACM-backed, not the CloudFront default certificate;
- Route53 apex A and AAAA records for `lesser.host` are present;
- every live Lambda `PUBLIC_BASE_URL` value in the template is `https://lesser.host`;
- the `WebUrl` output is `https://lesser.host`.

Lab/open-source behavior remains intentional: the validator skips non-live stages, so a lab template without
operator-local hosted-zone context can continue to use a CloudFront distribution URL.

## Non-mutating proof commands

These commands synthesize and validate artifacts only. They do not deploy.

```bash
cd cdk
npm run build
npx cdk synth -c stage=live --output .build/live-nodomain
node ../scripts/validate-live-domain-template.mjs live .build/live-nodomain/lesser-host-live.template.json
```

Expected result: failure. The error names `cdk/cdk.context.local.json` and `webHostedZoneId`.

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

For the AppTheory wrapper preflight without CloudFormation mutation:

```bash
LESSER_HOST_CDK_DRY_RUN=1 AWS_PROFILE=<operator-profile> theory app up --stage live --execute
```

Actual live deploys remain operator-authorized only. Never set a timeout on a CDK deploy.

## Incident encoded by this guard

On 2026-06-23, a live deploy was run after the gitignored operator-local context was missing. Because `webHostedZoneId`
was absent, the live template omitted the custom-domain resources and CloudFormation deleted `WebAliasA`,
`WebAliasAAAA`, and `WebCertificate`. Route53 apex had only NS/SOA records, CloudFront had `Aliases.Quantity = 0` with
`CloudFrontDefaultCertificate = true`, and Sim SSR failed DNS lookup for `lesser.host`.
