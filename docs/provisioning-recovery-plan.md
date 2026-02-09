# Provisioning Re-Engineering Plan (Resilient + Recoverable)

## Goals
- Make managed provisioning **deterministic, recoverable, and auditable**.
- Eliminate hidden/manual dependencies by codifying all required IAM roles and permissions.
- Provide **operator recovery tools** for existing slugs and partially created accounts.
- Ensure the UI reflects **true job state** (no “running forever”).

## Non-Goals
- No bypasses that skip required org permissions.
- No manual one-off fixes that cannot be reproduced in code.

---

## Contract (What must exist)

### Accounts
- **Control plane account** (lesser-host)
- **Org management/delegated admin account** (AWS Organizations)
- **Instance accounts** (one per slug)

### Required IAM Role (Org account)
**Role:** `lesser-host-org-vending`

**Trust policy:** allows control plane account to assume it.

**Permissions policy (minimum):**
- `organizations:CreateAccount`
- `organizations:DescribeCreateAccountStatus`
- `organizations:ListAccounts`
- `organizations:ListParents`
- `organizations:MoveAccount`

This role **must be created/managed by code** (org-bootstrap stack).

---

## Failure Modes & Recovery (First-class)

### 1) Account creation returns `EMAIL_ALREADY_EXISTS`
**Expected behavior:**
- Use `ListAccounts` (via org-vending role) to locate the existing account.
- Validate:
  - account name matches expected slug + prefix
  - account status is ACTIVE
- Resume provisioning using that account.

**If `ListAccounts` is denied:**
- Fail immediately with `org_permissions_missing` (no retries).
- Surface in UI and operator console.

### 2) Provisioning stuck with no progress
**Expected behavior:**
- Mark as **stalled** when no step advance for N minutes.
- UI shows “stalled” with last error and direct recovery action.

### 3) Partial/abandoned accounts
**Expected behavior:**
- Operator can **adopt existing account** for a slug.
- Requires validation via org-vending role (no bypass).
- Continue from account move → assume role → DNS → deploy.

---

## Implementation Plan

### A) Org-Bootstrap Stack (new CDK app in this repo)
- Creates/updates `lesser-host-org-vending` role in org account.
- Outputs role ARN for `managedOrgVendingRoleArn`.
- Deployed once per org account.

### B) Fail-Fast Permission Errors
- On `AccessDenied` for org calls, **fail job immediately**.
- Set:
  - `ProvisionJobStatus=error`
  - `Instance.ProvisionStatus=error`
  - `ErrorCode=org_permissions_missing`

### C) Operator Recovery Workflow
- API + UI:
  - “Adopt existing account” action
  - Input: `account_id` (if not already known)
  - Validate via org-vending role
  - Requeue job starting from account move/assume role

### D) UI Improvements
- Show **current job state** and **last failed job**.
- Show **stalled** indicator and recovery CTA.
- Ensure instance page reflects failures immediately.

### E) Tests + Rubric
- Unit tests for:
  - permission denied → fail fast
  - adopt existing account → validation + resume
- Run `gov-verify-rubric.sh` and fix any regressions.

---

## Rollout Plan
1. Deploy org-bootstrap stack to org account.
2. Deploy control plane updates (fail-fast + recovery API).
3. Deploy UI updates.
4. Run recovery on existing slugs (e.g. simulacrum).

---

## Progress Checklist
- [x] Org-bootstrap stack added
- [x] Fail-fast permission errors
- [x] Operator recovery workflow
- [x] UI stalled/failure visibility
- [ ] Tests + rubric pass
- [ ] Recovery executed for existing slug(s)
