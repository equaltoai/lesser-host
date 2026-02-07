import { describe, it, before } from "node:test";
import assert from "node:assert/strict";
import hre from "hardhat";

// Lazily initialized constants (must wait for connection)
let ethers;
let connection;

const HOST_FEE_BPS = 300;
const MIN_TIP = 10_000n;

// Initialize once before all tests
before(async () => {
    connection = await hre.network.connect();
    ethers = connection.ethers;
});

// ======== Helper: expected split ========
function expectedSplit(amount, hostBps = 300n) {
    const BPS = 10_000n;
    const lesserShare = (amount * 100n) / BPS;
    const hostShare = (amount * hostBps) / BPS;
    const actorShare = amount - lesserShare - hostShare;
    return { lesserShare, hostShare, actorShare };
}

// ======== Deploy helper ========
async function deploy() {
    const HOST_ID = ethers.id("host.example.com");
    const CONTENT_HASH = ethers.id("post:12345");

    const [owner, lesserWallet, hostWallet, actor, tipper, other] =
        await ethers.getSigners();

    const TipSplitter = await ethers.getContractFactory("TipSplitter");
    const splitter = await TipSplitter.deploy(lesserWallet.address, owner.address);

    const MockERC20 = await ethers.getContractFactory("MockERC20");
    const token = await MockERC20.deploy("TestToken", "TT", 18);
    const tokenAddr = await token.getAddress();

    await splitter.registerHost(HOST_ID, hostWallet.address, HOST_FEE_BPS);
    await splitter.setTokenAllowed(tokenAddr, true);
    await token.mint(tipper.address, ethers.parseEther("1000"));
    await token.connect(tipper).approve(
        await splitter.getAddress(),
        ethers.parseEther("1000")
    );

    return {
        splitter, token, tokenAddr, owner, lesserWallet,
        hostWallet, actor, tipper, other,
        HOST_ID, CONTENT_HASH,
    };
}

// ======================================================================
//  DEPLOYMENT
// ======================================================================
describe("TipSplitter — Deployment", () => {
    it("sets lesserWallet and owner correctly", async () => {
        const { splitter, lesserWallet, owner } = await deploy();
        assert.equal(await splitter.lesserWallet(), lesserWallet.address);
        assert.equal(await splitter.owner(), owner.address);
    });

    it("reverts if lesserWallet is zero", async () => {
        const [owner] = await ethers.getSigners();
        const F = await ethers.getContractFactory("TipSplitter");
        await assert.rejects(F.deploy(ethers.ZeroAddress, owner.address), /invalid lesser wallet/);
    });

    it("reverts if owner is zero", async () => {
        const [, lw] = await ethers.getSigners();
        const F = await ethers.getContractFactory("TipSplitter");
        await assert.rejects(F.deploy(lw.address, ethers.ZeroAddress), /OwnableInvalidOwner/);
    });

    it("stores correct constants", async () => {
        const { splitter } = await deploy();
        assert.equal(await splitter.LESSER_FEE_BPS(), 100n);
        assert.equal(await splitter.MAX_HOST_FEE_BPS(), 500n);
        assert.equal(await splitter.BPS_DENOMINATOR(), 10_000n);
        assert.equal(await splitter.MIN_TIP_AMOUNT(), 10_000n);
    });
});

// ======================================================================
//  RECEIVE FALLBACK
// ======================================================================
describe("TipSplitter — Receive fallback", () => {
    it("reverts direct ETH sends", async () => {
        const { splitter, tipper } = await deploy();
        await assert.rejects(
            tipper.sendTransaction({ to: await splitter.getAddress(), value: 1n }),
            /use tipETH/
        );
    });
});

// ======================================================================
//  ETH TIPS
// ======================================================================
describe("TipSplitter — tipETH", () => {
    it("splits tip correctly between lesser, host, and actor", async () => {
        const { splitter, lesserWallet, hostWallet, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        const s = expectedSplit(amount);

        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });

        assert.equal(await splitter.pendingETH(lesserWallet.address), s.lesserShare);
        assert.equal(await splitter.pendingETH(hostWallet.address), s.hostShare);
        assert.equal(await splitter.pendingETH(actor.address), s.actorShare);
    });

    it("emits TipSent event", async () => {
        const { splitter, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        const tx = await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });
        const receipt = await tx.wait();
        const tipEvent = receipt.logs.find((l) => l.fragment && l.fragment.name === "TipSent");
        assert.ok(tipEvent, "TipSent event should be emitted");
    });

    it("reverts if amount below minimum", async () => {
        const { splitter, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: 100n }),
            /amount below minimum/
        );
    });

    it("reverts if actor is zero address", async () => {
        const { splitter, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).tipETH(HOST_ID, ethers.ZeroAddress, CONTENT_HASH, { value: MIN_TIP }),
            /invalid actor/
        );
    });

    it("reverts if tipping yourself", async () => {
        const { splitter, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).tipETH(HOST_ID, tipper.address, CONTENT_HASH, { value: MIN_TIP }),
            /cannot tip yourself/
        );
    });

    it("reverts if host is not active", async () => {
        const { splitter, actor, tipper, CONTENT_HASH } = await deploy();
        const fakeHost = ethers.id("unregistered.host");
        await assert.rejects(
            splitter.connect(tipper).tipETH(fakeHost, actor.address, CONTENT_HASH, { value: MIN_TIP }),
            /host not active/
        );
    });

    it("reverts when paused", async () => {
        const { splitter, actor, tipper, owner, HOST_ID, CONTENT_HASH } = await deploy();
        await splitter.connect(owner).pause();
        await assert.rejects(
            splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: MIN_TIP }),
            /EnforcedPause/
        );
    });

    it("reverts if amount exceeds max tip", async () => {
        const { splitter, actor, tipper, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const cap = ethers.parseEther("0.5");
        await splitter.connect(owner).setMaxTipAmount(ethers.ZeroAddress, cap);
        await assert.rejects(
            splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: ethers.parseEther("1") }),
            /amount exceeds max/
        );
    });

    it("allows tip at or below max", async () => {
        const { splitter, actor, tipper, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const cap = ethers.parseEther("1");
        await splitter.connect(owner).setMaxTipAmount(ethers.ZeroAddress, cap);
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: cap });
    });
});

// ======================================================================
//  BATCH ETH TIPS
// ======================================================================
describe("TipSplitter — batchTipETH", () => {
    it("processes batch of 3 tips correctly", async () => {
        const { splitter, actor, tipper, other, HOST_ID, CONTENT_HASH } = await deploy();
        const amounts = [MIN_TIP, MIN_TIP * 2n, MIN_TIP * 3n];
        const total = amounts.reduce((a, b) => a + b, 0n);
        const actors = [actor.address, other.address, actor.address];
        const hashes = [CONTENT_HASH, CONTENT_HASH, CONTENT_HASH];

        await splitter.connect(tipper).batchTipETH(HOST_ID, actors, amounts, hashes, { value: total });

        const s0 = expectedSplit(amounts[0]);
        const s2 = expectedSplit(amounts[2]);
        assert.equal(await splitter.pendingETH(actor.address), s0.actorShare + s2.actorShare);
    });

    it("reverts on empty batch", async () => {
        const { splitter, tipper, HOST_ID } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).batchTipETH(HOST_ID, [], [], [], { value: 0n }),
            /invalid batch size/
        );
    });

    it("reverts on batch > 20", async () => {
        const { splitter, tipper, actor, HOST_ID, CONTENT_HASH } = await deploy();
        const actors = new Array(21).fill(actor.address);
        const amounts = new Array(21).fill(MIN_TIP);
        const hashes = new Array(21).fill(CONTENT_HASH);
        await assert.rejects(
            splitter.connect(tipper).batchTipETH(HOST_ID, actors, amounts, hashes, { value: MIN_TIP * 21n }),
            /invalid batch size/
        );
    });

    it("reverts on array length mismatch", async () => {
        const { splitter, tipper, actor, HOST_ID, CONTENT_HASH } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).batchTipETH(HOST_ID, [actor.address, actor.address], [MIN_TIP], [CONTENT_HASH, CONTENT_HASH], { value: MIN_TIP }),
            /array length mismatch/
        );
    });

    it("reverts if msg.value does not match total", async () => {
        const { splitter, tipper, actor, HOST_ID, CONTENT_HASH } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).batchTipETH(HOST_ID, [actor.address], [MIN_TIP], [CONTENT_HASH], { value: MIN_TIP - 1n }),
            /incorrect total/
        );
    });
});

// ======================================================================
//  ERC-20 TIPS
// ======================================================================
describe("TipSplitter — tipToken", () => {
    it("splits ERC-20 tip correctly", async () => {
        const { splitter, tokenAddr, lesserWallet, hostWallet, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("10");
        const s = expectedSplit(amount);

        await splitter.connect(tipper).tipToken(tokenAddr, HOST_ID, actor.address, amount, CONTENT_HASH);

        assert.equal(await splitter.pendingToken(tokenAddr, lesserWallet.address), s.lesserShare);
        assert.equal(await splitter.pendingToken(tokenAddr, hostWallet.address), s.hostShare);
        assert.equal(await splitter.pendingToken(tokenAddr, actor.address), s.actorShare);
    });

    it("reverts for disallowed token", async () => {
        const { splitter, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        const fake = "0x0000000000000000000000000000000000000001";
        await assert.rejects(
            splitter.connect(tipper).tipToken(fake, HOST_ID, actor.address, MIN_TIP, CONTENT_HASH),
            /token not allowed/
        );
    });

    it("reverts if token is zero address", async () => {
        const { splitter, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).tipToken(ethers.ZeroAddress, HOST_ID, actor.address, MIN_TIP, CONTENT_HASH),
            /token required/
        );
    });

    it("reverts if amount is 0", async () => {
        const { splitter, tokenAddr, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).tipToken(tokenAddr, HOST_ID, actor.address, 0, CONTENT_HASH),
            /amount must be > 0/
        );
    });

    it("reverts if amount below minimum", async () => {
        const { splitter, tokenAddr, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).tipToken(tokenAddr, HOST_ID, actor.address, 100n, CONTENT_HASH),
            /amount below minimum/
        );
    });

    it("reverts when max tip exceeded for token", async () => {
        const { splitter, tokenAddr, actor, tipper, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const cap = ethers.parseEther("5");
        await splitter.connect(owner).setMaxTipAmount(tokenAddr, cap);
        await assert.rejects(
            splitter.connect(tipper).tipToken(tokenAddr, HOST_ID, actor.address, ethers.parseEther("10"), CONTENT_HASH),
            /amount exceeds max/
        );
    });
});

// ======================================================================
//  BATCH ERC-20 TIPS
// ======================================================================
describe("TipSplitter — batchTipToken", () => {
    it("processes batch of 2 token tips", async () => {
        const { splitter, tokenAddr, actor, tipper, other, HOST_ID, CONTENT_HASH } = await deploy();
        const amounts = [MIN_TIP, MIN_TIP * 5n];

        await splitter.connect(tipper).batchTipToken(tokenAddr, HOST_ID, [actor.address, other.address], amounts, [CONTENT_HASH, CONTENT_HASH]);

        const s0 = expectedSplit(amounts[0]);
        assert.equal(await splitter.pendingToken(tokenAddr, actor.address), s0.actorShare);
    });

    it("reverts on empty batch", async () => {
        const { splitter, tokenAddr, tipper, HOST_ID } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).batchTipToken(tokenAddr, HOST_ID, [], [], []),
            /invalid batch size/
        );
    });
});

// ======================================================================
//  WITHDRAWALS
// ======================================================================
describe("TipSplitter — Withdrawals", () => {
    it("withdraws pending ETH", async () => {
        const { splitter, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });

        const s = expectedSplit(amount);
        const balBefore = await actor.provider.getBalance(actor.address);

        const tx = await splitter.connect(actor).withdraw(ethers.ZeroAddress);
        const receipt = await tx.wait();
        const gasUsed = receipt.gasUsed * receipt.gasPrice;

        const balAfter = await actor.provider.getBalance(actor.address);
        assert.equal(balAfter, balBefore + s.actorShare - gasUsed);
        assert.equal(await splitter.pendingETH(actor.address), 0n);
    });

    it("withdraws pending ERC-20", async () => {
        const { splitter, token, tokenAddr, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("10");
        await splitter.connect(tipper).tipToken(tokenAddr, HOST_ID, actor.address, amount, CONTENT_HASH);

        const s = expectedSplit(amount);
        const balBefore = await token.balanceOf(actor.address);
        await splitter.connect(actor).withdraw(tokenAddr);
        const balAfter = await token.balanceOf(actor.address);

        assert.equal(balAfter - balBefore, s.actorShare);
        assert.equal(await splitter.pendingToken(tokenAddr, actor.address), 0n);
    });

    it("reverts if no pending ETH", async () => {
        const { splitter, actor } = await deploy();
        await assert.rejects(splitter.connect(actor).withdraw(ethers.ZeroAddress), /no pending/);
    });

    it("reverts if no pending token", async () => {
        const { splitter, tokenAddr, actor } = await deploy();
        await assert.rejects(splitter.connect(actor).withdraw(tokenAddr), /no pending/);
    });

    it("allows withdrawals when paused", async () => {
        const { splitter, actor, tipper, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });
        await splitter.connect(owner).pause();
        await splitter.connect(actor).withdraw(ethers.ZeroAddress);
    });

    it("reverts when withdrawals are paused", async () => {
        const { splitter, actor, tipper, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });
        await splitter.connect(owner).setWithdrawalsPaused(true);
        await assert.rejects(splitter.connect(actor).withdraw(ethers.ZeroAddress), /withdrawals paused/);
    });

    it("works again after withdrawals unpause", async () => {
        const { splitter, actor, tipper, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });
        await splitter.connect(owner).setWithdrawalsPaused(true);
        await assert.rejects(splitter.connect(actor).withdraw(ethers.ZeroAddress), /withdrawals paused/);
        await splitter.connect(owner).setWithdrawalsPaused(false);
        await splitter.connect(actor).withdraw(ethers.ZeroAddress);
    });

    it("emits Withdrawal event", async () => {
        const { splitter, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });
        const tx = await splitter.connect(actor).withdraw(ethers.ZeroAddress);
        const receipt = await tx.wait();
        const event = receipt.logs.find((l) => l.fragment && l.fragment.name === "Withdrawal");
        assert.ok(event, "Withdrawal event should be emitted");
    });
});

// ======================================================================
//  HOST REGISTRY
// ======================================================================
describe("TipSplitter — Host Registry", () => {
    it("registers a new host", async () => {
        const { splitter, other, owner } = await deploy();
        const newHostId = ethers.id("new.host");
        await splitter.connect(owner).registerHost(newHostId, other.address, 200);
        const [wallet, feeBps, isActive] = await splitter.hosts(newHostId);
        assert.equal(wallet, other.address);
        assert.equal(feeBps, 200n);
        assert.equal(isActive, true);
    });

    it("reverts if host already exists", async () => {
        const { splitter, other, owner, HOST_ID } = await deploy();
        await assert.rejects(
            splitter.connect(owner).registerHost(HOST_ID, other.address, 200),
            /host exists/
        );
    });

    it("reverts if wallet is zero", async () => {
        const { splitter, owner } = await deploy();
        await assert.rejects(
            splitter.connect(owner).registerHost(ethers.id("z"), ethers.ZeroAddress, 100),
            /invalid wallet/
        );
    });

    it("reverts if fee too high", async () => {
        const { splitter, other, owner } = await deploy();
        await assert.rejects(
            splitter.connect(owner).registerHost(ethers.id("f"), other.address, 501),
            /fee too high/
        );
    });

    it("rejects wallet == lesserWallet (M-3)", async () => {
        const { splitter, lesserWallet, owner } = await deploy();
        await assert.rejects(
            splitter.connect(owner).registerHost(ethers.id("c"), lesserWallet.address, 100),
            /wallet cannot be lesser wallet/
        );
    });

    it("updates host wallet and fee", async () => {
        const { splitter, other, owner, HOST_ID } = await deploy();
        await splitter.connect(owner).updateHost(HOST_ID, other.address, 400);
        const [wallet, feeBps] = await splitter.hosts(HOST_ID);
        assert.equal(wallet, other.address);
        assert.equal(feeBps, 400n);
    });

    it("updateHost rejects wallet == lesserWallet (M-3)", async () => {
        const { splitter, lesserWallet, owner, HOST_ID } = await deploy();
        await assert.rejects(
            splitter.connect(owner).updateHost(HOST_ID, lesserWallet.address, 100),
            /wallet cannot be lesser wallet/
        );
    });

    it("reverts updateHost for missing host", async () => {
        const { splitter, other, owner } = await deploy();
        await assert.rejects(
            splitter.connect(owner).updateHost(ethers.id("missing"), other.address, 100),
            /host missing/
        );
    });

    it("sets host active/inactive and re-enables tips", async () => {
        const { splitter, owner, actor, tipper, HOST_ID, CONTENT_HASH } = await deploy();
        await splitter.connect(owner).setHostActive(HOST_ID, false);
        await assert.rejects(
            splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: MIN_TIP }),
            /host not active/
        );
        await splitter.connect(owner).setHostActive(HOST_ID, true);
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: MIN_TIP });
    });
});

// ======================================================================
//  WALLET MIGRATION (C-1, C-2)
// ======================================================================
describe("TipSplitter — Wallet Migration (C-1, C-2)", () => {
    it("C-1: setLesserWallet migrates pending ETH balances", async () => {
        const { splitter, lesserWallet, actor, tipper, other, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });

        const s = expectedSplit(amount);
        assert.equal(await splitter.pendingETH(lesserWallet.address), s.lesserShare);

        await splitter.connect(owner).setLesserWallet(other.address);
        assert.equal(await splitter.pendingETH(lesserWallet.address), 0n);
        assert.equal(await splitter.pendingETH(other.address), s.lesserShare);
    });

    it("C-1: setLesserWallet migrates pending token balances", async () => {
        const { splitter, tokenAddr, lesserWallet, actor, tipper, other, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("10");
        await splitter.connect(tipper).tipToken(tokenAddr, HOST_ID, actor.address, amount, CONTENT_HASH);

        const s = expectedSplit(amount);
        assert.equal(await splitter.pendingToken(tokenAddr, lesserWallet.address), s.lesserShare);

        await splitter.connect(owner).setLesserWallet(other.address);
        assert.equal(await splitter.pendingToken(tokenAddr, lesserWallet.address), 0n);
        assert.equal(await splitter.pendingToken(tokenAddr, other.address), s.lesserShare);
    });

    it("C-2: updateHost migrates pending ETH to new host wallet", async () => {
        const { splitter, hostWallet, actor, tipper, other, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });

        const s = expectedSplit(amount);
        assert.equal(await splitter.pendingETH(hostWallet.address), s.hostShare);

        await splitter.connect(owner).updateHost(HOST_ID, other.address, HOST_FEE_BPS);
        assert.equal(await splitter.pendingETH(hostWallet.address), 0n);
        assert.equal(await splitter.pendingETH(other.address), s.hostShare);
    });

    it("C-2: updateHost migrates pending token balances", async () => {
        const { splitter, tokenAddr, hostWallet, actor, tipper, other, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("10");
        await splitter.connect(tipper).tipToken(tokenAddr, HOST_ID, actor.address, amount, CONTENT_HASH);

        const s = expectedSplit(amount);
        assert.equal(await splitter.pendingToken(tokenAddr, hostWallet.address), s.hostShare);

        await splitter.connect(owner).updateHost(HOST_ID, other.address, HOST_FEE_BPS);
        assert.equal(await splitter.pendingToken(tokenAddr, hostWallet.address), 0n);
        assert.equal(await splitter.pendingToken(tokenAddr, other.address), s.hostShare);
    });

    it("emits PendingBalanceMigrated event", async () => {
        const { splitter, actor, tipper, other, owner, HOST_ID, CONTENT_HASH } = await deploy();
        const amount = ethers.parseEther("1");
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: amount });

        const tx = await splitter.connect(owner).setLesserWallet(other.address);
        const receipt = await tx.wait();
        const event = receipt.logs.find((l) => l.fragment && l.fragment.name === "PendingBalanceMigrated");
        assert.ok(event, "PendingBalanceMigrated event should be emitted");
    });

    it("no-op when wallet unchanged", async () => {
        const { splitter, lesserWallet, owner } = await deploy();
        await splitter.connect(owner).setLesserWallet(lesserWallet.address);
    });
});

// ======================================================================
//  TOKEN ALLOWLIST
// ======================================================================
describe("TipSplitter — Token Allowlist", () => {
    it("adds token to allowlist and enumerable list", async () => {
        const { splitter, owner } = await deploy();
        await splitter.connect(owner).setTokenAllowed("0x0000000000000000000000000000000000000042", true);
        assert.equal(await splitter.allowedTokenCount(), 2n);
    });

    it("removes token from allowlist", async () => {
        const { splitter, tokenAddr, owner } = await deploy();
        await splitter.connect(owner).setTokenAllowed(tokenAddr, false);
        assert.equal(await splitter.allowedTokens(tokenAddr), false);
        assert.equal(await splitter.allowedTokenCount(), 0n);
    });

    it("idempotent: re-adding same token no-op", async () => {
        const { splitter, tokenAddr, owner } = await deploy();
        await splitter.connect(owner).setTokenAllowed(tokenAddr, true);
        assert.equal(await splitter.allowedTokenCount(), 1n);
    });

    it("rejects zero address token", async () => {
        const { splitter, owner } = await deploy();
        await assert.rejects(
            splitter.connect(owner).setTokenAllowed(ethers.ZeroAddress, true),
            /token required/
        );
    });
});

// ======================================================================
//  ADMIN — LESSER WALLET
// ======================================================================
describe("TipSplitter — setLesserWallet", () => {
    it("updates lesserWallet", async () => {
        const { splitter, other, owner } = await deploy();
        await splitter.connect(owner).setLesserWallet(other.address);
        assert.equal(await splitter.lesserWallet(), other.address);
    });

    it("rejects zero address", async () => {
        const { splitter, owner } = await deploy();
        await assert.rejects(splitter.connect(owner).setLesserWallet(ethers.ZeroAddress), /invalid wallet/);
    });

    it("non-owner cannot call", async () => {
        const { splitter, other } = await deploy();
        await assert.rejects(splitter.connect(other).setLesserWallet(other.address), /OwnableUnauthorizedAccount/);
    });
});

// ======================================================================
//  ADMIN — MAX TIP AMOUNT
// ======================================================================
describe("TipSplitter — setMaxTipAmount", () => {
    it("sets and reads max tip for ETH", async () => {
        const { splitter, owner } = await deploy();
        const cap = ethers.parseEther("100");
        await splitter.connect(owner).setMaxTipAmount(ethers.ZeroAddress, cap);
        assert.equal(await splitter.maxTipAmount(ethers.ZeroAddress), cap);
    });

    it("removes cap with 0", async () => {
        const { splitter, owner } = await deploy();
        await splitter.connect(owner).setMaxTipAmount(ethers.ZeroAddress, ethers.parseEther("100"));
        await splitter.connect(owner).setMaxTipAmount(ethers.ZeroAddress, 0n);
        assert.equal(await splitter.maxTipAmount(ethers.ZeroAddress), 0n);
    });

    it("non-owner cannot call", async () => {
        const { splitter, other } = await deploy();
        await assert.rejects(splitter.connect(other).setMaxTipAmount(ethers.ZeroAddress, 100n), /OwnableUnauthorizedAccount/);
    });

    it("emits MaxTipAmountSet event", async () => {
        const { splitter, owner } = await deploy();
        const tx = await splitter.connect(owner).setMaxTipAmount(ethers.ZeroAddress, 999n);
        const receipt = await tx.wait();
        const event = receipt.logs.find((l) => l.fragment && l.fragment.name === "MaxTipAmountSet");
        assert.ok(event, "MaxTipAmountSet event should be emitted");
    });
});

// ======================================================================
//  PAUSE / UNPAUSE
// ======================================================================
describe("TipSplitter — Pause / Unpause", () => {
    it("owner can pause and unpause", async () => {
        const { splitter, owner } = await deploy();
        await splitter.connect(owner).pause();
        assert.equal(await splitter.paused(), true);
        await splitter.connect(owner).unpause();
        assert.equal(await splitter.paused(), false);
    });

    it("non-owner cannot pause", async () => {
        const { splitter, tipper } = await deploy();
        await assert.rejects(splitter.connect(tipper).pause(), /OwnableUnauthorizedAccount/);
    });

    it("non-owner cannot unpause", async () => {
        const { splitter, owner, tipper } = await deploy();
        await splitter.connect(owner).pause();
        await assert.rejects(splitter.connect(tipper).unpause(), /OwnableUnauthorizedAccount/);
    });

    it("non-owner cannot set withdrawals paused", async () => {
        const { splitter, tipper } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).setWithdrawalsPaused(true),
            /OwnableUnauthorizedAccount/
        );
    });
});

// ======================================================================
//  ACCESS CONTROL (Ownable2Step)
// ======================================================================
describe("TipSplitter — Access Control", () => {
    it("non-owner cannot registerHost", async () => {
        const { splitter, tipper, other } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).registerHost(ethers.id("rogue"), other.address, 100),
            /OwnableUnauthorizedAccount/
        );
    });

    it("non-owner cannot updateHost", async () => {
        const { splitter, tipper, other, HOST_ID } = await deploy();
        await assert.rejects(
            splitter.connect(tipper).updateHost(HOST_ID, other.address, 100),
            /OwnableUnauthorizedAccount/
        );
    });

    it("non-owner cannot setHostActive", async () => {
        const { splitter, tipper, HOST_ID } = await deploy();
        await assert.rejects(splitter.connect(tipper).setHostActive(HOST_ID, false), /OwnableUnauthorizedAccount/);
    });

    it("non-owner cannot setTokenAllowed", async () => {
        const { splitter, tipper, tokenAddr } = await deploy();
        await assert.rejects(splitter.connect(tipper).setTokenAllowed(tokenAddr, false), /OwnableUnauthorizedAccount/);
    });

    it("ownership transfers via 2-step", async () => {
        const { splitter, owner, other } = await deploy();
        await splitter.connect(owner).transferOwnership(other.address);
        assert.equal(await splitter.owner(), owner.address);
        await splitter.connect(other).acceptOwnership();
        assert.equal(await splitter.owner(), other.address);
    });
});

// ======================================================================
//  FEE SPLIT ARITHMETIC (M-4)
// ======================================================================
describe("TipSplitter — Fee Split Arithmetic", () => {
    it("split at MIN_TIP_AMOUNT produces non-zero lesser share", async () => {
        const { splitter, actor, tipper, lesserWallet, HOST_ID, CONTENT_HASH } = await deploy();
        await splitter.connect(tipper).tipETH(HOST_ID, actor.address, CONTENT_HASH, { value: MIN_TIP });
        const lesserPending = await splitter.pendingETH(lesserWallet.address);
        assert.ok(lesserPending > 0n, "Lesser share should be > 0 at minimum tip");
    });

    it("exact split: 1 ETH with 3% host fee", async () => {
        const amount = ethers.parseEther("1");
        const s = expectedSplit(amount);
        assert.equal(s.lesserShare, ethers.parseEther("0.01"));
        assert.equal(s.hostShare, ethers.parseEther("0.03"));
        assert.equal(s.actorShare, ethers.parseEther("0.96"));
    });

    it("all shares sum to original amount", async () => {
        const amounts = [
            10_000n,
            ethers.parseEther("0.01"),
            ethers.parseEther("1"),
            ethers.parseEther("100"),
            ethers.parseEther("123.456789"),
        ];
        for (const amount of amounts) {
            const s = expectedSplit(amount);
            assert.equal(s.lesserShare + s.hostShare + s.actorShare, amount);
        }
    });
});
