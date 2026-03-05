# lesser-host — Open Source Cleanup Plan

Preparation steps before making the repository public. Organized by priority.

---

## Context

- The `contracts/.env` values are Sepolia testnet — low risk, but should still not ship in a public repo
- Files like `docs/deployments/sepolia/latest.json` should remain on disk locally but be removed from git tracking
- The git author email (`aron23@gmail.com`) is the GitHub account that owns the org — not sensitive
- Domain names, provider names, and SSM path patterns are architectural, not secret — they stay

---

## 1. Extract operational values from cdk/cdk.json

`cdk/cdk.json` contains real AWS account IDs, Route53 zone IDs, wallet addresses, and personal email templates that are used for deployment but should not be in a public repo.

### Steps

1. Create `cdk/cdk.context.local.json` with the operational values extracted from `cdk/cdk.json`:
   - `managedOrgVendingRoleArn` (contains org account ID)
   - `orgBootstrapControlPlaneAccountId` (`<CONTROL_PLANE_ACCOUNT_ID>`)
   - `webHostedZoneId` (`<HOSTED_ZONE_ID>`)
   - `managedParentHostedZoneId` (`<MANAGED_PARENT_HOSTED_ZONE_ID>`)
   - `managedAccountEmailTemplateLab` (contains `<YOUR_EMAIL>`)
   - `managedAccountEmailTemplateLive` (contains `<YOUR_EMAIL>`)
   - `bootstrapWalletAddress`
   - `tipContractAddressLab`
   - `soulRegistryContractAddressLab`
   - `soulReputationAttestationContractAddressLab`
   - `soulValidationAttestationContractAddressLab`
   - `soulAdminSafeAddress`
   - `tipAdminSafeAddress`
   - `tipDefaultHostWalletAddress`

2. Add `cdk/cdk.context.local.json` to `.gitignore`

3. Update CDK stack to merge values from `cdk.context.local.json` at synth time (or use environment variables)

4. Replace extracted values in `cdk/cdk.json` with descriptive placeholders:
   ```json
   "orgBootstrapControlPlaneAccountId": "<YOUR_CONTROL_PLANE_ACCOUNT_ID>",
   "managedOrgVendingRoleArn": "arn:aws:iam::<YOUR_ORG_ACCOUNT_ID>:role/lesser-host-org-vending",
   "webHostedZoneId": "<YOUR_HOSTED_ZONE_ID>",
   "managedAccountEmailTemplateLab": "<YOUR_EMAIL>+lab-{slug}@<YOUR_DOMAIN>",
   ```

5. Create `cdk/cdk.context.local.json.example` with the same keys and placeholder values for reference

### Verification
- `cdk synth` still works with local config present
- `cdk/cdk.json` contains no real account IDs, zone IDs, or personal emails
- `cdk/cdk.context.local.json` is gitignored

---

## 2. Stop CDK synth from bundling contracts/.env

CDK synth copies `contracts/.env` (containing Sepolia deployer private key, Infura key, Etherscan key) into 147+ asset bundles under `cdk/cdk.out/`. While `cdk.out/` is gitignored, this is a bad pattern.

### Steps

1. Investigate which CDK construct bundles the `contracts/` directory as an asset
2. Add a `.cdkignore` or explicit exclude pattern to prevent `.env` from being copied into asset bundles
3. Alternatively, add an exclude in the asset bundling options (e.g., `exclude: ['**/.env']`)

### Post-fix

4. Delete existing `cdk/cdk.out/` to clear the 147 copies: `rm -rf cdk/cdk.out/`
5. Re-run `cdk synth` and verify no `.env` files appear in the output

### Optional

6. Rotate the Sepolia deployer private key, Infura project ID, and Etherscan API key
   - These are testnet values with no real funds at stake
   - Rotation is good hygiene but not urgent

---

## 3. Remove operational docs from git tracking (keep locally)

These files contain real deployment data (account IDs, contract addresses, Safe transaction payloads, operational logs) that should remain on disk but not ship in the public repo.

### Files to untrack

```bash
git rm --cached docs/deployments/sepolia/latest.json
git rm --cached docs/deployments/sepolia/safe-tx-builder-post-deploy.json
git rm --cached docs/deployments/sepolia/safe-tx-builder-set-mint-signer.json
git rm --cached docs/testing-plan.md
```

### Add to .gitignore

```gitignore
# Deployment manifests and operational logs (environment-specific)
docs/deployments/
docs/testing-plan.md
```

### Verification
- Files still exist on disk
- `git status` shows them as deleted from tracking
- New clones will not receive these files

---

## 4. Scrub real account IDs from remaining tracked docs

Two documentation files reference real AWS account IDs inline.

### Files

- `docs/managed-instance-provisioning.md` line 167: references `<CONTROL_PLANE_ACCOUNT_ID>`
- `docs/agent-managed-provisioning.md` lines 17-18: contains wallet addresses
- `docs/agent-impl-managed-provisioning.md` lines 7-8: contains wallet addresses

### Steps

1. Replace real account IDs with `<CONTROL_PLANE_ACCOUNT_ID>` placeholder
2. Replace real wallet addresses with `<ADMIN_WALLET>`, `<LESSER_WALLET>` etc.
3. These are instructional docs — placeholders are fine and arguably clearer

---

## 5. Review: items that can stay as-is

The following were flagged in the audit but do not need remediation:

| Item | Reason to keep |
|------|---------------|
| Git author `aron23@gmail.com` | This is the GitHub account that owns the org. Already public. |
| SSM parameter paths in `internal/secrets/keys.go` | Architectural, not secret. Paths reveal structure, not values. |
| Domain names (`lesser.host`, `lessersoul.ai`, `greater.website`, `lab.lesser.host`) | Product domains — will be publicly known. |
| Provider names (Migadu, Telnyx, Stripe, Infura, OpenAI, Anthropic) | Obvious from the code's imports and functionality. |
| Migadu SMTP host/port (`smtp.migadu.com:587`) | Published in Migadu's own documentation. |
| GitHub org and repo names (`equaltoai/lesser`, `equaltoai/lesser-body`) | These repos are already public. |
| Theory framework references (`theory-cloud/apptheory`, `theory-cloud/tabletheory`) | Visible in go.mod of already-public repos. |
| `fedipack.com` in test docs | Being untracked with testing-plan.md (step 3). |
| `contracts/.env.example` | Contains empty variable names only. Correct practice. |
| Test placeholder values (`sk_test`, `123456789012`, `@example.com`) | Intentionally safe test fixtures. |

---

## Execution order

1. **Step 1** (cdk.json extraction) — most impactful, do first
2. **Step 3** (untrack deployment docs) — quick, do alongside step 1
3. **Step 4** (scrub docs) — straightforward find-and-replace
4. **Step 2** (CDK .env bundling) — can be done independently
5. Commit all changes, verify `git diff` shows only placeholder replacements and untracked files
6. Final review: `git log --all --diff-filter=A -- '*.env' '*.pem' '*.key'` to catch anything missed
7. Push to public

---

## Post-public checklist

- [ ] Verify `cdk/cdk.context.local.json` is not visible in the public repo
- [ ] Verify `docs/deployments/` is not visible in the public repo
- [ ] Verify `docs/testing-plan.md` is not visible in the public repo
- [ ] Verify no real AWS account IDs appear anywhere in the public repo
- [ ] Verify `contracts/.env` is not in any tracked file or asset bundle
- [ ] Consider adding a CONTRIBUTING.md noting that operational values go in `cdk.context.local.json`
