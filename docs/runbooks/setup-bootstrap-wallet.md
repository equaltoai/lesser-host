# One-time setup bootstrap wallet

`lesser-host` setup is gated by `BOOTSTRAP_WALLET_ADDRESS` while the Control plane is locked.
For AppTheory deploys, the bootstrap wallet is now owned by the CDK/CloudFormation stack lifecycle:
CloudFormation creates the one-time EVM wallet during stack creation, stores the private key payload in
SSM Parameter Store as a `SecureString`, and passes only the derived address to the Control plane Lambda
environment. No private key is stored in CloudFormation templates, Lambda environment variables, git,
logs, or PR text.

## Deploy behavior

Use the AppTheory deploy contract:

```bash
AWS_PROFILE=Lesser AWS_REGION=us-east-1 theory app up --stage lab --execute
```

For `up`, `scripts/app-theory-cdk.sh` validates deploy provenance, builds/synthesizes CDK, and then:

1. If `BOOTSTRAP_WALLET_ADDRESS` is set to a valid EVM `0x` address, it passes that address to CDK as an
   **emergency override**. Placeholder values such as `<YOUR_BOOTSTRAP_WALLET_ADDRESS>` are rejected.
   In override mode CDK does not generate or store a bootstrap private key.
2. Otherwise, CDK creates a stack-owned custom resource. On CloudFormation `Create`, that resource
   generates a fresh one-time EVM wallet, writes JSON containing `private_key` and `address` to
   `/lesser-host/<stage>/setup/bootstrap-wallet-private-key` as a `SecureString`, and returns only the
   address to the template for `BOOTSTRAP_WALLET_ADDRESS`.
3. On CloudFormation `Update`, the resource re-reads the existing stack-owned SecureString and returns
   the same address; ordinary updates do not rotate the bootstrap wallet.
4. On lab stack deletion (`RemovalPolicy.DESTROY`), the resource deletes the SecureString. A genuinely new
   lab stack creation writes a fresh key and overwrites any stale pre-fix value at the same path instead
   of silently reusing it.

The AppTheory wrapper no longer reads or writes the generated bootstrap private key itself. That prevents
a stale, long-lived stage-scoped SSM parameter created outside CloudFormation from becoming authoritative
for future deployments.

For `down`, use the same AppTheory contract:

```bash
AWS_PROFILE=Lesser AWS_REGION=us-east-1 theory app down --stage lab --execute
```

Do not set a timeout on deploy/destroy commands. Let CloudFormation finish or roll back and capture the
complete output.

## Retrieve the private key for first setup

Only retrieve the private key when an operator is ready to import it into a wallet and finish setup. The
command below prints a secret to your terminal and shell history may capture it depending on your
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

After importing the key, open `/setup` and sign the bootstrap challenge once. The setup session token is
memory-only in the page: it is not initialized from `sessionStorage`, and refreshing the page requires
signing Step 1 again. After Step 1, the UI clears the connected bootstrap wallet. Then either:

- use the **passkey-only** Step 2 lane to create the primary admin and bind the first passkey atomically, or
- use the **wallet-first** Step 2 lane with a distinct real primary admin wallet, then register a primary admin
  passkey in Step 3.

In both lanes the bootstrap wallet remains setup-only authority and never becomes an actor credential.
`/setup/finalize` requires an authenticated primary admin session plus at least one registered WebAuthn
passkey.

The bootstrap wallet is one-time setup authority only. It must not be reused as the primary admin wallet,
and `/setup/admin` rejects attempts to link the configured bootstrap wallet as the primary admin
credential. On the passkey-only setup lane, `/setup/admin` also returns the primary admin session so the
operator can finalize without any wallet-linked actor credential.

## Manual override

To use an operator-supplied one-time bootstrap wallet instead of CloudFormation generation:

```bash
BOOTSTRAP_WALLET_ADDRESS=0x0123456789abcdef0123456789abcdef01234567 \
  AWS_PROFILE=Lesser AWS_REGION=us-east-1 theory app up --stage lab --execute
```

Use a real wallet address; the example address above is not suitable for an actual deploy. Treat override
mode as emergency/manual operation: the operator owns private-key storage and retrieval, and CDK will not
manage the SSM SecureString for that deploy.

## Recovery for the mis-bootstrapped lab from #893/#894

If a brand new lab deployment after #894 reused the same bootstrap key or `/setup` Step 1 appeared
complete immediately, assume two stale states may exist: browser `sessionStorage` and the pre-fix
stage-scoped SSM SecureString.

1. **Clear browser state first.** In every browser/profile used for setup, clear
   `sessionStorage['lesser-host:setupSessionToken']` for the lab origin, or open a fresh private window.
   The fixed UI removes that legacy key on page load and no longer uses it to complete Step 1.
2. **Deploy this fix to lab through AppTheory.** Do not manually write SSM. On stack create, the new
   CloudFormation custom resource will generate the key and overwrite any stale pre-fix value at
   `/lesser-host/lab/setup/bootstrap-wallet-private-key`.
3. **If the bad lab stack is still present and locked,** a normal `theory app up --stage lab --execute`
   updates code and the custom resource. Retrieve the current SecureString after the update and verify
   the address shown on `/setup/status` matches the stored payload's `address` before signing.
4. **If the bad lab stack was destroyed before this fix,** ensure the next lab rebuild uses this fix. The
   new stack creation overwrites the stale pre-fix SSM value rather than reusing it. Do not import or use
   an old key retrieved before the fixed stack creation.
5. **If lab was finalized with the bootstrap wallet as admin,** treat it as mis-bootstrapped. Do not
   promote it. If a real admin credential/passkey can be added and ownership rotated away from the
   bootstrap wallet with audit evidence, do that; otherwise rebuild lab control-plane state and rerun
   setup using a separate primary admin wallet.
6. **Rebuild lab state when in doubt.** Rebuild if `/setup/status` reports an unexpected bootstrap
   address, if a stale browser session can still affect the flow, if the primary admin is the bootstrap
   wallet, or if you cannot prove the generated key came from the CloudFormation-owned resource.

Do not delete or mutate live SSM parameters, DynamoDB tables, stacks, or retained resources as part of lab
recovery without explicit operator authorization.
