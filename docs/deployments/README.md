# Deployments

This directory is the canonical, in-repo place to track **latest on-chain contract deployments** and the **required post-deploy admin calls** (Safe-ready calldata).

Conventions:

- One subdirectory per network (example: `docs/deployments/sepolia/`).
- `latest.json` is the stable pointer that should always reflect the current “active” deployment for that network.
- Keep a Safe Transaction Builder import file alongside `latest.json` when there are required owner/admin calls.
- Keep `latest.json` free of secrets (SSM parameter *names* are OK; values are not).
- When deploying new contracts:
  1. Deploy contracts (Hardhat scripts in `contracts/`).
  2. Update `docs/deployments/<network>/latest.json` with addresses + tx hashes + required Safe calls.
  3. Update `cdk/cdk.json` context keys to point `lesser-host` at the new addresses.
  4. Deploy `lesser-host` for the relevant stage.
