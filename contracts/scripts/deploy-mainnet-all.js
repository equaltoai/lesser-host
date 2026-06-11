import hre from "hardhat";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const MAINNET_CHAIN_ID = 1;
const DEFAULT_MAINNET_RPC_SSM_PARAM = "/lesser-host/api/infura/mainnet";
const DEFAULT_MINT_SIGNER_SSM_PARAM = "/lesser-host/soul/live/mint-signer-key";
const DEFAULT_DEPLOYMENT_RECORD = "../docs/deployments/mainnet/latest.json";
const DEFAULT_SAFE_TX_BUILDER = "../docs/deployments/mainnet/safe-tx-builder-soul-tip-post-deploy.json";

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
    post_deploy_required_safe_calls: [
      ...(previous.post_deploy_required_safe_calls ?? []),
      ...(nextRecord.post_deploy_required_safe_calls ?? []),
    ],
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

  const signers = await ethers.getSigners();
  if (signers.length === 0) {
    throw new Error("No deployer signer available. Set DEPLOYER_PRIVATE_KEY.");
  }
  const deployer = signers[0];

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

  const SoulRegistry = await ethers.getContractFactory("SoulRegistry");
  const soulRegistry = await SoulRegistry.deploy(initialOwner, claimWindowSeconds);
  await soulRegistry.waitForDeployment();
  const soulRegistryAddr = await soulRegistry.getAddress();

  const ReputationAttestation = await ethers.getContractFactory("ReputationAttestation");
  const repAtt = await ReputationAttestation.deploy(initialOwner);
  await repAtt.waitForDeployment();
  const repAttAddr = await repAtt.getAddress();

  const ValidationAttestation = await ethers.getContractFactory("ValidationAttestation");
  const valAtt = await ValidationAttestation.deploy(initialOwner);
  await valAtt.waitForDeployment();
  const valAttAddr = await valAtt.getAddress();

  const TipSplitter = await ethers.getContractFactory("TipSplitter");
  const tipSplitter = await TipSplitter.deploy(lesserWallet, initialOwner, soulRegistryAddr);
  await tipSplitter.waitForDeployment();
  const tipSplitterAddr = await tipSplitter.getAddress();

  const EtherealBlobRenderer = await ethers.getContractFactory("EtherealBlobRenderer");
  const blobRenderer = await EtherealBlobRenderer.deploy();
  await blobRenderer.waitForDeployment();
  const blobRendererAddr = await blobRenderer.getAddress();

  const SacredGeometryRenderer = await ethers.getContractFactory("SacredGeometryRenderer");
  const sacredRenderer = await SacredGeometryRenderer.deploy();
  await sacredRenderer.waitForDeployment();
  const sacredRendererAddr = await sacredRenderer.getAddress();

  const SigilRenderer = await ethers.getContractFactory("SigilRenderer");
  const sigilRenderer = await SigilRenderer.deploy();
  await sigilRenderer.waitForDeployment();
  const sigilRendererAddr = await sigilRenderer.getAddress();

  const deployments = {
    SoulRegistry: { address: soulRegistryAddr, deployment_tx_hash: txHash(soulRegistry) },
    ReputationAttestation: { address: repAttAddr, deployment_tx_hash: txHash(repAtt) },
    ValidationAttestation: { address: valAttAddr, deployment_tx_hash: txHash(valAtt) },
    TipSplitter: { address: tipSplitterAddr, deployment_tx_hash: txHash(tipSplitter) },
    EtherealBlobRenderer: { address: blobRendererAddr, deployment_tx_hash: txHash(blobRenderer) },
    SacredGeometryRenderer: { address: sacredRendererAddr, deployment_tx_hash: txHash(sacredRenderer) },
    SigilRenderer: { address: sigilRendererAddr, deployment_tx_hash: txHash(sigilRenderer) },
  };

  console.log("Deployed contracts:");
  for (const [name, deployment] of Object.entries(deployments)) {
    console.log(`  ${name}: ${deployment.address}`);
    console.log(`    Tx: ${deployment.deployment_tx_hash}`);
  }
  console.log("");

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
      },
    },
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
  console.log("");
  console.log("Suggested CDK context updates (live stage):");
  console.log(JSON.stringify(record.lesser_host.cdk_context, null, 2));
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
