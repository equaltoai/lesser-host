// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Interface for on-chain soul avatar renderers.
interface ISoulAvatarRenderer {
    /// @notice Render an SVG avatar for a given tokenId.
    function renderAvatar(uint256 tokenId) external view returns (string memory);

    /// @notice Renderer style display name.
    function styleName() external pure returns (string memory);
}
