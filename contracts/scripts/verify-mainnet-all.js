// Etherscan source verification for the full live soul/tip contract set.
// Mainnet source verification is mandatory repo discipline: run this after
// deploy-mainnet-all.js and BEFORE executing the Safe post-deploy batch.
//
// Usage: ETHERSCAN_API_KEY=... npm run verify:mainnet:all
import hre from "hardhat";
import { verifyContract } from "@nomicfoundation/hardhat-verify/verify";
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const DEFAULT_DEPLOYMENT_RECORD = "../docs/deployments/mainnet/latest.json";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const contractsDir = resolve(scriptDir, "..");

function optionalEnv(name) {
  const value = process.env[name];
  return value ? value.trim() : "";
}

async function main() {
  if (!optionalEnv("ETHERSCAN_API_KEY")) {
    throw new Error("ETHERSCAN_API_KEY is required for Etherscan source verification");
  }

  const recordPath = resolve(contractsDir, optionalEnv("MAINNET_DEPLOYMENT_RECORD_PATH") || DEFAULT_DEPLOYMENT_RECORD);
  if (!existsSync(recordPath)) {
    throw new Error(`Deployment record not found: ${recordPath}. Run deploy:mainnet:all first.`);
  }
  const record = JSON.parse(readFileSync(recordPath, "utf8"));
  const contracts = record.contracts ?? {};

  const missing = (name) => {
    throw new Error(`Deployment record ${recordPath} is missing contracts.${name}.address`);
  };
  const addr = (name) => contracts[name]?.address ?? missing(name);

  // Constructor args must match deploy-mainnet-all.js exactly.
  const plan = [
    ["SoulRegistry", [record.owner_safe, record.soul_claim_window_seconds]],
    ["ReputationAttestation", [record.owner_safe]],
    ["ValidationAttestation", [record.owner_safe]],
    ["TipSplitter", [record.lesser_wallet, record.owner_safe, addr("SoulRegistry")]],
    ["EtherealBlobRenderer", []],
    ["SacredGeometryRenderer", []],
    ["SigilRenderer", []],
  ];

  const failures = [];
  for (const [name, constructorArgs] of plan) {
    const address = addr(name);
    console.log(`Verifying ${name} at ${address} ...`);
    try {
      const ok = await verifyContract({ address, constructorArgs, provider: "etherscan" }, hre);
      console.log(`  ${name}: ${ok ? "verified (or already verified)" : "verification returned false"}`);
      if (!ok) failures.push(name);
    } catch (err) {
      console.error(`  ${name}: verification failed: ${err.message ?? err}`);
      failures.push(name);
    }
  }

  if (failures.length > 0) {
    throw new Error(`Etherscan verification failed for: ${failures.join(", ")}`);
  }
  console.log("");
  console.log("All contracts verified on Etherscan.");
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
