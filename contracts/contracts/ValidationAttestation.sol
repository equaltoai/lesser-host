// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Ownable2Step} from "@openzeppelin/contracts/access/Ownable2Step.sol";

/**
 * @title ValidationAttestation
 * @notice Publishes Merkle roots for off-chain validation result snapshots.
 */
contract ValidationAttestation is Ownable2Step {
    bytes32 private _root;
    uint256 private _blockRef;
    uint256 private _count;
    uint256 private _timestamp;

    event RootPublished(bytes32 indexed root, uint256 blockRef, uint256 count, uint256 timestamp);

    constructor(address initialOwner) Ownable(initialOwner) {}

    function publishRoot(bytes32 root, uint256 blockRef, uint256 count) external onlyOwner {
        _root = root;
        _blockRef = blockRef;
        _count = count;
        _timestamp = block.timestamp;
        emit RootPublished(root, blockRef, count, _timestamp);
    }

    function latestRoot() external view returns (bytes32 root, uint256 blockRef, uint256 count, uint256 timestamp) {
        return (_root, _blockRef, _count, _timestamp);
    }
}

