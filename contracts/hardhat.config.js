import "dotenv/config";
import HardhatEthers from "@nomicfoundation/hardhat-ethers";

/** @type import('hardhat/config').HardhatUserConfig */
const sepoliaUrl = process.env.SEPOLIA_RPC_URL;
const sepoliaAccounts = process.env.DEPLOYER_PRIVATE_KEY
  ? [process.env.DEPLOYER_PRIVATE_KEY]
  : [];

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

const config = {
  plugins: [HardhatEthers],
  solidity: {
    version: "0.8.24",
    settings: {
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
