// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface ISoulAvatarRenderer {
    function renderAvatar(uint256 tokenId) external view returns (string memory);
    function styleName() external pure returns (string memory);
}
