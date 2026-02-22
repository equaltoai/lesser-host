// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {SignatureChecker} from "@openzeppelin/contracts/utils/cryptography/SignatureChecker.sol";
import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Ownable2Step} from "@openzeppelin/contracts/access/Ownable2Step.sol";
import {Pausable} from "@openzeppelin/contracts/utils/Pausable.sol";

import {ISoulAvatarRenderer} from "./ISoulAvatarRenderer.sol";
import {SoulSVGUtils} from "./SoulSVGUtils.sol";

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

    // Transfer tracking
    mapping(uint256 => uint256) public transferCount;
    mapping(uint256 => uint256) public lastTransferredAt;

    // Avatar renderer registry
    mapping(uint8 => address) private _renderers;
    mapping(uint256 => uint8) private _avatarStyle;

    // Permit-based minting
    address public mintSigner;
    uint256 public mintFee;
    mapping(bytes32 => bool) private _usedPermits;

    bytes32 private constant _ROTATION_TYPEHASH =
        keccak256("WalletRotationProposal(uint256 agentId,address currentWallet,address newWallet,uint256 nonce,uint256 deadline)");

    bytes32 private constant _MINT_PERMIT_TYPEHASH =
        keccak256("MintPermit(address to,uint256 agentId,string metaURI,uint8 avatarStyle,uint256 deadline)");

    event SoulMinted(uint256 indexed agentId, address indexed to, string metaURI);
    event MetaURISet(uint256 indexed agentId, string metaURI);
    event WalletRotated(uint256 indexed agentId, address indexed oldWallet, address indexed newWallet, uint256 nonce);
    event SoulBurned(uint256 indexed agentId, address indexed lastWallet);
    event SoulTransferred(uint256 indexed agentId, uint256 transferCount, uint256 timestamp);
    event AvatarStyleChanged(uint256 indexed agentId, uint8 style);
    event RendererUpdated(uint8 indexed styleId, address renderer);
    event MintSignerUpdated(address indexed signer);
    event MintFeeUpdated(uint256 fee);

    constructor(address initialOwner, uint256 claimWindowSeconds_)
        ERC721("LesserSoul", "SOUL")
        Ownable(initialOwner)
        EIP712("LesserSoul", "1")
    {
        claimWindowSeconds = claimWindowSeconds_;
    }

    // ========= Identity registry =========

    /// @notice Mint a new soul token via permit signed by mintSigner.
    function mintSoul(
        address to,
        uint256 agentId,
        string calldata metaURI,
        uint8 avatarStyle,
        uint256 deadline,
        bytes calldata permit
    ) external payable whenNotPaused {
        if (msg.value != mintFee) {
            revert("SoulRegistry: incorrect fee");
        }
        if (block.timestamp > deadline) {
            revert("SoulRegistry: expired");
        }
        if (mintSigner == address(0)) {
            revert("SoulRegistry: no mint signer");
        }

        bytes32 structHash = keccak256(abi.encode(
            _MINT_PERMIT_TYPEHASH,
            to,
            agentId,
            keccak256(bytes(metaURI)),
            avatarStyle,
            deadline
        ));
        bytes32 digest = _hashTypedDataV4(structHash);

        if (_usedPermits[digest]) {
            revert("SoulRegistry: permit reused");
        }
        _usedPermits[digest] = true;

        if (!SignatureChecker.isValidSignatureNow(mintSigner, digest, permit)) {
            revert("SoulRegistry: invalid permit");
        }

        _mintSoulInternal(to, agentId, metaURI, avatarStyle);
    }

    /// @notice Mint a new soul token directly (owner only, no permit/fee).
    function mintSoulOwner(address to, uint256 agentId, string calldata metaURI, uint8 avatarStyle) external onlyOwner whenNotPaused {
        _mintSoulInternal(to, agentId, metaURI, avatarStyle);
    }

    function _mintSoulInternal(address to, uint256 agentId, string calldata metaURI, uint8 avatarStyle) internal {
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
        _avatarStyle[agentId] = avatarStyle;
        _safeMint(to, agentId);

        emit SoulMinted(agentId, to, metaURI);
    }

    /// @notice Burn a soul token permanently. Only callable by contract owner.
    function burnSoul(uint256 agentId) external onlyOwner whenNotPaused {
        address lastWallet = _agentWallet[agentId];
        _requireOwned(agentId);
        _update(address(0), agentId, address(0));
        emit SoulBurned(agentId, lastWallet);
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

    /// @notice ERC-721 tokenURI resolves to on-chain avatar (if renderer set) or metaURI fallback.
    function tokenURI(uint256 tokenId) public view override returns (string memory) {
        _requireOwned(tokenId);

        uint8 style = _avatarStyle[tokenId];
        address renderer = _renderers[style];
        if (renderer == address(0)) {
            return _metaURI[tokenId];
        }

        ISoulAvatarRenderer r = ISoulAvatarRenderer(renderer);
        string memory svg = r.renderAvatar(tokenId);
        string memory sName = r.styleName();

        string memory imageData = string.concat(
            "data:image/svg+xml;base64,",
            SoulSVGUtils.base64Encode(bytes(svg))
        );

        string memory soulboundStr = _ownerOf(tokenId) != address(0) &&
            _mintedAt[tokenId] > 0 &&
            block.timestamp >= _mintedAt[tokenId] + claimWindowSeconds
            ? "true"
            : "false";

        string memory json = string.concat(
            '{"name":"Soul #',
            SoulSVGUtils.toString(tokenId),
            '","description":"Lesser Soul Token","image":"',
            imageData,
            '","attributes":[{"trait_type":"Transfer Count","value":',
            SoulSVGUtils.toString(transferCount[tokenId]),
            '},{"trait_type":"Style","value":"',
            sName,
            '"},{"trait_type":"Soulbound","value":"',
            soulboundStr,
            '"}]}'
        );

        return string.concat(
            "data:application/json;base64,",
            SoulSVGUtils.base64Encode(bytes(json))
        );
    }

    // ========= Avatar management =========

    /// @notice Register a renderer for a style ID. Only callable by contract owner.
    function setRenderer(uint8 styleId, address renderer) external onlyOwner {
        _renderers[styleId] = renderer;
        emit RendererUpdated(styleId, renderer);
    }

    /// @notice Set avatar style for a token. Only callable by token holder.
    function setAvatarStyle(uint256 tokenId, uint8 style) external whenNotPaused {
        if (ownerOf(tokenId) != msg.sender) {
            revert("SoulRegistry: not token holder");
        }
        _avatarStyle[tokenId] = style;
        emit AvatarStyleChanged(tokenId, style);
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

    // ========= Mint signer admin =========

    /// @notice Set the mint signer address. Only callable by contract owner.
    function setMintSigner(address signer) external onlyOwner {
        mintSigner = signer;
        emit MintSignerUpdated(signer);
    }

    /// @notice Set the mint fee (wei). Only callable by contract owner.
    function setMintFee(uint256 fee) external onlyOwner {
        mintFee = fee;
        emit MintFeeUpdated(fee);
    }

    /// @notice Withdraw accumulated mint fees. Only callable by contract owner.
    function withdrawFees(address payable recipient) external onlyOwner {
        if (recipient == address(0)) {
            revert("SoulRegistry: zero recipient");
        }
        uint256 balance = address(this).balance;
        if (balance == 0) {
            revert("SoulRegistry: no balance");
        }
        (bool ok,) = recipient.call{value: balance}("");
        if (!ok) {
            revert("SoulRegistry: transfer failed");
        }
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

        // Transfer tracking: increment on genuine transfers (from != 0 && to != 0).
        if (from != address(0) && to != address(0)) {
            transferCount[tokenId]++;
            lastTransferredAt[tokenId] = block.timestamp;
            emit SoulTransferred(tokenId, transferCount[tokenId], block.timestamp);
        }

        if (to == address(0)) {
            delete _agentWallet[tokenId];
        } else {
            _agentWallet[tokenId] = to;
        }

        return prev;
    }
}
