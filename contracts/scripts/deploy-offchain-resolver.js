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
  const signer = requireEnv("ENS_GATEWAY_SIGNER");
  const gatewayUrl = requireEnv("ENS_GATEWAY_URL");

  if (!ethers.isAddress(initialOwner)) {
    throw new Error(`INITIAL_OWNER is not a valid address: ${initialOwner}`);
  }
  if (!ethers.isAddress(signer)) {
    throw new Error(`ENS_GATEWAY_SIGNER is not a valid address: ${signer}`);
  }
  if (!gatewayUrl.startsWith("https://")) {
    throw new Error("ENS_GATEWAY_URL must be an https:// URL template");
  }
  if (!gatewayUrl.includes("{sender}") || !gatewayUrl.includes("{data}")) {
    throw new Error("ENS_GATEWAY_URL must include {sender} and {data} substitutions");
  }

  const expectedGatewayUrls = new Map([
    [11155111, "https://lab.lesser.host/resolve?sender={sender}&data={data}"],
    [1, "https://lesser.host/resolve?sender={sender}&data={data}"],
  ]);
  const expectedGatewayUrl = expectedGatewayUrls.get(chainId);
  if (expectedGatewayUrl && gatewayUrl !== expectedGatewayUrl) {
    throw new Error(
      `ENS_GATEWAY_URL for chainId ${chainId} must be ${expectedGatewayUrl}`,
    );
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
  if (chainId === 11155111) {
    console.log(`  CDK context: ensGatewayResolverAddressLab=${address}`);
  } else if (chainId === 1) {
    console.log(`  CDK context: ensGatewayResolverAddressLive=${address}`);
  }
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});

