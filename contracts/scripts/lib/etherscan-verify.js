import { Interface } from "ethers";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const DEFAULT_API_URL = "https://api.etherscan.io/v2/api";
const DEFAULT_CHAIN_ID = "1";
const AGPL_V3_LICENSE_TYPE = "13";
const DEFAULT_POLL_ATTEMPTS = 24;
const DEFAULT_POLL_INTERVAL_MS = 5_000;

function readJson(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function resultMessage(response) {
  return typeof response?.result === "string" ? response.result : "Invalid Etherscan response";
}

function isAlreadyVerified(message) {
  return message.startsWith("Contract source code already verified") || message.startsWith("Already Verified");
}

async function requestJson({ apiKey, apiUrl, body, params, fetchImpl }) {
  const url = new URL(apiUrl);
  url.search = new URLSearchParams({ ...params, apikey: apiKey }).toString();

  let response;
  try {
    response = await fetchImpl(url, body === undefined
      ? { method: "GET" }
      : {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams(body),
        });
  } catch {
    throw new Error("Etherscan request failed");
  }

  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new Error(`Etherscan returned a non-JSON response (HTTP ${response.status})`);
  }
  if (!response.ok) {
    throw new Error(`Etherscan request failed (HTTP ${response.status}): ${resultMessage(payload)}`);
  }
  return payload;
}

export function loadVerificationInput({ contractsDir, contractName, constructorArgs }) {
  const artifactPath = resolve(
    contractsDir,
    "artifacts",
    "contracts",
    `${contractName}.sol`,
    `${contractName}.json`,
  );
  const artifact = readJson(artifactPath);
  if (!artifact.buildInfoId || !artifact.sourceName || !Array.isArray(artifact.abi)) {
    throw new Error(`Incomplete Hardhat artifact for ${contractName}; run hardhat compile first`);
  }

  const buildInfo = readJson(resolve(contractsDir, "artifacts", "build-info", `${artifact.buildInfoId}.json`));
  if (!buildInfo.input || !buildInfo.solcLongVersion) {
    throw new Error(`Incomplete Hardhat build info for ${contractName}; run hardhat compile first`);
  }

  const encoded = new Interface(artifact.abi).encodeDeploy(constructorArgs).replace(/^0x/, "");
  return {
    sourceCode: JSON.stringify(buildInfo.input),
    contractName: `${artifact.sourceName}:${artifact.contractName}`,
    compilerVersion: `v${buildInfo.solcLongVersion}`,
    constructorArguments: encoded,
  };
}

export async function verifyOnEtherscan({
  address,
  apiKey,
  compilerVersion,
  constructorArguments,
  contractName,
  sourceCode,
  apiUrl = DEFAULT_API_URL,
  chainId = DEFAULT_CHAIN_ID,
  fetchImpl = globalThis.fetch,
  pollAttempts = DEFAULT_POLL_ATTEMPTS,
  pollIntervalMs = DEFAULT_POLL_INTERVAL_MS,
  sleep = (ms) => new Promise((resolveSleep) => setTimeout(resolveSleep, ms)),
}) {
  if (!apiKey) throw new Error("Etherscan API key is required");
  if (typeof fetchImpl !== "function") throw new Error("A fetch implementation is required");

  const submission = await requestJson({
    apiKey,
    apiUrl,
    fetchImpl,
    params: { module: "contract", action: "verifysourcecode", chainid: String(chainId) },
    body: {
      contractaddress: address,
      sourceCode,
      contractname: contractName,
      compilerversion: compilerVersion,
      codeformat: "solidity-standard-json-input",
      constructorArguments,
      licenseType: AGPL_V3_LICENSE_TYPE,
    },
  });
  const submissionMessage = resultMessage(submission);
  if (isAlreadyVerified(submissionMessage)) return true;
  if (String(submission.status) !== "1" || !submissionMessage) {
    throw new Error(`Etherscan verification request failed: ${submissionMessage}`);
  }

  for (let attempt = 0; attempt < pollAttempts; attempt += 1) {
    if (attempt > 0 || pollIntervalMs > 0) await sleep(pollIntervalMs);
    const status = await requestJson({
      apiKey,
      apiUrl,
      fetchImpl,
      params: {
        module: "contract",
        action: "checkverifystatus",
        chainid: String(chainId),
        guid: submissionMessage,
      },
    });
    const message = resultMessage(status);
    if (message === "Pass - Verified" || isAlreadyVerified(message)) return true;
    if (message === "Pending in queue") continue;
    throw new Error(`Etherscan verification failed: ${message}`);
  }

  throw new Error(`Etherscan verification did not complete after ${pollAttempts} status checks`);
}
