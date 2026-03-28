This directory vendors a patched `aws-cdk-lib@2.245.0` tarball until AWS publishes an upstream release that no longer bundles `brace-expansion@5.0.3`.

Artifact:
- `aws-cdk-lib-2.245.0-brace-expansion-5.0.5.tgz`

Provenance:
- Source package: `aws-cdk-lib@2.245.0` from the public npm registry
- Replacement package: `brace-expansion@5.0.5` from the public npm registry
- Patch date: `2026-03-28`
- SHA-256: `2a9c940ab9038874291b7e949c13d35d3b7e46a5164596295b48e39aedd39285`

Why this exists:
- `npm audit fix` cannot remediate the advisory because `brace-expansion` is bundled inside the published `aws-cdk-lib` tarball.
- As of `2026-03-28`, `2.245.0` is still the latest `aws-cdk-lib` release on npm.

Retirement plan:
- Replace the file dependency with the next upstream `aws-cdk-lib` release once it ships a non-vulnerable bundled copy.
