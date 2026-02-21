// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

contract MockERC8004IdentityRegistry {
    mapping(uint256 => address) private _agentWallet;

    function setAgentWallet(uint256 agentId, address wallet) external {
        _agentWallet[agentId] = wallet;
    }

    function getAgentWallet(uint256 agentId) external view returns (address) {
        return _agentWallet[agentId];
    }
}

