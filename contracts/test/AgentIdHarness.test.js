import { describe, it, before } from "node:test";
import assert from "node:assert/strict";
import hre from "hardhat";

let ethers;

before(async () => {
  const connection = await hre.network.connect();
  ethers = connection.ethers;
});

async function deployHarness() {
  const AgentIdHarness = await ethers.getContractFactory("AgentIdHarness");
  return AgentIdHarness.deploy();
}

describe("AgentIdHarness — agentId derivation vectors", () => {
  it("matches the lesser-soul published test vectors", async () => {
    const harness = await deployHarness();

    const cases = [
      {
        domain: "example.lesser.social",
        local: "agent-alice",
        want: "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab",
      },
      {
        domain: "xn--mnich-kva.example",
        local: "agent-alice",
        want: "0xf0b2c505271215e7bbbac618dc24f69de8aff1207d880d9c10b0779e7ce1b5e3",
      },
      {
        domain: "xn--r8jz45g.xn--zckzah",
        local: "agent-alice",
        want: "0x4744283784f8b135533d6b699c52ad842588b0c418c21a4a7c778df201572565",
      },
      {
        domain: "dev.example.com",
        local: "agent-bob",
        want: "0xf5e2da2896de9116a9463270defab5abd70be7be4722f57fd841079ded2c6cf6",
      },
      {
        domain: "stage.dev.example.com",
        local: "soul_researcher",
        want: "0x803682e2e7629f07fe1c65670bf29bf19691a339e0252001a48737cfe22dd9f5",
      },
    ];

    for (const c of cases) {
      const gotUint = await harness.deriveAgentId(c.domain, c.local);
      assert.equal(gotUint, BigInt(c.want));

      const gotBytes32 = await harness.deriveAgentIdBytes32(c.domain, c.local);
      assert.equal(gotBytes32, c.want);
    }
  });
});

