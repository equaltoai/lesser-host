import hre from "hardhat";

function requireEnv(name) {
  const value = process.env[name];
  if (!value || value.trim() === "") {
    throw new Error(`Missing required env var: ${name}`);
  }
  return value.trim();
}

async function main() {
  const connection = await hre.network.connect();
  const { ethers } = connection;

  const net = await ethers.provider.getNetwork();
  const chainId = Number(net.chainId);
  if (chainId !== 11155111) {
    throw new Error(`Refusing to deploy: expected Sepolia chainId=11155111, got chainId=${chainId}`);
  }

  const soulRegistryAddress = requireEnv("SOUL_REGISTRY_ADDRESS");
  if (!ethers.isAddress(soulRegistryAddress)) {
    throw new Error(`SOUL_REGISTRY_ADDRESS is not a valid address: ${soulRegistryAddress}`);
  }

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

  console.log("Deploying soul avatar renderers to Sepolia...");
  console.log(`  Network: ${net.name || "unknown"} (chainId=${chainId})`);
  console.log(`  Deployer: ${deployer.address}`);
  console.log(`  SOUL_REGISTRY_ADDRESS: ${soulRegistryAddress}`);
  console.log("");

  // 1) EtherealBlobRenderer
  const EtherealBlobRenderer = await ethers.getContractFactory("EtherealBlobRenderer");
  const blobRenderer = await EtherealBlobRenderer.deploy();
  await blobRenderer.waitForDeployment();
  const blobRendererAddr = await blobRenderer.getAddress();
  const blobRendererTx = blobRenderer.deploymentTransaction();

  // 2) SacredGeometryRenderer
  const SacredGeometryRenderer = await ethers.getContractFactory("SacredGeometryRenderer");
  const sacredRenderer = await SacredGeometryRenderer.deploy();
  await sacredRenderer.waitForDeployment();
  const sacredRendererAddr = await sacredRenderer.getAddress();
  const sacredRendererTx = sacredRenderer.deploymentTransaction();

  // 3) SigilRenderer
  const SigilRenderer = await ethers.getContractFactory("SigilRenderer");
  const sigilRenderer = await SigilRenderer.deploy();
  await sigilRenderer.waitForDeployment();
  const sigilRendererAddr = await sigilRenderer.getAddress();
  const sigilRendererTx = sigilRenderer.deploymentTransaction();

  console.log("Deployed renderers:");
  console.log(`  EtherealBlobRenderer:    ${blobRendererAddr}`);
  console.log(`    Tx: ${blobRendererTx ? blobRendererTx.hash : "unknown"}`);
  console.log(`  SacredGeometryRenderer:  ${sacredRendererAddr}`);
  console.log(`    Tx: ${sacredRendererTx ? sacredRendererTx.hash : "unknown"}`);
  console.log(`  SigilRenderer:           ${sigilRendererAddr}`);
  console.log(`    Tx: ${sigilRendererTx ? sigilRendererTx.hash : "unknown"}`);
  console.log("");

  console.log("Required Safe multisig transactions (setRenderer on SoulRegistry):");
  console.log(`  Target: ${soulRegistryAddress}`);
  console.log(`  SoulRegistry.setRenderer(0, ${blobRendererAddr})   // Ethereal Blob`);
  console.log(`  SoulRegistry.setRenderer(1, ${sacredRendererAddr})  // Sacred Geometry`);
  console.log(`  SoulRegistry.setRenderer(2, ${sigilRendererAddr})           // Sigil`);
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
