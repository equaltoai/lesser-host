import assert from "node:assert/strict";
import { dirname, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { loadVerificationInput, verifyOnEtherscan } from "../scripts/lib/etherscan-verify.js";

const contractsDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function jsonResponse(payload, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() { return payload; },
  };
}

test("loads exact Hardhat build input and ABI-encodes constructor arguments", () => {
  const input = loadVerificationInput({
    contractsDir,
    contractName: "ReputationAttestation",
    constructorArgs: ["0x0000000000000000000000000000000000001234"],
  });

  assert.equal(input.contractName, "contracts/ReputationAttestation.sol:ReputationAttestation");
  assert.equal(input.compilerVersion, "v0.8.24+commit.e11b9ed9");
  assert.equal(input.constructorArguments.length, 64);
  assert.equal(JSON.parse(input.sourceCode).settings.evmVersion, "cancun");
});

test("submits AGPL standard-json input and polls until verified", async () => {
  const requests = [];
  const responses = [
    jsonResponse({ status: "1", message: "OK", result: "verification-guid" }),
    jsonResponse({ status: "0", message: "NOTOK", result: "Pending in queue" }),
    jsonResponse({ status: "1", message: "OK", result: "Pass - Verified" }),
  ];
  const fetchImpl = async (url, options) => {
    requests.push({ url: new URL(url), options });
    return responses.shift();
  };

  const verified = await verifyOnEtherscan({
    address: "0x0000000000000000000000000000000000001234",
    apiKey: "test-secret-key",
    compilerVersion: "v0.8.24+commit.e11b9ed9",
    constructorArguments: "",
    contractName: "contracts/SigilRenderer.sol:SigilRenderer",
    sourceCode: JSON.stringify({ language: "Solidity", sources: {}, settings: {} }),
    fetchImpl,
    pollIntervalMs: 0,
    sleep: async () => {},
  });

  assert.equal(verified, true);
  assert.equal(requests.length, 3);
  assert.equal(requests[0].url.searchParams.get("action"), "verifysourcecode");
  assert.equal(requests[0].url.searchParams.get("chainid"), "1");
  assert.equal(requests[0].url.searchParams.get("apikey"), "test-secret-key");
  const submission = Object.fromEntries(requests[0].options.body);
  assert.equal(submission.codeformat, "solidity-standard-json-input");
  assert.equal(submission.licenseType, "13");
  assert.equal(submission.apikey, undefined);
  assert.equal(requests[2].url.searchParams.get("guid"), "verification-guid");
});

test("treats an already verified response as success", async () => {
  const verified = await verifyOnEtherscan({
    address: "0x0000000000000000000000000000000000001234",
    apiKey: "test-secret-key",
    compilerVersion: "v0.8.24+commit.e11b9ed9",
    constructorArguments: "",
    contractName: "contracts/SigilRenderer.sol:SigilRenderer",
    sourceCode: "{}",
    fetchImpl: async () => jsonResponse({
      status: "0",
      message: "NOTOK",
      result: "Contract source code already verified",
    }),
  });
  assert.equal(verified, true);
});

test("does not expose the API key when transport fails", async () => {
  await assert.rejects(
    verifyOnEtherscan({
      address: "0x0000000000000000000000000000000000001234",
      apiKey: "must-not-leak",
      compilerVersion: "v0.8.24+commit.e11b9ed9",
      constructorArguments: "",
      contractName: "contracts/SigilRenderer.sol:SigilRenderer",
      sourceCode: "{}",
      fetchImpl: async () => { throw new Error("transport included must-not-leak"); },
    }),
    (error) => error.message === "Etherscan request failed" && !error.message.includes("must-not-leak"),
  );
});
