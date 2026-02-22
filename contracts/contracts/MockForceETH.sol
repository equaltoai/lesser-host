// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Test helper that can self-destruct to force-send ETH.
contract MockForceETH {
    // solhint-disable-next-line no-empty-blocks
    constructor() payable {}

    /// @notice Self-destruct and force-send ETH to the target address.
    function destroyAndSend(address payable to) external {
        selfdestruct(to);
    }
}
