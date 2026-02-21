// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

contract MockForceETH {
    // solhint-disable-next-line no-empty-blocks
    constructor() payable {}

    function destroyAndSend(address payable to) external {
        selfdestruct(to);
    }
}
