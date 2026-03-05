// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import {IERC165} from "@openzeppelin/contracts/utils/introspection/IERC165.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Ownable2Step} from "@openzeppelin/contracts/access/Ownable2Step.sol";

/// @notice ENSIP-10 ExtendedResolver interface.
interface IExtendedResolver is IERC165 {
    /// @notice ENSIP-10: resolve a DNS-encoded name and a resolver function call.
    /// @dev For CCIP-Read resolvers, this should revert with OffchainLookup.
    function resolve(bytes calldata name, bytes calldata data) external view returns (bytes memory);
}

/// @notice Offchain resolver interface for EIP-3668 CCIP-Read.
interface IOffchainResolver is IExtendedResolver {
    /// @notice Verify gateway response and return the resolved data.
    function resolveWithProof(bytes calldata response, bytes calldata extraData) external view returns (bytes memory);

    /// @notice Update the gateway URL template. Owner only.
    function setGatewayUrl(string calldata url) external;

    /// @notice Update the authorized signer address. Owner only.
    function setSigner(address signer) external;

    /// @notice Returns the current gateway URL template.
    function gatewayUrl() external view returns (string memory);

    /// @notice Returns the current authorized signer.
    function signer() external view returns (address);
}

/// @notice Gateway-side resolver service called by CCIP-Read clients.
interface IResolverService {
    function resolve(bytes calldata name, bytes calldata data)
        external
        view
        returns (bytes memory result, uint64 expires, bytes memory sig);
}

library SignatureVerifier {
    /// @dev Generates a hash for signing/verifying (matches ENS offchain-resolver SignatureVerifier).
    function makeSignatureHash(address target, uint64 expires, bytes memory request, bytes memory result)
        internal
        pure
        returns (bytes32)
    {
        return keccak256(abi.encodePacked(hex"1900", target, expires, keccak256(request), keccak256(result)));
    }

    /// @dev Verifies a signed response and returns (signer, result).
    /// @param request ABI encoded `(bytes callData, address sender)` from the OffchainLookup revert.
    /// @param response ABI encoded `(bytes result, uint64 expires, bytes sig)`.
    function verify(bytes calldata request, bytes calldata response) internal view returns (address, bytes memory) {
        (bytes memory result, uint64 expires, bytes memory sig) = abi.decode(response, (bytes, uint64, bytes));
        (bytes memory callData, address sender) = abi.decode(request, (bytes, address));

        bytes32 hash = makeSignatureHash(sender, expires, callData, result);
        address recovered;
        if (sig.length == 65) {
            recovered = ECDSA.recover(hash, sig);
        } else if (sig.length == 64) {
            bytes32 r;
            bytes32 vs;
            assembly ("memory-safe") {
                r := mload(add(sig, 0x20))
                vs := mload(add(sig, 0x40))
            }
            recovered = ECDSA.recover(hash, r, vs);
        } else {
            revert("SignatureVerifier: invalid signature length");
        }
        require(expires >= block.timestamp, "SignatureVerifier: Signature expired");
        return (recovered, result);
    }
}

/**
 * @title OffchainResolver
 * @notice ENS CCIP-Read resolver for lessersoul.eth subdomains.
 *
 * This implements ENSIP-10 `resolve(bytes,bytes)` by reverting with EIP-3668 OffchainLookup,
 * directing clients to an HTTPS gateway that returns a signed response. The callback verifies
 * the signature and returns the resolved result bytes.
 */
contract OffchainResolver is IOffchainResolver, Ownable2Step {
    /// @notice EIP-3668 OffchainLookup error.
    error OffchainLookup(address sender, string[] urls, bytes callData, bytes4 callbackFunction, bytes extraData);

    /// @notice Emitted when the gateway URL template changes.
    event GatewayUrlUpdated(string url);
    /// @notice Emitted when the signer rotates.
    event SignerUpdated(address indexed signer, address indexed previousSigner);

    /// @notice Gateway URL template (may include {sender} and {data} substitutions per EIP-3668).
    string public gatewayUrl;
    /// @notice Current authorized signer address.
    address public signer;
    /// @notice Previous authorized signer address (kept to allow zero-downtime rotation).
    address public previousSigner;

    constructor(address initialOwner, string memory url, address signer_) Ownable(initialOwner) {
        if (bytes(url).length == 0) {
            revert("OffchainResolver: empty url");
        }
        if (signer_ == address(0)) {
            revert("OffchainResolver: zero signer");
        }
        gatewayUrl = url;
        signer = signer_;
    }

    /// @notice ENSIP-10 resolve implementation: reverts with OffchainLookup.
    function resolve(bytes calldata name, bytes calldata data) external view override returns (bytes memory) {
        bytes memory callData = abi.encodeWithSelector(IResolverService.resolve.selector, name, data);
        string[] memory urls = new string[](1);
        urls[0] = gatewayUrl;
        revert OffchainLookup(
            address(this),
            urls,
            callData,
            OffchainResolver.resolveWithProof.selector,
            abi.encode(callData, address(this))
        );
    }

    /// @notice Callback used by CCIP-Read compatible clients to verify and parse the response.
    function resolveWithProof(bytes calldata response, bytes calldata extraData)
        external
        view
        override
        returns (bytes memory)
    {
        (address recovered, bytes memory result) = SignatureVerifier.verify(extraData, response);
        if (recovered != signer && recovered != previousSigner) {
            revert("OffchainResolver: invalid signature");
        }
        return result;
    }

    /// @notice Update the gateway URL template. Owner only.
    function setGatewayUrl(string calldata url) external override onlyOwner {
        if (bytes(url).length == 0) {
            revert("OffchainResolver: empty url");
        }
        gatewayUrl = url;
        emit GatewayUrlUpdated(url);
    }

    /// @notice Update the authorized signer address. Owner only.
    /// @dev Keeps the previous signer active to prevent downtime during rotation.
    function setSigner(address signer_) external override onlyOwner {
        if (signer_ == address(0)) {
            revert("OffchainResolver: zero signer");
        }
        if (signer_ == signer) {
            // Clearing the previous signer is useful once gateway/client caches have expired.
            if (previousSigner != address(0)) {
                previousSigner = address(0);
                emit SignerUpdated(signer_, previousSigner);
            }
            return;
        }

        previousSigner = signer;
        signer = signer_;
        emit SignerUpdated(signer_, previousSigner);
    }

    function supportsInterface(bytes4 interfaceID) public pure override returns (bool) {
        return interfaceID == type(IExtendedResolver).interfaceId
            || interfaceID == type(IOffchainResolver).interfaceId
            || interfaceID == type(IERC165).interfaceId;
    }
}
