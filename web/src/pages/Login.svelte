<script lang="ts">
	import { onMount } from 'svelte';
	import type { ApiError } from 'src/lib/api/http';
	import { walletChallenge, walletLogin } from 'src/lib/api/controlPlane';
	import { portalWalletChallenge, portalWalletLogin } from 'src/lib/api/portal';
	import { webAuthnLoginBegin, webAuthnLoginFinish } from 'src/lib/api/webauthn';
	import { navigate } from 'src/lib/router';
	import { clearSession, setSession, session } from 'src/lib/session';
	import { getChainId, getEthereumProvider, personalSign, requestAccounts } from 'src/lib/wallet/ethereum';
	import type { Eip1193Provider } from 'src/lib/wallet/ethereum';
	import { serializeCredentialRequest, toPublicKeyRequestOptions } from 'src/lib/webauthn/client';
	import { Alert, Button, Card, Container, Heading, Spinner, Text, TextField } from 'src/lib/ui';

	type LoginMode = 'portal' | 'operator';
	type OperatorMethod = 'wallet' | 'passkey';

	let mode = $state<LoginMode>('portal');
	let operatorMethod = $state<OperatorMethod>('wallet');

	let provider = $state<Eip1193Provider | null>(null);
	let walletAddress = $state<string>('');
	let walletChainId = $state<number>(Number.parseInt(import.meta.env.VITE_WALLET_CHAIN_ID || '1', 10) || 1);
	let walletError = $state<string | null>(null);

	let portalEmail = $state('');
	let portalDisplayName = $state('');
	let portalLoading = $state(false);
	let portalError = $state<string | null>(null);
	let portalChallenge = $state<{ id: string; message: string } | null>(null);

	let operatorUsername = $state('');
	let operatorLoading = $state(false);
	let operatorError = $state<string | null>(null);
	let operatorChallenge = $state<{ id: string; message: string } | null>(null);

	let passkeyUsername = $state('');
	let passkeyLoading = $state(false);
	let passkeyError = $state<string | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function defaultRouteForRole(role: string): string {
		if (role === 'admin' || role === 'operator') return '/operator';
		return '/portal';
	}

	async function connectWallet() {
		walletError = null;

		const p = getEthereumProvider();
		if (!p) {
			walletError = 'No EIP-1193 wallet found (window.ethereum missing). Install MetaMask or another wallet.';
			return;
		}

		try {
			provider = p;
			const accounts = await requestAccounts(p);
			walletAddress = accounts[0] ? String(accounts[0]) : '';
			walletChainId = await getChainId(p);
		} catch (err) {
			walletError = formatError(err);
		}
	}

	async function beginPortalChallenge() {
		portalError = null;
		portalChallenge = null;

		if (!walletAddress) {
			portalError = 'Connect your wallet first.';
			return;
		}

		portalLoading = true;
		try {
			const challenge = await portalWalletChallenge(walletAddress, walletChainId);
			portalChallenge = { id: challenge.id, message: challenge.message };
		} catch (err) {
			portalError = formatError(err);
		} finally {
			portalLoading = false;
		}
	}

	async function loginPortalWithWallet() {
		portalError = null;

		if (!provider) {
			portalError = 'Connect your wallet first.';
			return;
		}
		if (!walletAddress) {
			portalError = 'Wallet address missing.';
			return;
		}
		if (!portalChallenge) {
			portalError = 'Create a challenge first.';
			return;
		}

		portalLoading = true;
		try {
			const signature = await personalSign(provider, portalChallenge.message, walletAddress);
			const sessionData = await portalWalletLogin({
				challengeId: portalChallenge.id,
				address: walletAddress,
				signature,
				message: portalChallenge.message,
				email: portalEmail.trim() || undefined,
				display_name: portalDisplayName.trim() || undefined,
			});

			setSession({
				tokenType: sessionData.token_type,
				token: sessionData.token,
				expiresAt: sessionData.expires_at,
				username: sessionData.username,
				role: sessionData.role,
				method: sessionData.method,
				walletAddress,
			});
			navigate(defaultRouteForRole(sessionData.role));
		} catch (err) {
			portalError = formatError(err);
		} finally {
			portalLoading = false;
		}
	}

	async function beginOperatorChallenge() {
		operatorError = null;
		operatorChallenge = null;

		const username = operatorUsername.trim();
		if (!username) {
			operatorError = 'Username is required.';
			return;
		}
		if (!walletAddress) {
			operatorError = 'Connect your wallet first.';
			return;
		}

		operatorLoading = true;
		try {
			const challenge = await walletChallenge({ username, address: walletAddress, chainId: walletChainId });
			operatorChallenge = { id: challenge.id, message: challenge.message };
		} catch (err) {
			operatorError = formatError(err);
		} finally {
			operatorLoading = false;
		}
	}

	async function loginOperatorWithWallet() {
		operatorError = null;

		const username = operatorUsername.trim();
		if (!username) {
			operatorError = 'Username is required.';
			return;
		}
		if (!provider) {
			operatorError = 'Connect your wallet first.';
			return;
		}
		if (!walletAddress) {
			operatorError = 'Wallet address missing.';
			return;
		}
		if (!operatorChallenge) {
			operatorError = 'Create a challenge first.';
			return;
		}

		operatorLoading = true;
		try {
			const signature = await personalSign(provider, operatorChallenge.message, walletAddress);
			const sessionData = await walletLogin({
				challengeId: operatorChallenge.id,
				address: walletAddress,
				signature,
				message: operatorChallenge.message,
			});

			setSession({
				tokenType: sessionData.token_type,
				token: sessionData.token,
				expiresAt: sessionData.expires_at,
				username: sessionData.username,
				role: sessionData.role,
				method: sessionData.method,
				walletAddress,
			});
			navigate(defaultRouteForRole(sessionData.role));
		} catch (err) {
			operatorError = formatError(err);
		} finally {
			operatorLoading = false;
		}
	}

	async function loginOperatorWithPasskey() {
		passkeyError = null;

		const username = passkeyUsername.trim();
		if (!username) {
			passkeyError = 'Username is required.';
			return;
		}
		if (!window.PublicKeyCredential || !navigator.credentials) {
			passkeyError = 'Passkeys are not supported in this browser.';
			return;
		}

		passkeyLoading = true;
		try {
			const begin = await webAuthnLoginBegin(username);
			const options = toPublicKeyRequestOptions(begin.publicKey);
			const credential = (await navigator.credentials.get({
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

			const response = serializeCredentialRequest(credential);
			const sessionData = await webAuthnLoginFinish({
				username,
				challenge: begin.challenge,
				response,
				device_name: '',
			});

			setSession({
				tokenType: sessionData.token_type,
				token: sessionData.token,
				expiresAt: sessionData.expires_at,
				username: sessionData.username,
				role: sessionData.role,
				method: sessionData.method,
			});
			navigate(defaultRouteForRole(sessionData.role));
		} catch (err) {
			passkeyError = formatError(err);
		} finally {
			passkeyLoading = false;
		}
	}

	onMount(() => {
		const current = $session;
		if (current) {
			navigate(defaultRouteForRole(current.role));
		}
	});
</script>

<Container size="lg" gutter="lg">
	<div class="login">
		<header class="login__header">
			<div class="login__title">
				<Heading level={1}>Sign in</Heading>
				<Text color="secondary">Authenticate with wallet or passkey.</Text>
			</div>
			<div class="login__header-actions">
				<Button variant="ghost" onclick={() => navigate('/')}>Home</Button>
				<Button variant="ghost" onclick={() => navigate('/setup')}>Setup</Button>
			</div>
		</header>

		<Card variant="outlined" padding="lg">
			<div class="login__mode-toggle">
				<Button
					variant={mode === 'portal' ? 'solid' : 'outline'}
					onclick={() => {
						mode = 'portal';
						operatorMethod = 'wallet';
					}}
				>
					Portal
				</Button>
				<Button
					variant={mode === 'operator' ? 'solid' : 'outline'}
					onclick={() => {
						mode = 'operator';
					}}
				>
					Operator
				</Button>
			</div>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={2} size="xl">Wallet</Heading>
			{/snippet}

			<div class="login__wallet">
				<div class="login__wallet-actions">
					<Button variant="solid" onclick={() => void connectWallet()}>
						{walletAddress ? 'Reconnect wallet' : 'Connect wallet'}
					</Button>
				</div>

				{#if walletError}
					<Alert variant="error" title="Wallet error">{walletError}</Alert>
				{/if}

				<Text size="sm" color="secondary">
					Connected: <span class="login__mono">{walletAddress || '—'}</span> · Chain:
					<span class="login__mono">{String(walletChainId)}</span>
				</Text>
			</div>
		</Card>

		{#if mode === 'portal'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={2} size="xl">Portal login</Heading>
				{/snippet}

				<Text size="sm" color="secondary">
					First login creates a customer account for your wallet.
				</Text>

				<div class="login__form">
					<TextField label="Email (optional)" bind:value={portalEmail} type="email" />
					<TextField label="Display name (optional)" bind:value={portalDisplayName} />
				</div>

				<div class="login__row">
					<Button variant="outline" onclick={() => void beginPortalChallenge()} disabled={portalLoading}>
						Create challenge
					</Button>
					<Button
						variant="solid"
						onclick={() => void loginPortalWithWallet()}
						disabled={portalLoading || !portalChallenge}
					>
						Sign & login
					</Button>
				</div>

				{#if portalLoading}
					<div class="login__loading">
						<Spinner size="sm" />
						<Text size="sm">Working…</Text>
					</div>
				{/if}

				{#if portalError}
					<Alert variant="error" title="Portal login failed">{portalError}</Alert>
				{/if}

				{#if portalChallenge}
					<Alert variant="info" title="Signature required">
						<Text size="sm">Sign the exact message below to authenticate.</Text>
					</Alert>
					<pre class="login__message">{portalChallenge.message}</pre>
				{/if}
			</Card>
		{:else}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={2} size="xl">Operator login</Heading>
				{/snippet}

				<div class="login__mode-toggle">
					<Button
						variant={operatorMethod === 'wallet' ? 'solid' : 'outline'}
						onclick={() => (operatorMethod = 'wallet')}
					>
						Wallet
					</Button>
					<Button
						variant={operatorMethod === 'passkey' ? 'solid' : 'outline'}
						onclick={() => (operatorMethod = 'passkey')}
					>
						Passkey
					</Button>
				</div>

				{#if operatorMethod === 'wallet'}
					<Text size="sm" color="secondary">Sign in as an operator/admin using a linked wallet.</Text>

					<div class="login__form">
						<TextField label="Username" bind:value={operatorUsername} required />
					</div>

					<div class="login__row">
						<Button
							variant="outline"
							onclick={() => void beginOperatorChallenge()}
							disabled={operatorLoading}
						>
							Create challenge
						</Button>
						<Button
							variant="solid"
							onclick={() => void loginOperatorWithWallet()}
							disabled={operatorLoading || !operatorChallenge}
						>
							Sign & login
						</Button>
					</div>

					{#if operatorLoading}
						<div class="login__loading">
							<Spinner size="sm" />
							<Text size="sm">Working…</Text>
						</div>
					{/if}

					{#if operatorError}
						<Alert variant="error" title="Operator login failed">{operatorError}</Alert>
					{/if}

					{#if operatorChallenge}
						<Alert variant="info" title="Signature required">
							<Text size="sm">Sign the exact message below to authenticate.</Text>
						</Alert>
						<pre class="login__message">{operatorChallenge.message}</pre>
					{/if}
				{:else}
					<Text size="sm" color="secondary">Sign in with a registered passkey (WebAuthn).</Text>

					<div class="login__form">
						<TextField label="Username" bind:value={passkeyUsername} required />
					</div>

					<div class="login__row">
						<Button variant="solid" onclick={() => void loginOperatorWithPasskey()} disabled={passkeyLoading}>
							Use passkey
						</Button>
						<Button variant="outline" onclick={() => clearSession()} disabled={passkeyLoading}>
							Clear session
						</Button>
					</div>

					{#if passkeyLoading}
						<div class="login__loading">
							<Spinner size="sm" />
							<Text size="sm">Waiting for passkey…</Text>
						</div>
					{/if}

					{#if passkeyError}
						<Alert variant="error" title="Passkey login failed">{passkeyError}</Alert>
					{/if}
				{/if}
			</Card>
		{/if}

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={2} size="xl">Session</Heading>
			{/snippet}

			{#if $session}
				<Text size="sm">
					Signed in as <span class="login__mono">{$session.username}</span> (<span class="login__mono"
						>{$session.role}</span
					>) · expires <span class="login__mono">{$session.expiresAt}</span>
				</Text>
				<div class="login__row">
					<Button variant="outline" onclick={() => navigate(defaultRouteForRole($session.role))}>
						Continue
					</Button>
					<Button
						variant="ghost"
						onclick={() => {
							clearSession();
							portalChallenge = null;
							operatorChallenge = null;
						}}
					>
						Logout
					</Button>
				</div>
			{:else}
				<Text size="sm" color="secondary">No active session.</Text>
			{/if}
		</Card>
	</div>
</Container>

<style>
	.login {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.login__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.login__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.login__header-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.login__mode-toggle {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.login__wallet {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
	}

	.login__wallet-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.login__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.login__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}

	.login__loading {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.login__message {
		margin-top: var(--gr-spacing-scale-3);
		padding: var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-color-border-subtle);
		border-radius: var(--gr-border-radius-md);
		background: var(--gr-color-surface-subtle);
		font-family: ui-monospace, SFMono-Regular, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono',
			'Courier New', monospace;
		font-size: 0.85rem;
		white-space: pre-wrap;
		line-height: 1.3;
	}

	.login__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
