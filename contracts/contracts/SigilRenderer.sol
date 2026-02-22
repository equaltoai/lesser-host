// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ISoulAvatarRenderer} from "./ISoulAvatarRenderer.sol";
import {SoulPRNG} from "./SoulPRNG.sol";
import {SoulSVGUtils} from "./SoulSVGUtils.sol";

/// @title SigilRenderer
/// @notice Generates bilateral-symmetry sigil SVG avatars on-chain from tokenId.
contract SigilRenderer is ISoulAvatarRenderer {
    struct Grid {
        uint256 gridSz;
        uint256 cellSz;
        uint256 margin;
        string fg;
        string bg;
    }

    /// @notice Renderer style display name.
    function styleName() external pure override returns (string memory) {
        return "Sigil";
    }

    /// @notice Render an SVG avatar for a given tokenId.
    function renderAvatar(uint256 tokenId) external view override returns (string memory) {
        SoulPRNG.State memory st = SoulPRNG.seed(SoulSVGUtils.toString(tokenId));

        Grid memory g;
        uint256 gridIdx;
        (gridIdx, st) = SoulPRNG.randomChoice(st, 4);
        g.gridSz = gridIdx + 4;
        g.cellSz = 140 / g.gridSz;
        g.margin = (200 - 140) / 2;

        uint256 hue;
        (hue, st) = SoulPRNG.randomInt(st, 0, 360);

        uint256 isDarkRand;
        (isDarkRand, st) = SoulPRNG.randomInt(st, 0, 2);
        string memory hueStr = SoulSVGUtils.toString(hue);

        if (isDarkRand == 1) {
            g.bg = "#111";
            g.fg = string.concat("hsl(", hueStr, ",80%,65%)");
        } else {
            g.bg = "#eee";
            g.fg = string.concat("hsl(", hueStr, ",80%,35%)");
        }

        string memory rects;
        (rects, st) = _buildRects(st, g);

        string memory decorations;
        (decorations, st) = _buildDecorations(st, g);

        return string.concat(
            '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200" style="background-color:', g.bg, '">'
            '<g fill="', g.fg, '">',
            rects,
            '</g>',
            decorations,
            '</svg>'
        );
    }

    function _buildRects(SoulPRNG.State memory st, Grid memory g) private pure returns (string memory result, SoulPRNG.State memory) {
        result = "";
        uint256 numCols = (g.gridSz + 1) / 2;
        for (uint256 y = 0; y < g.gridSz; y++) {
            for (uint256 x = 0; x < numCols; x++) {
                uint256 fillRand;
                (fillRand, st) = SoulPRNG.randomInt(st, 0, 1000);
                if (fillRand > 400) {
                    result = string.concat(result, _rectStr(g.margin + x * g.cellSz, g.margin + y * g.cellSz, g.cellSz + 1));
                    uint256 mx = g.gridSz - 1 - x;
                    if (mx != x) {
                        result = string.concat(result, _rectStr(g.margin + mx * g.cellSz, g.margin + y * g.cellSz, g.cellSz + 1));
                    }
                }
            }
        }
        return (result, st);
    }

    function _rectStr(uint256 x, uint256 y, uint256 size) private pure returns (string memory) {
        return string.concat(
            '<rect x="', SoulSVGUtils.toString(x),
            '" y="', SoulSVGUtils.toString(y),
            '" width="', SoulSVGUtils.toString(size),
            '" height="', SoulSVGUtils.toString(size), '" />'
        );
    }

    function _buildDecorations(SoulPRNG.State memory st, Grid memory g) private pure returns (string memory result, SoulPRNG.State memory) {
        result = "";
        uint256 frameRand;
        (frameRand, st) = SoulPRNG.randomInt(st, 0, 1000);
        if (frameRand > 500) {
            result = string.concat(
                result,
                '<rect x="', SoulSVGUtils.toString(g.margin - 10),
                '" y="', SoulSVGUtils.toString(g.margin - 10),
                '" width="160" height="160" fill="none" stroke="', g.fg, '" stroke-width="2" />'
            );
        }

        uint256 circleRand;
        (circleRand, st) = SoulPRNG.randomInt(st, 0, 1000);
        if (circleRand > 700) {
            result = string.concat(
                result,
                '<circle cx="100" cy="100" r="105" fill="none" stroke="', g.fg,
                '" stroke-width="1.5" stroke-dasharray="5,5" />'
            );
        }
        return (result, st);
    }
}
