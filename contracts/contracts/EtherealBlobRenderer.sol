// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ISoulAvatarRenderer} from "./ISoulAvatarRenderer.sol";
import {SoulPRNG} from "./SoulPRNG.sol";
import {SoulSVGUtils} from "./SoulSVGUtils.sol";

/// @title EtherealBlobRenderer
/// @notice Generates ethereal blob SVG avatars on-chain from tokenId.
contract EtherealBlobRenderer is ISoulAvatarRenderer {
    function styleName() external pure override returns (string memory) {
        return "Ethereal Blob";
    }

    function renderAvatar(uint256 tokenId) external view override returns (string memory) {
        SoulPRNG.State memory st = SoulPRNG.seed(SoulSVGUtils.toString(tokenId));

        uint256 hue;
        (hue, st) = SoulPRNG.randomInt(st, 0, 360);

        uint256 hueOffsetIdx;
        (hueOffsetIdx, st) = SoulPRNG.randomChoice(st, 3);
        uint256[3] memory offsets = [uint256(60), 120, 180];
        uint256 hue2 = (hue + offsets[hueOffsetIdx]) % 360;

        uint256 bgLightness;
        (bgLightness, st) = SoulPRNG.randomInt(st, 5, 15);

        uint256 blobSize;
        (blobSize, st) = SoulPRNG.randomInt(st, 50, 80);

        uint256 freqX;
        (freqX, st) = SoulPRNG.randomInt(st, 10, 50);
        uint256 freqY;
        (freqY, st) = SoulPRNG.randomInt(st, 10, 50);

        uint256 scale;
        (scale, st) = SoulPRNG.randomInt(st, 20, 100);

        string memory id = SoulSVGUtils.toString(tokenId);

        string memory defs = _buildDefs(id, hue, hue2, bgLightness, freqX, freqY, scale);
        return _buildSvg(id, defs, blobSize);
    }

    function _buildDefs(
        string memory id,
        uint256 hue, uint256 hue2, uint256 bgLightness,
        uint256 freqX, uint256 freqY, uint256 scale
    ) private pure returns (string memory) {
        string memory hueStr = SoulSVGUtils.toString(hue);

        string memory gradient = string.concat(
            '<linearGradient id="bg-', id, '" x1="0%" y1="0%" x2="100%" y2="100%">'
            '<stop offset="0%" stop-color="hsl(', hueStr, ',80%,', SoulSVGUtils.toString(bgLightness), '%)" />'
            '<stop offset="100%" stop-color="#000" />'
            '</linearGradient>'
        );

        string memory radialGrad = string.concat(
            '<radialGradient id="glow-', id, '" cx="50%" cy="50%" r="50%">'
            '<stop offset="0%" stop-color="hsl(', hueStr, ',100%,70%)" stop-opacity="0.8" />'
            '<stop offset="70%" stop-color="hsl(', SoulSVGUtils.toString(hue2), ',100%,50%)" stop-opacity="0.3" />'
            '<stop offset="100%" stop-color="hsl(', hueStr, ',100%,20%)" stop-opacity="0" />'
            '</radialGradient>'
        );

        string memory filter = string.concat(
            '<filter id="df-', id, '">'
            '<feTurbulence type="fractalNoise" baseFrequency="0.0', SoulSVGUtils.toString(freqX),
            ' 0.0', SoulSVGUtils.toString(freqY), '" numOctaves="3" result="noise" seed="', id, '"/>'
            '<feDisplacementMap in="SourceGraphic" in2="noise" scale="', SoulSVGUtils.toString(scale),
            '" xChannelSelector="R" yChannelSelector="G"/>'
            '</filter>'
        );

        return string.concat('<defs>', gradient, radialGrad, filter, '</defs>');
    }

    function _buildSvg(string memory id, string memory defs, uint256 blobSize) private pure returns (string memory) {
        uint256 innerR = blobSize * 3 / 10;
        return string.concat(
            '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200">',
            defs,
            '<rect width="200" height="200" fill="url(#bg-', id, ')" />'
            '<circle cx="100" cy="100" r="', SoulSVGUtils.toString(blobSize),
            '" fill="url(#glow-', id, ')" filter="url(#df-', id, ')" />'
            '<circle cx="100" cy="100" r="', SoulSVGUtils.toString(innerR),
            '" fill="#fff" opacity="0.6" filter="blur(8px)" />'
            '</svg>'
        );
    }
}
