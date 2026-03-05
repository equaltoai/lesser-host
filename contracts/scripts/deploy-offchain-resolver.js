import hre from "hardhat";

function requireEnv(name) {
  const value = process.env[name];
  if (!value || value.trim() === "") {
    throw new Error(`Missing required env var: ${name}`);
  }
  return value.trim();
}

function optionalEnv(name, fallback = "") {
  const value = process.env[name];
  if (!value || value.trim() === "") {
    return fallback;
  }
  return value.trim();
}

async function main() {
  const connection = await hre.network.connect();
  const { ethers } = connection;

  const net = await ethers.provider.getNetwork();
  const chainId = Number(net.chainId);

  const initialOwner = requireEnv("INITIAL_OWNER");
  const signer = requireEnv("ENS_GATEWAY_SIGNER");
  const gatewayUrl = optionalEnv(
    "ENS_GATEWAY_URL",
    "https://ens-gateway.lessersoul.ai/resolve?sender={sender}&data={data}",
  );

  if (!ethers.isAddress(initialOwner)) {
    throw new Error(`INITIAL_OWNER is not a valid address: ${initialOwner}`);
  }
  if (!ethers.isAddress(signer)) {
    throw new Error(`ENS_GATEWAY_SIGNER is not a valid address: ${signer}`);
  }
  if (!gatewayUrl.startsWith("https://")) {
    throw new Error("ENS_GATEWAY_URL must be an https:// URL template");
  }

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

  const OffchainResolver = await ethers.getContractFactory("OffchainResolver");
  const resolver = await OffchainResolver.deploy(initialOwner, gatewayUrl, signer);
  await resolver.waitForDeployment();

  const address = await resolver.getAddress();
  const tx = resolver.deploymentTransaction();

  console.log("OffchainResolver deployed");
  console.log(`  Network: ${net.name || "unknown"} (chainId=${chainId})`);
  console.log(`  Deployer: ${deployer.address}`);
  console.log(`  Contract: ${address}`);
  console.log(`  Tx Hash: ${tx ? tx.hash : "unknown"}`);
  console.log("  Constructor args:");
  console.log(`    INITIAL_OWNER=${initialOwner}`);
  console.log(`    ENS_GATEWAY_URL=${gatewayUrl}`);
  console.log(`    ENS_GATEWAY_SIGNER=${signer}`);
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});

