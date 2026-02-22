import { describe, it, before } from "node:test";
import assert from "node:assert/strict";
import hre from "hardhat";

let ethers;

before(async () => {
  const connection = await hre.network.connect();
  ethers = connection.ethers;
});

async function deployRenderer(name) {
  const Factory = await ethers.getContractFactory(name);
  const instance = await Factory.deploy();
  return instance;
}

describe("EtherealBlobRenderer", () => {
  it("renderAvatar returns valid SVG", async () => {
    const renderer = await deployRenderer("EtherealBlobRenderer");
    const svg = await renderer.renderAvatar(1n);
    assert.ok(svg.startsWith("<svg"), "should start with <svg");
    assert.ok(svg.endsWith("</svg>"), "should end with </svg>");
  });

  it("styleName returns 'Ethereal Blob'", async () => {
    const renderer = await deployRenderer("EtherealBlobRenderer");
    assert.equal(await renderer.styleName(), "Ethereal Blob");
  });

  it("is deterministic: same tokenId produces same output", async () => {
    const renderer = await deployRenderer("EtherealBlobRenderer");
    const svg1 = await renderer.renderAvatar(42n);
    const svg2 = await renderer.renderAvatar(42n);
    assert.equal(svg1, svg2);
  });

  it("different tokenIds produce different output", async () => {
    const renderer = await deployRenderer("EtherealBlobRenderer");
    const svg1 = await renderer.renderAvatar(1n);
    const svg2 = await renderer.renderAvatar(2n);
    assert.notEqual(svg1, svg2);
  });
});

describe("SacredGeometryRenderer", () => {
  it("renderAvatar returns valid SVG", async () => {
    const renderer = await deployRenderer("SacredGeometryRenderer");
    const svg = await renderer.renderAvatar(1n);
    assert.ok(svg.startsWith("<svg"), "should start with <svg");
    assert.ok(svg.endsWith("</svg>"), "should end with </svg>");
  });

  it("styleName returns 'Sacred Geometry'", async () => {
    const renderer = await deployRenderer("SacredGeometryRenderer");
    assert.equal(await renderer.styleName(), "Sacred Geometry");
  });

  it("is deterministic: same tokenId produces same output", async () => {
    const renderer = await deployRenderer("SacredGeometryRenderer");
    const svg1 = await renderer.renderAvatar(42n);
    const svg2 = await renderer.renderAvatar(42n);
    assert.equal(svg1, svg2);
  });

  it("different tokenIds produce different output", async () => {
    const renderer = await deployRenderer("SacredGeometryRenderer");
    const svg1 = await renderer.renderAvatar(1n);
    const svg2 = await renderer.renderAvatar(2n);
    assert.notEqual(svg1, svg2);
  });
});

describe("SigilRenderer", () => {
  it("renderAvatar returns valid SVG", async () => {
    const renderer = await deployRenderer("SigilRenderer");
    const svg = await renderer.renderAvatar(1n);
    assert.ok(svg.startsWith("<svg"), "should start with <svg");
    assert.ok(svg.endsWith("</svg>"), "should end with </svg>");
  });

  it("styleName returns 'Sigil'", async () => {
    const renderer = await deployRenderer("SigilRenderer");
    assert.equal(await renderer.styleName(), "Sigil");
  });

  it("is deterministic: same tokenId produces same output", async () => {
    const renderer = await deployRenderer("SigilRenderer");
    const svg1 = await renderer.renderAvatar(42n);
    const svg2 = await renderer.renderAvatar(42n);
    assert.equal(svg1, svg2);
  });

  it("different tokenIds produce different output", async () => {
    const renderer = await deployRenderer("SigilRenderer");
    const svg1 = await renderer.renderAvatar(1n);
    const svg2 = await renderer.renderAvatar(2n);
    assert.notEqual(svg1, svg2);
  });
});
