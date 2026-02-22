// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @title SoulSVGUtils
/// @notice Utility functions for on-chain SVG generation: base64 encoding, number-to-string, hex color channels.
library SoulSVGUtils {
    bytes private constant _BASE64_TABLE = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

    /// @notice RFC 4648 base64 encode.
    function base64Encode(bytes memory data) internal pure returns (string memory) {
        if (data.length == 0) return "";

        uint256 encodedLen = 4 * ((data.length + 2) / 3);
        bytes memory result = new bytes(encodedLen);

        uint256 i;
        uint256 j;
        for (i = 0; i + 2 < data.length; i += 3) {
            uint256 a = uint256(uint8(data[i]));
            uint256 b = uint256(uint8(data[i + 1]));
            uint256 c = uint256(uint8(data[i + 2]));
            uint256 triple = (a << 16) | (b << 8) | c;
            result[j++] = _BASE64_TABLE[(triple >> 18) & 0x3F];
            result[j++] = _BASE64_TABLE[(triple >> 12) & 0x3F];
            result[j++] = _BASE64_TABLE[(triple >> 6) & 0x3F];
            result[j++] = _BASE64_TABLE[triple & 0x3F];
        }

        if (data.length % 3 == 1) {
            uint256 a = uint256(uint8(data[i]));
            uint256 triple = a << 16;
            result[j++] = _BASE64_TABLE[(triple >> 18) & 0x3F];
            result[j++] = _BASE64_TABLE[(triple >> 12) & 0x3F];
            result[j++] = "=";
            result[j++] = "=";
        } else if (data.length % 3 == 2) {
            uint256 a = uint256(uint8(data[i]));
            uint256 b = uint256(uint8(data[i + 1]));
            uint256 triple = (a << 16) | (b << 8);
            result[j++] = _BASE64_TABLE[(triple >> 18) & 0x3F];
            result[j++] = _BASE64_TABLE[(triple >> 12) & 0x3F];
            result[j++] = _BASE64_TABLE[(triple >> 6) & 0x3F];
            result[j++] = "=";
        }

        return string(result);
    }

    /// @notice Convert uint256 to decimal string.
    function toString(uint256 value) internal pure returns (string memory) {
        if (value == 0) return "0";
        uint256 temp = value;
        uint256 digits;
        while (temp != 0) {
            digits++;
            temp /= 10;
        }
        bytes memory buffer = new bytes(digits);
        while (value != 0) {
            digits--;
            buffer[digits] = bytes1(uint8(48 + (value % 10)));
            value /= 10;
        }
        return string(buffer);
    }

    /// @notice Convert a value 0-255 to a 2-char hex string for RGB channels.
    function toColorString(uint256 value) internal pure returns (string memory) {
        if (value > 255) value = 255;
        bytes memory hexChars = "0123456789abcdef";
        bytes memory result = new bytes(2);
        result[0] = hexChars[value >> 4];
        result[1] = hexChars[value & 0xF];
        return string(result);
    }

    /// @notice Convert int256 to decimal string (supports negative numbers).
    function toIntString(int256 value) internal pure returns (string memory) {
        if (value == 0) return "0";
        bool negative = value < 0;
        uint256 absVal = negative ? uint256(-value) : uint256(value);
        string memory numStr = toString(absVal);
        if (negative) {
            return string.concat("-", numStr);
        }
        return numStr;
    }
}
