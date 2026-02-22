// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ISoulAvatarRenderer} from "./ISoulAvatarRenderer.sol";
import {SoulPRNG} from "./SoulPRNG.sol";
import {SoulSVGUtils} from "./SoulSVGUtils.sol";

/// @title SacredGeometryRenderer
/// @notice Generates sacred geometry SVG avatars on-chain from tokenId.
///         Uses golden ratio, Fibonacci point counts, and a sin lookup table.
contract SacredGeometryRenderer is ISoulAvatarRenderer {
    // Sin lookup table: sin(0..90 degrees) * 10000, indexed by degree.
    int256[91] private _sinTable = [
        int256(0), 175, 349, 523, 698, 872, 1045, 1219, 1392, 1564,
        1736, 1908, 2079, 2250, 2419, 2588, 2756, 2924, 3090, 3256,
        3420, 3584, 3746, 3907, 4067, 4226, 4384, 4540, 4695, 4848,
        5000, 5150, 5299, 5446, 5592, 5736, 5878, 6018, 6157, 6293,
        6428, 6561, 6691, 6820, 6947, 7071, 7193, 7314, 7431, 7547,
        7660, 7771, 7880, 7986, 8090, 8192, 8290, 8387, 8480, 8572,
        8660, 8746, 8829, 8910, 8988, 9063, 9135, 9205, 9272, 9336,
        9397, 9455, 9511, 9563, 9613, 9659, 9703, 9744, 9781, 9816,
        9848, 9877, 9903, 9925, 9945, 9962, 9976, 9986, 9994, 9998,
        10000
    ];

    // Golden ratio scaled by 10000.
    uint256 private constant PHI = 16180;
    // Fibonacci point counts for shape layers.
    uint256[5] private _fibPoints = [uint256(3), 5, 8, 13, 21];

    // Packed layer params to avoid stack depth issues.
    struct LayerParams {
        uint256 radius;
        uint256 points;
        string hueStr;
        uint256 lineWidthX10;
        uint256 rotationOffsetX10;
        uint256 shapeChoice;
        uint256 hasFill;
    }

    function styleName() external pure override returns (string memory) {
        return "Sacred Geometry";
    }

    function renderAvatar(uint256 tokenId) external view override returns (string memory) {
        SoulPRNG.State memory st = SoulPRNG.seed(SoulSVGUtils.toString(tokenId));

        uint256 layers;
        (layers, st) = SoulPRNG.randomInt(st, 4, 9);

        uint256 baseHue;
        (baseHue, st) = SoulPRNG.randomInt(st, 0, 360);

        string memory shapes = "";

        for (uint256 i = 0; i < layers; i++) {
            LayerParams memory lp;
            (lp, st) = _buildLayerParams(st, i, baseHue);
            shapes = string.concat(shapes, _renderLayer(lp));
        }

        // Center dot
        uint256 dotR;
        (dotR, st) = SoulPRNG.randomInt(st, 2, 6);
        shapes = string.concat(
            shapes,
            '<circle cx="0" cy="0" r="', SoulSVGUtils.toString(dotR),
            '" fill="hsl(', SoulSVGUtils.toString(baseHue), ',100%,80%)" />'
        );

        return string.concat(
            '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200" style="background-color:#0d1117">'
            '<g transform="translate(100,100)">',
            shapes,
            '</g></svg>'
        );
    }

    function _buildLayerParams(SoulPRNG.State memory st, uint256 i, uint256 baseHue) private view returns (LayerParams memory lp, SoulPRNG.State memory) {
        // radius = 95 / PHI^i
        uint256 radiusScaled = 950000;
        for (uint256 p = 0; p < i; p++) {
            radiusScaled = (radiusScaled * 10000) / PHI;
        }
        lp.radius = radiusScaled / 10000;

        uint256 pointsIdx;
        (pointsIdx, st) = SoulPRNG.randomChoice(st, 5);
        lp.points = _fibPoints[pointsIdx];

        lp.hueStr = SoulSVGUtils.toString((baseHue + (i * 1375) / 10) % 360);

        (lp.lineWidthX10, st) = SoulPRNG.randomInt(st, 5, 25);

        uint256 randOffset;
        (randOffset, st) = SoulPRNG.randomInt(st, 0, 300);
        lp.rotationOffsetX10 = ((i * 1375) + randOffset) % 3600;

        (lp.shapeChoice, st) = SoulPRNG.randomChoice(st, 3);
        (lp.hasFill, st) = SoulPRNG.randomInt(st, 0, 2);

        return (lp, st);
    }

    function _renderLayer(LayerParams memory lp) private view returns (string memory) {
        if (lp.shapeChoice == 0) {
            return _renderPolygon(lp);
        } else if (lp.shapeChoice == 1) {
            return _renderCircles(lp);
        } else {
            return _renderLines(lp);
        }
    }

    function _strokeAttr(string memory hueStr, uint256 lineWidthX10) private pure returns (string memory) {
        return string.concat(
            ' stroke="hsl(', hueStr, ',80%,60%)" stroke-width="',
            _formatDecimal1(lineWidthX10), '"'
        );
    }

    function _renderPolygon(LayerParams memory lp) private view returns (string memory) {
        string memory pathData = "";
        for (uint256 j = 0; j < lp.points; j++) {
            uint256 angleDegX10 = (j * 3600) / lp.points + lp.rotationOffsetX10;
            (int256 px, int256 py) = _polarToCart(lp.radius, angleDegX10);
            pathData = string.concat(
                pathData,
                j == 0 ? "M" : "L",
                SoulSVGUtils.toIntString(px), ",", SoulSVGUtils.toIntString(py), " "
            );
        }
        pathData = string.concat(pathData, "Z");

        string memory fillStr = lp.hasFill == 1
            ? string.concat("hsl(", lp.hueStr, ",60%,20%)")
            : "none";

        return string.concat(
            '<path d="', pathData, '"',
            _strokeAttr(lp.hueStr, lp.lineWidthX10),
            ' fill="', fillStr, '" opacity="0.8" />'
        );
    }

    function _renderCircles(LayerParams memory lp) private view returns (string memory) {
        string memory result = "";
        uint256 rCenter = (lp.radius * 10000) / PHI;
        uint256 cr = lp.radius - rCenter;
        string memory strokeStr = _strokeAttr(lp.hueStr, lp.lineWidthX10);
        for (uint256 j = 0; j < lp.points; j++) {
            uint256 angleDegX10 = (j * 3600) / lp.points + lp.rotationOffsetX10;
            (int256 px, int256 py) = _polarToCart(rCenter, angleDegX10);
            result = string.concat(
                result,
                '<circle cx="', SoulSVGUtils.toIntString(px),
                '" cy="', SoulSVGUtils.toIntString(py),
                '" r="', SoulSVGUtils.toString(cr),
                '" fill="none"', strokeStr, ' />'
            );
        }
        return result;
    }

    function _renderLines(LayerParams memory lp) private view returns (string memory) {
        string memory result = "";
        uint256 rIn = (lp.radius * 10000) / PHI;
        string memory strokeStr = _strokeAttr(lp.hueStr, lp.lineWidthX10);
        for (uint256 j = 0; j < lp.points; j++) {
            uint256 angleDegX10 = (j * 3600) / lp.points + lp.rotationOffsetX10;
            (int256 pxOuter, int256 pyOuter) = _polarToCart(lp.radius, angleDegX10);
            (int256 pxInner, int256 pyInner) = _polarToCart(rIn, angleDegX10);
            result = string.concat(
                result,
                '<line x1="', SoulSVGUtils.toIntString(pxInner),
                '" y1="', SoulSVGUtils.toIntString(pyInner),
                '" x2="', SoulSVGUtils.toIntString(pxOuter),
                '" y2="', SoulSVGUtils.toIntString(pyOuter),
                '"', strokeStr, ' />'
            );
        }
        return result;
    }

    function _polarToCart(uint256 radius, uint256 angleDegX10) private view returns (int256 x, int256 y) {
        angleDegX10 = angleDegX10 % 3600;
        int256 sinVal = _sinLookup(angleDegX10);
        int256 cosVal = _sinLookup(angleDegX10 + 900);
        x = (int256(radius) * cosVal) / 10000;
        y = (int256(radius) * sinVal) / 10000;
    }

    function _sinLookup(uint256 angleDegX10) private view returns (int256) {
        uint256 deg = (angleDegX10 / 10) % 360;
        if (deg <= 90) {
            return _sinTable[deg];
        } else if (deg <= 180) {
            return _sinTable[180 - deg];
        } else if (deg <= 270) {
            return -_sinTable[deg - 180];
        } else {
            return -_sinTable[360 - deg];
        }
    }

    function _formatDecimal1(uint256 valX10) private pure returns (string memory) {
        return string.concat(SoulSVGUtils.toString(valX10 / 10), ".", SoulSVGUtils.toString(valX10 % 10));
    }
}
