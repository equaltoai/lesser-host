import hre from "hardhat";

function requireEnv(name) {
  const value = process.env[name];
  if (!value || value.trim() === "") {
    throw new Error(`Missing required env var: ${name}`);
  }
  return value.trim();
}

function requireBigIntEnv(name) {
  const value = requireEnv(name);
  try {
    return BigInt(value);
  } catch {
    throw new Error(`${name} must be a base-10 integer (got: ${value})`);
  }
}

async function main() {
  const connection = await hre.network.connect();
  const { ethers } = connection;

  const net = await ethers.provider.getNetwork();
  const chainId = Number(net.chainId);
  if (chainId !== 11155111) {
    throw new Error(`Refusing to deploy: expected Sepolia chainId=11155111, got chainId=${chainId}`);
  }

  const initialOwner = requireEnv("INITIAL_OWNER");
  const lesserWallet = requireEnv("LESSER_WALLET");
  const claimWindowSeconds = requireBigIntEnv("SOUL_CLAIM_WINDOW_SECONDS");

  if (!ethers.isAddress(initialOwner)) {
    throw new Error(`INITIAL_OWNER is not a valid address: ${initialOwner}`);
  }
  if (!ethers.isAddress(lesserWallet)) {
    throw new Error(`LESSER_WALLET is not a valid address: ${lesserWallet}`);
  }
  if (claimWindowSeconds < 0n) {
    throw new Error("SOUL_CLAIM_WINDOW_SECONDS must be >= 0");
  }

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

  console.log("Deploying full contract set to Sepolia...");
  console.log(`  Network: ${net.name || "unknown"} (chainId=${chainId})`);
  console.log(`  Deployer: ${deployer.address}`);
  console.log(`  INITIAL_OWNER: ${initialOwner}`);
  console.log(`  LESSER_WALLET: ${lesserWallet}`);
  console.log(`  SOUL_CLAIM_WINDOW_SECONDS: ${claimWindowSeconds.toString()}`);
  console.log("");

  // 1) SoulRegistry
  const SoulRegistry = await ethers.getContractFactory("SoulRegistry");
  const soulRegistry = await SoulRegistry.deploy(initialOwner, claimWindowSeconds);
  await soulRegistry.waitForDeployment();
  const soulRegistryAddr = await soulRegistry.getAddress();
  const soulRegistryTx = soulRegistry.deploymentTransaction();

  // 2) ReputationAttestation
  const ReputationAttestation = await ethers.getContractFactory("ReputationAttestation");
  const repAtt = await ReputationAttestation.deploy(initialOwner);
  await repAtt.waitForDeployment();
  const repAttAddr = await repAtt.getAddress();
  const repAttTx = repAtt.deploymentTransaction();

  // 3) ValidationAttestation
  const ValidationAttestation = await ethers.getContractFactory("ValidationAttestation");
  const valAtt = await ValidationAttestation.deploy(initialOwner);
  await valAtt.waitForDeployment();
  const valAttAddr = await valAtt.getAddress();
  const valAttTx = valAtt.deploymentTransaction();

  // 4) TipSplitter (points at SoulRegistry for agent tips)
  const TipSplitter = await ethers.getContractFactory("TipSplitter");
  const tipSplitter = await TipSplitter.deploy(lesserWallet, initialOwner, soulRegistryAddr);
  await tipSplitter.waitForDeployment();
  const tipSplitterAddr = await tipSplitter.getAddress();
  const tipSplitterTx = tipSplitter.deploymentTransaction();

  // 5) EtherealBlobRenderer
  const EtherealBlobRenderer = await ethers.getContractFactory("EtherealBlobRenderer");
  const blobRenderer = await EtherealBlobRenderer.deploy();
  await blobRenderer.waitForDeployment();
  const blobRendererAddr = await blobRenderer.getAddress();
  const blobRendererTx = blobRenderer.deploymentTransaction();

  // 6) SacredGeometryRenderer
  const SacredGeometryRenderer = await ethers.getContractFactory("SacredGeometryRenderer");
  const sacredRenderer = await SacredGeometryRenderer.deploy();
  await sacredRenderer.waitForDeployment();
  const sacredRendererAddr = await sacredRenderer.getAddress();
  const sacredRendererTx = sacredRenderer.deploymentTransaction();

  // 7) SigilRenderer
  const SigilRenderer = await ethers.getContractFactory("SigilRenderer");
  const sigilRenderer = await SigilRenderer.deploy();
  await sigilRenderer.waitForDeployment();
  const sigilRendererAddr = await sigilRenderer.getAddress();
  const sigilRendererTx = sigilRenderer.deploymentTransaction();

  console.log("Deployed contracts:");
  console.log(`  SoulRegistry:            ${soulRegistryAddr}`);
  console.log(`    Tx: ${soulRegistryTx ? soulRegistryTx.hash : "unknown"}`);
  console.log(`  ReputationAttestation:   ${repAttAddr}`);
  console.log(`    Tx: ${repAttTx ? repAttTx.hash : "unknown"}`);
  console.log(`  ValidationAttestation:   ${valAttAddr}`);
  console.log(`    Tx: ${valAttTx ? valAttTx.hash : "unknown"}`);
  console.log(`  TipSplitter:             ${tipSplitterAddr}`);
  console.log(`    Tx: ${tipSplitterTx ? tipSplitterTx.hash : "unknown"}`);
  console.log(`  EtherealBlobRenderer:    ${blobRendererAddr}`);
  console.log(`    Tx: ${blobRendererTx ? blobRendererTx.hash : "unknown"}`);
  console.log(`  SacredGeometryRenderer:  ${sacredRendererAddr}`);
  console.log(`    Tx: ${sacredRendererTx ? sacredRendererTx.hash : "unknown"}`);
  console.log(`  SigilRenderer:           ${sigilRendererAddr}`);
  console.log(`    Tx: ${sigilRendererTx ? sigilRendererTx.hash : "unknown"}`);
  console.log("");

  // Basic sanity checks (read-only)
  const tipOwner = await tipSplitter.owner();
  const tipLesserWallet = await tipSplitter.lesserWallet();
  const tipAgentRegistry = await tipSplitter.agentIdentityRegistry();

  const soulOwner = await soulRegistry.owner();
  const repOwner = await repAtt.owner();
  const valOwner = await valAtt.owner();

  console.log("Sanity checks:");
  console.log(`  TipSplitter.owner():              ${tipOwner}`);
  console.log(`  TipSplitter.lesserWallet():        ${tipLesserWallet}`);
  console.log(`  TipSplitter.agentIdentityRegistry(): ${tipAgentRegistry}`);
  console.log(`  SoulRegistry.owner():             ${soulOwner}`);
  console.log(`  ReputationAttestation.owner():     ${repOwner}`);
  console.log(`  ValidationAttestation.owner():     ${valOwner}`);
  console.log("");

  console.log("Required Safe multisig transactions (Phase 2 — setRenderer):");
  console.log(`  SoulRegistry.setRenderer(0, ${blobRendererAddr})   // Ethereal Blob`);
  console.log(`  SoulRegistry.setRenderer(1, ${sacredRendererAddr})  // Sacred Geometry`);
  console.log(`  SoulRegistry.setRenderer(2, ${sigilRendererAddr})           // Sigil`);
  console.log("");

  console.log("Suggested CDK context updates (lab stage):");
  console.log(JSON.stringify(
    {
      tipEnabledLab: "true",
      tipChainIdLab: "11155111",
      tipContractAddressLab: tipSplitterAddr,
      tipRpcUrlSsmParamLab: "/lesser-host/api/infura/sepolia",
      soulEnabledLab: "true",
      soulChainIdLab: "11155111",
      soulRegistryContractAddressLab: soulRegistryAddr,
      soulReputationAttestationContractAddressLab: repAttAddr,
      soulValidationAttestationContractAddressLab: valAttAddr,
      soulRpcUrlSsmParamLab: "/lesser-host/api/infura/sepolia",
    },
    null,
    2,
  ));
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});

