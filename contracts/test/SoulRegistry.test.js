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

describe("SoulRegistry — Minting + identity registry", () => {
  it("mints tokenId==agentId, stores metaURI, and resolves getAgentWallet", async () => {
    const { registry, owner, alice } = await deployRegistry({
      claimWindowSeconds: 3600n,
    });
    const agentId = 1n;
    const metaURI = "https://example.com/registration.json";

    await registry.connect(owner).mintSoul(alice.address, agentId, metaURI);

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
      .mintSoul(alice.address, agentId, "ipfs://example");

    await assert.rejects(
      registry
        .connect(owner)
        .mintSoul(alice.address, agentId, "ipfs://example2"),
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

    await registry.connect(owner).mintSoul(alice.address, agentId, "ipfs://m");
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

    await registry.connect(owner).mintSoul(alice.address, agentId, "ipfs://m");
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
    await registry.connect(owner).mintSoul(alice.address, agentId, "ipfs://m");

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

