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

  const initialOwner = requireEnv("INITIAL_OWNER");
  const claimWindowSeconds = requireBigIntEnv("SOUL_CLAIM_WINDOW_SECONDS");

  if (!ethers.isAddress(initialOwner)) {
    throw new Error(`INITIAL_OWNER is not a valid address: ${initialOwner}`);
  }
  if (claimWindowSeconds < 0n) {
    throw new Error("SOUL_CLAIM_WINDOW_SECONDS must be >= 0");
  }

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

  const SoulRegistry = await ethers.getContractFactory("SoulRegistry");
  const registry = await SoulRegistry.deploy(initialOwner, claimWindowSeconds);
  await registry.waitForDeployment();

  const address = await registry.getAddress();
  const tx = registry.deploymentTransaction();

  console.log("SoulRegistry deployed");
  console.log(`  Network: ${net.name || "unknown"} (chainId=${chainId})`);
  console.log(`  Deployer: ${deployer.address}`);
  console.log(`  Contract: ${address}`);
  console.log(`  Tx Hash: ${tx ? tx.hash : "unknown"}`);
  console.log("  Constructor args:");
  console.log(`    INITIAL_OWNER=${initialOwner}`);
  console.log(`    SOUL_CLAIM_WINDOW_SECONDS=${claimWindowSeconds.toString()}`);
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});

