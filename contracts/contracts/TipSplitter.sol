// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {SafeERC20} from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Ownable2Step} from "@openzeppelin/contracts/access/Ownable2Step.sol";
import {Pausable} from "@openzeppelin/contracts/utils/Pausable.sol";
import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";

/**
 * @title TipSplitter
 * @notice Splits tips between Lesser org, host instance, and actor.
 * @dev Host registry + token allowlist are managed by the contract owner (Safe).
 */
contract TipSplitter is Ownable2Step, Pausable, ReentrancyGuard {
    using SafeERC20 for IERC20;

    uint256 public constant LESSER_FEE_BPS = 100; // 1%
    uint256 public constant MAX_HOST_FEE_BPS = 500; // 5%
    uint256 public constant BPS_DENOMINATOR = 10_000;
    uint256 public constant MIN_TIP_AMOUNT = 10_000; // minimum to guarantee non-zero fee splits

    address public lesserWallet;

    struct HostConfig {
        address wallet;
        uint16 feeBps;
        bool isActive;
    }

    mapping(bytes32 => HostConfig) public hosts; // hostId => config

    mapping(address => uint256) public pendingETH; // recipient => wei
    mapping(address => mapping(address => uint256)) public pendingToken; // token => recipient => amount

    mapping(address => bool) public allowedTokens; // ERC20 token allowlist (ETH is always allowed)
    address[] public allowedTokenList; // enumerable list for migration
    mapping(address => uint256) private _tokenIndex; // token => index in allowedTokenList (1-based)

    /// @notice Per-token max tip amount (0 = unlimited). Use address(0) key for ETH.
    mapping(address => uint256) public maxTipAmount;

    event TipSent(
        bytes32 indexed hostId,
        address indexed token,
        address indexed tipper,
        address actor,
        uint256 amount,
        uint256 lesserShare,
        uint256 hostShare,
        uint256 actorShare,
        bytes32 contentHash
    );

    event Withdrawal(address indexed token, address indexed recipient, uint256 amount);

    event HostRegistered(bytes32 indexed hostId, address indexed wallet, uint16 feeBps);
    event HostUpdated(bytes32 indexed hostId, address indexed wallet, uint16 feeBps);
    event HostActiveSet(bytes32 indexed hostId, bool isActive);

    event TokenAllowedSet(address indexed token, bool allowed);
    event LesserWalletUpdated(address indexed oldWallet, address indexed newWallet);
    event PendingBalanceMigrated(address indexed token, address indexed from, address indexed to, uint256 amount);
    event MaxTipAmountSet(address indexed token, uint256 amount);

    constructor(address _lesserWallet, address _owner) Ownable(_owner) {
        require(_lesserWallet != address(0), "TipSplitter: invalid lesser wallet");
        require(_owner != address(0), "TipSplitter: invalid owner");
        lesserWallet = _lesserWallet;
    }

    receive() external payable {
        revert("TipSplitter: use tipETH");
    }

    // ========= Tips (ETH) =========

    function tipETH(bytes32 hostId, address actor, bytes32 contentHash)
        external
        payable
        nonReentrant
        whenNotPaused
    {
        _tipETH(hostId, actor, msg.value, contentHash);
    }

    /// @notice Batch-tip multiple actors in ETH within a single transaction.
    /// @dev The summation of amounts[] is safe from overflow because Solidity 0.8.x
    ///      uses checked arithmetic — any uint256 overflow will revert automatically.
    function batchTipETH(bytes32 hostId, address[] calldata actors, uint256[] calldata amounts, bytes32[] calldata contentHashes)
        external
        payable
        nonReentrant
        whenNotPaused
    {
        uint256 n = actors.length;
        require(n > 0 && n <= 20, "TipSplitter: invalid batch size");
        require(amounts.length == n && contentHashes.length == n, "TipSplitter: array length mismatch");

        uint256 total = 0;
        for (uint256 i = 0; i < n; i++) {
            total += amounts[i]; // checked arithmetic — overflow reverts
        }
        require(msg.value == total, "TipSplitter: incorrect total");

        for (uint256 i = 0; i < n; i++) {
            _tipETH(hostId, actors[i], amounts[i], contentHashes[i]);
        }
    }

    function _tipETH(bytes32 hostId, address actor, uint256 amount, bytes32 contentHash) internal {
        require(amount >= MIN_TIP_AMOUNT, "TipSplitter: amount below minimum");
        require(actor != address(0), "TipSplitter: invalid actor");
        require(actor != msg.sender, "TipSplitter: cannot tip yourself");

        uint256 cap = maxTipAmount[address(0)];
        require(cap == 0 || amount <= cap, "TipSplitter: amount exceeds max");

        HostConfig memory host = hosts[hostId];
        require(host.wallet != address(0) && host.isActive, "TipSplitter: host not active");

        (uint256 lesserShare, uint256 hostShare, uint256 actorShare) = _split(host.feeBps, amount);

        pendingETH[lesserWallet] += lesserShare;
        pendingETH[host.wallet] += hostShare;
        pendingETH[actor] += actorShare;

        emit TipSent(hostId, address(0), msg.sender, actor, amount, lesserShare, hostShare, actorShare, contentHash);
    }

    // ========= Tips (ERC20) =========

    function tipToken(address token, bytes32 hostId, address actor, uint256 amount, bytes32 contentHash)
        external
        nonReentrant
        whenNotPaused
    {
        _tipToken(token, hostId, actor, amount, contentHash);
    }

    /// @notice Batch-tip multiple actors in an ERC-20 token within a single transaction.
    /// @dev The summation of amounts[] is safe from overflow because Solidity 0.8.x
    ///      uses checked arithmetic — any uint256 overflow will revert automatically.
    ///      Uses balance-before/after pattern to defend against fee-on-transfer tokens.
    function batchTipToken(
        address token,
        bytes32 hostId,
        address[] calldata actors,
        uint256[] calldata amounts,
        bytes32[] calldata contentHashes
    ) external nonReentrant whenNotPaused {
        uint256 n = actors.length;
        require(n > 0 && n <= 20, "TipSplitter: invalid batch size");
        require(amounts.length == n && contentHashes.length == n, "TipSplitter: array length mismatch");
        require(token != address(0), "TipSplitter: token required");
        require(allowedTokens[token], "TipSplitter: token not allowed");

        uint256 total = 0;
        for (uint256 i = 0; i < n; i++) {
            total += amounts[i]; // checked arithmetic — overflow reverts
        }
        require(total > 0, "TipSplitter: total must be > 0");

        // Measure actual received amount to defend against fee-on-transfer tokens
        uint256 balBefore = IERC20(token).balanceOf(address(this));
        IERC20(token).safeTransferFrom(msg.sender, address(this), total);
        uint256 received = IERC20(token).balanceOf(address(this)) - balBefore;
        require(received == total, "TipSplitter: fee-on-transfer tokens not supported");

        for (uint256 i = 0; i < n; i++) {
            _creditToken(hostId, token, actors[i], amounts[i], contentHashes[i], msg.sender);
        }
    }

    /// @dev Uses balance-before/after pattern to defend against fee-on-transfer tokens.
    function _tipToken(address token, bytes32 hostId, address actor, uint256 amount, bytes32 contentHash) internal {
        require(token != address(0), "TipSplitter: token required");
        require(allowedTokens[token], "TipSplitter: token not allowed");
        require(amount > 0, "TipSplitter: amount must be > 0");

        // Measure actual received amount to defend against fee-on-transfer tokens
        uint256 balBefore = IERC20(token).balanceOf(address(this));
        IERC20(token).safeTransferFrom(msg.sender, address(this), amount);
        uint256 received = IERC20(token).balanceOf(address(this)) - balBefore;
        require(received == amount, "TipSplitter: fee-on-transfer tokens not supported");

        _creditToken(hostId, token, actor, amount, contentHash, msg.sender);
    }

    function _creditToken(bytes32 hostId, address token, address actor, uint256 amount, bytes32 contentHash, address tipper) internal {
        require(actor != address(0), "TipSplitter: invalid actor");
        require(actor != tipper, "TipSplitter: cannot tip yourself");
        require(amount >= MIN_TIP_AMOUNT, "TipSplitter: amount below minimum");

        uint256 cap = maxTipAmount[token];
        require(cap == 0 || amount <= cap, "TipSplitter: amount exceeds max");

        HostConfig memory host = hosts[hostId];
        require(host.wallet != address(0) && host.isActive, "TipSplitter: host not active");

        (uint256 lesserShare, uint256 hostShare, uint256 actorShare) = _split(host.feeBps, amount);

        pendingToken[token][lesserWallet] += lesserShare;
        pendingToken[token][host.wallet] += hostShare;
        pendingToken[token][actor] += actorShare;

        emit TipSent(hostId, token, tipper, actor, amount, lesserShare, hostShare, actorShare, contentHash);
    }

    // ========= Withdrawals =========

    /// @notice Withdraw pending balance. Paused during emergency to enable full freeze.
    function withdraw(address token) external nonReentrant whenNotPaused {
        if (token == address(0)) {
            uint256 ethAmount = pendingETH[msg.sender];
            require(ethAmount > 0, "TipSplitter: no pending");
            pendingETH[msg.sender] = 0;

            (bool ok, ) = payable(msg.sender).call{value: ethAmount}("");
            require(ok, "TipSplitter: withdrawal failed");

            emit Withdrawal(address(0), msg.sender, ethAmount);
            return;
        }

        uint256 tokenAmount = pendingToken[token][msg.sender];
        require(tokenAmount > 0, "TipSplitter: no pending");
        pendingToken[token][msg.sender] = 0;

        IERC20(token).safeTransfer(msg.sender, tokenAmount);
        emit Withdrawal(token, msg.sender, tokenAmount);
    }

    // ========= Host Registry (owner) =========

    function registerHost(bytes32 hostId, address wallet, uint16 feeBps) external onlyOwner {
        require(wallet != address(0), "TipSplitter: invalid wallet");
        require(wallet != lesserWallet, "TipSplitter: wallet cannot be lesser wallet");
        require(feeBps <= MAX_HOST_FEE_BPS, "TipSplitter: fee too high");
        require(hosts[hostId].wallet == address(0), "TipSplitter: host exists");

        hosts[hostId] = HostConfig({wallet: wallet, feeBps: feeBps, isActive: true});
        emit HostRegistered(hostId, wallet, feeBps);
    }

    function updateHost(bytes32 hostId, address wallet, uint16 feeBps) external onlyOwner {
        require(hosts[hostId].wallet != address(0), "TipSplitter: host missing");
        require(wallet != address(0), "TipSplitter: invalid wallet");
        require(wallet != lesserWallet, "TipSplitter: wallet cannot be lesser wallet");
        require(feeBps <= MAX_HOST_FEE_BPS, "TipSplitter: fee too high");

        address oldWallet = hosts[hostId].wallet;
        if (oldWallet != wallet) {
            _migratePendingBalances(oldWallet, wallet);
        }

        hosts[hostId].wallet = wallet;
        hosts[hostId].feeBps = feeBps;
        emit HostUpdated(hostId, wallet, feeBps);
    }

    function setHostActive(bytes32 hostId, bool active) external onlyOwner {
        require(hosts[hostId].wallet != address(0), "TipSplitter: host missing");
        hosts[hostId].isActive = active;
        emit HostActiveSet(hostId, active);
    }

    // ========= Token Allowlist (owner) =========

    function setTokenAllowed(address token, bool allowed) external onlyOwner {
        require(token != address(0), "TipSplitter: token required");

        if (allowed && !allowedTokens[token]) {
            allowedTokenList.push(token);
            _tokenIndex[token] = allowedTokenList.length; // 1-based
        } else if (!allowed && allowedTokens[token]) {
            uint256 idx = _tokenIndex[token] - 1; // convert to 0-based
            uint256 last = allowedTokenList.length - 1;
            if (idx != last) {
                address lastToken = allowedTokenList[last];
                allowedTokenList[idx] = lastToken;
                _tokenIndex[lastToken] = idx + 1; // 1-based
            }
            allowedTokenList.pop();
            delete _tokenIndex[token];
        }

        allowedTokens[token] = allowed;
        emit TokenAllowedSet(token, allowed);
    }

    /// @notice Returns the number of tokens in the allowlist.
    function allowedTokenCount() external view returns (uint256) {
        return allowedTokenList.length;
    }

    // ========= Admin =========

    function setLesserWallet(address newWallet) external onlyOwner {
        require(newWallet != address(0), "TipSplitter: invalid wallet");
        address old = lesserWallet;
        if (old != newWallet) {
            _migratePendingBalances(old, newWallet);
        }
        lesserWallet = newWallet;
        emit LesserWalletUpdated(old, newWallet);
    }

    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }

    /// @notice Set the maximum tip amount for a token. Use address(0) for ETH. 0 = unlimited.
    function setMaxTipAmount(address token, uint256 amount) external onlyOwner {
        maxTipAmount[token] = amount;
        emit MaxTipAmountSet(token, amount);
    }

    // ========= Helpers =========

    /// @dev Migrates all pending ETH and token balances from one address to another.
    function _migratePendingBalances(address from, address to) internal {
        // Migrate pending ETH
        uint256 ethBal = pendingETH[from];
        if (ethBal > 0) {
            pendingETH[from] = 0;
            pendingETH[to] += ethBal;
            emit PendingBalanceMigrated(address(0), from, to, ethBal);
        }

        // Migrate pending tokens for all known allowed tokens
        uint256 len = allowedTokenList.length;
        for (uint256 i = 0; i < len; i++) {
            address token = allowedTokenList[i];
            uint256 tokenBal = pendingToken[token][from];
            if (tokenBal > 0) {
                pendingToken[token][from] = 0;
                pendingToken[token][to] += tokenBal;
                emit PendingBalanceMigrated(token, from, to, tokenBal);
            }
        }
    }

    function _split(uint16 hostFeeBps, uint256 amount)
        internal
        pure
        returns (uint256 lesserShare, uint256 hostShare, uint256 actorShare)
    {
        lesserShare = (amount * LESSER_FEE_BPS) / BPS_DENOMINATOR;
        hostShare = (amount * hostFeeBps) / BPS_DENOMINATOR;
        actorShare = amount - lesserShare - hostShare;
    }
}
