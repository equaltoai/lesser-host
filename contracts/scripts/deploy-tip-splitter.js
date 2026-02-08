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

  const lesserWallet = requireEnv("LESSER_WALLET");
  const initialOwner = requireEnv("INITIAL_OWNER");

  if (!ethers.isAddress(lesserWallet)) {
    throw new Error(`LESSER_WALLET is not a valid address: ${lesserWallet}`);
  }
  if (!ethers.isAddress(initialOwner)) {
    throw new Error(`INITIAL_OWNER is not a valid address: ${initialOwner}`);
  }

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

  const TipSplitter = await ethers.getContractFactory("TipSplitter");
  const splitter = await TipSplitter.deploy(lesserWallet, initialOwner);
  await splitter.waitForDeployment();

  const address = await splitter.getAddress();
  const tx = splitter.deploymentTransaction();

  console.log("TipSplitter deployed");
  console.log(`  Network: ${hre.network.name}`);
  console.log(`  Deployer: ${deployer.address}`);
  console.log(`  Contract: ${address}`);
  console.log(`  Tx Hash: ${tx ? tx.hash : "unknown"}`);
  console.log("  Constructor args:");
  console.log(`    LESSER_WALLET=${lesserWallet}`);
  console.log(`    INITIAL_OWNER=${initialOwner}`);
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
