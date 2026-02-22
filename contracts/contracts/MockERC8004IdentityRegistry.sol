// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Minimal ERC-8004 identity registry mock for testing.
contract MockERC8004IdentityRegistry {
    mapping(uint256 => address) private _agentWallet;

    /// @notice Set the wallet for an agentId.
    function setAgentWallet(uint256 agentId, address wallet) external {
        _agentWallet[agentId] = wallet;
    }

    /// @notice Resolve the wallet for an agentId.
    function getAgentWallet(uint256 agentId) external view returns (address) {
        return _agentWallet[agentId];
    }
}
