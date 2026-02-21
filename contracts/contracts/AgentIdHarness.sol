// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/**
 * @title AgentIdHarness
 * @notice Test-only harness for the lesser-soul agentId derivation formula.
 *
 * Per `docs/adr/0002-canonical-identifiers-and-signatures.md`, agentId is derived off-chain as:
 *   uint256(keccak256(abi.encodePacked(normalizedDomain, "/", normalizedLocalAgentId)))
 *
 * Contracts treat agentId as an opaque uint256 input; this harness exists to
 * prove the Solidity implementation matches the published test vectors.
 */
contract AgentIdHarness {
    function deriveAgentId(string calldata normalizedDomain, string calldata normalizedLocalAgentId) external pure returns (uint256) {
        return uint256(keccak256(abi.encodePacked(normalizedDomain, "/", normalizedLocalAgentId)));
    }

    function deriveAgentIdBytes32(
        string calldata normalizedDomain,
        string calldata normalizedLocalAgentId
    ) external pure returns (bytes32) {
        return keccak256(abi.encodePacked(normalizedDomain, "/", normalizedLocalAgentId));
    }
}
