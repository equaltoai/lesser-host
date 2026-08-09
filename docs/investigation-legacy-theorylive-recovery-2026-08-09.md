# Investigation: Legacy TheoryLive agent recovery

## Reported symptom

Body reported that Silas Vane, Della Marlowe, Iris Okonkwo, and Mags Doyle are active TheoryLive hosted identities with matching Lesser bindings, but they predate Body's current registry/content projections. Body cannot consistently discover or revise them and must not invent genesis identity, provenance, or revision history. Silas's advertised Host registration URI returns `404`.

## Dimensions

- Surface: Control plane Soul registry and hosted-genesis instance read APIs
- Lambda: `control-plane-api`
- Tenant context: Managed instance Slug `theory`; domain `theory.greater.website`; raw Instance API keys were not read or logged
- On-chain context: none; all four use `instance_trust` and `hosted_bound_soul`
- Release context: Host live v1.6.3; this is legacy state, not a consumer-release-verification failure
- Gov-infra context: the existing `soul-integrity-scan-m2` missed one inconsistent-state shape
- Recent deploys: v1.6.3 was already live before this read-only investigation

## Specialist elevation check

- `evolve-soul-registry`: required because recovery reads off-chain Soul registry and hosted-genesis state.
- `audit-trust-and-safety`: required because the proposed read must preserve InstanceKey `sha256(raw_key)` authentication and strict tenant bounds.
- No Solidity, Mint-signer, Safe-ready payload, CSP, Trust API, provisioning, or Managed update change is implicated.

## What is definitely true

Read-only consistent DynamoDB and S3 reads on 2026-08-09 established:

| Agent | Host identity | Promotion | Version history | Registration artifact | Hosted-genesis source |
| --- | --- | --- | --- | --- | --- |
| Della Marlowe (`0x57d1…65c3`) | active, self-description version 2 | graduated, published version 2 | v1 → v2 SHA chain intact | current and both versioned objects match recorded SHA256 values | legacy exact `producedDeclarations` plus tenant/registration/conversation binding |
| Iris Okonkwo (`0x1838…defd`) | active, self-description version 2 | graduated, published version 2 | v1 → v2 SHA chain intact | current and both versioned objects match recorded SHA256 values | legacy exact `producedDeclarations` plus tenant/registration/conversation binding |
| Mags Doyle (`0xb534…9405`) | active, self-description version 1 | graduated, published version 1 | v1 record intact | current and versioned objects match the recorded SHA256 | legacy exact `producedDeclarations` plus tenant/registration/conversation binding |
| Silas Vane (`0xed00…ee86`) | active, self-description version 1 | graduated, published version 1 | no `SoulAgentVersion` row | no current object, versioned object, prior S3 version, or delete marker | legacy exact `producedDeclarations` plus tenant/registration/conversation binding |

Additional facts:

- All four legacy declaration blobs decode to the same bounded declaration family: `selfDescription`, `capabilities`, `boundaries`, and `transparency`.
- For Della, Iris, and Mags, the public version-1 registration contains byte-equivalent semantic `selfDescription`, `capabilities`, and `transparency` values. Published boundaries are a publication transformation that adds version/signing metadata; the public registration is therefore not a lossless copy of the original declaration payload.
- The four `HostedGenesisSession` rows predate the typed candidate/publication-checkpoint contract. They retain tenant, registration, agent, conversation, turn, and completion bindings, but no typed candidate hash or publication checkpoint.
- Existing exact instance reads can perform bounded `declaration_ready` → `published` convergence when exact version evidence exists. That convergence does not republish or increment a Soul version, but it can omit `produced_declarations` from the resulting published response and therefore is not an adequate migration-read contract.
- The current public registration handler reads only the current S3 key. It correctly returns `404` for Silas because no artifact exists.
- S3 bucket versioning is enabled. The absence of every Silas object version/delete marker makes deletion an unsupported explanation; no original registration bytes were found.
- `soul-integrity-scan-m2` incorrectly returned green for Silas because it gated both missing-history and missing-current findings on `maxVersion > 0`. Commit `3fe4426` fixes the scan test-first so a positive identity self-description version with zero history fails closed.

## Expected versus actual

Expected: every active identity with self-description version `N > 0` has an exact `VERSION#N` record, checksum-matching versioned object, and checksum-matching current registration projection. Legacy recovery reads should return exact source declarations and honest provenance without replay or publication side effects.

Actual: Della, Iris, and Mags satisfy registration integrity but lack a purpose-built adoption projection. Silas advertises a missing registration and lacks the immutable publication evidence required to recreate the alleged original artifact honestly. Exact legacy declarations remain available.

## Fix-locus verdict

- **Host:** provide a tenant-bounded, InstanceKey-authenticated, read-only recovery inventory/detail contract; expose exact legacy declarations without messages; label provenance and integrity state explicitly; keep replay/finalize/publication out of the recovery path; keep the strengthened integrity scan.
- **Body:** call Host directly through its existing managed `LESSER_HOST_INSTANCE_KEY_ARN` credential path, then implement additive, operator-gated adoption into Body registry/content projections. Body must not write Host or Lesser state.
- **Lesser:** no recovery-read dependency. Lesser binding state remains authoritative in Lesser and is not written by Host or Body; existing Lesser-mediated private self-scope authorization remains separate from InstanceKey authority.
- **On-chain:** no change.

## Hypotheses (ranked)

1. **Silas was advanced by a legacy partial-finalization or repair path that did not create registration history/artifacts.** Evidence: the conversation completed much earlier than the identical identity/promotion update timestamp; identity and promotion claim version 1 while version/S3 evidence is entirely absent. Against: current code writes the version record before S3 and identity advancement, so the exact historical code/operation remains unidentified.
2. **An original Silas artifact was deleted.** Evidence for: the URI now returns `404`. Evidence against: S3 versioning is enabled and there is no version or delete marker; DynamoDB has no version record. This is unlikely.
3. **Body can recover losslessly from public registration alone.** Evidence for: current public artifacts contain final public self-description data for three agents. Evidence against: public files omit Host registration/conversation provenance and transform boundaries; Silas has no artifact. This hypothesis is false for the requested migration guarantee.

## Verification step

Implement a pure recovery-source selector against fixtures representing all four states, then run it against read-only live metadata. It must deterministically classify Silas as `legacy_declarations_only` and the other three as `published_artifact_verified`, without reading messages, mutating a session, republishing, or incrementing a version.

## Proposed next skill

`scope-need` → `evolve-soul-registry` + `audit-trust-and-safety` → `enumerate-changes` → `plan-roadmap`.
