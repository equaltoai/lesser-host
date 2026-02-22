import { describe, it, before } from "node:test";
import assert from "node:assert/strict";
import hre from "hardhat";

let ethers;

before(async () => {
  const connection = await hre.network.connect();
  ethers = connection.ethers;
});

describe("Soul attestations — ReputationAttestation", () => {
  it("publishes and returns latest root", async () => {
    const [owner] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ReputationAttestation");
    const c = await F.deploy(owner.address);

    const root = ethers.id("root1");
    await c.publishRoot(root, 123n, 5n);

    const latest = await c.latestRoot();
    assert.equal(latest[0], root);
    assert.equal(latest[1], 123n);
    assert.equal(latest[2], 5n);
    assert.ok(latest[3] > 0n);
  });

  it("emits previousRoot in event", async () => {
    const [owner] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ReputationAttestation");
    const c = await F.deploy(owner.address);

    const root1 = ethers.id("root1");
    const root2 = ethers.id("root2");

    const tx1 = await c.publishRoot(root1, 1n, 1n);
    const receipt1 = await tx1.wait();
    const ev1 = receipt1.logs.find(
      (l) => l.fragment && l.fragment.name === "RootPublished",
    );
    assert.ok(ev1);
    assert.equal(ev1.args[1], ethers.ZeroHash); // previousRoot is zero on first publish

    const tx2 = await c.publishRoot(root2, 2n, 2n);
    const receipt2 = await tx2.wait();
    const ev2 = receipt2.logs.find(
      (l) => l.fragment && l.fragment.name === "RootPublished",
    );
    assert.ok(ev2);
    assert.equal(ev2.args[1], root1); // previousRoot should be root1
  });

  it("reverts on empty root", async () => {
    const [owner] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ReputationAttestation");
    const c = await F.deploy(owner.address);

    await assert.rejects(
      c.publishRoot(ethers.ZeroHash, 1n, 1n),
      /empty root/,
    );
  });

  it("reverts when non-owner publishes", async () => {
    const [owner, other] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ReputationAttestation");
    const c = await F.deploy(owner.address);

    await assert.rejects(
      c.connect(other).publishRoot(ethers.id("root"), 1n, 1n),
      /OwnableUnauthorizedAccount/,
    );
  });

  it("reverts when paused", async () => {
    const [owner] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ReputationAttestation");
    const c = await F.deploy(owner.address);

    await c.pause();
    await assert.rejects(
      c.publishRoot(ethers.id("root"), 1n, 1n),
      /EnforcedPause/,
    );
    await c.unpause();
    await c.publishRoot(ethers.id("root"), 1n, 1n);
  });
});

describe("Soul attestations — ValidationAttestation", () => {
  it("publishes and returns latest root", async () => {
    const [owner] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ValidationAttestation");
    const c = await F.deploy(owner.address);

    const root = ethers.id("root2");
    await c.publishRoot(root, 456n, 7n);

    const latest = await c.latestRoot();
    assert.equal(latest[0], root);
    assert.equal(latest[1], 456n);
    assert.equal(latest[2], 7n);
    assert.ok(latest[3] > 0n);
  });

  it("emits previousRoot in event", async () => {
    const [owner] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ValidationAttestation");
    const c = await F.deploy(owner.address);

    const root1 = ethers.id("root1");
    const root2 = ethers.id("root2");

    await c.publishRoot(root1, 1n, 1n);

    const tx2 = await c.publishRoot(root2, 2n, 2n);
    const receipt2 = await tx2.wait();
    const ev2 = receipt2.logs.find(
      (l) => l.fragment && l.fragment.name === "RootPublished",
    );
    assert.ok(ev2);
    assert.equal(ev2.args[1], root1); // previousRoot should be root1
  });

  it("reverts on empty root", async () => {
    const [owner] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ValidationAttestation");
    const c = await F.deploy(owner.address);

    await assert.rejects(
      c.publishRoot(ethers.ZeroHash, 1n, 1n),
      /empty root/,
    );
  });

  it("reverts when paused", async () => {
    const [owner] = await ethers.getSigners();
    const F = await ethers.getContractFactory("ValidationAttestation");
    const c = await F.deploy(owner.address);

    await c.pause();
    await assert.rejects(
      c.publishRoot(ethers.id("root"), 1n, 1n),
      /EnforcedPause/,
    );
    await c.unpause();
    await c.publishRoot(ethers.id("root"), 1n, 1n);
  });
});
