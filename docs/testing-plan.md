# lesser-host Testing Plan (Structured Checklist)

This checklist is designed for **handoff between an LLM operator** (can run shell commands and control a browser) and a **human operator** (can sign with a wallet, approve Safe transactions, and complete WebAuthn). Each section is tagged:

- **[LLM]** can be completed by the LLM
- **[HUMAN]** requires a human operator
- **[LLM+HUMAN]** needs both (explicit handoff)

Use this document as a living record. Check items as you go and paste key evidence links and notes.

---

## **0) Handoff Metadata**

- [x] **[HUMAN]** Stage and account confirmed (e.g., `lab` in sandbox AWS).
- [x] **[LLM]** Record deployment target:
  - Stage: `lab`
  - Region: `us-east-1`
  - Account: `lesser` (693925625407)
  - Stack: `lesser-host-lab` (UPDATE_COMPLETE 2026-02-05)
  - Deploy command used: `cd cdk && npm run deploy`
- [x] **[LLM]** Record contract:
  - Sepolia TipSplitter: `0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe`
  - Explorer: `https://sepolia.etherscan.io/address/0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe`
- [x] **[HUMAN]** Confirm wallet addresses for:
  - `TIP_ADMIN_SAFE_ADDRESS`: `0xfE63333F303D4f7b2354f7E3eca752C812D65907`
  - `TIP_DEFAULT_HOST_WALLET_ADDRESS`: `0x1e14865a53a994b01b9ccfef42669dc0bfe98805`

---

## **1) Preconditions & Secrets**

- [x] **[LLM]** Confirm required SSM params exist:
  - `/lesser-host/api/infura/sepolia` (SecureString) ✓ v1
  - `/lesser-host/api/infura/mainnet` (SecureString) ✓ v1
- [x] **[HUMAN]** Confirm additional secrets exist if enabled:
  - `/lesser-host/stripe/lab/publishable` (String) ✓
  - `/lesser-host/stripe/lab/secret` (SecureString) ✓
  - `/lesser-host/stripe/lab/webhook` (SecureString) ✓
  - `/lesser-host/api/openai/service` (SecureString) ✓
  - `/lesser-host/api/claude` (SecureString) ✓
- [x] **[LLM]** Confirm CDK context values for tip config:
  - `tipEnabledLab=true` ✓
  - `tipChainIdLab=11155111` ✓
  - `tipContractAddressLab=0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe` ✓
  - `tipRpcUrlSsmParamLab=/lesser-host/api/infura/sepolia` ✓
  - `tipEnabledLive=false` ✓ (until mainnet)

---

## **2) Local Unit & Component Tests**

- [x] **[LLM]** Go tests:
  - Command: `go test ./...`
  - Result: All 22 packages pass (6 no test files)
- [x] **[LLM]** Contracts tests:
  - Command: `cd contracts && npm test`
  - Result: 73 pass, 0 fail (15 suites)
- [x] **[LLM]** Web lint:
  - Command: `cd web && npm run lint`
  - Result: Clean, no warnings
- [x] **[LLM]** Web typecheck:
  - Command: `cd web && npm run typecheck`
  - Result: svelte-check 0 errors, tsc clean
- [x] **[LLM]** Web tests:
  - Command: `cd web && npm test`
  - Result: 5 files, 10 tests pass

---

## **3) CDK & Infra Validation**

- [x] **[LLM]** CDK synth:
  - Command: `cd cdk && npm run synth`
  - Result: Synth successful, template generated
- [x] **[LLM]** Validate CloudFront behaviors (by config review):
  - `api/*`, `auth/*`, `setup/status`, `setup/bootstrap/*`, `setup/admin`, `setup/finalize` → control-plane ✓
  - `.well-known/*` → trust-api (cached) ✓
  - `attestations`, `attestations/*` → trust-api (no-cache/cached) ✓
  - SPA CloudFront function excludes `/api/`, `/auth/`, `/setup/`, `/.well-known/`, `/attestations` from rewrite ✓

---

## **4) Setup Bootstrap**

- [x] **[LLM]** `GET /setup/status` (before finalize)
  - State: `active`, already bootstrapped (2026-02-05T18:32:10Z)
  - Bootstrap wallet: `0x80189edB676D51b2FB2257B2AD38e018B20CA46E` ✓
  - Primary admin: `aron` ✓
- [x] **[LLM+HUMAN]** `/setup/bootstrap/challenge` + `/verify`
  - Already completed (bootstrapped_at set)
- [x] **[LLM+HUMAN]** `/setup/admin`
  - Already completed (primary_admin_set: true)
- [x] **[LLM]** `/setup/finalize`
  - Already completed (control_plane_state: active, finalize_allowed: false)

Handoff notes:
- Setup was previously completed on 2026-02-05. No action needed.

---

## **5) Auth & Sessions**

- [x] **[LLM+HUMAN]** Wallet login:
  - `/auth/wallet/challenge` ✓
  - **HUMAN** signed challenge via MetaMask (browser UI)
  - Login successful: `aron` / `admin` / `wallet` method
- [x] **[LLM]** Session persistence (check `sessionStorage`)
  - `lesser-host:session:v1` stored in sessionStorage ✓
  - Token validated via `GET /api/v1/operators/me` → 200 ✓
- [x] **[LLM]** Logout (`/api/v1/auth/logout`) invalidates session
  - UI logout button works ✓
  - NOTE: curl POST to `/api/v1/auth/logout` returns 404 — may need redeployment

---

## **6) WebAuthn (Passkeys)**

- [x] **[LLM+HUMAN]** Register begin/finish
  - **HUMAN** completed WebAuthn prompt in UI
  - Passkey created successfully (2026-02-08)
- [x] **[LLM+HUMAN]** Login begin/finish
  - Logged out and back in using passkey (2026-02-08)

Handoff notes:
- **HUMAN** must be physically present for WebAuthn prompts.

---

## **7) Tip Registry (Sepolia)**

- [x] **[LLM]** Validate config:
  - `TIP_CHAIN_ID` = 11155111
  - `TIP_RPC_URL_SSM_PARAM` = `/lesser-host/api/infura/sepolia` (SSM param exists)
  - `TIP_CONTRACT_ADDRESS` = `0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe`
- [ ] **[LLM+HUMAN]** Create and verify registration
  - **LLM** initiates registration
  - **HUMAN** signs wallet challenge + provides proofs
  - **LLM** submits proof + signature
- [ ] **[LLM+HUMAN]** Registration UI flow (`/tip-registry/register`)
  - Load the page and confirm it is public (no auth required)
  - Generate proof with domain + wallet + fee (DNS TXT shown)
  - **HUMAN** publishes DNS record or HTTPS well-known file
  - **HUMAN** signs wallet message via browser wallet
  - Verify proofs and capture Safe payload
  - Record the resulting operation id + Safe payload
- [x] **[LLM]** Generate Safe payload for:
  - `registerHost` (ensure host)
  - Operation: `tipop_4b582728afa15a0ff884b82e6415f47f`
  - Domain: `fedipack.com`
  - Safe: `0xfE63333f303D4f7b2354f7E3eca752C812d65907`
  - To: `0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe`
  - Value: `0`
  - Wallet: `0x1e14865a53a994b01b9ccfef42669dc0bfe98805`
  - Data: `0xbb361a87d60f4586d7e89146cf0948252460732025fbac5ef478a5c91bafea4ecd17f3c8...00000000000000000000000000000000000000000000000000000000000001f4`
-  - Status: `executed`
-  - Exec tx: `0x280e2f68e1ea14bfe46fe6f8fa8e3fb4e986fb533b93edacff28fcf560d72fa8`
- [x] **[LLM]** Note prior register op (blocked by contract)
  - Operation: `tipop_ee86c0ef021a45cc1b082fd28a5fff70`
  - Wallet: `0x80189edb676d51b2fb2257b2ad38e018b20ca46e` (contract rejects `wallet == lesserWallet`)
- [x] **[LLM]** Generate Safe payload for:
  - `setHostActive`
  - Operation: `tipop_c0bdfe9c14c96d73a9febe0216aea2b0`
  - Domain: `fedipack.com`
  - Safe: `0xfE63333f303D4f7b2354f7E3eca752C812d65907`
  - To: `0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe`
  - Value: `0`
  - Data: `0xd5bc7c21d60f4586d7e89146cf0948252460732025fbac5ef478a5c91bafea4ecd17f3c8...0000000000000000000000000000000000000000000000000000000000000001`
  - Status: `executed` (reenabled)
  - Exec tx: `0x62c7a1e1d35d96e5add3808d6809cdb225aaaa9ba1b4c91f1dc5905419bce115`
  - Additional disable op: `tipop_571f858dbdd8648801ce993153c8cc26`
  - Disable tx: `0x4e3b2fa8e4d274947316bc96d0e183f56acefd0e575825610ea1cca153c73681`
  - Note: `updateHost` not yet exercised
- [x] **[LLM]** Generate Safe payload for:
  - `setTokenAllowed`
  - Operation: `tipop_f00d40ae26bce82822e8fc228ef90873`
  - Token: `0xfff9976782d46cc05630d1f6ebab18b2324d6b14` (Sepolia WETH)
  - Allowed: `true`
  - Status: `executed`
  - Exec tx: `0xe609a2242aff0510f663fefd58db0bf751525ce59ac3690d4a0317931f705c7a`
- [x] **[HUMAN]** Execute Safe tx (if testing on-chain)
- [x] **[LLM]** Verify on-chain state via RPC:
  - `hosts(hostId)` → active `true`, wallet `0x1e14865a53a994b01b9ccfef42669dc0bfe98805`, fee 500
  - `allowedTokens(token)` → `true` for `0xfff9976782d46cc05630d1f6ebab18b2324d6b14`

---

## **8) Trust API**

- [x] **[LLM]** `GET /.well-known/jwks.json`
  - 200 OK
- [x] **[LLM]** `GET /attestations` + `/attestations/{id}`
  - `/attestations` → 400 missing required params (expected)
  - `/attestations?...` → 404 (no attestation found)
  - `/attestations/{id}` (all-zero id) → 404
- [ ] **[LLM]** Instance auth flow:
  - Use instance API key to access instance-scoped endpoints

---

## **9) Workers & Queues**

- [ ] **[LLM]** Preview queue processing
- [ ] **[LLM]** Safety queue processing
- [ ] **[LLM]** Provision worker:
  - Ensure CodeBuild runner triggers on queue message

Evidence:
- CloudWatch logs for workers
- SQS DLQ empty

---

## **10) Portal & Payments**

- [ ] **[LLM+HUMAN]** Portal wallet login
  - **HUMAN** signs challenge
  - **LLM** submits
- [ ] **[LLM+HUMAN]** Operator approves portal user
  - **HUMAN** opens `/operator/approvals/users`
  - **LLM** confirms user appears as pending
  - **HUMAN** approves (or rejects) with optional note
  - **LLM** retries instance creation to confirm approval unblocks provisioning
- [ ] **[LLM]** `/api/v1/portal/me`
- [ ] **[LLM+HUMAN]** Stripe checkout (if enabled)
  - **HUMAN** completes payment
  - **LLM** verifies webhook + billing state

---

## **11) CSP & SPA Routing**

- [x] **[LLM]** Validate CSP headers from CloudFront
  - `default-src 'none'; ...; style-src 'self'; script-src 'self'; connect-src 'self'`
- [x] **[LLM]** SPA route loads correctly for deep link (non-file path)
  - `GET /account` → 200 `text/html`
- [x] **[LLM]** Ensure API routes are not rewritten to `/index.html`
  - `GET /api/v1/operators/me` → 401 `application/json`

---

## **12) Security & Negative Tests**

- [x] **[LLM]** Unauthed request to protected endpoint returns 401
  - `GET /api/v1/operators/me` → 401
- [x] **[LLM]** Invalid token returns 401
  - `GET /api/v1/operators/me` with bogus token → 401
- [ ] **[LLM]** Invalid WebAuthn origin rejected
- [ ] **[LLM]** Tip registry fails when `TIP_CONTRACT_ADDRESS` missing
- [ ] **[LLM]** Withdrawals pause works; tips pause works

---

## **13) Evidence Log**

Record all supporting evidence (logs, tx hashes, screenshots).

- Deployment:
  - Stack name:
  - CloudFront URL:
- Wallet auth:
  - Challenge IDs:
- Tip registry:
  - Safe tx hash (if executed):
  - Sepolia tx hash:
- Trust API:
  - JWKS response timestamp:
- Workers:
  - Log group names:

---

## **14) Exit Criteria**

- [ ] **[LLM]** All unit tests pass
- [ ] **[LLM]** CDK synth passes
- [ ] **[LLM+HUMAN]** Auth flows validated
- [ ] **[LLM+HUMAN]** Tip registry works end‑to‑end on Sepolia
- [ ] **[LLM]** CSP + SPA routing validated
- [ ] **[LLM]** No critical errors in CloudWatch logs

---

## **15) Handoff Notes**

If pausing handoff, record:
- Current step:
- Next required action:
- Who owns the next action (LLM vs HUMAN):
- Any blockers:
