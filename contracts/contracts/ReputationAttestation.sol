// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Ownable2Step} from "@openzeppelin/contracts/access/Ownable2Step.sol";
import {Pausable} from "@openzeppelin/contracts/utils/Pausable.sol";

/**
 * @title ReputationAttestation
 * @notice Publishes Merkle roots for off-chain reputation snapshots.
 */
contract ReputationAttestation is Ownable2Step, Pausable {
    bytes32 private _root;
    uint256 private _blockRef;
    uint256 private _count;
    uint256 private _timestamp;

    event RootPublished(bytes32 indexed root, bytes32 indexed previousRoot, uint256 blockRef, uint256 count, uint256 timestamp);

    constructor(address initialOwner) Ownable(initialOwner) {}

    function publishRoot(bytes32 root, uint256 blockRef, uint256 count) external onlyOwner whenNotPaused {
        require(root != bytes32(0), "ReputationAttestation: empty root");
        bytes32 previousRoot = _root;
        _root = root;
        _blockRef = blockRef;
        _count = count;
        _timestamp = block.timestamp;
        emit RootPublished(root, previousRoot, blockRef, count, _timestamp);
    }

    function latestRoot() external view returns (bytes32 root, uint256 blockRef, uint256 count, uint256 timestamp) {
        return (_root, _blockRef, _count, _timestamp);
    }

    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }
}
