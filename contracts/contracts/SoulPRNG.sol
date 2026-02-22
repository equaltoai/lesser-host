// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @title SoulPRNG
/// @notice Deterministic PRNG library porting xmur3 + mulberry32 from JavaScript.
///         All arithmetic uses 32-bit unsigned integers with wrapping semantics.
library SoulPRNG {
    struct State {
        uint32 s;
    }

    /// @notice Seed the PRNG from a string (xmur3 hash → mulberry32 initial state).
    function seed(string memory str) internal pure returns (State memory) {
        bytes memory b = bytes(str);
        uint32 h;
        unchecked {
            h = 1779033703 ^ uint32(b.length);
            for (uint256 i = 0; i < b.length; i++) {
                h = _imul(h ^ uint32(uint8(b[i])), 3432918353);
                h = (h << 13) | (h >> 19);
            }
            // xmur3 finalizer (one call)
            h = _imul(h ^ (h >> 16), 2246822507);
            h = _imul(h ^ (h >> 13), 3266489909);
            h = h ^ (h >> 16);
        }
        return State({s: h});
    }

    /// @notice Get next random value as a fraction in [0, 1e18).
    ///         Returns (value, updatedState).
    function next(State memory st) internal pure returns (uint256 value, State memory) {
        unchecked {
            uint32 t = st.s + 0x6D2B79F5;
            st.s = t;
            t = _imul(t ^ (t >> 15), t | 1);
            t ^= t + _imul(t ^ (t >> 7), t | 61);
            t = t ^ (t >> 14);
            // t is now a uint32 random value. Return as fraction * 1e18 / 2^32
            value = (uint256(t) * 1e18) / 4294967296;
        }
        return (value, st);
    }

    /// @notice Random value in [min, max) scaled by 1e18.
    function randomRange(State memory st, uint256 min, uint256 max) internal pure returns (uint256 value, State memory) {
        uint256 r;
        (r, st) = next(st);
        value = min + (r * (max - min)) / 1e18;
        return (value, st);
    }

    /// @notice Random integer in [min, max).
    function randomInt(State memory st, uint256 min, uint256 max) internal pure returns (uint256 value, State memory) {
        uint256 r;
        (r, st) = next(st);
        value = min + (r * (max - min)) / 1e18;
        return (value, st);
    }

    /// @notice Random index in [0, len).
    function randomChoice(State memory st, uint256 len) internal pure returns (uint256 index, State memory) {
        return randomInt(st, 0, len);
    }

    /// @notice 32-bit imul (matches JS Math.imul behavior).
    function _imul(uint32 a, uint32 b) private pure returns (uint32) {
        unchecked {
            return uint32(uint64(a) * uint64(b));
        }
    }
}
