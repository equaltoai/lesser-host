import { describe, it, before } from "node:test";
import assert from "node:assert/strict";
import hre from "hardhat";
import { ethers as ethersPkg } from "ethers";

let ethers;

before(async () => {
  const connection = await hre.network.connect();
  ethers = connection.ethers;
});

function dnsEncode(name) {
  const labels = name.split(".").filter((l) => l.length > 0);
  let hex = "0x";
  for (const label of labels) {
    if (label.length > 63) {
      throw new Error("label too long");
    }
    hex += label.length.toString(16).padStart(2, "0");
    hex += Buffer.from(label, "utf8").toString("hex");
  }
  hex += "00";
  return hex;
}

const ResolverServiceIface = new ethersPkg.Interface([
  "function resolve(bytes name, bytes data) view returns (bytes result, uint64 expires, bytes sig)",
]);

async function deployOffchainResolver({ url, signer, owner } = {}) {
  const [defaultOwner, other] = await ethers.getSigners();
  const initialOwner = owner ?? defaultOwner.address;
  const gatewayUrl =
    url ??
    "https://ens-gateway.lessersoul.ai/resolve?sender={sender}&data={data}";

  const OffchainResolver = await ethers.getContractFactory("OffchainResolver");
  const resolver = await OffchainResolver.deploy(
    initialOwner,
    gatewayUrl,
    signer,
  );
  await resolver.waitForDeployment();
  return { resolver, owner: defaultOwner, other, gatewayUrl };
}

function extractRevertData(err) {
  return (
    err?.data ||
    err?.error?.data ||
    err?.info?.error?.data ||
    err?.info?.error?.data?.data
  );
}

describe("OffchainResolver — CCIP-Read contract", () => {
  it("resolve reverts with OffchainLookup and correct calldata/extraData", async () => {
    const signerWallet = ethersPkg.Wallet.createRandom();
    const { resolver, gatewayUrl } = await deployOffchainResolver({
      signer: signerWallet.address,
    });

    const name = dnsEncode("agent-alice.lessersoul.eth");
    const data = "0x12345678";

    let caught;
    try {
      await resolver.resolve(name, data);
    } catch (err) {
      caught = err;
    }
    assert.ok(caught, "expected OffchainLookup revert");

    const revertData = extractRevertData(caught);
    assert.ok(revertData, "expected revert data");

    const parsed = resolver.interface.parseError(revertData);
    assert.equal(parsed.name, "OffchainLookup");

    const sender = parsed.args[0];
    const urls = parsed.args[1];
    const callData = parsed.args[2];
    const callback = parsed.args[3];
    const extraData = parsed.args[4];

    const addr = await resolver.getAddress();
    assert.equal(sender, addr);
    assert.equal(urls.length, 1);
    assert.equal(urls[0], gatewayUrl);

    const expectedCallData = ResolverServiceIface.encodeFunctionData("resolve", [
      name,
      data,
    ]);
    assert.equal(callData, expectedCallData);

    const expectedCallback = resolver.interface.getFunction("resolveWithProof")
      .selector;
    assert.equal(callback, expectedCallback);

    const expectedExtraData = ethersPkg.AbiCoder.defaultAbiCoder().encode(
      ["bytes", "address"],
      [expectedCallData, addr],
    );
    assert.equal(extraData, expectedExtraData);
  });

  it("resolveWithProof returns result for a valid signature", async () => {
    const signerWallet = ethersPkg.Wallet.createRandom();
    const signingKey = new ethersPkg.SigningKey(signerWallet.privateKey);

    const { resolver } = await deployOffchainResolver({
      signer: signerWallet.address,
    });
    const addr = await resolver.getAddress();

    const name = dnsEncode("agent-alice.lessersoul.eth");
    const data = "0x12345678";
    const callData = ResolverServiceIface.encodeFunctionData("resolve", [
      name,
      data,
    ]);

    const result = ethersPkg.AbiCoder.defaultAbiCoder().encode(
      ["address"],
      ["0x000000000000000000000000000000000000bEEF"],
    );
    const expires =
      BigInt((await ethers.provider.getBlock("latest")).timestamp) + 300n;

    const messageHash = ethersPkg.solidityPackedKeccak256(
      ["bytes", "address", "uint64", "bytes32", "bytes32"],
      ["0x1900", addr, expires, ethersPkg.keccak256(callData), ethersPkg.keccak256(result)],
    );
    const sig = signingKey.sign(messageHash);

    const response = ethersPkg.AbiCoder.defaultAbiCoder().encode(
      ["bytes", "uint64", "bytes"],
      [result, expires, sig.compactSerialized],
    );
    const extraData = ethersPkg.AbiCoder.defaultAbiCoder().encode(
      ["bytes", "address"],
      [callData, addr],
    );

    const got = await resolver.resolveWithProof(response, extraData);
    assert.equal(got, result);
  });

  it("resolveWithProof accepts the previous signer during rotation (no downtime)", async () => {
    const signer1 = ethersPkg.Wallet.createRandom();
    const signer2 = ethersPkg.Wallet.createRandom();
    const sk1 = new ethersPkg.SigningKey(signer1.privateKey);
    const sk2 = new ethersPkg.SigningKey(signer2.privateKey);

    const { resolver, owner } = await deployOffchainResolver({
      signer: signer1.address,
      owner: (await ethers.getSigners())[0].address,
    });
    const addr = await resolver.getAddress();

    // Rotate signer on-chain.
    await resolver.connect(owner).setSigner(signer2.address);
    assert.equal(await resolver.signer(), signer2.address);
    assert.equal(await resolver.previousSigner(), signer1.address);

    const callData = "0xdeadbeef";
    const result = "0x1234";
    const expires =
      BigInt((await ethers.provider.getBlock("latest")).timestamp) + 300n;

    const hash = ethersPkg.solidityPackedKeccak256(
      ["bytes", "address", "uint64", "bytes32", "bytes32"],
      ["0x1900", addr, expires, ethersPkg.keccak256(callData), ethersPkg.keccak256(result)],
    );

    const response1 = ethersPkg.AbiCoder.defaultAbiCoder().encode(
      ["bytes", "uint64", "bytes"],
      [result, expires, sk1.sign(hash).compactSerialized],
    );
    const response2 = ethersPkg.AbiCoder.defaultAbiCoder().encode(
      ["bytes", "uint64", "bytes"],
      [result, expires, sk2.sign(hash).compactSerialized],
    );
    const extraData = ethersPkg.AbiCoder.defaultAbiCoder().encode(
      ["bytes", "address"],
      [callData, addr],
    );

    assert.equal(await resolver.resolveWithProof(response1, extraData), result);
    assert.equal(await resolver.resolveWithProof(response2, extraData), result);
  });

  it("owner-only setters enforce access control", async () => {
    const signerWallet = ethersPkg.Wallet.createRandom();
    const { resolver, owner, other } = await deployOffchainResolver({
      signer: signerWallet.address,
    });

    await assert.rejects(
      resolver.connect(other).setGatewayUrl("https://x.example/{data}"),
      /OwnableUnauthorizedAccount/,
    );

    await assert.rejects(
      resolver.connect(other).setSigner(other.address),
      /OwnableUnauthorizedAccount/,
    );

    await assert.rejects(
      resolver.connect(owner).setSigner(ethers.ZeroAddress),
      /zero signer/,
    );
  });
});

