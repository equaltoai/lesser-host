<!--
 @component
 Account — M17 slim identity and session view for /portal/account.

 Project 42 M17 re-skin: replaces the M1 flat profile display with a
 slim two-panel DefinitionList layout (Identity + Session) matching the
 design reference at docs/design/web-ui-rework-2026-05-24/project/src/
 portal-pages-2.jsx PortalAccount (lines ~471-500). Preserves existing
 data-layer behavior, WebAuthn passkey management for operator/admin
 accounts, and single-session sign-out.

 Posture invariants preserved:
   - Strict-CSP safe: no inline style attributes or inline scripts; no
     third-party origins.
   - Multi-tenant isolation: consumes only self-scoped portal/operator
     me endpoints; no cross-tenant reads.
   - Trust-API instance-auth untouched (account page does not handle
     instance keys).
   - No auth/session backend changes; UI only.
   - Wallet/session identifiers masked/truncated.
   - No PageFrame wrapper (PortalShell provides one); fixes double-
     nesting on both /portal/account and legacy /account routes.

 Design deviations (deliberate):
   - "Rotate token" is rendered as a disabled button because no session-
     token rotation endpoint exists in the current backend. Honest help
     text notes this.
   - "Sign out" is single-session only (the existing backend /api/v1/auth/
     logout terminates only the current session). The button label is
     "Sign out" (not "Sign out all sessions") to avoid misleading users.
   - IP field renders "—" (unavailable) because session metadata does
     not include client IP in the current session store.
   - Passkey management is preserved for operator/admin accounts as a
     third panel below the main two panels because removing it would
     break existing supported WebAuthn management behavior.

 Source: issue equaltoai/lesser-host#551
 @license AGPL-3.0-only
 -->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { OperatorMeResponse } from 'src/lib/api/operators';
	import { getOperatorMe } from 'src/lib/api/operators';
	import type { PortalMeResponse } from 'src/lib/api/portal';
	import { getPortalMe } from 'src/lib/api/portal';
	import type { WebAuthnCredentialSummary } from 'src/lib/api/webauthn';
	import {
		webAuthnCredentials,
		webAuthnDeleteCredential,
		webAuthnRegisterBegin,
		webAuthnRegisterFinish,
		webAuthnUpdateCredential,
	} from 'src/lib/api/webauthn';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { serializeCredentialCreation, toPublicKeyCreationOptions } from 'src/lib/webauthn/client';
	import { Eyebrow } from 'src/lib/components/primitives';
	import { Panel } from 'src/lib/shell';
	import {
		Alert,
		Button,
		DefinitionItem,
		DefinitionList,
		Heading,
		Spinner,
		Text,
		TextField,
	} from 'src/lib/ui';

	// ── Profile type ───────────────────────────────────────────────────

	type Profile = {
		username: string;
		role: string;
		displayName?: string;
		email?: string;
	};

	// ── State ──────────────────────────────────────────────────────────

	let profileLoading = $state(false);
	let profileError = $state<string | null>(null);
	let profile = $state<Profile | null>(null);

	// Passkey management (operator/admin only; preserved from M1)
	let passkeysLoading = $state(false);
	let passkeysError = $state<string | null>(null);
	let passkeys = $state<WebAuthnCredentialSummary[]>([]);

	let newPasskeyName = $state('');
	let registerLoading = $state(false);
	let registerError = $state<string | null>(null);

	let editId = $state<string | null>(null);
	let editName = $state('');
	let editLoading = $state(false);
	let editError = $state<string | null>(null);

	let signingOut = $state(false);

	// ── Helpers ────────────────────────────────────────────────────────

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function isOperatorRole(role: string): boolean {
		return role === 'admin' || role === 'operator';
	}

	/** Mask a wallet address to first 6 + … + last 4 characters. */
	function maskWallet(addr: string | undefined): string {
		const a = (addr ?? '').trim();
		if (!a) return '';
		if (a.length <= 14) return a;
		return `${a.slice(0, 6)}…${a.slice(-4)}`;
	}

	/** Format an ISO expiry string for human readability. */
	function formatExpiry(expiresAt: string | undefined): string {
		if (!expiresAt) return '—';
		try {
			const d = new Date(expiresAt);
			if (!Number.isFinite(d.getTime())) return '—';
			return d.toLocaleString();
		} catch {
			return '—';
		}
	}

	// ── Profile loading ────────────────────────────────────────────────

	async function loadProfile() {
		profileError = null;
		profile = null;

		const current = $session;
		if (!current) {
			navigate('/login');
			return;
		}

		profileLoading = true;
		try {
			if (isOperatorRole(current.role)) {
				const me: OperatorMeResponse = await getOperatorMe(current.token);
				profile = {
					username: me.username,
					role: me.role,
					displayName: me.display_name || undefined,
				};
			} else {
				const me: PortalMeResponse = await getPortalMe(current.token);
				profile = {
					username: me.username,
					role: me.role,
					displayName: me.display_name || undefined,
					email: me.email || undefined,
				};
			}
		} catch (err) {
			profileError = formatError(err);
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
			}
		} finally {
			profileLoading = false;
		}
	}

	// ── Passkey management (preserved from M1 for operator/admin) ──────

	async function loadPasskeys() {
		passkeysError = null;
		passkeys = [];

		const current = $session;
		if (!current) return;
		if (!isOperatorRole(current.role)) return;

		passkeysLoading = true;
		try {
			const res = await webAuthnCredentials(current.token);
			passkeys = res.credentials;
		} catch (err) {
			passkeysError = formatError(err);
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
			}
		} finally {
			passkeysLoading = false;
		}
	}

	async function registerPasskey() {
		registerError = null;

		const current = $session;
		if (!current) {
			registerError = 'Sign in first.';
			return;
		}
		if (!isOperatorRole(current.role)) {
			registerError = 'Operator session required.';
			return;
		}
		if (!window.PublicKeyCredential || !navigator.credentials) {
			registerError = 'Passkeys are not supported in this browser.';
			return;
		}

		registerLoading = true;
		try {
			const begin = await webAuthnRegisterBegin(current.token);
			const options = toPublicKeyCreationOptions(begin.publicKey);
			const credential = (await navigator.credentials.create({
				publicKey: options,
			})) as Credential | null;

			if (!credential) {
				registerError = 'No credential returned.';
				return;
			}
			if (!(credential instanceof PublicKeyCredential)) {
				registerError = 'Unexpected credential type.';
				return;
			}

			const response = serializeCredentialCreation(credential);
			await webAuthnRegisterFinish(current.token, {
				challenge: begin.challenge,
				response,
				credential_name: newPasskeyName.trim(),
			});
			newPasskeyName = '';
			await loadPasskeys();
		} catch (err) {
			registerError = formatError(err);
		} finally {
			registerLoading = false;
		}
	}

	function startRename(cred: WebAuthnCredentialSummary) {
		editError = null;
		editId = cred.id;
		editName = cred.name;
	}

	function cancelRename() {
		editError = null;
		editId = null;
		editName = '';
	}

	async function saveRename() {
		editError = null;

		const current = $session;
		if (!current) {
			editError = 'Sign in first.';
			return;
		}
		if (!isOperatorRole(current.role)) {
			editError = 'Operator session required.';
			return;
		}
		if (!editId) {
			editError = 'No credential selected.';
			return;
		}
		const name = editName.trim();
		if (!name) {
			editError = 'Name is required.';
			return;
		}

		editLoading = true;
		try {
			await webAuthnUpdateCredential(current.token, editId, name);
			await loadPasskeys();
			cancelRename();
		} catch (err) {
			editError = formatError(err);
		} finally {
			editLoading = false;
		}
	}

	async function deletePasskey(cred: WebAuthnCredentialSummary) {
		passkeysError = null;

		const current = $session;
		if (!current) {
			passkeysError = 'Sign in first.';
			return;
		}
		if (!isOperatorRole(current.role)) {
			passkeysError = 'Operator session required.';
			return;
		}

		if (!confirm(`Delete passkey "${cred.name}"?`)) return;

		passkeysLoading = true;
		try {
			await webAuthnDeleteCredential(current.token, cred.id);
			await loadPasskeys();
		} catch (err) {
			passkeysError = formatError(err);
		} finally {
			passkeysLoading = false;
		}
	}

	// ── Sign out (single session) ──────────────────────────────────────

	async function handleSignOut() {
		signingOut = true;
		try {
			await logout();
			navigate('/login');
		} catch {
			// logout() already clears session locally
			navigate('/login');
		} finally {
			signingOut = false;
		}
	}

	// ── Lifecycle ──────────────────────────────────────────────────────

	onMount(() => {
		if (!$session) {
			navigate('/login');
			return;
		}
		void loadProfile();
		void loadPasskeys();
	});
</script>

<!-- ═══ Page header ════════════════════════════════════════════════════ -->

<div class="account__header">
	<Eyebrow>Settings</Eyebrow>
	<Heading level={1}>Account</Heading>
	<Text size="sm" color="secondary">Identity, sessions, and connected wallets.</Text>
</div>

<!-- ═══ Content ════════════════════════════════════════════════════════ -->

{#if !$session}
	<Alert variant="warning" title="Signed out">
		<Text size="sm">Sign in to manage account settings.</Text>
	</Alert>
{:else if profileLoading}
	<div class="account__loading">
		<Spinner size="md" />
		<Text>Loading…</Text>
	</div>
{:else if profileError}
	<Alert variant="error" title="Account error">{profileError}</Alert>
{:else if profile}
	<div class="account__panels">
		<!-- ── Identity / Profile panel ─────────────────────────────── -->
		<Panel title="Profile" headerLevel={2}>
			<Eyebrow>Identity</Eyebrow>
			<DefinitionList>
				<DefinitionItem label="Username" monospace>
					<Text size="sm">{profile.username}</Text>
				</DefinitionItem>
				<DefinitionItem label="Display name">
					<Text size="sm">{profile.displayName || '—'}</Text>
				</DefinitionItem>
				<DefinitionItem label="Email" monospace>
					<Text size="sm">{profile.email || '—'}</Text>
				</DefinitionItem>
				<DefinitionItem label="Role" monospace>
					<Text size="sm">{profile.role}</Text>
				</DefinitionItem>
			</DefinitionList>
		</Panel>

		<!-- ── Session panel ────────────────────────────────────────── -->
		<Panel title="Current session" headerLevel={2}>
			<Eyebrow>Session</Eyebrow>
			<DefinitionList>
				<DefinitionItem label="Method" monospace>
					<Text size="sm">{$session.method || '—'}</Text>
				</DefinitionItem>
				<DefinitionItem label="Wallet" monospace>
					<Text size="sm">
						{@const wallet = $session.walletAddress}
						{wallet ? maskWallet(wallet) : '—'}
					</Text>
				</DefinitionItem>
				<DefinitionItem label="Token expires" monospace>
					<Text size="sm">{formatExpiry($session.expiresAt)}</Text>
				</DefinitionItem>
				<DefinitionItem label="IP">
					<Text size="sm" color="secondary">—</Text>
				</DefinitionItem>
			</DefinitionList>

			<div class="account__actions">
				<Button variant="outline" size="sm" disabled
					title="Session-token rotation is not yet available"
				>
					Rotate token
				</Button>
				<Button variant="ghost" size="sm"
					onclick={() => void handleSignOut()}
					disabled={signingOut}
				>
					Sign out
				</Button>
			</div>

			<div class="account__actions-help">
				<Text size="sm" color="secondary">
					Session-token rotation is not yet supported. Sign out signs out the current
					session only.
				</Text>
			</div>
		</Panel>

		<!-- ── Passkey management (operator/admin only, preserved) ───── -->
		{#if isOperatorRole(profile.role)}
			<Panel title="Passkeys" headerLevel={2}>
				<Text size="sm" color="secondary">
					Register passkeys (WebAuthn) for operator/admin accounts. Challenges expire in ~5 minutes.
				</Text>

				<div class="account__form">
					<TextField label="New passkey name (optional)" bind:value={newPasskeyName} />
				</div>

				<div class="account__row">
					<Button variant="solid" onclick={() => void registerPasskey()} disabled={registerLoading}>
						Add passkey
					</Button>
				</div>

				{#if registerLoading}
					<div class="account__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Waiting for passkey…</Text>
					</div>
				{/if}

				{#if registerError}
					<Alert variant="error" title="Passkey registration failed">{registerError}</Alert>
				{/if}

				{#if passkeysError}
					<Alert variant="error" title="Failed to load passkeys">{passkeysError}</Alert>
				{/if}

				{#if passkeysLoading}
					<div class="account__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Loading passkeys…</Text>
					</div>
				{:else if passkeys.length === 0}
					<Alert variant="info" title="No passkeys">
						<Text size="sm">No passkeys registered yet.</Text>
					</Alert>
				{:else}
					<ul class="account__passkey-list">
						{#each passkeys as cred (cred.id)}
							<li class="account__passkey-item">
								<div class="account__passkey-meta">
									<Text size="sm" weight="medium">{cred.name}</Text>
									<Text size="sm" color="secondary">
										Created: <span class="account__mono">{cred.created_at}</span>
									</Text>
									<Text size="sm" color="secondary">
										Last used: <span class="account__mono">{cred.last_used_at}</span>
									</Text>
									<Text size="sm" color="secondary">
										ID: <span class="account__mono">{cred.id}</span>
									</Text>
								</div>

								<div class="account__passkey-actions">
									<Button variant="outline" onclick={() => startRename(cred)} disabled={editLoading}>
										Rename
									</Button>
									<Button
										variant="outline"
										onclick={() => void deletePasskey(cred)}
										disabled={passkeysLoading}
									>
										Delete
									</Button>
								</div>
							</li>
						{/each}
					</ul>
				{/if}
			</Panel>

			{#if editId}
				<Panel title="Rename passkey" headerLevel={2}>
					<div class="account__form">
						<TextField label="Passkey name" bind:value={editName} required />
					</div>
					<div class="account__row">
						<Button variant="solid" onclick={() => void saveRename()} disabled={editLoading}>
							Save
						</Button>
						<Button variant="outline" onclick={() => cancelRename()} disabled={editLoading}>
							Cancel
						</Button>
					</div>
					{#if editLoading}
						<div class="account__loading-inline">
							<Spinner size="sm" />
							<Text size="sm">Saving…</Text>
						</div>
					{/if}
					{#if editError}
						<Alert variant="error" title="Rename failed">{editError}</Alert>
					{/if}
				</Panel>
			{/if}
		{/if}
	</div>
{/if}

<!-- ═══ Styles (CSP-safe: no inline style attributes) ══════════════════ -->

<style>
	.account__header {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1);
		margin-bottom: var(--ds-space-6);
	}

	.account__loading {
		display: flex;
		gap: var(--ds-space-3);
		align-items: center;
	}

	.account__loading-inline {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		margin-top: var(--ds-space-3);
	}

	.account__panels {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-6);
	}

	.account__actions {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		margin-top: var(--ds-space-4);
	}

	.account__actions-help {
		margin-top: var(--ds-space-2);
	}

	.account__row {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		flex-wrap: wrap;
		margin-top: var(--ds-space-4);
	}

	.account__form {
		display: grid;
		gap: var(--ds-space-3);
		margin-top: var(--ds-space-4);
	}

	.account__passkey-list {
		list-style: none;
		padding: 0;
		margin: var(--ds-space-4) 0 0 0;
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3);
	}

	.account__passkey-item {
		display: flex;
		gap: var(--ds-space-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
		padding: var(--ds-space-3);
		border: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
		border-radius: var(--ds-radius-md, var(--gr-radius-md));
		background: var(--ds-bg-raised, var(--gr-color-surface));
	}

	.account__passkey-meta {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-2);
		min-width: min(520px, 100%);
	}

	.account__passkey-actions {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.account__mono {
		font-family: var(--ds-font-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
	}
</style>
