// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {SafeERC20} from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Ownable2Step} from "@openzeppelin/contracts/access/Ownable2Step.sol";
import {Pausable} from "@openzeppelin/contracts/utils/Pausable.sol";
import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";

interface IERC8004IdentityRegistry {
    function getAgentWallet(uint256 agentId) external view returns (address);
}

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
    address public agentIdentityRegistry;
    bool public withdrawalsPaused;

    struct HostConfig {
        address wallet;
        uint16 feeBps;
        bool isActive;
    }

    mapping(bytes32 => HostConfig) public hosts; // hostId => config

    mapping(address => uint256) public pendingETH; // recipient => wei
    mapping(address => mapping(address => uint256)) public pendingToken; // token => recipient => amount
    uint256 public totalPendingETH; // total tracked ETH liabilities across all recipients
    mapping(address => uint256) public totalPendingToken; // token => total tracked token liabilities

    mapping(address => bool) public allowedTokens; // ERC20 token allowlist (ETH is always allowed)
    address[] public allowedTokenList; // enumerable list for allowlist management
    mapping(address => uint256) private _tokenIndex; // token => index in allowedTokenList (1-based)

    mapping(address => uint256) private _hostWalletRefCount; // wallet => number of hosts using it

    // recipient => list of tokens with a non-zero pending balance for that recipient.
    // Maintained to make migrations bounded to the recipient's active tokens (instead of a global ever-allowed list).
    mapping(address => address[]) private _recipientTokenList;
    mapping(address => mapping(address => uint256)) private _recipientTokenIndex; // recipient => token => index in _recipientTokenList (1-based)

    /// @notice Per-token max tip amount (0 = unlimited). Use address(0) key for ETH.
    mapping(address => uint256) public maxTipAmount;
    /// @notice Per-token min tip amount (0 = MIN_TIP_AMOUNT unless hasCustomMinTipAmount is true). Use address(0) key for ETH.
    mapping(address => uint256) public minTipAmount;
    mapping(address => bool) public hasCustomMinTipAmount;

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
    event MaxTipAmountSet(address indexed token, uint256 amount);
    event MinTipAmountSet(address indexed token, uint256 oldAmount, uint256 newAmount);
    event WithdrawalsPausedSet(bool paused);
    event StraySwept(address indexed token, address indexed destination, uint256 amount);
    event AgentIdentityRegistryUpdated(address indexed oldRegistry, address indexed newRegistry);
    event AgentTipSent(
        bytes32 indexed hostId,
        uint256 indexed agentId,
        address indexed token,
        address tipper,
        address agentWallet,
        uint256 amount,
        bytes32 contentHash
    );

    constructor(address _lesserWallet, address initialOwner, address _agentIdentityRegistry) Ownable(initialOwner) {
        require(_lesserWallet != address(0), "TipSplitter: invalid lesser wallet");
        require(initialOwner != address(0), "TipSplitter: invalid owner");
        if (_agentIdentityRegistry != address(0)) {
            require(_agentIdentityRegistry.code.length > 0, "TipSplitter: invalid agent registry");
        }
        lesserWallet = _lesserWallet;
        agentIdentityRegistry = _agentIdentityRegistry;
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
        require(amount >= _effectiveMinTipAmount(address(0)), "TipSplitter: amount below minimum");
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
        totalPendingETH += amount;

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
        // slither-disable-next-line incorrect-equality
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
        // slither-disable-next-line incorrect-equality
        require(received == amount, "TipSplitter: fee-on-transfer tokens not supported");

        _creditToken(hostId, token, actor, amount, contentHash, msg.sender);
    }

    function _creditToken(bytes32 hostId, address token, address actor, uint256 amount, bytes32 contentHash, address tipper) internal {
        require(actor != address(0), "TipSplitter: invalid actor");
        require(actor != tipper, "TipSplitter: cannot tip yourself");
        require(amount >= _effectiveMinTipAmount(token), "TipSplitter: amount below minimum");

        uint256 cap = maxTipAmount[token];
        require(cap == 0 || amount <= cap, "TipSplitter: amount exceeds max");

        HostConfig memory host = hosts[hostId];
        require(host.wallet != address(0) && host.isActive, "TipSplitter: host not active");

        (uint256 lesserShare, uint256 hostShare, uint256 actorShare) = _split(host.feeBps, amount);

        _creditPendingToken(token, lesserWallet, lesserShare);
        _creditPendingToken(token, host.wallet, hostShare);
        _creditPendingToken(token, actor, actorShare);

        emit TipSent(hostId, token, tipper, actor, amount, lesserShare, hostShare, actorShare, contentHash);
    }

    // ========= Tips (ERC-8004 agents) =========

    function tipAgentETH(bytes32 hostId, uint256 agentId, bytes32 contentHash)
        external
        payable
        nonReentrant
        whenNotPaused
    {
        address actor = _resolveAgentWallet(agentId);
        _tipETH(hostId, actor, msg.value, contentHash);
        emit AgentTipSent(hostId, agentId, address(0), msg.sender, actor, msg.value, contentHash);
    }

    function tipAgentToken(address token, bytes32 hostId, uint256 agentId, uint256 amount, bytes32 contentHash)
        external
        nonReentrant
        whenNotPaused
    {
        address actor = _resolveAgentWallet(agentId);
        _tipToken(token, hostId, actor, amount, contentHash);
        emit AgentTipSent(hostId, agentId, token, msg.sender, actor, amount, contentHash);
    }

    function _resolveAgentWallet(uint256 agentId) internal view returns (address) {
        address reg = agentIdentityRegistry;
        require(reg != address(0), "TipSplitter: agent registry not set");
        address wallet = IERC8004IdentityRegistry(reg).getAgentWallet(agentId);
        require(wallet != address(0), "TipSplitter: agent wallet not set");
        return wallet;
    }

    // ========= Withdrawals =========

    /// @notice Withdraw pending balance. Withdrawals can be paused independently.
    function withdraw(address token) external nonReentrant {
        require(!withdrawalsPaused, "TipSplitter: withdrawals paused");
        if (token == address(0)) {
            uint256 ethAmount = pendingETH[msg.sender];
            require(ethAmount > 0, "TipSplitter: no pending");
            pendingETH[msg.sender] = 0;
            totalPendingETH -= ethAmount;

            // slither-disable-next-line low-level-calls
            (bool ok, ) = payable(msg.sender).call{value: ethAmount}("");
            require(ok, "TipSplitter: withdrawal failed");

            emit Withdrawal(address(0), msg.sender, ethAmount);
            return;
        }

        uint256 tokenAmount = pendingToken[token][msg.sender];
        require(tokenAmount > 0, "TipSplitter: no pending");
        pendingToken[token][msg.sender] = 0;
        _removeRecipientToken(msg.sender, token);
        totalPendingToken[token] -= tokenAmount;

        IERC20(token).safeTransfer(msg.sender, tokenAmount);
        emit Withdrawal(token, msg.sender, tokenAmount);
    }

    // ========= Host Registry (owner) =========

    function registerHost(bytes32 hostId, address wallet, uint16 feeBps) external onlyOwner {
        require(wallet != address(0), "TipSplitter: invalid wallet");
        require(wallet != lesserWallet, "TipSplitter: wallet cannot be lesser wallet");
        require(feeBps <= MAX_HOST_FEE_BPS, "TipSplitter: fee too high");
        require(hosts[hostId].wallet == address(0), "TipSplitter: host exists");

        _hostWalletRefCount[wallet]++;
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
            _hostWalletRefCount[oldWallet]--;
            _hostWalletRefCount[wallet]++;
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

    /// @notice Add or remove an ERC20 token from the allowlist.
    /// @dev DO NOT allow rebasing tokens. This contract defends against fee-on-transfer but cannot handle rebasing shifts.
    function setTokenAllowed(address token, bool allowed) external onlyOwner {
        require(token != address(0), "TipSplitter: token required");
        if (allowed) {
            require(token.code.length > 0, "TipSplitter: token has no code");
        }

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
        require(newWallet != lesserWallet, "TipSplitter: no-op");
        require(_hostWalletRefCount[newWallet] == 0, "TipSplitter: wallet is a host wallet");
        address old = lesserWallet;
        lesserWallet = newWallet;
        emit LesserWalletUpdated(old, newWallet);
    }

    function setAgentIdentityRegistry(address registry) external onlyOwner {
        if (registry != address(0)) {
            require(registry.code.length > 0, "TipSplitter: invalid agent registry");
        }
        address old = agentIdentityRegistry;
        agentIdentityRegistry = registry;
        emit AgentIdentityRegistryUpdated(old, registry);
    }

    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }

    /// @notice Pause or unpause withdrawals independently from tips.
    function setWithdrawalsPaused(bool paused_) external onlyOwner {
        withdrawalsPaused = paused_;
        emit WithdrawalsPausedSet(paused_);
    }

    /// @notice Set the maximum tip amount for a token. Use address(0) for ETH. 0 = unlimited.
    function setMaxTipAmount(address token, uint256 amount) external onlyOwner {
        if (amount > 0) {
            uint256 effectiveMin = _effectiveMinTipAmount(token);
            require(amount >= effectiveMin, "TipSplitter: max below min");
        }
        maxTipAmount[token] = amount;
        emit MaxTipAmountSet(token, amount);
    }

    /// @notice Set the minimum tip amount for a token. Use address(0) for ETH.
    function setMinTipAmount(address token, uint256 amount) external onlyOwner {
        require(amount >= 100, "TipSplitter: min amount too low");
        uint256 cap = maxTipAmount[token];
        if (cap > 0) {
            require(amount <= cap, "TipSplitter: min exceeds max");
        }
        uint256 oldAmount = minTipAmount[token];
        minTipAmount[token] = amount;
        hasCustomMinTipAmount[token] = true;
        emit MinTipAmountSet(token, oldAmount, amount);
    }

    // ========= Emergency =========

    /// @notice Sweep untracked ETH balance to lesserWallet.
    /// @dev Only sweeps ETH above tracked pending liabilities; requires full emergency pause.
    function sweepStrayETH() external onlyOwner nonReentrant {
        require(paused(), "TipSplitter: must be paused");
        require(withdrawalsPaused, "TipSplitter: withdrawals must be paused");

        uint256 bal = address(this).balance;
        uint256 accounted = totalPendingETH;
        require(bal > accounted, "TipSplitter: no stray");
        uint256 amount = bal - accounted;

        // slither-disable-next-line low-level-calls
        (bool ok, ) = payable(lesserWallet).call{value: amount}("");
        require(ok, "TipSplitter: sweep failed");

        emit StraySwept(address(0), lesserWallet, amount);
    }

    /// @notice Sweep untracked token balance to lesserWallet.
    /// @dev Only sweeps token balance above tracked pending liabilities; requires full emergency pause.
    function sweepStrayToken(address token) external onlyOwner nonReentrant {
        require(paused(), "TipSplitter: must be paused");
        require(withdrawalsPaused, "TipSplitter: withdrawals must be paused");
        require(token != address(0), "TipSplitter: token required");

        uint256 bal = IERC20(token).balanceOf(address(this));
        uint256 accounted = totalPendingToken[token];
        require(bal > accounted, "TipSplitter: no stray");
        uint256 amount = bal - accounted;

        IERC20(token).safeTransfer(lesserWallet, amount);
        emit StraySwept(token, lesserWallet, amount);
    }

    // ========= Helpers =========

    function _effectiveMinTipAmount(address token) internal view returns (uint256) {
        if (hasCustomMinTipAmount[token]) {
            return minTipAmount[token];
        }
        return MIN_TIP_AMOUNT;
    }

    function _creditPendingToken(address token, address recipient, uint256 amount) internal {
        if (amount == 0) {
            return;
        }
        if (_recipientTokenIndex[recipient][token] == 0) {
            _recipientTokenList[recipient].push(token);
            _recipientTokenIndex[recipient][token] = _recipientTokenList[recipient].length; // 1-based
        }
        pendingToken[token][recipient] += amount;
        totalPendingToken[token] += amount;
    }

    function _removeRecipientToken(address recipient, address token) internal {
        uint256 idx = _recipientTokenIndex[recipient][token];
        if (idx == 0) {
            return;
        }
        uint256 last = _recipientTokenList[recipient].length;
        if (idx != last) {
            address lastToken = _recipientTokenList[recipient][last - 1];
            _recipientTokenList[recipient][idx - 1] = lastToken;
            _recipientTokenIndex[recipient][lastToken] = idx;
        }
        _recipientTokenList[recipient].pop();
        delete _recipientTokenIndex[recipient][token];
    }

    function _split(uint16 hostFeeBps, uint256 amount)
        internal
        pure
        returns (uint256 lesserShare, uint256 hostShare, uint256 actorShare)
    {
        lesserShare = (amount * LESSER_FEE_BPS) / BPS_DENOMINATOR;
        require(lesserShare > 0, "TipSplitter: amount too small for split");
        hostShare = (amount * hostFeeBps) / BPS_DENOMINATOR;
        actorShare = amount - lesserShare - hostShare;
    }
}
