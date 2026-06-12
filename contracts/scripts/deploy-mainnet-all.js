import hre from "hardhat";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const MAINNET_CHAIN_ID = 1;
// The live admin Safe (v1.4.1, 2-of-2). All deployed contracts are owned by it.
const LIVE_SAFE = "0xfE63333F303D4f7b2354f7E3eca752C812D65907";
const DEFAULT_MAINNET_RPC_SSM_PARAM = "/lesser-host/api/infura/mainnet";
const DEFAULT_MINT_SIGNER_SSM_PARAM = "/lesser-host/soul/live/mint-signer-key";
const DEFAULT_DEPLOYMENT_RECORD = "../docs/deployments/mainnet/latest.json";
const DEFAULT_SAFE_TX_BUILDER = "../docs/deployments/mainnet/safe-tx-builder-soul-tip-post-deploy.json";
const DEFAULT_DEPLOY_CHECKPOINT = "../docs/deployments/mainnet/deploy-checkpoint.json";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const contractsDir = resolve(scriptDir, "..");

function requireEnv(name) {
  const value = process.env[name];
  if (!value || value.trim() === "") {
    throw new Error(`Missing required env var: ${name}`);
  }
  return value.trim();
}

function optionalEnv(name) {
  const value = process.env[name];
  return value ? value.trim() : "";
}

function requireBigIntEnv(name) {
  const value = requireEnv(name);
  try {
    return BigInt(value);
  } catch {
    throw new Error(`${name} must be a base-10 integer (got: ${value})`);
  }
}

function optionalAddressListEnv(name, ethers) {
  const value = optionalEnv(name);
  if (!value) return [];
  return value.split(",")
    .map((entry) => entry.trim())
    .filter(Boolean)
    .map((entry) => {
      const [labelOrAddress, maybeAddress] = entry.includes(":") ? entry.split(":", 2) : ["", entry];
      const label = maybeAddress ? labelOrAddress.trim() : "";
      const address = ethers.getAddress((maybeAddress || labelOrAddress).trim());
      return { label, address };
    });
}

function txHash(contract) {
  const tx = contract.deploymentTransaction();
  return tx ? tx.hash : "unknown";
}

async function receiptFor(ethers, hash) {
  if (!hash || hash === "unknown") return undefined;
  return ethers.provider.getTransactionReceipt(hash);
}

function encodeSoulCall(ethers, name, args) {
  const iface = new ethers.Interface([
    "function setRenderer(uint8 styleId, address renderer)",
    "function setMintFee(uint256 fee)",
    "function addAttestor(address attestor)",
    "function setMintSigner(address signer)",
  ]);
  return iface.encodeFunctionData(name, args);
}

function encodeTipCall(ethers, name, args) {
  const iface = new ethers.Interface([
    "function setTokenAllowed(address token, bool allowed)",
  ]);
  return iface.encodeFunctionData(name, args);
}

function safeTransaction({ to, data, method, inputs, values }) {
  return {
    to,
    value: "0",
    data,
    contractMethod: { inputs, name: method, payable: false },
    contractInputsValues: values,
  };
}

function mergeDeploymentRecord(path, nextRecord) {
  if (!existsSync(path)) return nextRecord;
  const previous = JSON.parse(readFileSync(path, "utf8"));
  // Dedupe Safe calls by (to, data) so re-runs cannot accumulate duplicate
  // "required" entries; new entries win over stale ones with the same target.
  const safeCalls = [];
  const seenSafeCalls = new Set();
  for (const call of [
    ...(nextRecord.post_deploy_required_safe_calls ?? []),
    ...(previous.post_deploy_required_safe_calls ?? []),
  ]) {
    const key = `${(call.to ?? "").toLowerCase()}|${call.data ?? ""}`;
    if (seenSafeCalls.has(key)) continue;
    seenSafeCalls.add(key);
    safeCalls.push(call);
  }
  return {
    ...previous,
    ...nextRecord,
    contracts: {
      ...(previous.contracts ?? {}),
      ...(nextRecord.contracts ?? {}),
    },
    ssm: {
      ...(previous.ssm ?? {}),
      ...(nextRecord.ssm ?? {}),
    },
    lesser_host: {
      ...(previous.lesser_host ?? {}),
      ...(nextRecord.lesser_host ?? {}),
      cdk_context: {
        ...(previous.lesser_host?.cdk_context ?? {}),
        ...(nextRecord.lesser_host?.cdk_context ?? {}),
      },
    },
    verification: {
      ...(previous.verification ?? {}),
      ...(nextRecord.verification ?? {}),
    },
    post_deploy_required_safe_calls: safeCalls,
  };
}

function writeJSON(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, JSON.stringify(value, null, 2) + "\n");
}

async function main() {
  const connection = await hre.network.connect();
  const { ethers } = connection;

  const net = await ethers.provider.getNetwork();
  const chainId = Number(net.chainId);
  if (chainId !== MAINNET_CHAIN_ID) {
    throw new Error(`Refusing to deploy: expected Ethereum mainnet chainId=1, got chainId=${chainId}`);
  }

  const initialOwner = ethers.getAddress(requireEnv("INITIAL_OWNER"));
  const lesserWallet = ethers.getAddress(requireEnv("LESSER_WALLET"));
  const claimWindowSeconds = requireBigIntEnv("SOUL_CLAIM_WINDOW_SECONDS");
  const mintFeeWei = requireBigIntEnv("SOUL_MINT_FEE_WEI");
  const mintAttestor = ethers.getAddress(requireEnv("SOUL_MINT_ATTESTOR"));
  const mintSigner = optionalEnv("SOUL_MINT_SIGNER") ? ethers.getAddress(optionalEnv("SOUL_MINT_SIGNER")) : "";
  const tokenAllowlist = optionalAddressListEnv("TIP_ALLOWED_TOKENS", ethers);
  const rpcSsmParam = optionalEnv("MAINNET_RPC_SSM_PARAM") || DEFAULT_MAINNET_RPC_SSM_PARAM;
  const mintSignerSsmParam = optionalEnv("SOUL_MINT_SIGNER_KEY_SSM_PARAM") || DEFAULT_MINT_SIGNER_SSM_PARAM;

  if (claimWindowSeconds < 0n) {
    throw new Error("SOUL_CLAIM_WINDOW_SECONDS must be >= 0");
  }
  if (mintFeeWei < 0n) {
    throw new Error("SOUL_MINT_FEE_WEI must be >= 0");
  }
  if (initialOwner === ethers.ZeroAddress) throw new Error("INITIAL_OWNER must not be zero");
  if (lesserWallet === ethers.ZeroAddress) throw new Error("LESSER_WALLET must not be zero");
  if (mintAttestor === ethers.ZeroAddress) throw new Error("SOUL_MINT_ATTESTOR must not be zero");

  // REHEARSAL=1 runs against a chainId-1 mainnet fork: same guards and flow,
  // but the deployer is a simulated account so the PUBLIC_DEPLOYER_ADDRESS
  // match is skipped. Everything else (owner checks, asserts) still applies.
  const rehearsal = optionalEnv("REHEARSAL") === "1";

  // Ownership is set irrevocably at construction; a wrong INITIAL_OWNER
  // permanently locks governance. Hard-fail unless it is the known live Safe
  // (ALLOW_OWNER_OVERRIDE=1 is an explicit, deliberate escape hatch) and the
  // address actually has code on this chain.
  if (initialOwner !== ethers.getAddress(LIVE_SAFE) && optionalEnv("ALLOW_OWNER_OVERRIDE") !== "1") {
    throw new Error(
      `INITIAL_OWNER ${initialOwner} does not match the live Safe ${LIVE_SAFE}. ` +
      "Set ALLOW_OWNER_OVERRIDE=1 only if you intend to deploy with a different owner."
    );
  }
  const ownerCode = await ethers.provider.getCode(initialOwner);
  if (ownerCode === "0x") {
    throw new Error(
      `INITIAL_OWNER ${initialOwner} has no code on chainId ${chainId}. ` +
      "Expected the live Safe contract; refusing to deploy contracts owned by an EOA/empty address."
    );
  }

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

  // Guard against deploying with a stale .env: the generated env records the
  // funded deployer address; the runtime key must derive the same address.
  // Required outside rehearsal — an env missing the var is itself suspect.
  if (!rehearsal) {
    const expectedDeployer = requireEnv("PUBLIC_DEPLOYER_ADDRESS");
    if (ethers.getAddress(expectedDeployer) !== deployer.address) {
      throw new Error(
        `Deployer ${deployer.address} does not match PUBLIC_DEPLOYER_ADDRESS ${expectedDeployer}. ` +
        "The loaded .env appears stale or mismatched; refusing to deploy."
      );
    }
  }

  console.log("Deploying full live contract set to Ethereum mainnet...");
  console.log(`  Network: ${net.name || "unknown"} (chainId=${chainId})`);
  console.log(`  Deployer: ${deployer.address}`);
  console.log(`  INITIAL_OWNER: ${initialOwner}`);
  console.log(`  LESSER_WALLET: ${lesserWallet}`);
  console.log(`  SOUL_CLAIM_WINDOW_SECONDS: ${claimWindowSeconds.toString()}`);
  console.log(`  SOUL_MINT_FEE_WEI: ${mintFeeWei.toString()}`);
  console.log(`  SOUL_MINT_ATTESTOR: ${mintAttestor}`);
  if (mintSigner) console.log(`  SOUL_MINT_SIGNER: ${mintSigner}`);
  if (tokenAllowlist.length > 0) console.log(`  TIP_ALLOWED_TOKENS: ${tokenAllowlist.map((t) => t.label ? `${t.label}:${t.address}` : t.address).join(",")}`);
  console.log("");

  // Checkpoint: every successful deploy is recorded immediately so a
  // mid-sequence failure never loses addresses, and a RESUME=1 re-run
  // reuses already-deployed contracts instead of duplicating them.
  const checkpointPath = resolve(contractsDir, optionalEnv("MAINNET_DEPLOY_CHECKPOINT_PATH") || DEFAULT_DEPLOY_CHECKPOINT);
  let checkpoint = {};
  if (existsSync(checkpointPath)) {
    if (optionalEnv("RESUME") !== "1") {
      throw new Error(
        `Deploy checkpoint ${checkpointPath} exists from a previous run. ` +
        "Re-run with RESUME=1 to reuse the already-deployed contracts, or delete the checkpoint to force a fresh deploy."
      );
    }
    checkpoint = JSON.parse(readFileSync(checkpointPath, "utf8"));
    console.log(`Resuming from checkpoint ${checkpointPath} (${Object.keys(checkpoint).length} contract(s) recorded)`);
  }

  async function deployStep(name, args) {
    if (checkpoint[name]?.address) {
      console.log(`  ${name}: reusing ${checkpoint[name].address} from checkpoint`);
      const attached = await ethers.getContractAt(name, checkpoint[name].address);
      return { contract: attached, address: checkpoint[name].address, hash: checkpoint[name].deployment_tx_hash };
    }
    const factory = await ethers.getContractFactory(name);
    const contract = await factory.deploy(...args);
    await contract.waitForDeployment();
    const address = await contract.getAddress();
    const hash = txHash(contract);
    console.log(`  ${name}: ${address}`);
    console.log(`    Tx: ${hash}`);
    checkpoint[name] = { address, deployment_tx_hash: hash };
    writeJSON(checkpointPath, checkpoint);
    return { contract, address, hash };
  }

  // Funding preflight: estimate the full remaining deploy sequence before
  // sending anything, so the run cannot fail out-of-funds mid-sequence.
  const deployPlan = [
    ["SoulRegistry", [initialOwner, claimWindowSeconds]],
    ["ReputationAttestation", [initialOwner]],
    ["ValidationAttestation", [initialOwner]],
    // Registry address is unknown pre-deploy; the constructor requires a
    // contract address (code.length > 0), so the Safe is a same-shape
    // placeholder that yields an equivalent gas estimate.
    ["TipSplitter", [lesserWallet, initialOwner, checkpoint.SoulRegistry?.address ?? initialOwner]],
    ["EtherealBlobRenderer", []],
    ["SacredGeometryRenderer", []],
    ["SigilRenderer", []],
  ];
  let estimatedGas = 0n;
  for (const [name, args] of deployPlan) {
    if (checkpoint[name]?.address) continue;
    const factory = await ethers.getContractFactory(name);
    const deployTx = await factory.getDeployTransaction(...args);
    estimatedGas += await ethers.provider.estimateGas({ ...deployTx, from: deployer.address });
  }
  const feeData = await ethers.provider.getFeeData();
  const gasPriceWei = feeData.maxFeePerGas ?? feeData.gasPrice ?? 0n;
  const estimatedCostWei = estimatedGas * gasPriceWei;
  const requiredWei = (estimatedCostWei * 150n) / 100n; // 150% buffer
  const balanceWei = await ethers.provider.getBalance(deployer.address);
  console.log("Funding preflight:");
  console.log(`  Estimated gas (remaining deploys): ${estimatedGas.toString()}`);
  console.log(`  Gas price (maxFeePerGas):          ${ethers.formatUnits(gasPriceWei, "gwei")} gwei`);
  console.log(`  Estimated cost / required (x1.5):  ${ethers.formatEther(estimatedCostWei)} / ${ethers.formatEther(requiredWei)} ETH`);
  console.log(`  Deployer balance:                  ${ethers.formatEther(balanceWei)} ETH`);
  console.log("");
  if (balanceWei < requiredWei) {
    throw new Error(
      `Deployer balance ${ethers.formatEther(balanceWei)} ETH is below the buffered requirement ` +
      `${ethers.formatEther(requiredWei)} ETH. Top up ${deployer.address} before deploying.`
    );
  }

  console.log("Deploying contracts:");
  const { contract: soulRegistry, address: soulRegistryAddr } = await deployStep("SoulRegistry", [initialOwner, claimWindowSeconds]);
  const { contract: repAtt, address: repAttAddr } = await deployStep("ReputationAttestation", [initialOwner]);
  const { contract: valAtt, address: valAttAddr } = await deployStep("ValidationAttestation", [initialOwner]);
  const { contract: tipSplitter, address: tipSplitterAddr } = await deployStep("TipSplitter", [lesserWallet, initialOwner, soulRegistryAddr]);
  const { address: blobRendererAddr } = await deployStep("EtherealBlobRenderer", []);
  const { address: sacredRendererAddr } = await deployStep("SacredGeometryRenderer", []);
  const { address: sigilRendererAddr } = await deployStep("SigilRenderer", []);
  console.log("");

  const deployments = {};
  for (const [name] of deployPlan) {
    deployments[name] = { ...checkpoint[name] };
  }

  const [tipOwner, tipLesserWallet, tipAgentRegistry, tipPaused, tipWithdrawalsPaused] = await Promise.all([
    tipSplitter.owner(),
    tipSplitter.lesserWallet(),
    tipSplitter.agentIdentityRegistry(),
    tipSplitter.paused(),
    tipSplitter.withdrawalsPaused(),
  ]);
  const [soulOwner, soulClaimWindow, soulMintFee, soulMintSigner, soulPaused, isAttestor] = await Promise.all([
    soulRegistry.owner(),
    soulRegistry.claimWindowSeconds(),
    soulRegistry.mintFee(),
    soulRegistry.mintSigner(),
    soulRegistry.paused(),
    soulRegistry.isAttestor(mintAttestor),
  ]);
  const [repOwner, valOwner] = await Promise.all([repAtt.owner(), valAtt.owner()]);

  console.log("Read-only sanity checks:");
  console.log(`  SoulRegistry.owner():              ${soulOwner}`);
  console.log(`  SoulRegistry.claimWindowSeconds(): ${soulClaimWindow.toString()}`);
  console.log(`  SoulRegistry.mintFee():            ${soulMintFee.toString()} (set by Safe post-deploy)`);
  console.log(`  SoulRegistry.mintSigner():         ${soulMintSigner} (optional Safe post-deploy)`);
  console.log(`  SoulRegistry.isAttestor(attestor): ${isAttestor} (set by Safe post-deploy)`);
  console.log(`  SoulRegistry.paused():             ${soulPaused}`);
  console.log(`  TipSplitter.owner():               ${tipOwner}`);
  console.log(`  TipSplitter.lesserWallet():        ${tipLesserWallet}`);
  console.log(`  TipSplitter.agentIdentityRegistry(): ${tipAgentRegistry}`);
  console.log(`  TipSplitter.paused():              ${tipPaused}`);
  console.log(`  TipSplitter.withdrawalsPaused():   ${tipWithdrawalsPaused}`);
  console.log(`  ReputationAttestation.owner():     ${repOwner}`);
  console.log(`  ValidationAttestation.owner():     ${valOwner}`);
  console.log("");

  // Sanity checks are hard requirements: a wrong owner or mis-wired
  // constructor must stop the run before any Safe payload is written.
  function assertEq(label, actual, expected) {
    if (String(actual).toLowerCase() !== String(expected).toLowerCase()) {
      throw new Error(`Sanity check failed: ${label} is ${actual}, expected ${expected}`);
    }
  }
  assertEq("SoulRegistry.owner()", soulOwner, initialOwner);
  assertEq("SoulRegistry.claimWindowSeconds()", soulClaimWindow, claimWindowSeconds);
  assertEq("TipSplitter.owner()", tipOwner, initialOwner);
  assertEq("TipSplitter.lesserWallet()", tipLesserWallet, lesserWallet);
  assertEq("TipSplitter.agentIdentityRegistry()", tipAgentRegistry, soulRegistryAddr);
  assertEq("ReputationAttestation.owner()", repOwner, initialOwner);
  assertEq("ValidationAttestation.owner()", valOwner, initialOwner);
  if (soulPaused || tipPaused || tipWithdrawalsPaused) {
    throw new Error("Sanity check failed: a freshly deployed contract reports paused state");
  }

  const safeCalls = [
    {
      description: `SoulRegistry.setRenderer(0, ${blobRendererAddr}) // Ethereal Blob`,
      to: soulRegistryAddr,
      value_wei: "0",
      data: encodeSoulCall(ethers, "setRenderer", [0, blobRendererAddr]),
    },
    {
      description: `SoulRegistry.setRenderer(1, ${sacredRendererAddr}) // Sacred Geometry`,
      to: soulRegistryAddr,
      value_wei: "0",
      data: encodeSoulCall(ethers, "setRenderer", [1, sacredRendererAddr]),
    },
    {
      description: `SoulRegistry.setRenderer(2, ${sigilRendererAddr}) // Sigil`,
      to: soulRegistryAddr,
      value_wei: "0",
      data: encodeSoulCall(ethers, "setRenderer", [2, sigilRendererAddr]),
    },
    {
      description: `SoulRegistry.setMintFee(${mintFeeWei.toString()})`,
      to: soulRegistryAddr,
      value_wei: "0",
      data: encodeSoulCall(ethers, "setMintFee", [mintFeeWei]),
    },
    {
      description: `SoulRegistry.addAttestor(${mintAttestor}) // derived from ${mintSignerSsmParam}`,
      to: soulRegistryAddr,
      value_wei: "0",
      data: encodeSoulCall(ethers, "addAttestor", [mintAttestor]),
    },
  ];

  if (mintSigner) {
    safeCalls.push({
      description: `SoulRegistry.setMintSigner(${mintSigner}) // optional permit minting path`,
      to: soulRegistryAddr,
      value_wei: "0",
      data: encodeSoulCall(ethers, "setMintSigner", [mintSigner]),
    });
  }

  for (const token of tokenAllowlist) {
    safeCalls.push({
      description: `TipSplitter.setTokenAllowed(${token.label ? `${token.label}:` : ""}${token.address}, true)`,
      to: tipSplitterAddr,
      value_wei: "0",
      data: encodeTipCall(ethers, "setTokenAllowed", [token.address, true]),
    });
  }

  const safeTxBuilder = {
    version: "1.0",
    chainId: String(MAINNET_CHAIN_ID),
    createdAt: Date.now(),
    meta: {
      name: "lesser-host mainnet soul/tip post-deploy",
      description: "SoulRegistry renderers, mint fee, mint attestor, optional mint signer, and optional TipSplitter token allowlist.",
      txBuilderVersion: "1.0.0",
      createdFromSafeAddress: initialOwner,
      createdFromOwnerAddress: "",
    },
    transactions: [
      safeTransaction({
        to: soulRegistryAddr,
        method: "setRenderer",
        data: safeCalls[0].data,
        inputs: [
          { internalType: "uint8", name: "styleId", type: "uint8" },
          { internalType: "address", name: "renderer", type: "address" },
        ],
        values: { styleId: "0", renderer: blobRendererAddr },
      }),
      safeTransaction({
        to: soulRegistryAddr,
        method: "setRenderer",
        data: safeCalls[1].data,
        inputs: [
          { internalType: "uint8", name: "styleId", type: "uint8" },
          { internalType: "address", name: "renderer", type: "address" },
        ],
        values: { styleId: "1", renderer: sacredRendererAddr },
      }),
      safeTransaction({
        to: soulRegistryAddr,
        method: "setRenderer",
        data: safeCalls[2].data,
        inputs: [
          { internalType: "uint8", name: "styleId", type: "uint8" },
          { internalType: "address", name: "renderer", type: "address" },
        ],
        values: { styleId: "2", renderer: sigilRendererAddr },
      }),
      safeTransaction({
        to: soulRegistryAddr,
        method: "setMintFee",
        data: safeCalls[3].data,
        inputs: [{ internalType: "uint256", name: "fee", type: "uint256" }],
        values: { fee: mintFeeWei.toString() },
      }),
      safeTransaction({
        to: soulRegistryAddr,
        method: "addAttestor",
        data: safeCalls[4].data,
        inputs: [{ internalType: "address", name: "attestor", type: "address" }],
        values: { attestor: mintAttestor },
      }),
    ],
  };

  if (mintSigner) {
    const signerCall = safeCalls.find((call) => call.description.startsWith("SoulRegistry.setMintSigner"));
    safeTxBuilder.transactions.push(safeTransaction({
      to: soulRegistryAddr,
      method: "setMintSigner",
      data: signerCall.data,
      inputs: [{ internalType: "address", name: "signer", type: "address" }],
      values: { signer: mintSigner },
    }));
  }

  for (const token of tokenAllowlist) {
    const call = safeCalls.find((candidate) => candidate.description.includes(token.address));
    safeTxBuilder.transactions.push(safeTransaction({
      to: tipSplitterAddr,
      method: "setTokenAllowed",
      data: call.data,
      inputs: [
        { internalType: "address", name: "token", type: "address" },
        { internalType: "bool", name: "allowed", type: "bool" },
      ],
      values: { token: token.address, allowed: "true" },
    }));
  }

  for (const [name, deployment] of Object.entries(deployments)) {
    const receipt = await receiptFor(ethers, deployment.deployment_tx_hash);
    if (receipt) {
      deployment.deployment_block_number = receipt.blockNumber;
      deployment.gas_used = receipt.gasUsed.toString();
      deployment.effective_gas_price_wei = (receipt.gasPrice ?? 0n).toString();
    }
    // Auditable fingerprint of what actually landed on chain. Contracts with
    // immutables differ from the artifact at the immutable slots, so this is
    // recorded as-is; source-level verification happens via verify:mainnet:all.
    deployment.deployed_code_keccak256 = ethers.keccak256(await ethers.provider.getCode(deployment.address));
  }

  const latestBlock = await ethers.provider.getBlock("latest");
  const record = {
    schema_version: 1,
    network: "mainnet",
    chain_id: MAINNET_CHAIN_ID,
    stage: "live",
    deployed_at_utc: latestBlock ? new Date(Number(latestBlock.timestamp) * 1000).toISOString() : new Date().toISOString(),
    deployer_eoa: deployer.address,
    owner_safe: initialOwner,
    lesser_wallet: lesserWallet,
    soul_claim_window_seconds: claimWindowSeconds.toString(),
    soul_mint_fee_wei: mintFeeWei.toString(),
    soul_mint_attestor_address: mintAttestor,
    soul_avatar_renderers: {
      "0": blobRendererAddr,
      "1": sacredRendererAddr,
      "2": sigilRendererAddr,
    },
    contracts: deployments,
    ssm: {
      rpc_url_ssm_param: rpcSsmParam,
      soul_mint_signer_key_ssm_param: mintSignerSsmParam,
    },
    lesser_host: {
      public_base_url: "https://lesser.host",
      safe_tx_builder_soul_tip_post_deploy_file: "docs/deployments/mainnet/safe-tx-builder-soul-tip-post-deploy.json",
      cdk_context: {
        tipEnabledLive: "true",
        tipChainIdLive: String(MAINNET_CHAIN_ID),
        tipContractAddressLive: tipSplitterAddr,
        tipRpcUrlSsmParamLive: rpcSsmParam,
        soulEnabledLive: "true",
        soulChainIdLive: String(MAINNET_CHAIN_ID),
        soulRegistryContractAddressLive: soulRegistryAddr,
        soulReputationAttestationContractAddressLive: repAttAddr,
        soulValidationAttestationContractAddressLive: valAttAddr,
        soulRpcUrlSsmParamLive: rpcSsmParam,
        soulMintSignerKeySsmParamLive: mintSignerSsmParam,
        // Live admin flows hard-require these (tip_registry_auto_ops.go,
        // handlers_soul_registry.go); without Live-suffixed values the live
        // stage falls back to the shared lab/Sepolia context entries.
        soulAdminSafeAddressLive: initialOwner,
        tipAdminSafeAddressLive: initialOwner,
        tipDefaultHostWalletAddressLive: lesserWallet,
      },
    },
    post_deploy_pending_operator_steps: [
      "Execute the Safe Transaction Builder batch (renderers, mint fee, attestor, optional mint signer / token allowlist).",
      "Register at least one TipSplitter host via a Safe registerHost transaction — every tip path reverts 'host not active' until then; do not enable tipEnabledLive before this.",
      "Verify all seven contracts on Etherscan (npm run verify:mainnet:all) before executing the Safe batch.",
      "Apply the suggested cdk_context block verbatim to the live stage context (replace the stale in-tree live soul/tip values).",
      "Copy this record + the Safe tx-builder JSON + Safe execution tx hashes into durable evidence (gov-infra/evidence/) after execution.",
    ],
    verification: {
      soul_registry_owner: soulOwner,
      soul_registry_claim_window_seconds: soulClaimWindow.toString(),
      soul_registry_mint_fee_before_safe_post_deploy: soulMintFee.toString(),
      soul_registry_mint_signer_before_safe_post_deploy: soulMintSigner,
      soul_registry_attestor_before_safe_post_deploy: isAttestor,
      soul_registry_paused: soulPaused,
      tip_splitter_owner: tipOwner,
      tip_splitter_lesser_wallet: tipLesserWallet,
      tip_splitter_agent_identity_registry: tipAgentRegistry,
      tip_splitter_paused: tipPaused,
      tip_splitter_withdrawals_paused: tipWithdrawalsPaused,
      reputation_attestation_owner: repOwner,
      validation_attestation_owner: valOwner,
    },
    post_deploy_required_safe_calls: safeCalls,
  };

  const recordPath = resolve(contractsDir, optionalEnv("MAINNET_DEPLOYMENT_RECORD_PATH") || DEFAULT_DEPLOYMENT_RECORD);
  const safeTxPath = resolve(contractsDir, optionalEnv("MAINNET_SAFE_TX_BUILDER_PATH") || DEFAULT_SAFE_TX_BUILDER);
  writeJSON(recordPath, mergeDeploymentRecord(recordPath, record));
  writeJSON(safeTxPath, safeTxBuilder);

  console.log("Required Safe multisig transactions written:");
  console.log(`  ${safeTxPath}`);
  console.log("Deployment record written:");
  console.log(`  ${recordPath}`);
  console.log("Deploy checkpoint retained (guards against accidental re-deploys):");
  console.log(`  ${checkpointPath}`);
  console.log("");
  console.log("Next steps (also recorded in post_deploy_pending_operator_steps):");
  console.log("  1. npm run verify:mainnet:all   # Etherscan source verification, mandatory before Safe execution");
  console.log("  2. Import + execute the Safe Transaction Builder batch");
  console.log("  3. Queue the first TipSplitter registerHost Safe tx before enabling tipEnabledLive");
  console.log("");
  console.log("Suggested CDK context updates (live stage):");
  console.log(JSON.stringify(record.lesser_host.cdk_context, null, 2));
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
