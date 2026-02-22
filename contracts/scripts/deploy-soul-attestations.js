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

  const initialOwner = requireEnv("INITIAL_OWNER");
  if (!ethers.isAddress(initialOwner)) {
    throw new Error(`INITIAL_OWNER is not a valid address: ${initialOwner}`);
  }

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

  const ReputationAttestation = await ethers.getContractFactory("ReputationAttestation");
  const rep = await ReputationAttestation.deploy(initialOwner);
  await rep.waitForDeployment();

  const ValidationAttestation = await ethers.getContractFactory("ValidationAttestation");
  const val = await ValidationAttestation.deploy(initialOwner);
  await val.waitForDeployment();

  const repAddr = await rep.getAddress();
  const valAddr = await val.getAddress();
  const repTx = rep.deploymentTransaction();
  const valTx = val.deploymentTransaction();

  console.log("Soul attestations deployed");
  console.log(`  Network: ${net.name || "unknown"} (chainId=${chainId})`);
  console.log(`  Deployer: ${deployer.address}`);
  console.log("  ReputationAttestation:");
  console.log(`    Contract: ${repAddr}`);
  console.log(`    Tx Hash: ${repTx ? repTx.hash : "unknown"}`);
  console.log("  ValidationAttestation:");
  console.log(`    Contract: ${valAddr}`);
  console.log(`    Tx Hash: ${valTx ? valTx.hash : "unknown"}`);
  console.log("  Constructor args:");
  console.log(`    INITIAL_OWNER=${initialOwner}`);
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});

