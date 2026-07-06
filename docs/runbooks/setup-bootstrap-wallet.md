# One-time setup bootstrap wallet

`lesser-host` setup is gated by `BOOTSTRAP_WALLET_ADDRESS` while the Control plane is locked.
For AppTheory deploys, `scripts/app-theory-cdk.sh` resolves that address at deploy time so no real bootstrap wallet address is committed to
`cdk/cdk.json`, and no private key is stored in CloudFormation, Lambda environment variables,
logs, or PR text.

## Deploy behavior

Use the AppTheory deploy contract:

```bash
AWS_PROFILE=Lesser AWS_REGION=us-east-1 theory app up --stage lab --execute
```

For `up`, the wrapper resolves the bootstrap wallet in this order:

1. If `BOOTSTRAP_WALLET_ADDRESS` is set to a valid EVM `0x` address, pass that address to CDK.
   Placeholder values such as `<YOUR_BOOTSTRAP_WALLET_ADDRESS>` are rejected.
2. Otherwise, read `/lesser-host/<stage>/setup/bootstrap-wallet-private-key` from SSM Parameter
   Store as a `SecureString` and derive the address from the stored JSON payload.
3. If the SSM parameter is missing, generate a one-time EVM wallet, store JSON containing
   `private_key` and `address` in that `SecureString`, and pass only the address to CDK.

The deploy output prints only the SSM parameter name and the non-secret address. It never prints
the private key.

For `down`, the wrapper never creates a wallet. It uses a valid `BOOTSTRAP_WALLET_ADDRESS` override
when present, otherwise reads the SSM address if the parameter exists, otherwise destroys with an
empty bootstrap-wallet context override.

## Retrieve the private key for first setup

Only retrieve the private key when an operator is ready to import it into a wallet and finish setup.
The command below prints a secret to your terminal and shell history may capture it depending on your
environment.

```bash
aws ssm get-parameter \
  --profile Lesser \
  --region us-east-1 \
  --name /lesser-host/lab/setup/bootstrap-wallet-private-key \
  --with-decryption \
  --query 'Parameter.Value' \
  --output text | jq -r '.private_key'
```

Use the corresponding stage path for live:

```bash
aws ssm get-parameter \
  --profile Lesser \
  --region us-east-1 \
  --name /lesser-host/live/setup/bootstrap-wallet-private-key \
  --with-decryption \
  --query 'Parameter.Value' \
  --output text | jq -r '.private_key'
```

After importing the key, open `/setup` and sign the bootstrap challenge once. The UI clears the
connected bootstrap wallet after that verification. Reconnect the real primary admin credential,
create the primary admin, register a primary admin passkey, and then finalize setup.

The bootstrap wallet is one-time setup authority only. It must not be reused as the primary admin
wallet, and `/setup/admin` rejects attempts to link the configured bootstrap wallet as the primary
admin credential.

If a lab environment was already finalized with the bootstrap wallet as the primary admin, treat that
environment as mis-bootstrapped: do not promote it to live. Use the existing admin session to add a
real admin credential/passkey if possible, then rotate ownership away from the bootstrap wallet; if
that cannot be proven cleanly, rebuild the lab control-plane state and rerun setup with a separate
primary admin wallet. Do not reuse or publish the SSM-stored bootstrap private key as an operator
credential.

## Manual override

To use an operator-supplied one-time bootstrap wallet instead of SSM generation:

```bash
BOOTSTRAP_WALLET_ADDRESS=0x0123456789abcdef0123456789abcdef01234567 \
  AWS_PROFILE=Lesser AWS_REGION=us-east-1 theory app up --stage lab --execute
```

Use a real wallet address; the example address above is not suitable for an actual deploy.
