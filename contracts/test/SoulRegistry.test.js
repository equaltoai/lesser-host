import { describe, it, before } from "node:test";
import assert from "node:assert/strict";
import hre from "hardhat";

let ethers;

before(async () => {
  const connection = await hre.network.connect();
  ethers = connection.ethers;
});

async function increaseTime(seconds) {
  await ethers.provider.send("evm_increaseTime", [Number(seconds)]);
  await ethers.provider.send("evm_mine", []);
}

async function deployRegistry({ claimWindowSeconds = 3600n } = {}) {
  const [owner, alice, bob, other] = await ethers.getSigners();
  const SoulRegistry = await ethers.getContractFactory("SoulRegistry");
  const registry = await SoulRegistry.deploy(owner.address, claimWindowSeconds);
  return { registry, owner, alice, bob, other };
}

async function deployRenderers() {
  const EtherealBlob = await ethers.getContractFactory("EtherealBlobRenderer");
  const blob = await EtherealBlob.deploy();

  const SacredGeometry = await ethers.getContractFactory("SacredGeometryRenderer");
  const sacred = await SacredGeometry.deploy();

  const Sigil = await ethers.getContractFactory("SigilRenderer");
  const sigil = await Sigil.deploy();

  return { blob, sacred, sigil };
}

describe("SoulRegistry — Minting + identity registry", () => {
  it("mints tokenId==agentId, stores metaURI, and resolves getAgentWallet", async () => {
    const { registry, owner, alice } = await deployRegistry({
      claimWindowSeconds: 3600n,
    });
    const agentId = 1n;
    const metaURI = "https://example.com/registration.json";

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, metaURI, 0);

    assert.equal(await registry.ownerOf(agentId), alice.address);
    assert.equal(await registry.tokenURI(agentId), metaURI);
    assert.equal(await registry.getAgentWallet(agentId), alice.address);
    assert.equal(await registry.agentOfToken(agentId), agentId);
  });

  it("reverts if minting the same agentId twice", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 2n;

    await registry
      .connect(owner)
      .mintSoulOwner(alice.address, agentId, "ipfs://example", 0);

    await assert.rejects(
      registry
        .connect(owner)
        .mintSoulOwner(alice.address, agentId, "ipfs://example2", 0),
      /already minted/,
    );
  });
});

describe("SoulRegistry — Soulbound behavior", () => {
  it("allows normal transfers during claim window, then blocks transfers", async () => {
    const { registry, owner, alice, bob } = await deployRegistry({
      claimWindowSeconds: 60n,
    });
    const agentId = 10n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    await registry
      .connect(alice)
      .transferFrom(alice.address, bob.address, agentId);

    assert.equal(await registry.ownerOf(agentId), bob.address);
    assert.equal(await registry.getAgentWallet(agentId), bob.address);

    await increaseTime(61);

    await assert.rejects(
      registry
        .connect(bob)
        .transferFrom(bob.address, alice.address, agentId),
      /soulbound/,
    );
  });
});

describe("SoulRegistry — Wallet rotation", () => {
  it("rotates wallet with two typed-data signatures even when soulbound", async () => {
    const { registry, owner, alice, bob, other } = await deployRegistry({
      claimWindowSeconds: 1n,
    });
    const agentId = 99n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    await increaseTime(2);

    await assert.rejects(
      registry
        .connect(alice)
        .transferFrom(alice.address, bob.address, agentId),
      /soulbound/,
    );

    const chainId = (await ethers.provider.getNetwork()).chainId;
    const verifyingContract = await registry.getAddress();
    const domain = {
      name: "LesserSoul",
      version: "1",
      chainId,
      verifyingContract,
    };
    const types = {
      WalletRotationProposal: [
        { name: "agentId", type: "uint256" },
        { name: "currentWallet", type: "address" },
        { name: "newWallet", type: "address" },
        { name: "nonce", type: "uint256" },
        { name: "deadline", type: "uint256" },
      ],
    };

    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const nonce = 0n;
    const deadline = BigInt(now + 3600);
    const message = {
      agentId,
      currentWallet: alice.address,
      newWallet: bob.address,
      nonce,
      deadline,
    };

    const newSig = await bob.signTypedData(domain, types, message);
    const currentSig = await alice.signTypedData(domain, types, message);

    await registry
      .connect(owner)
      .rotateWallet(agentId, bob.address, nonce, deadline, currentSig, newSig);

    assert.equal(await registry.ownerOf(agentId), bob.address);
    assert.equal(await registry.getAgentWallet(agentId), bob.address);
    assert.equal(await registry.agentNonces(agentId), 1n);

    await assert.rejects(
      registry
        .connect(owner)
        .rotateWallet(agentId, other.address, nonce, deadline, currentSig, newSig),
      /bad nonce/,
    );
  });

  it("reverts rotation if called by non-owner", async () => {
    const { registry, owner, alice, bob } = await deployRegistry();
    const agentId = 7n;
    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);

    const chainId = (await ethers.provider.getNetwork()).chainId;
    const verifyingContract = await registry.getAddress();
    const domain = {
      name: "LesserSoul",
      version: "1",
      chainId,
      verifyingContract,
    };
    const types = {
      WalletRotationProposal: [
        { name: "agentId", type: "uint256" },
        { name: "currentWallet", type: "address" },
        { name: "newWallet", type: "address" },
        { name: "nonce", type: "uint256" },
        { name: "deadline", type: "uint256" },
      ],
    };
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const message = {
      agentId,
      currentWallet: alice.address,
      newWallet: bob.address,
      nonce: 0n,
      deadline: BigInt(now + 3600),
    };

    const newSig = await bob.signTypedData(domain, types, message);
    const currentSig = await alice.signTypedData(domain, types, message);

    await assert.rejects(
      registry
        .connect(alice)
        .rotateWallet(
          agentId,
          bob.address,
          message.nonce,
          message.deadline,
          currentSig,
          newSig,
        ),
      /OwnableUnauthorizedAccount/,
    );
  });
});

describe("SoulRegistry — Burn", () => {
  it("burn succeeds for owner, emits event, clears wallet", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 200n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    assert.equal(await registry.getAgentWallet(agentId), alice.address);

    const tx = await registry.connect(owner).burnSoul(agentId);
    const receipt = await tx.wait();
    const burnEvent = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "SoulBurned";
      } catch {
        return false;
      }
    });
    assert.ok(burnEvent, "SoulBurned event should be emitted");

    // Wallet should be cleared
    assert.equal(await registry.getAgentWallet(agentId), ethers.ZeroAddress);

    // ownerOf should revert
    await assert.rejects(registry.ownerOf(agentId), /ERC721NonexistentToken/);
  });

  it("burn works when token is soulbound", async () => {
    const { registry, owner, alice } = await deployRegistry({
      claimWindowSeconds: 1n,
    });
    const agentId = 201n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    await increaseTime(2);

    // Confirm it's soulbound
    assert.equal(await registry.isSoulbound(agentId), true);

    // Burn should still work (owner-only, bypasses soulbound)
    await registry.connect(owner).burnSoul(agentId);
    await assert.rejects(registry.ownerOf(agentId), /ERC721NonexistentToken/);
  });

  it("reverts burn for non-owner", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 202n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);

    await assert.rejects(
      registry.connect(alice).burnSoul(agentId),
      /OwnableUnauthorizedAccount/,
    );
  });

  it("reverts burn for missing agent", async () => {
    const { registry, owner } = await deployRegistry();

    await assert.rejects(
      registry.connect(owner).burnSoul(999n),
      /ERC721NonexistentToken/,
    );
  });
});

describe("SoulRegistry — Transfer tracking", () => {
  it("transferCount is 0 after mint", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 300n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    assert.equal(await registry.transferCount(agentId), 0n);
  });

  it("increments transferCount on normal transfer", async () => {
    const { registry, owner, alice, bob } = await deployRegistry({
      claimWindowSeconds: 3600n,
    });
    const agentId = 301n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    await registry.connect(alice).transferFrom(alice.address, bob.address, agentId);

    assert.equal(await registry.transferCount(agentId), 1n);
  });

  it("increments transferCount on rotation", async () => {
    const { registry, owner, alice, bob } = await deployRegistry({
      claimWindowSeconds: 1n,
    });
    const agentId = 302n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    await increaseTime(2);

    const chainId = (await ethers.provider.getNetwork()).chainId;
    const verifyingContract = await registry.getAddress();
    const domain = {
      name: "LesserSoul",
      version: "1",
      chainId,
      verifyingContract,
    };
    const types = {
      WalletRotationProposal: [
        { name: "agentId", type: "uint256" },
        { name: "currentWallet", type: "address" },
        { name: "newWallet", type: "address" },
        { name: "nonce", type: "uint256" },
        { name: "deadline", type: "uint256" },
      ],
    };
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const message = {
      agentId,
      currentWallet: alice.address,
      newWallet: bob.address,
      nonce: 0n,
      deadline: BigInt(now + 3600),
    };
    const currentSig = await alice.signTypedData(domain, types, message);
    const newSig = await bob.signTypedData(domain, types, message);

    await registry
      .connect(owner)
      .rotateWallet(agentId, bob.address, 0n, message.deadline, currentSig, newSig);

    assert.equal(await registry.transferCount(agentId), 1n);
  });

  it("does NOT increment on burn or mint", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 303n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    assert.equal(await registry.transferCount(agentId), 0n);

    await registry.connect(owner).burnSoul(agentId);
    assert.equal(await registry.transferCount(agentId), 0n);
  });

  it("lastTransferredAt is set on transfer", async () => {
    const { registry, owner, alice, bob } = await deployRegistry({
      claimWindowSeconds: 3600n,
    });
    const agentId = 304n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    assert.equal(await registry.lastTransferredAt(agentId), 0n);

    await registry.connect(alice).transferFrom(alice.address, bob.address, agentId);
    const lastTx = await registry.lastTransferredAt(agentId);
    assert.ok(lastTx > 0n, "lastTransferredAt should be set");
  });

  it("emits SoulTransferred event", async () => {
    const { registry, owner, alice, bob } = await deployRegistry({
      claimWindowSeconds: 3600n,
    });
    const agentId = 305n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    const tx = await registry.connect(alice).transferFrom(alice.address, bob.address, agentId);
    const receipt = await tx.wait();

    const transferEvent = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "SoulTransferred";
      } catch {
        return false;
      }
    });
    assert.ok(transferEvent, "SoulTransferred event should be emitted");
  });
});

describe("SoulRegistry — Avatar styles", () => {
  it("mintSoulOwner sets avatar style", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 400n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 2);
    // No direct getter for _avatarStyle, but it affects tokenURI behavior
    // when a renderer is registered
  });

  it("setAvatarStyle by token holder succeeds", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 401n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    const tx = await registry.connect(alice).setAvatarStyle(agentId, 1);
    const receipt = await tx.wait();

    const styleEvent = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "AvatarStyleChanged";
      } catch {
        return false;
      }
    });
    assert.ok(styleEvent, "AvatarStyleChanged event should be emitted");
  });

  it("setAvatarStyle reverts for non-holder", async () => {
    const { registry, owner, alice, bob } = await deployRegistry();
    const agentId = 402n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);

    await assert.rejects(
      registry.connect(bob).setAvatarStyle(agentId, 1),
      /not token holder/,
    );
  });

  it("tokenURI falls back to metaURI when no renderer set", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 403n;
    const metaURI = "ipfs://test-meta-uri";

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, metaURI, 0);
    assert.equal(await registry.tokenURI(agentId), metaURI);
  });

  it("tokenURI returns data URI with renderer", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const { blob } = await deployRenderers();

    await registry.connect(owner).setRenderer(0, await blob.getAddress());

    const agentId = 404n;
    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);

    const uri = await registry.tokenURI(agentId);
    assert.ok(uri.startsWith("data:application/json;base64,"), "should be data URI");

    // Decode and verify JSON structure
    const json = Buffer.from(uri.replace("data:application/json;base64,", ""), "base64").toString("utf-8");
    const parsed = JSON.parse(json);
    assert.ok(parsed.name.includes("Soul #"), "name should contain Soul #");
    assert.ok(parsed.image.startsWith("data:image/svg+xml;base64,"), "image should be base64 SVG");
    assert.ok(Array.isArray(parsed.attributes), "attributes should be an array");
  });

  it("setRenderer access control: only owner", async () => {
    const { registry, alice } = await deployRegistry();
    const { blob } = await deployRenderers();

    await assert.rejects(
      registry.connect(alice).setRenderer(0, await blob.getAddress()),
      /OwnableUnauthorizedAccount/,
    );
  });
});

// ========= Permit-based minting =========

const MINT_FEE = 500000000000000n; // 0.0005 ETH

async function deployRegistryWithSigner({ claimWindowSeconds = 3600n } = {}) {
  const [owner, alice, bob, other] = await ethers.getSigners();
  const signer = ethers.Wallet.createRandom().connect(ethers.provider);
  const SoulRegistry = await ethers.getContractFactory("SoulRegistry");
  const registry = await SoulRegistry.deploy(owner.address, claimWindowSeconds);
  await registry.connect(owner).setMintSigner(signer.address);
  await registry.connect(owner).setMintFee(MINT_FEE);
  return { registry, owner, alice, bob, other, signer };
}

async function signMintPermit(signer, registry, { to, agentId, metaURI, avatarStyle, deadline }) {
  const chainId = (await ethers.provider.getNetwork()).chainId;
  const verifyingContract = await registry.getAddress();
  const domain = {
    name: "LesserSoul",
    version: "1",
    chainId,
    verifyingContract,
  };
  const types = {
    MintPermit: [
      { name: "to", type: "address" },
      { name: "agentId", type: "uint256" },
      { name: "metaURI", type: "string" },
      { name: "avatarStyle", type: "uint8" },
      { name: "deadline", type: "uint256" },
    ],
  };
  const message = { to, agentId, metaURI, avatarStyle, deadline };
  return signer.signTypedData(domain, types, message);
}

describe("SoulRegistry — Permit minting", () => {
  it("mint with valid permit succeeds and emits SoulMinted", async () => {
    const { registry, signer, alice } = await deployRegistryWithSigner();
    const agentId = 500n;
    const metaURI = "https://example.com/meta.json";
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const permit = await signMintPermit(signer, registry, {
      to: alice.address,
      agentId,
      metaURI,
      avatarStyle: 0,
      deadline,
    });

    const tx = await registry
      .connect(alice)
      .mintSoul(alice.address, agentId, metaURI, 0, deadline, permit, {
        value: MINT_FEE,
      });
    const receipt = await tx.wait();

    assert.equal(await registry.ownerOf(agentId), alice.address);
    assert.equal(await registry.getAgentWallet(agentId), alice.address);

    const mintEvent = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "SoulMinted";
      } catch {
        return false;
      }
    });
    assert.ok(mintEvent, "SoulMinted event should be emitted");
  });

  it("mint with expired deadline reverts", async () => {
    const { registry, signer, alice } = await deployRegistryWithSigner();
    const agentId = 501n;
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now - 1);

    const permit = await signMintPermit(signer, registry, {
      to: alice.address,
      agentId,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      deadline,
    });

    await assert.rejects(
      registry
        .connect(alice)
        .mintSoul(alice.address, agentId, "ipfs://m", 0, deadline, permit, {
          value: MINT_FEE,
        }),
      /expired/,
    );
  });

  it("mint with replay permit reverts", async () => {
    const { registry, signer, alice } = await deployRegistryWithSigner();
    const agentId = 502n;
    const metaURI = "ipfs://m";
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const permit = await signMintPermit(signer, registry, {
      to: alice.address,
      agentId,
      metaURI,
      avatarStyle: 0,
      deadline,
    });

    await registry
      .connect(alice)
      .mintSoul(alice.address, agentId, metaURI, 0, deadline, permit, {
        value: MINT_FEE,
      });

    // Same permit again with same parameters
    await assert.rejects(
      registry
        .connect(alice)
        .mintSoul(alice.address, 502n, metaURI, 0, deadline, permit, {
          value: MINT_FEE,
        }),
      /permit reused/,
    );
  });

  it("mint with invalid signer reverts", async () => {
    const { registry, alice } = await deployRegistryWithSigner();
    const agentId = 504n;
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    // Sign with a random wallet that is NOT the mintSigner
    const wrongSigner = ethers.Wallet.createRandom().connect(ethers.provider);
    const permit = await signMintPermit(wrongSigner, registry, {
      to: alice.address,
      agentId,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      deadline,
    });

    await assert.rejects(
      registry
        .connect(alice)
        .mintSoul(alice.address, agentId, "ipfs://m", 0, deadline, permit, {
          value: MINT_FEE,
        }),
      /invalid permit/,
    );
  });

  it("mint with incorrect fee reverts", async () => {
    const { registry, signer, alice } = await deployRegistryWithSigner();
    const agentId = 505n;
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const permit = await signMintPermit(signer, registry, {
      to: alice.address,
      agentId,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      deadline,
    });

    await assert.rejects(
      registry
        .connect(alice)
        .mintSoul(alice.address, agentId, "ipfs://m", 0, deadline, permit, {
          value: MINT_FEE - 1n,
        }),
      /incorrect fee/,
    );

    // Also test strictly greater than
    await assert.rejects(
      registry
        .connect(alice)
        .mintSoul(alice.address, agentId, "ipfs://m", 0, deadline, permit, {
          value: MINT_FEE + 1n,
        }),
      /incorrect fee/,
    );
  });

  it("mint with no mintSigner set reverts", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 506n;
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    // mintSigner is address(0) by default
    await assert.rejects(
      registry
        .connect(alice)
        .mintSoul(alice.address, agentId, "ipfs://m", 0, deadline, "0x" + "00".repeat(65), {
          value: 0n,
        }),
      /no mint signer/,
    );
  });

  it("mintSoulOwner works for owner without permit", async () => {
    const { registry, owner, alice } = await deployRegistryWithSigner();
    const agentId = 507n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    assert.equal(await registry.ownerOf(agentId), alice.address);
  });

  it("mintSoulOwner reverts for non-owner", async () => {
    const { registry, alice } = await deployRegistryWithSigner();

    await assert.rejects(
      registry.connect(alice).mintSoulOwner(alice.address, 508n, "ipfs://m", 0),
      /OwnableUnauthorizedAccount/,
    );
  });
});

describe("SoulRegistry — Mint signer admin", () => {
  it("setMintSigner access control: only owner", async () => {
    const { registry, alice } = await deployRegistry();

    await assert.rejects(
      registry.connect(alice).setMintSigner(alice.address),
      /OwnableUnauthorizedAccount/,
    );
  });

  it("setMintSigner emits MintSignerUpdated", async () => {
    const { registry, owner, alice } = await deployRegistry();

    const tx = await registry.connect(owner).setMintSigner(alice.address);
    const receipt = await tx.wait();
    const ev = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "MintSignerUpdated";
      } catch {
        return false;
      }
    });
    assert.ok(ev, "MintSignerUpdated event should be emitted");
    assert.equal(await registry.mintSigner(), alice.address);
  });

  it("setMintSigner reverts for zero address", async () => {
    const { registry, owner } = await deployRegistry();
    await assert.rejects(
      registry.connect(owner).setMintSigner(ethers.ZeroAddress),
      /zero signer/,
    );
  });

  it("setMintFee access control: only owner", async () => {
    const { registry, alice } = await deployRegistry();

    await assert.rejects(
      registry.connect(alice).setMintFee(MINT_FEE),
      /OwnableUnauthorizedAccount/,
    );
  });

  it("setMintFee emits MintFeeUpdated", async () => {
    const { registry, owner } = await deployRegistry();

    const tx = await registry.connect(owner).setMintFee(MINT_FEE);
    const receipt = await tx.wait();
    const ev = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "MintFeeUpdated";
      } catch {
        return false;
      }
    });
    assert.ok(ev, "MintFeeUpdated event should be emitted");
    assert.equal(await registry.mintFee(), MINT_FEE);
  });

  it("withdrawFees succeeds for owner and sends ETH", async () => {
    const { registry, owner, signer, alice, bob } = await deployRegistryWithSigner();
    const agentId = 600n;
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const permit = await signMintPermit(signer, registry, {
      to: alice.address,
      agentId,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      deadline,
    });

    await registry
      .connect(alice)
      .mintSoul(alice.address, agentId, "ipfs://m", 0, deadline, permit, {
        value: MINT_FEE,
      });

    const contractBalance = await ethers.provider.getBalance(await registry.getAddress());
    assert.equal(contractBalance, MINT_FEE);
    assert.equal(await registry.accumulatedFees(), MINT_FEE);

    const balBefore = await ethers.provider.getBalance(bob.address);
    await registry.connect(owner).withdrawFees(bob.address);
    const balAfter = await ethers.provider.getBalance(bob.address);
    assert.ok(balAfter > balBefore, "recipient should have received ETH");

    const contractBalanceAfter = await ethers.provider.getBalance(await registry.getAddress());
    assert.equal(contractBalanceAfter, 0n);
    assert.equal(await registry.accumulatedFees(), 0n);
  });

  it("withdrawFees reverts for non-owner", async () => {
    const { registry, alice } = await deployRegistryWithSigner();

    await assert.rejects(
      registry.connect(alice).withdrawFees(alice.address),
      /OwnableUnauthorizedAccount/,
    );
  });
});

// ========= v2: Attestor registry + selfMintSoul + principalOf =========

async function deployRegistryWithAttestor({ claimWindowSeconds = 3600n } = {}) {
  const [owner, alice, bob, other] = await ethers.getSigners();
  const attestor = ethers.Wallet.createRandom().connect(ethers.provider);
  const SoulRegistry = await ethers.getContractFactory("SoulRegistry");
  const registry = await SoulRegistry.deploy(owner.address, claimWindowSeconds);
  await registry.connect(owner).addAttestor(attestor.address);
  await registry.connect(owner).setMintFee(MINT_FEE);
  return { registry, owner, alice, bob, other, attestor };
}

async function signSelfMintAttestation(attestor, registry, { to, agentId, metaURI, avatarStyle, principal, deadline, submitter }) {
  const chainId = (await ethers.provider.getNetwork()).chainId;
  const verifyingContract = await registry.getAddress();
  const domain = {
    name: "LesserSoul",
    version: "1",
    chainId,
    verifyingContract,
  };
  const types = {
    SelfMintAttestation: [
      { name: "to", type: "address" },
      { name: "agentId", type: "uint256" },
      { name: "metaURI", type: "string" },
      { name: "avatarStyle", type: "uint8" },
      { name: "principal", type: "address" },
      { name: "deadline", type: "uint256" },
      { name: "submitter", type: "address" },
    ],
  };
  const message = { to, agentId, metaURI, avatarStyle, principal, deadline, submitter };
  return attestor.signTypedData(domain, types, message);
}

describe("SoulRegistry — Attestor registry (v2)", () => {
  it("addAttestor sets attestor and emits event", async () => {
    const { registry, owner, alice } = await deployRegistry();

    const tx = await registry.connect(owner).addAttestor(alice.address);
    const receipt = await tx.wait();

    assert.equal(await registry.isAttestor(alice.address), true);

    const ev = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "AttestorAdded";
      } catch {
        return false;
      }
    });
    assert.ok(ev, "AttestorAdded event should be emitted");
  });

  it("removeAttestor clears attestor and emits event", async () => {
    const { registry, owner, alice } = await deployRegistry();

    await registry.connect(owner).addAttestor(alice.address);
    assert.equal(await registry.isAttestor(alice.address), true);

    const tx = await registry.connect(owner).removeAttestor(alice.address);
    const receipt = await tx.wait();

    assert.equal(await registry.isAttestor(alice.address), false);

    const ev = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "AttestorRemoved";
      } catch {
        return false;
      }
    });
    assert.ok(ev, "AttestorRemoved event should be emitted");
  });

  it("addAttestor reverts for zero address", async () => {
    const { registry, owner } = await deployRegistry();

    await assert.rejects(
      registry.connect(owner).addAttestor(ethers.ZeroAddress),
      /zero attestor/,
    );
  });

  it("addAttestor reverts for non-owner", async () => {
    const { registry, alice } = await deployRegistry();

    await assert.rejects(
      registry.connect(alice).addAttestor(alice.address),
      /OwnableUnauthorizedAccount/,
    );
  });

  it("removeAttestor reverts for non-owner", async () => {
    const { registry, owner, alice } = await deployRegistry();
    await registry.connect(owner).addAttestor(alice.address);

    await assert.rejects(
      registry.connect(alice).removeAttestor(alice.address),
      /OwnableUnauthorizedAccount/,
    );
  });

  it("isAttestor returns false for non-attestor", async () => {
    const { registry, alice } = await deployRegistry();
    assert.equal(await registry.isAttestor(alice.address), false);
  });

  it("addAttestor reverts if already attestor", async () => {
    const { registry, owner, alice } = await deployRegistry();
    await registry.connect(owner).addAttestor(alice.address);
    await assert.rejects(
      registry.connect(owner).addAttestor(alice.address),
      /already attestor/,
    );
  });

  it("removeAttestor reverts if not attestor", async () => {
    const { registry, owner, alice } = await deployRegistry();
    await assert.rejects(
      registry.connect(owner).removeAttestor(alice.address),
      /not attestor/,
    );
  });
});

describe("SoulRegistry — selfMintSoul (v2)", () => {
  it("selfMintSoul with valid attestation succeeds", async () => {
    const { registry, attestor, alice } = await deployRegistryWithAttestor();
    const agentId = 700n;
    const metaURI = "https://example.com/self-mint.json";
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const principalSig = await signSelfMintAttestation(attestor, registry, {
      to: alice.address,
      agentId,
      metaURI,
      avatarStyle: 0,
      principal: alice.address,
      deadline,
      submitter: alice.address,
    });

    const tx = await registry
      .connect(alice)
      .selfMintSoul(alice.address, agentId, metaURI, 0, alice.address, deadline, principalSig, {
        value: MINT_FEE,
      });
    const receipt = await tx.wait();

    assert.equal(await registry.ownerOf(agentId), alice.address);
    assert.equal(await registry.getAgentWallet(agentId), alice.address);
    assert.equal(await registry.principalOf(agentId), alice.address);

    const mintEvent = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "SoulMinted";
      } catch {
        return false;
      }
    });
    assert.ok(mintEvent, "SoulMinted event should be emitted");

    const principalEvent = receipt.logs.find((l) => {
      try {
        return registry.interface.parseLog(l)?.name === "PrincipalDeclared";
      } catch {
        return false;
      }
    });
    assert.ok(principalEvent, "PrincipalDeclared event should be emitted");
  });

  it("selfMintSoul with different principal and recipient succeeds", async () => {
    const { registry, attestor, alice, bob } = await deployRegistryWithAttestor();
    const agentId = 701n;
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const principalSig = await signSelfMintAttestation(attestor, registry, {
      to: alice.address,
      agentId,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      principal: bob.address,
      deadline,
      submitter: alice.address,
    });

    await registry
      .connect(alice)
      .selfMintSoul(alice.address, agentId, "ipfs://m", 0, bob.address, deadline, principalSig, {
        value: MINT_FEE,
      });

    assert.equal(await registry.ownerOf(agentId), alice.address);
    assert.equal(await registry.principalOf(agentId), bob.address);
  });

  it("selfMintSoul reverts with zero principal", async () => {
    const { registry, attestor, alice } = await deployRegistryWithAttestor();
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const principalSig = await signSelfMintAttestation(attestor, registry, {
      to: alice.address,
      agentId: 702n,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      principal: ethers.ZeroAddress,
      deadline,
      submitter: alice.address,
    });

    await assert.rejects(
      registry
        .connect(alice)
        .selfMintSoul(alice.address, 702n, "ipfs://m", 0, ethers.ZeroAddress, deadline, principalSig, {
          value: MINT_FEE,
        }),
      /principal required/,
    );
  });

  it("selfMintSoul reverts with incorrect fee", async () => {
    const { registry, attestor, alice } = await deployRegistryWithAttestor();
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const principalSig = await signSelfMintAttestation(attestor, registry, {
      to: alice.address,
      agentId: 703n,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      principal: alice.address,
      deadline,
      submitter: alice.address,
    });

    await assert.rejects(
      registry
        .connect(alice)
        .selfMintSoul(alice.address, 703n, "ipfs://m", 0, alice.address, deadline, principalSig, {
          value: MINT_FEE - 1n,
        }),
      /incorrect fee/,
    );
  });

  it("selfMintSoul reverts when deadline is expired", async () => {
    const { registry, attestor, alice } = await deployRegistryWithAttestor();
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now - 1);

    const principalSig = await signSelfMintAttestation(attestor, registry, {
      to: alice.address,
      agentId: 706n,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      principal: alice.address,
      deadline,
      submitter: alice.address,
    });

    await assert.rejects(
      registry
        .connect(alice)
        .selfMintSoul(alice.address, 706n, "ipfs://m", 0, alice.address, deadline, principalSig, {
          value: MINT_FEE,
        }),
      /expired/,
    );
  });

  it("selfMintSoul reverts if called by a different submitter than signed", async () => {
    const { registry, attestor, alice, bob } = await deployRegistryWithAttestor();
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const principalSig = await signSelfMintAttestation(attestor, registry, {
      to: alice.address,
      agentId: 707n,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      principal: alice.address,
      deadline,
      submitter: alice.address,
    });

    await assert.rejects(
      registry
        .connect(bob)
        .selfMintSoul(alice.address, 707n, "ipfs://m", 0, alice.address, deadline, principalSig, {
          value: MINT_FEE,
        }),
      /invalid attestation/,
    );
  });

  it("selfMintSoul reverts with non-attestor signer", async () => {
    const { registry, alice } = await deployRegistryWithAttestor();
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    // Sign with a random wallet that is NOT a registered attestor
    const fakeAttestor = ethers.Wallet.createRandom().connect(ethers.provider);
    const principalSig = await signSelfMintAttestation(fakeAttestor, registry, {
      to: alice.address,
      agentId: 704n,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      principal: alice.address,
      deadline,
      submitter: alice.address,
    });

    await assert.rejects(
      registry
        .connect(alice)
        .selfMintSoul(alice.address, 704n, "ipfs://m", 0, alice.address, deadline, principalSig, {
          value: MINT_FEE,
        }),
      /invalid attestation/,
    );
  });

  it("selfMintSoul reverts after attestor is removed", async () => {
    const { registry, owner, attestor, alice } = await deployRegistryWithAttestor();
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    // Remove the attestor
    await registry.connect(owner).removeAttestor(attestor.address);

    const principalSig = await signSelfMintAttestation(attestor, registry, {
      to: alice.address,
      agentId: 705n,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      principal: alice.address,
      deadline,
      submitter: alice.address,
    });

    await assert.rejects(
      registry
        .connect(alice)
        .selfMintSoul(alice.address, 705n, "ipfs://m", 0, alice.address, deadline, principalSig, {
          value: MINT_FEE,
        }),
      /invalid attestation/,
    );
  });
});

describe("SoulRegistry — principalOf (v2)", () => {
  it("principalOf returns address(0) for mintSoulOwner (no principal)", async () => {
    const { registry, owner, alice } = await deployRegistry();
    const agentId = 800n;

    await registry.connect(owner).mintSoulOwner(alice.address, agentId, "ipfs://m", 0);
    assert.equal(await registry.principalOf(agentId), ethers.ZeroAddress);
  });

  it("principalOf returns address(0) for permit-based mintSoul (no principal)", async () => {
    const { registry, signer, alice } = await deployRegistryWithSigner();
    const agentId = 801n;
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const permit = await signMintPermit(signer, registry, {
      to: alice.address,
      agentId,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      deadline,
    });

    await registry
      .connect(alice)
      .mintSoul(alice.address, agentId, "ipfs://m", 0, deadline, permit, {
        value: MINT_FEE,
      });

    assert.equal(await registry.principalOf(agentId), ethers.ZeroAddress);
  });

  it("principalOf returns correct address for selfMintSoul", async () => {
    const { registry, attestor, alice, bob } = await deployRegistryWithAttestor();
    const agentId = 802n;
    const now = (await ethers.provider.getBlock("latest")).timestamp;
    const deadline = BigInt(now + 3600);

    const principalSig = await signSelfMintAttestation(attestor, registry, {
      to: alice.address,
      agentId,
      metaURI: "ipfs://m",
      avatarStyle: 0,
      principal: bob.address,
      deadline,
      submitter: alice.address,
    });

    await registry
      .connect(alice)
      .selfMintSoul(alice.address, agentId, "ipfs://m", 0, bob.address, deadline, principalSig, {
        value: MINT_FEE,
      });

    assert.equal(await registry.principalOf(agentId), bob.address);
  });

  it("principalOf returns address(0) for unminted agentId", async () => {
    const { registry } = await deployRegistry();
    assert.equal(await registry.principalOf(999n), ethers.ZeroAddress);
  });
});
