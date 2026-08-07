import "dotenv/config";
import HardhatEthers from "@nomicfoundation/hardhat-ethers";

/** @type import('hardhat/config').HardhatUserConfig */
const sepoliaUrl = process.env.SEPOLIA_RPC_URL;
const sepoliaAccounts = process.env.DEPLOYER_PRIVATE_KEY
  ? [process.env.DEPLOYER_PRIVATE_KEY]
  : [];
const mainnetUrl = process.env.MAINNET_RPC_URL;

const networks = {
  hardhat: {
    type: "edr-simulated",
  },
};

if (sepoliaUrl) {
  networks.sepolia = {
    type: "http",
    chainId: 11155111,
    url: sepoliaUrl,
    accounts: sepoliaAccounts,
  };
}

if (mainnetUrl) {
  networks.mainnet = {
    type: "http",
    chainId: 1,
    url: mainnetUrl,
    accounts: sepoliaAccounts,
  };

  // Rehearsal-only fork of mainnet (chainId 1) so deploy-mainnet-all.js can be
  // exercised end-to-end (REHEARSAL=1) without sending real transactions.
  networks.mainnetFork = {
    type: "edr-simulated",
    chainId: 1,
    forking: {
      url: mainnetUrl,
    },
  };
}

const config = {
  plugins: [HardhatEthers],
  solidity: {
    version: "0.8.24",
    settings: {
      evmVersion: "cancun",
      optimizer: {
        enabled: true,
        runs: 200,
      },
    },
  },
  paths: {
    sources: "./contracts",
    tests: "./test",
    cache: "./cache",
    artifacts: "./artifacts",
  },
  networks,
};

export default config;
