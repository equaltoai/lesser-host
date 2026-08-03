# Dependency security residuals

This file records npm security findings that cannot currently be resolved by a
published dependency version. A residual stays open until its upstream chain
offers a patched release; it is not a waiver of the Gov-infra rubric or npm
security checks.

## `elliptic` — GHSA-848j-6mx2-7j84

- **Status:** no patched release is published. The npm registry's latest
  `elliptic` release is `6.6.1`, while the advisory affects `<=6.6.1` and npm
  reports `fixAvailable: false`.
- **Reachability:** development-only contract verification tooling:
  `@nomicfoundation/hardhat-verify@3.0.20` -> `@ethersproject/abi@5.8.0` ->
  `@ethersproject/hash@5.8.0` -> `@ethersproject/abstract-signer@5.8.0` ->
  `@ethersproject/abstract-provider@5.8.0` ->
  `@ethersproject/transactions@5.8.0` ->
  `@ethersproject/signing-key@5.8.0` -> `elliptic@6.6.1`.
- **Disposition:** retain the exact resolved version rather than inventing an
  invalid override. Re-evaluate when `elliptic` publishes a patched release or
  `@nomicfoundation/hardhat-verify` removes the dependency chain.

