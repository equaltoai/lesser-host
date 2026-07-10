# Investigation: hosted/off-chain conversation reads require a SoulRegistry contract

- Date: 2026-07-09 (evidence collected through 2026-07-10 UTC)
- Status: root cause confirmed
- Fix locus: `lesser-host`

## Reported symptom

`GET /api/v1/soul/instance/agents/{agentId}/mint-conversations` returns:

```text
409 soul_mint.conflict: soul registry is not configured
```

The affected identity is a valid hosted/off-chain Soul. Removing the failed conversation from Lesser, and later
removing its bounded compatibility state from Host, did not change the response.

## Dimensions

- Surface: Control plane / Soul registry hosted-genesis reads
- Lambda: `control-plane-api`
- Tenant context: one managed instance; identifiers are omitted or hashed below
- On-chain context: neither affected endpoint makes an on-chain call
- Live deployment observed during investigation:
  - stack `lesser-host-live`, `UPDATE_COMPLETE`
  - latest update completed 2026-07-09T23:58:22Z
  - `lesser-host-live-control-plane-api` active and last modified 2026-07-09T23:54:31Z
- Live Soul configuration at reproduction time:
  - `SOUL_ENABLED=true`
  - `SOUL_CHAIN_ID=8453`
  - empty `SOUL_RPC_URL_SSM_PARAM`
  - empty Registry, ReputationAttestation, and ValidationAttestation contract addresses

## Specialist elevation check

- Soul registry: elevate through `evolve-soul-registry`
- Instance API key and tenant boundary: focused `audit-trust-and-safety` walk required
- Gov-infra rubric: no policy defect identified
- Provisioning / Consumer release verification: not implicated
- Framework: no AppTheory or TableTheory defect identified

## What is definitely true

### 1. The shared agent-read context rejects before authentication or data lookup

The routes are registered at `internal/controlplane/server.go:315-316`:

- `GET /api/v1/soul/instance/agents/{agentId}/mint-conversations`
- `GET /api/v1/soul/instance/agents/{agentId}/mint-conversations/{conversationId}`

Both call `requireMintConversationInstanceReadContext`. At
`internal/controlplane/handlers_soul_mint_conversation_instance_read.go:98-104`, that helper first invokes
`requireSoulRegistryConfigured()`.

`internal/controlplane/handlers_soul_config.go:44-58` requires all of the following:

- an initialized Store;
- `SoulEnabled=true`;
- a positive Soul chain ID; and
- a nonempty valid EVM SoulRegistry address.

The helper returns before InstanceKey authentication, agent parsing, identity lookup, tenant-bound access checks, or
conversation/session reads. Its `app.conflict` maps to the observed `409 soul_mint.conflict`.

### 2. Live logs prove the exact path

CloudWatch records repeated agent-scoped list failures with:

- route class `mint_conversation_list`;
- status `409`;
- error code `soul_mint.conflict`;
- duration `0 ms`; and
- an empty instance-slug hash because the guard returns before the InstanceKey is loaded.

Adjacent registration-scoped hosted conversation reads using the same InstanceKey returned `200` and populated the
instance-slug hash. This rules out routing, key validity, and tenant ownership as the cause of the reported 409.

### 3. Hosted/off-chain is intentionally contract-independent

`docs/adr/0007-hosted-identity-and-ens-invariants.md` makes Host state authoritative for hosted identity and states that
SoulRegistry mint state is optional assurance. `docs/soul-agent-first-client-contract.md` likewise defines
`hosted_offchain` as usable and publishable before an on-chain receipt exists.

The implementation already follows that rule for registration-scoped begin/create/read/complete/recover. Commit
`ccbabbe0459` removed the same full-registry guard from `requireSoulInstanceBootstrapContext`, replacing it with Store,
strict InstanceKey authentication, and `SoulEnabled` checks. Empty-contract tests cover that family while retaining a
full contract requirement for a genuinely on-chain signing preflight.

### 4. This is a latent and incompletely migrated guard

- `ed0d9c0a` introduced the agent-scoped reads and the unconditional contract guard on 2026-05-11.
- ADR 0007 subsequently codified contract-independent hosted identity.
- `ccbabbe0459` corrected registration-scoped hosted bootstrap on 2026-06-27 but missed the older agent-read helper.
- `7b82120c` later moved the list endpoint to durable `HostedGenesisSession` state while reusing the stale helper.

The current test fixture always supplies chain `1` and a dummy valid Registry address. Agent list/get tests never clear
those values, so the suite does not exercise the supported hosted/off-chain configuration.

### 5. Tenant isolation is currently preserved and must remain unchanged

After the erroneous guard, the read context still performs the correct controls:

- bearer token is looked up only as `sha256(raw_key)`;
- missing, unknown, or revoked keys return `401`;
- the identity must belong to a domain verified for the authenticated instance;
- list lookup is qualified by instance Slug and agent ID;
- single lookup is qualified by instance Slug and separately verifies agent ID;
- the list projection excludes messages and produced declarations;
- response-size limits, rate limits, hashed audit identifiers, and structured audit events are enforced.

The proper fix removes no authentication or isolation control. It removes only the unrelated on-chain prerequisite.
It also stops unauthenticated callers receiving a pre-authentication configuration oracle: they will again reach the
normal `401` path.

### 6. Conversation cleanup cannot affect this response

No conversation record is read before the registry guard fails. Stale or deleted conversation state therefore cannot
cause or resolve this 409.

### 7. The actual response violates the published route contract

The two OpenAPI operations in `docs/contracts/openapi.yaml` document `200`, validation/auth/boundary failures, bounded
response failures, rate limiting, and internal errors. They do not document a `409`, because an EVM Registry is not a
semantic prerequisite for these reads.

### 8. Restoring the recovered mainnet contracts is independent

Read-only AWS, RPC, explorer, and Safe-history verification established a coherent Ethereum mainnet deployment:

- chain ID `1`;
- SoulRegistry `0x60FBa71F84BD613118D38F7d0375c36693dAecbA`;
- ReputationAttestation `0xE690D736B2c84D550F07aF60cDe1bC9e742C8a9F`;
- ValidationAttestation `0x45c50CD0DA080Ae8F934CAD21a9fE30A0fe1aAF4`;
- owner Safe `0xfE63333F303D4f7b2354f7E3eca752C812D65907`, currently 2-of-2;
- Registry unpaused, mint fee `500000000000000` wei, claim window `0`;
- the live Mint-signer SecureString derives the Registry's configured signer, which is an authorized attestor;
- the three runtime contracts contain code and are source-verified;
- executed Safe history proves renderer, fee, attestor, and Mint-signer setup.

A read-only scan of all 233 current live table items found one Soul identity, which is `hosted_offchain`, has no token ID
or mint transaction hash, and has no `SoulOperation`. There is no existing immutable-on-chain reference requiring a
chain migration before a future configuration cutover.

Reconnecting those contracts is valid operational work, but it must not replace the empty-contract regression fix.

### 9. Evidence provenance for the recovered mainnet preflight

The mainnet conclusions above are reproducible from the following bounded sources; no secret value is included:

- local recovered record: `docs/deployments/mainnet/latest.recovered-2026-07-06.json`;
- live AWS reads through profile `Lesser`: Lambda configuration, SSM parameter metadata, CloudFormation state, and a
  projection-only DynamoDB scan;
- read-only JSON-RPC at Ethereum block `25499229`: chain ID, bytecode/code hashes, contract owners/paused state,
  Registry fee/claim window/Mint-signer/attestor, renderer events, and Safe owners/threshold;
- source verification pages for
  [SoulRegistry](https://etherscan.io/address/0x60FBa71F84BD613118D38F7d0375c36693dAecbA#code),
  [ReputationAttestation](https://etherscan.io/address/0xE690D736B2c84D550F07aF60cDe1bC9e742C8a9F#code), and
  [ValidationAttestation](https://etherscan.io/address/0x45c50CD0DA080Ae8F934CAD21a9fE30A0fe1aAF4#code); and
- executed 2-of-2 Safe setup transaction
  [`0x44996f3f...0504928`](https://etherscan.io/tx/0x44996f3f45b41eaaf0a2b01bccdacec8dfe14dc9fd5090a73d34db70b0504928),
  corroborated by the Safe Transaction Service recovery file under `/tmp/safe-history-20260706T163922Z/`.

The alternate mainnet RPC parameter was tested in-process without printing its SecureString and returned chain ID `1`
and current blocks. The Mint-signer parameter was likewise handled only in-process; only its derived public address and
the boolean on-chain match were emitted. These checks must be repeated immediately before a future live deployment and
captured in that deployment's durable Evidence.

## Fix-locus verdict

Fix here in Host. This is not a Lesser, tenant-data, AppTheory/TableTheory, external-provider, or on-chain-state defect.

## Hypotheses, ranked

1. **Confirmed: the unconditional full-registry guard causes the 409.** Live configuration, logs, code, and history all
   match this path.
2. **Trigger only: the live Registry address is empty.** Empty contract configuration is supported for
   `hosted_offchain`; configuring a contract masks but does not correct the defect.
3. **Rejected: stale failed-conversation state.** The guard returns before any session or compatibility-row query.
4. **Rejected: invalid InstanceKey or tenant mismatch.** Those checks occur later and map to `401` or `403`; adjacent
   registration-scoped calls with the same key succeed.
5. **Rejected: framework or routing defect.** Requests reach the intended handler and emit its exact typed error.

## Verification step

Add failing tests first for agent-scoped list and single GET with:

- `SoulEnabled=true`;
- `SoulChainID=0`; and
- empty `SoulRegistryContractAddress`.

After the guard split, both must return `200`. Existing invalid/revoked-key and cross-tenant tests must remain `401` and
`403`, and a genuinely on-chain operation must still reject missing Registry/RPC configuration.

## Proposed next skill

`scope-need`, followed by `evolve-soul-registry` and a focused `audit-trust-and-safety` walk, then
`enumerate-changes` for implementation.
