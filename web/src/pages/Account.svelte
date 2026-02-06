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
	import { navigate } from 'src/lib/router';
	import { logout } from 'src/lib/auth/logout';
	import { session } from 'src/lib/session';
	import { serializeCredentialCreation, toPublicKeyCreationOptions } from 'src/lib/webauthn/client';
	import { Alert, Button, Card, Container, Heading, Spinner, Text, TextField } from 'src/lib/ui';

	type Profile = {
		username: string;
		role: string;
		displayName?: string;
		email?: string;
	};

	let profileLoading = $state(false);
	let profileError = $state<string | null>(null);
	let profile = $state<Profile | null>(null);

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

	async function loadPasskeys() {
		passkeysError = null;
		passkeys = [];

		const current = $session;
		if (!current) {
			return;
		}
		if (!isOperatorRole(current.role)) {
			return;
		}

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

		if (!confirm(`Delete passkey "${cred.name}"?`)) {
			return;
		}

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

	onMount(() => {
		if (!$session) {
			navigate('/login');
			return;
		}
		void loadProfile();
		void loadPasskeys();
	});

	async function handleLogout() {
		await logout();
		navigate('/login');
	}
</script>

<Container size="lg" gutter="lg">
	<div class="account">
		<header class="account__header">
			<div class="account__title">
				<Heading level={1}>Account</Heading>
				<Text color="secondary">Settings and passkeys.</Text>
			</div>
			<div class="account__actions">
				<Button variant="ghost" onclick={() => navigate('/portal')}>Portal</Button>
				<Button variant="ghost" onclick={() => navigate('/operator')}>Operator</Button>
				<Button variant="ghost" onclick={() => navigate('/login')}>Sign in</Button>
				<Button
					variant="ghost"
					onclick={() => void handleLogout()}
				>
					Logout
				</Button>
			</div>
		</header>

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
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={2} size="xl">Profile</Heading>
				{/snippet}

				<div class="account__profile">
					<Text size="sm">
						Username: <span class="account__mono">{profile.username}</span>
					</Text>
					<Text size="sm">
						Role: <span class="account__mono">{profile.role}</span>
					</Text>
					<Text size="sm">
						Display name: <span class="account__mono">{profile.displayName || '—'}</span>
					</Text>
					<Text size="sm">
						Email: <span class="account__mono">{profile.email || '—'}</span>
					</Text>
					<Text size="sm">
						Method: <span class="account__mono">{$session.method || '—'}</span>
					</Text>
					<Text size="sm">
						Linked wallet: <span class="account__mono">{$session.walletAddress || '—'}</span>
					</Text>
				</div>

				<div class="account__row">
					<Button variant="outline" onclick={() => void loadProfile()} disabled={profileLoading}>
						Refresh profile
					</Button>
				</div>
			</Card>

			{#if isOperatorRole(profile.role)}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<Heading level={2} size="xl">Passkeys</Heading>
					{/snippet}

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
						<Button variant="outline" onclick={() => void loadPasskeys()} disabled={passkeysLoading}>
							Refresh passkeys
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
						<div class="account__passkey-list">
							{#each passkeys as cred (cred.id)}
								<Card variant="outlined" padding="md">
									<div class="account__passkey">
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
											<Button variant="outline" onclick={() => void deletePasskey(cred)} disabled={passkeysLoading}>
												Delete
											</Button>
										</div>
									</div>
								</Card>
							{/each}
						</div>
					{/if}
				</Card>

				{#if editId}
					<Card variant="outlined" padding="lg">
						{#snippet header()}
							<Heading level={2} size="xl">Rename passkey</Heading>
						{/snippet}

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
					</Card>
				{/if}
			{/if}
		{/if}
	</div>
</Container>

<style>
	.account {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.account__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.account__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.account__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.account__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.account__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.account__profile {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.account__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-4);
	}

	.account__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.account__passkey-list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.account__passkey {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.account__passkey-meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		min-width: min(520px, 100%);
	}

	.account__passkey-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.account__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
