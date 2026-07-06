<script lang="ts">
	import { onMount } from 'svelte';

	import type { SetupStatusResponse, WalletChallengeResponse } from 'src/lib/api/controlPlane';
	import {
		getSetupStatus,
		setupBootstrapChallenge,
		setupBootstrapVerify,
		setupCreateAdmin,
		setupFinalize,
		walletChallenge,
		walletLogin,
	} from 'src/lib/api/controlPlane';
	import type { ApiError } from 'src/lib/api/http';
	import { linkProps, navigate } from 'src/lib/router';
	import { getChainId, getEthereumProvider, personalSign, requestAccounts } from 'src/lib/wallet/ethereum';
	import type { Eip1193Provider } from 'src/lib/wallet/ethereum';
	import {
		webAuthnCredentials,
		webAuthnRegisterBegin,
		webAuthnRegisterFinish,
	} from 'src/lib/api/webauthn';
	import { serializeCredentialCreation, toPublicKeyCreationOptions } from 'src/lib/webauthn/client';
	import {
		Alert,
		Button,
		Card,
		Checkbox,
		Container,
		DefinitionItem,
		DefinitionList,
		Heading,
		Link,
		Spinner,
		StepIndicator,
		Text,
		TextField,
	} from 'src/lib/ui';

	const SETUP_SESSION_KEY = 'lesser-host:setupSessionToken';

	let statusLoading = $state(false);
	let statusError = $state<string | null>(null);
	let status = $state<SetupStatusResponse | null>(null);

	let provider = $state<Eip1193Provider | null>(null);
	let walletAddress = $state<string>('');
	let walletChainId = $state<number>(Number.parseInt(import.meta.env.VITE_WALLET_CHAIN_ID || '1', 10) || 1);
	let walletError = $state<string | null>(null);
	let walletNotice = $state<string | null>(null);

	let setupSessionToken = $state<string>(sessionStorage.getItem(SETUP_SESSION_KEY) || '');

	let bootstrapLoading = $state(false);
	let bootstrapError = $state<string | null>(null);
	let bootstrapChallenge = $state<WalletChallengeResponse | null>(null);

	let adminUsername = $state<string>('');
	let adminDisplayName = $state<string>('');
	let adminLoading = $state(false);
	let adminError = $state<string | null>(null);
	let adminChallenge = $state<WalletChallengeResponse | null>(null);
	let adminSessionToken = $state('');
	let adminSessionUsername = $state('');

	let passkeyName = $state('Primary admin setup passkey');
	let passkeyLoading = $state(false);
	let passkeyError = $state<string | null>(null);
	let passkeyRegistered = $state(false);

	let finalizeLoading = $state(false);
	let finalizeError = $state<string | null>(null);
	let finalizeAckLock = $state(false);
	let finalizeAckBackup = $state(false);
	let finalizeConfirm = $state('');

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function normalizeAddress(addr: string | undefined | null): string {
		return (addr || '').trim().toLowerCase();
	}

	function connectedBootstrapWalletMessage(): string | null {
		const bootstrap = normalizeAddress(status?.bootstrap_wallet_address);
		const connected = normalizeAddress(walletAddress);
		if (!bootstrap || !connected || bootstrap !== connected) return null;
		return 'The bootstrap wallet is one-time setup authority and cannot be the primary admin wallet. Step 2 needs the real primary admin credential.';
	}

	function clearConnectedWallet(notice: string | null = null) {
		provider = null;
		walletAddress = '';
		walletError = null;
		walletNotice = notice;
		bootstrapChallenge = null;
		adminChallenge = null;
		adminSessionToken = '';
		adminSessionUsername = '';
	}

	function clearAdminSession() {
		adminSessionToken = '';
		adminSessionUsername = '';
		adminChallenge = null;
	}

	async function refreshStatus() {
		statusLoading = true;
		statusError = null;
		try {
			status = await getSetupStatus();
			if (status && !status.locked) {
				setupSessionToken = '';
				sessionStorage.removeItem(SETUP_SESSION_KEY);
			}
		} catch (err) {
			statusError = formatError(err);
		} finally {
			statusLoading = false;
		}
	}

	async function connectWallet() {
		walletError = null;
		walletNotice = null;

		const p = getEthereumProvider();
		if (!p) {
			walletError = 'No EIP-1193 wallet found (window.ethereum missing). Install MetaMask or another wallet.';
			return;
		}

		try {
			provider = p;
			const accounts = await requestAccounts(p);
			const nextAddress = accounts[0] ? String(accounts[0]) : '';
			if (normalizeAddress(nextAddress) !== normalizeAddress(walletAddress)) {
				clearAdminSession();
			}
			walletAddress = nextAddress;
			walletChainId = await getChainId(p);
		} catch (err) {
			walletError = formatError(err);
		}
	}

	async function beginBootstrap() {
		bootstrapError = null;
		bootstrapChallenge = null;

		if (!status?.locked) {
			bootstrapError = 'Control plane is already active.';
			return;
		}
		if (!status.bootstrap_wallet_address_set) {
			bootstrapError = 'Bootstrap wallet is not configured.';
			return;
		}
		if (!walletAddress) {
			bootstrapError = 'Connect the bootstrap wallet first.';
			return;
		}

		bootstrapLoading = true;
		try {
			bootstrapChallenge = await setupBootstrapChallenge(walletAddress, walletChainId);
		} catch (err) {
			bootstrapError = formatError(err);
		} finally {
			bootstrapLoading = false;
		}
	}

	async function completeBootstrap() {
		bootstrapError = null;

		if (!provider) {
			bootstrapError = 'Connect the bootstrap wallet first.';
			return;
		}
		if (!bootstrapChallenge) {
			bootstrapError = 'Create a bootstrap challenge first.';
			return;
		}
		if (!walletAddress) {
			bootstrapError = 'Wallet address missing.';
			return;
		}

		bootstrapLoading = true;
		try {
			const signature = await personalSign(provider, bootstrapChallenge.message, walletAddress);
			const verified = await setupBootstrapVerify({
				challengeId: bootstrapChallenge.id,
				address: walletAddress,
				signature,
				message: bootstrapChallenge.message,
			});
			setupSessionToken = verified.token;
			sessionStorage.setItem(SETUP_SESSION_KEY, verified.token);
			clearConnectedWallet(
				'Bootstrap wallet cleared. Reconnect the real primary admin credential before Step 2.',
			);
		} catch (err) {
			bootstrapError = formatError(err);
		} finally {
			bootstrapLoading = false;
			await refreshStatus();
		}
	}

	async function beginAdminChallenge() {
		adminError = null;
		adminChallenge = null;

		if (!setupSessionToken) {
			adminError = 'Bootstrap session is missing. Complete Step 1 first.';
			return;
		}
		if (!walletAddress) {
			adminError = 'Connect the admin wallet first.';
			return;
		}
		const bootstrapMessage = connectedBootstrapWalletMessage();
		if (bootstrapMessage) {
			adminError = bootstrapMessage;
			return;
		}
		const username = adminUsername.trim();
		if (!username) {
			adminError = 'Username is required.';
			return;
		}

		adminLoading = true;
		try {
			adminChallenge = await walletChallenge({ username, address: walletAddress, chainId: walletChainId });
		} catch (err) {
			adminError = formatError(err);
		} finally {
			adminLoading = false;
		}
	}

	async function createAdmin() {
		adminError = null;

		if (!setupSessionToken) {
			adminError = 'Bootstrap session is missing. Complete Step 1 first.';
			return;
		}
		if (!provider) {
			adminError = 'Connect the admin wallet first.';
			return;
		}
		if (!adminChallenge) {
			adminError = 'Create an admin wallet challenge first.';
			return;
		}
		if (!walletAddress) {
			adminError = 'Wallet address missing.';
			return;
		}
		const bootstrapMessage = connectedBootstrapWalletMessage();
		if (bootstrapMessage) {
			adminError = bootstrapMessage;
			return;
		}
		if (normalizeAddress(adminChallenge.address) !== normalizeAddress(walletAddress)) {
			adminError = 'Connected wallet changed after the admin challenge was created. Create a new challenge.';
			adminChallenge = null;
			return;
		}

		const username = adminUsername.trim();
		if (!username) {
			adminError = 'Username is required.';
			return;
		}

		adminLoading = true;
		try {
			const signature = await personalSign(provider, adminChallenge.message, walletAddress);
			await setupCreateAdmin(setupSessionToken, {
				username,
				displayName: adminDisplayName.trim() || undefined,
				wallet: {
					challengeId: adminChallenge.id,
					address: walletAddress,
					signature,
					message: adminChallenge.message,
				},
			});
			adminChallenge = null;
			clearAdminSession();
			passkeyRegistered = false;
		} catch (err) {
			adminError = formatError(err);
		} finally {
			adminLoading = false;
			await refreshStatus();
		}
	}

	async function ensureAdminSessionToken(): Promise<string> {
		if (!status?.primary_admin_set) {
			throw new Error('Primary admin is not configured yet.');
		}
		const username = (status.primary_admin_username || adminUsername).trim();
		if (!username) {
			throw new Error('Primary admin username missing.');
		}
		if (!provider || !walletAddress) {
			throw new Error('Connect the primary admin wallet first.');
		}
		const bootstrapMessage = connectedBootstrapWalletMessage();
		if (bootstrapMessage) {
			throw new Error(bootstrapMessage);
		}
		if (adminSessionToken && adminSessionUsername === username) {
			return adminSessionToken;
		}

		const challenge = await walletChallenge({ username, address: walletAddress, chainId: walletChainId });
		const signature = await personalSign(provider, challenge.message, walletAddress);
		const session = await walletLogin({
			challengeId: challenge.id,
			address: walletAddress,
			signature,
			message: challenge.message,
		});
		if (session.username !== username || session.role !== 'admin') {
			throw new Error('Primary admin wallet did not return an admin session.');
		}

		adminSessionToken = session.token;
		adminSessionUsername = session.username;
		return session.token;
	}

	async function refreshPasskeyStatus() {
		passkeyError = null;

		passkeyLoading = true;
		try {
			const token = await ensureAdminSessionToken();
			const res = await webAuthnCredentials(token);
			passkeyRegistered = res.credentials.length > 0;
			if (!passkeyRegistered) {
				passkeyError = 'No primary admin passkey is registered yet.';
			}
		} catch (err) {
			passkeyError = formatError(err);
			adminSessionToken = '';
			adminSessionUsername = '';
		} finally {
			passkeyLoading = false;
		}
	}

	async function registerPasskey() {
		passkeyError = null;

		if (!window.PublicKeyCredential || !navigator.credentials) {
			passkeyError = 'Passkeys are not supported in this browser.';
			return;
		}

		passkeyLoading = true;
		try {
			const token = await ensureAdminSessionToken();
			const begin = await webAuthnRegisterBegin(token);
			const options = toPublicKeyCreationOptions(begin.publicKey);
			const credential = (await navigator.credentials.create({
				publicKey: options,
			})) as Credential | null;

			if (!credential) {
				passkeyError = 'No credential returned.';
				return;
			}
			if (!(credential instanceof PublicKeyCredential)) {
				passkeyError = 'Unexpected credential type.';
				return;
			}

			const response = serializeCredentialCreation(credential);
			await webAuthnRegisterFinish(token, {
				challenge: begin.challenge,
				response,
				credential_name: passkeyName.trim() || 'Primary admin setup passkey',
			});
			passkeyRegistered = true;
		} catch (err) {
			passkeyError = formatError(err);
			adminSessionToken = '';
			adminSessionUsername = '';
		} finally {
			passkeyLoading = false;
		}
	}

	async function finalizeSetup() {
		finalizeError = null;

		if (!status?.locked) {
			finalizeError = 'Control plane is already active.';
			return;
		}
		if (!status.primary_admin_set) {
			finalizeError = 'Primary admin is not configured yet.';
			return;
		}
		if (!passkeyRegistered) {
			finalizeError = 'Register the primary admin passkey in Step 3 before finalizing.';
			return;
		}
		if (!provider || !walletAddress) {
			finalizeError = 'Connect the primary admin wallet first.';
			return;
		}
		const bootstrapMessage = connectedBootstrapWalletMessage();
		if (bootstrapMessage) {
			finalizeError = bootstrapMessage;
			return;
		}
		if (!finalizeAckLock || !finalizeAckBackup || finalizeConfirm.trim().toUpperCase() !== 'FINALIZE') {
			finalizeError = 'Confirm the warnings to finalize.';
			return;
		}

		const username = (status.primary_admin_username || adminUsername).trim();
		if (!username) {
			finalizeError = 'Primary admin username missing.';
			return;
		}

		finalizeLoading = true;
		try {
			const token = await ensureAdminSessionToken();
			await setupFinalize(token);
			setupSessionToken = '';
			sessionStorage.removeItem(SETUP_SESSION_KEY);
		} catch (err) {
			finalizeError = formatError(err);
			adminSessionToken = '';
			adminSessionUsername = '';
		} finally {
			finalizeLoading = false;
			await refreshStatus();
		}
	}

	const step1Complete = $derived(Boolean(setupSessionToken));
	const step2Complete = $derived(Boolean(status?.primary_admin_set));
	const step3Complete = $derived(passkeyRegistered);
	const step4Complete = $derived(Boolean(status && !status.locked));

	const statusLocked = $derived.by(() => Boolean(status?.locked));
	const connectedWalletIsBootstrap = $derived.by(() => Boolean(connectedBootstrapWalletMessage()));

	const activeStep = $derived.by(() => {
		if (step4Complete) return 0;
		if (!step1Complete) return 1;
		if (!step2Complete) return 2;
		if (!step3Complete) return 3;
		return 4;
	});

	onMount(() => {
		void refreshStatus();
	});
</script>

<Container size="lg" gutter="lg">
	<div class="setup">
		<header class="setup__header">
			<div class="setup__title">
				<Heading level={1}>Setup</Heading>
				<Text color="secondary">Bootstrap the lesser.host control plane.</Text>
			</div>
			<div class="setup__header-actions">
				<Button variant="outline" onclick={() => void refreshStatus()} disabled={statusLoading}>Refresh</Button>
				<Link {...linkProps('/')} variant="ghost">Home</Link>
			</div>
		</header>

		{#if statusLoading}
			<div class="setup__loading">
				<Spinner size="md" />
				<Text>Loading setup status…</Text>
			</div>
		{:else if statusError}
			<Alert variant="error" title="Failed to load /setup/status">{statusError}</Alert>
		{:else if status}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<div class="setup__status-header">
						<Heading level={2} size="xl">Environment</Heading>
						{#if statusLocked}
							<Text color="warning" weight="medium">Locked</Text>
						{:else}
							<Text color="success" weight="medium">Active</Text>
						{/if}
					</div>
				{/snippet}

				<DefinitionList>
					<DefinitionItem label="Stage" monospace>{status.stage}</DefinitionItem>
					<DefinitionItem label="Control plane state" monospace>{status.control_plane_state}</DefinitionItem>
					<DefinitionItem label="Bootstrapped at" monospace>{status.bootstrapped_at || '—'}</DefinitionItem>
					<DefinitionItem label="Bootstrap wallet" monospace>
						{status.bootstrap_wallet_address || '—'}
					</DefinitionItem>
					<DefinitionItem label="Primary admin" monospace>
						{status.primary_admin_username || '—'}
					</DefinitionItem>
				</DefinitionList>
			</Card>

			{#if !status.locked}
				<Alert variant="success" title="Setup complete">
					<Text size="sm">
						The control plane is active. Setup endpoints are read-only now. Proceed to sign in.
					</Text>
					<div class="setup__row">
						<Button variant="solid" onclick={() => navigate('/login')}>Sign in</Button>
						<Link {...linkProps('/')} variant="default">Home</Link>
					</div>
				</Alert>
			{:else if !status.bootstrap_wallet_address_set}
				<Alert variant="error" title="Bootstrap wallet not configured">
					<Text size="sm">
						This deployment is missing `BOOTSTRAP_WALLET_ADDRESS`. Configure it and redeploy, then retry.
					</Text>
				</Alert>
			{:else}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<Heading level={2} size="xl">Wallet</Heading>
					{/snippet}

					<div class="setup__wallet">
						<div class="setup__wallet-actions">
							<Button variant="solid" onclick={() => void connectWallet()}>
								{walletAddress ? 'Reconnect wallet' : 'Connect wallet'}
							</Button>
						</div>

						{#if walletError}
							<Alert variant="error" title="Wallet error">{walletError}</Alert>
						{/if}
						{#if walletNotice}
							<Alert variant="info" title="Wallet reconnect required">{walletNotice}</Alert>
						{/if}
						{#if connectedWalletIsBootstrap && step1Complete}
							<Alert variant="warning" title="Bootstrap wallet cannot be admin">
								<Text size="sm">
									The bootstrap wallet is one-time setup authority. Step 2 needs the real primary admin
									credential; reconnect that wallet before creating the admin.
								</Text>
							</Alert>
						{/if}

						<DefinitionList>
							<DefinitionItem label="Connected address" monospace>{walletAddress || '—'}</DefinitionItem>
							<DefinitionItem label="Chain ID" monospace>{String(walletChainId)}</DefinitionItem>
							<DefinitionItem label="Expected bootstrap address" monospace>
								{status.bootstrap_wallet_address || '—'}
							</DefinitionItem>
						</DefinitionList>
					</div>
				</Card>

				<div class="setup__steps">
					<div class="setup__step-indicators">
						<StepIndicator
							number={1}
							label="Bootstrap session"
							state={step1Complete ? 'completed' : activeStep === 1 ? 'active' : 'pending'}
						/>
						<StepIndicator
							number={2}
							label="Create admin"
							state={step2Complete ? 'completed' : activeStep === 2 ? 'active' : 'pending'}
						/>
						<StepIndicator
							number={3}
							label="Register passkey"
							state={step3Complete ? 'completed' : activeStep === 3 ? 'active' : 'pending'}
						/>
						<StepIndicator
							number={4}
							label="Finalize"
							state={step4Complete ? 'completed' : activeStep === 4 ? 'active' : 'pending'}
						/>
					</div>

					<Card variant="outlined" padding="lg">
						{#snippet header()}
							<Heading level={2} size="xl">Step 1 — Bootstrap session</Heading>
						{/snippet}

						<Text size="sm" color="secondary">
							This verifies the configured bootstrap wallet and creates a short-lived setup session. The
							bootstrap wallet is one-time setup authority only; it cannot become the primary admin wallet.
						</Text>

						<div class="setup__row">
							<Button
								variant="outline"
								onclick={() => void beginBootstrap()}
								disabled={bootstrapLoading || step1Complete}
							>
								Create challenge
							</Button>
							<Button
								variant="solid"
								onclick={() => void completeBootstrap()}
								disabled={bootstrapLoading || step1Complete || !bootstrapChallenge}
							>
								Sign & verify
							</Button>
						</div>

						{#if bootstrapError}
							<Alert variant="error" title="Bootstrap failed">{bootstrapError}</Alert>
						{/if}

						{#if step1Complete}
							<Alert variant="success" title="Setup session created">
								<Text size="sm">
									Setup session token stored in sessionStorage for this tab. Reconnect the real primary
									admin credential for Step 2.
								</Text>
							</Alert>
						{:else if bootstrapChallenge}
							<Alert variant="info" title="Signature required">
								<Text size="sm">Sign the exact message below with the bootstrap wallet.</Text>
							</Alert>
							<pre class="setup__message">{bootstrapChallenge.message}</pre>
						{/if}
					</Card>

					<Card variant="outlined" padding="lg">
						{#snippet header()}
							<Heading level={2} size="xl">Step 2 — Create primary admin</Heading>
						{/snippet}

						{#if status.primary_admin_set}
							<Alert variant="success" title="Primary admin already configured">
								<Text size="sm">Username: {status.primary_admin_username}</Text>
							</Alert>
						{:else}
							<Text size="sm" color="secondary">
								This creates the primary admin operator user and links the connected wallet. Use the real
								primary admin credential here, not the one-time bootstrap wallet.
							</Text>

							{#if connectedWalletIsBootstrap}
								<Alert variant="warning" title="Reconnect primary admin credential">
									<Text size="sm">
										The connected wallet is the bootstrap wallet. It is one-time setup authority and cannot
										be linked as the primary admin wallet.
									</Text>
								</Alert>
							{/if}

							<div class="setup__form">
								<TextField label="Username" bind:value={adminUsername} required />
								<TextField label="Display name (optional)" bind:value={adminDisplayName} />
							</div>

							<div class="setup__row">
								<Button
									variant="outline"
									onclick={() => void beginAdminChallenge()}
									disabled={adminLoading || !step1Complete || connectedWalletIsBootstrap}
								>
									Create challenge
								</Button>
								<Button
									variant="solid"
									onclick={() => void createAdmin()}
									disabled={adminLoading || !step1Complete || connectedWalletIsBootstrap || !adminChallenge}
								>
									Sign & create admin
								</Button>
							</div>

							{#if adminError}
								<Alert variant="error" title="Create admin failed">{adminError}</Alert>
							{/if}

							{#if adminChallenge}
								<Alert variant="info" title="Signature required">
									<Text size="sm">
										Sign the exact message below with the admin wallet (must match the wallet you want linked).
									</Text>
								</Alert>
								<pre class="setup__message">{adminChallenge.message}</pre>
							{/if}
						{/if}
					</Card>

					<Card variant="outlined" padding="lg">
						{#snippet header()}
							<Heading level={2} size="xl">Step 3 — Register primary admin passkey</Heading>
						{/snippet}

						{#if !status.primary_admin_set}
							<Alert variant="info" title="Create admin first">
								<Text size="sm">Create the primary admin in Step 2 before registering a passkey.</Text>
							</Alert>
						{:else}
							<Text size="sm" color="secondary">
								Register an explicit passkey for the primary admin before finalize. The existing WebAuthn
								APIs require signing in as the primary admin wallet, so keep the real admin wallet connected.
							</Text>

							{#if connectedWalletIsBootstrap}
								<Alert variant="warning" title="Reconnect primary admin credential">
									<Text size="sm">
										The bootstrap wallet is one-time setup authority. Reconnect the real primary admin
										credential before checking or registering passkeys.
									</Text>
								</Alert>
							{/if}

							<div class="setup__form">
								<TextField label="Passkey name" bind:value={passkeyName} />
							</div>

							<div class="setup__row">
								<Button
									variant="outline"
									onclick={() => void refreshPasskeyStatus()}
									disabled={passkeyLoading || !status.primary_admin_set || connectedWalletIsBootstrap}
								>
									Check passkeys
								</Button>
								<Button
									variant="solid"
									onclick={() => void registerPasskey()}
									disabled={passkeyLoading || !status.primary_admin_set || connectedWalletIsBootstrap}
								>
									Register passkey
								</Button>
							</div>

							{#if passkeyLoading}
								<div class="setup__loading">
									<Spinner size="sm" />
									<Text size="sm">Waiting for passkey…</Text>
								</div>
							{/if}

							{#if passkeyError}
								<Alert variant="error" title="Passkey setup failed">{passkeyError}</Alert>
							{/if}

							{#if passkeyRegistered}
								<Alert variant="success" title="Primary admin passkey ready">
									<Text size="sm">Finalize is now available for the primary admin.</Text>
								</Alert>
							{/if}
						{/if}
					</Card>

					<Card variant="outlined" padding="lg">
						{#snippet header()}
							<Heading level={2} size="xl">Step 4 — Finalize</Heading>
						{/snippet}

						<Text size="sm" color="secondary">
							Finalizing activates the control plane and locks bootstrap-only endpoints.
						</Text>

						<div class="setup__warnings">
							<label class="setup__checkbox">
								<Checkbox bind:checked={finalizeAckLock} />
								<span>I understand finalize is irreversible for this stage.</span>
							</label>
							<label class="setup__checkbox">
								<Checkbox bind:checked={finalizeAckBackup} />
								<span>I have access to the primary admin wallet and can sign again later.</span>
							</label>
							<TextField
								label="Type FINALIZE to confirm"
								bind:value={finalizeConfirm}
								placeholder="FINALIZE"
							/>
						</div>

						<div class="setup__row">
							<Button
								variant="solid"
								onclick={() => void finalizeSetup()}
								disabled={finalizeLoading || !status.primary_admin_set || !step3Complete || connectedWalletIsBootstrap}
							>
								Sign in & finalize
							</Button>
						</div>

						{#if finalizeError}
							<Alert variant="error" title="Finalize failed">{finalizeError}</Alert>
						{/if}
					</Card>
					</div>
				{/if}
				{:else}
					<Alert variant="warning" title="No response">No response from /setup/status.</Alert>
				{/if}
		</div>
	</Container>

<style>
	.setup {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.setup__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.setup__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.setup__header-actions {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2);
	}

	.setup__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.setup__status-header {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--gr-spacing-scale-3);
	}

	.setup__wallet {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4);
	}

	.setup__wallet-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
	}

	.setup__steps {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4);
	}

	.setup__step-indicators {
		display: flex;
		flex-wrap: wrap;
		gap: var(--gr-spacing-scale-6);
		align-items: center;
	}

	.setup__row {
		display: flex;
		flex-wrap: wrap;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
	}

	.setup__form {
		display: grid;
		grid-template-columns: 1fr;
		gap: var(--gr-spacing-scale-4);
		margin-top: var(--gr-spacing-scale-4);
	}

	.setup__warnings {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.setup__checkbox {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2);
	}

	.setup__message {
		margin: var(--gr-spacing-scale-4) 0 0;
		padding: var(--gr-spacing-scale-4);
		background: var(--gr-semantic-background-secondary, #f3f4f6);
		border: 1px solid var(--gr-semantic-border-default, #e5e7eb);
		border-radius: var(--gr-radii-md, 0.375rem);
		white-space: pre-wrap;
		word-break: break-word;
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
		font-size: var(--gr-typography-fontSize-sm, 0.875rem);
		line-height: var(--gr-typography-lineHeight-relaxed, 1.75);
	}
</style>
