# Agent ID Derivation Test Vectors (M0)

This document is a conformance target for `normalizedDomain`, `normalizedLocalAgentId`, and `agentId`.

## Normalization (normative)

### `normalizedDomain`

`normalizedDomain` MUST follow the exact rules described in `docs/adr/0002-canonical-identifiers-and-signatures.md`
and mirror `lesser-host/internal/domains.NormalizeDomain` (tip registry normalization).

### `normalizedLocalAgentId`

`normalizedLocalAgentId` MUST follow the exact rules described in
`docs/adr/0002-canonical-identifiers-and-signatures.md`.

## Derivation (normative)

`agentId = uint256(keccak256(abi.encodePacked(normalizedDomain, "/", normalizedLocalAgentId)))`

Equivalently, compute Keccak-256 over the UTF-8 bytes of:

`"${normalizedDomain}/${normalizedLocalAgentId}"`

## Test vectors (EXACT)

1)
- `rawDomain`: `" Example.Lesser.Social. "`
- `rawLocal`: `"agent-alice"`
- `normalizedDomain`: `"example.lesser.social"`
- `normalizedLocal`: `"agent-alice"`
- `agentId`: `0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab`

2)
- `rawDomain`: `"münich.example"`
- `rawLocal`: `"agent-alice"`
- `normalizedDomain`: `"xn--mnich-kva.example"`
- `normalizedLocal`: `"agent-alice"`
- `agentId`: `0xf0b2c505271215e7bbbac618dc24f69de8aff1207d880d9c10b0779e7ce1b5e3`

3)
- `rawDomain`: `"例え.テスト"`
- `rawLocal`: `"agent-alice"`
- `normalizedDomain`: `"xn--r8jz45g.xn--zckzah"`
- `normalizedLocal`: `"agent-alice"`
- `agentId`: `0x4744283784f8b135533d6b699c52ad842588b0c418c21a4a7c778df201572565`

4)
- `rawDomain`: `"dev.EXAMPLE.com"`
- `rawLocal`: `"  @Agent-Bob  "`
- `normalizedDomain`: `"dev.example.com"`
- `normalizedLocal`: `"agent-bob"`
- `agentId`: `0xf5e2da2896de9116a9463270defab5abd70be7be4722f57fd841079ded2c6cf6`

5)
- `rawDomain`: `"stage.Dev.Example.Com."`
- `rawLocal`: `"soul_researcher"`
- `normalizedDomain`: `"stage.dev.example.com"`
- `normalizedLocal`: `"soul_researcher"`
- `agentId`: `0x803682e2e7629f07fe1c65670bf29bf19691a339e0252001a48737cfe22dd9f5`

## Negative cases (expected rejection)

These must be rejected by normalization/validation (exact error messages are implementation-defined):

- `rawDomain="https://example.com"` (scheme)
- `rawDomain="example.com:443"` (port)
- `rawDomain="user@example.com"` (credentials delimiter)
- `rawDomain="*.example.com"` (wildcard)
- `rawLocal="agent/alice"` (slash)
- `rawLocal="agent:alice"` (colon)
- `rawLocal="alice@example.com"` (`@` after normalization)
