import hre from "hardhat";

function requireEnv(name) {
  const value = process.env[name];
  if (!value || value.trim() === "") {
    throw new Error(`Missing required env var: ${name}`);
  }
  return value.trim();
}

function optionalEnv(name) {
  const value = process.env[name];
  if (!value) return "";
  return value.trim();
}

async function main() {
  const connection = await hre.network.connect();
  const { ethers } = connection;

  const net = await ethers.provider.getNetwork();
  const chainId = Number(net.chainId);

  const lesserWallet = requireEnv("LESSER_WALLET");
  const initialOwner = requireEnv("INITIAL_OWNER");

  const identityRegistryByChainId = () => {
    if (chainId === 1) return "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432";
    if (chainId === 11155111) return "0x8004A818BFB912233c491871b3d84c89A494BD9e";
    return ethers.ZeroAddress;
  };
  const agentIdentityRegistry = optionalEnv("AGENT_IDENTITY_REGISTRY") || identityRegistryByChainId();

  if (!ethers.isAddress(lesserWallet)) {
    throw new Error(`LESSER_WALLET is not a valid address: ${lesserWallet}`);
  }
  if (!ethers.isAddress(initialOwner)) {
    throw new Error(`INITIAL_OWNER is not a valid address: ${initialOwner}`);
  }
  if (!ethers.isAddress(agentIdentityRegistry)) {
    throw new Error(`AGENT_IDENTITY_REGISTRY is not a valid address: ${agentIdentityRegistry}`);
  }
  if (agentIdentityRegistry !== ethers.ZeroAddress) {
    const code = await ethers.provider.getCode(agentIdentityRegistry);
    if (code === "0x") {
      throw new Error(`AGENT_IDENTITY_REGISTRY has no code at: ${agentIdentityRegistry}`);
    }
  }

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

  const TipSplitter = await ethers.getContractFactory("TipSplitter");
  const splitter = await TipSplitter.deploy(lesserWallet, initialOwner, agentIdentityRegistry);
  await splitter.waitForDeployment();

  const address = await splitter.getAddress();
  const tx = splitter.deploymentTransaction();

  console.log("TipSplitter deployed");
  console.log(`  Network: ${net.name || "unknown"} (chainId=${chainId})`);
  console.log(`  Deployer: ${deployer.address}`);
  console.log(`  Contract: ${address}`);
  console.log(`  Tx Hash: ${tx ? tx.hash : "unknown"}`);
  console.log("  Constructor args:");
  console.log(`    LESSER_WALLET=${lesserWallet}`);
  console.log(`    INITIAL_OWNER=${initialOwner}`);
  console.log(`    AGENT_IDENTITY_REGISTRY=${agentIdentityRegistry}`);
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
