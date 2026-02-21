// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {SignatureChecker} from "@openzeppelin/contracts/utils/cryptography/SignatureChecker.sol";
import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Ownable2Step} from "@openzeppelin/contracts/access/Ownable2Step.sol";
import {Pausable} from "@openzeppelin/contracts/utils/Pausable.sol";

/**
 * @title SoulRegistry
 * @notice ERC-721 soul tokens + EIP-8004 identity registry compatibility.
 *
 * Design notes (see lesser-soul/SPEC.md):
 * - tokenId == agentId for determinism
 * - getAgentWallet(agentId) returns the currently bound wallet for TipSplitter (IERC8004IdentityRegistry)
 * - transfers are allowed only during an initial claim window; after that, tokens are soulbound
 * - wallet rotation is Safe-first (onlyOwner) but requires signatures from both current and new wallets
 */
contract SoulRegistry is ERC721, Ownable2Step, Pausable, EIP712 {

    // Claim window duration (seconds). After this, normal ERC-721 transfers are blocked.
    uint256 public immutable claimWindowSeconds;

    // tokenId (== agentId) -> current wallet
    mapping(uint256 => address) private _agentWallet;

    // tokenId -> metaURI (registration file URI)
    mapping(uint256 => string) private _metaURI;

    // tokenId -> mint timestamp (seconds)
    mapping(uint256 => uint256) private _mintedAt;

    // agentId -> replay-protection nonce (used for rotations)
    mapping(uint256 => uint256) public agentNonces;

    bytes32 private constant _ROTATION_TYPEHASH =
        keccak256("WalletRotationProposal(uint256 agentId,address currentWallet,address newWallet,uint256 nonce,uint256 deadline)");

    event SoulMinted(uint256 indexed agentId, address indexed to, string metaURI);
    event MetaURISet(uint256 indexed agentId, string metaURI);
    event WalletRotated(uint256 indexed agentId, address indexed oldWallet, address indexed newWallet, uint256 nonce);

    constructor(address initialOwner, uint256 claimWindowSeconds_)
        ERC721("LesserSoul", "SOUL")
        Ownable(initialOwner)
        EIP712("LesserSoul", "1")
    {
        claimWindowSeconds = claimWindowSeconds_;
    }

    // ========= Identity registry =========

    /// @notice Mint a new soul token for an agent. tokenId == agentId.
    function mintSoul(address to, uint256 agentId, string calldata metaURI) external onlyOwner whenNotPaused {
        if (to == address(0)) {
            revert ERC721InvalidReceiver(address(0));
        }
        if (bytes(metaURI).length == 0) {
            revert("SoulRegistry: metaURI required");
        }
        if (_ownerOf(agentId) != address(0)) {
            revert("SoulRegistry: already minted");
        }

        _mintedAt[agentId] = block.timestamp;
        _metaURI[agentId] = metaURI;
        _safeMint(to, agentId);

        emit SoulMinted(agentId, to, metaURI);
    }

    /// @notice Update metadata URI for an existing soul.
    function setMetaURI(uint256 agentId, string calldata metaURI) external onlyOwner whenNotPaused {
        _requireOwned(agentId);
        if (bytes(metaURI).length == 0) {
            revert("SoulRegistry: metaURI required");
        }
        _metaURI[agentId] = metaURI;
        emit MetaURISet(agentId, metaURI);
    }

    /// @notice EIP-8004 compatibility: resolve wallet bound to agentId.
    function getAgentWallet(uint256 agentId) external view returns (address) {
        return _agentWallet[agentId];
    }

    /// @notice Returns the agent ID for a given token ID.
    function agentOfToken(uint256 tokenId) external view returns (uint256) {
        _requireOwned(tokenId);
        return tokenId;
    }

    /// @notice Check whether a soul is currently soulbound (non-transferable by normal ERC-721 transfers).
    function isSoulbound(uint256 tokenId) external view returns (bool) {
        if (_ownerOf(tokenId) == address(0)) {
            return false;
        }
        uint256 mintedAt = _mintedAt[tokenId];
        // mintedAt is always set at mint time; treat missing as soulbound to fail safe.
        if (mintedAt == 0) {
            return true;
        }
        return block.timestamp >= mintedAt + claimWindowSeconds;
    }

    /// @notice ERC-721 tokenURI resolves to metaURI (registration file URI).
    function tokenURI(uint256 tokenId) public view override returns (string memory) {
        _requireOwned(tokenId);
        return _metaURI[tokenId];
    }

    // ========= Wallet rotation =========

    /**
     * @notice Rotate the wallet bound to agentId.
     * @dev Safe-first (onlyOwner) but requires signatures from both current and new wallets.
     */
    function rotateWallet(
        uint256 agentId,
        address newWallet,
        uint256 nonce,
        uint256 deadline,
        bytes calldata currentSig,
        bytes calldata newSig
    ) external onlyOwner whenNotPaused {
        address currentWallet = _agentWallet[agentId];
        if (currentWallet == address(0) || _ownerOf(agentId) == address(0)) {
            revert("SoulRegistry: agent missing");
        }
        if (newWallet == address(0)) {
            revert("SoulRegistry: invalid new wallet");
        }
        if (newWallet == currentWallet) {
            revert("SoulRegistry: no-op");
        }
        if (block.timestamp > deadline) {
            revert("SoulRegistry: expired");
        }
        if (nonce != agentNonces[agentId]) {
            revert("SoulRegistry: bad nonce");
        }

        bytes32 structHash = keccak256(abi.encode(_ROTATION_TYPEHASH, agentId, currentWallet, newWallet, nonce, deadline));
        bytes32 digest = _hashTypedDataV4(structHash);

        if (!SignatureChecker.isValidSignatureNow(currentWallet, digest, currentSig)) {
            revert("SoulRegistry: invalid current sig");
        }
        if (!SignatureChecker.isValidSignatureNow(newWallet, digest, newSig)) {
            revert("SoulRegistry: invalid new sig");
        }

        agentNonces[agentId] = nonce + 1;

        // Transfer the soul token to the new wallet even when soulbound.
        _update(newWallet, agentId, address(0));

        emit WalletRotated(agentId, currentWallet, newWallet, nonce);
    }

    // ========= Admin =========

    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }

    // ========= Soulbound enforcement =========

    function _update(address to, uint256 tokenId, address auth) internal override returns (address) {
        if (paused()) {
            revert("SoulRegistry: paused");
        }

        address from = _ownerOf(tokenId);

        // Block normal ERC-721 transfers after the claim window expires.
        // Rotation uses auth == address(0) to bypass the operator check and is allowed even when soulbound.
        if (from != address(0) && to != address(0) && auth != address(0)) {
            uint256 mintedAt = _mintedAt[tokenId];
            if (mintedAt == 0 || block.timestamp >= mintedAt + claimWindowSeconds) {
                revert("SoulRegistry: soulbound");
            }
        }

        address prev = super._update(to, tokenId, auth);

        if (to == address(0)) {
            delete _agentWallet[tokenId];
        } else {
            _agentWallet[tokenId] = to;
        }

        return prev;
    }
}

