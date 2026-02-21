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
});

