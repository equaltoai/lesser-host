<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { SoulAgentRegistrationBeginResponse, SoulAgentRegistrationVerifyResponse } from 'src/lib/api/soul';
	import { soulAgentRegistrationBegin, soulAgentRegistrationVerify } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { getEthereumProvider, personalSign, requestAccounts } from 'src/lib/wallet/ethereum';
	import {
		Alert,
		Button,
		Card,
		CopyButton,
		DefinitionItem,
		DefinitionList,
		Heading,
		Spinner,
		Text,
		TextArea,
		TextField,
	} from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let domain = $state('');
	let localId = $state('');
	let walletAddress = $state('');
	let capabilities = $state('');

	let beginLoading = $state(false);
	let beginError = $state<string | null>(null);
	let beginResult = $state<SoulAgentRegistrationBeginResponse | null>(null);

	let signature = $state('');
	let signLoading = $state(false);
	let signError = $state<string | null>(null);

	let verifyLoading = $state(false);
	let verifyError = $state<string | null>(null);
	let verifyResult = $state<SoulAgentRegistrationVerifyResponse | null>(null);

	const dnsProof = $derived.by(() => beginResult?.proofs?.find((p) => p.method === 'dns_txt') || null);
	const httpsProof = $derived.by(() => beginResult?.proofs?.find((p) => p.method === 'https_well_known') || null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function parseCapabilities(raw: string): string[] {
		const parts = raw
			.split(',')
			.map((p) => p.trim())
			.filter(Boolean);
		const seen: Record<string, true> = {};
		const out: string[] = [];
		for (const p of parts) {
			const lower = p.toLowerCase();
			if (seen[lower]) continue;
			seen[lower] = true;
			out.push(lower);
		}
		return out;
	}

	async function handleBegin() {
		beginError = null;
		signError = null;
		verifyError = null;
		beginResult = null;
		verifyResult = null;
		signature = '';

		const nextDomain = domain.trim();
		const nextLocal = localId.trim();
		const nextWallet = walletAddress.trim();

		if (!nextDomain) {
			beginError = 'Domain is required.';
			return;
		}
		if (!nextLocal) {
			beginError = 'Local ID is required.';
			return;
		}
		if (!nextWallet) {
			beginError = 'Wallet address is required.';
			return;
		}

		beginLoading = true;
		try {
			beginResult = await soulAgentRegistrationBegin(token, {
				domain: nextDomain,
				local_id: nextLocal,
				wallet_address: nextWallet,
				capabilities: parseCapabilities(capabilities),
			});
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			beginError = formatError(err);
		} finally {
			beginLoading = false;
		}
	}

	async function useConnectedWallet() {
		signError = null;
		const provider = getEthereumProvider();
		if (!provider) {
			signError = 'No wallet detected. Install or enable a wallet extension.';
			return;
		}
		try {
			const accounts = await requestAccounts(provider);
			if (!accounts.length) {
				signError = 'Wallet returned no accounts.';
				return;
			}
			walletAddress = accounts[0];
		} catch (err) {
			signError = formatError(err);
		}
	}

	async function signMessage() {
		signError = null;
		signature = '';

		const provider = getEthereumProvider();
		if (!provider) {
			signError = 'No wallet detected. Install or enable a wallet extension.';
			return;
		}
		if (!beginResult?.wallet?.message) {
			signError = 'Generate a registration challenge first.';
			return;
		}

		const addr = walletAddress.trim();
		if (!addr) {
			signError = 'Wallet address is required.';
			return;
		}

		signLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(addr.toLowerCase())) {
				signError = 'Connected wallet does not match the wallet address above.';
				return;
			}

			signature = await personalSign(provider, beginResult.wallet.message, addr);
		} catch (err) {
			signError = formatError(err);
		} finally {
			signLoading = false;
		}
	}

	async function verifyRegistration() {
		verifyError = null;
		verifyResult = null;

		if (!beginResult?.registration?.id) {
			verifyError = 'Generate a registration challenge first.';
			return;
		}
		if (!signature) {
			verifyError = 'Sign the registration message first.';
			return;
		}

		verifyLoading = true;
		try {
			verifyResult = await soulAgentRegistrationVerify(token, beginResult.registration.id, signature);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			verifyError = formatError(err);
		} finally {
			verifyLoading = false;
		}
	}

	onMount(() => {
		const params = new URLSearchParams(window.location.search);
		const presetDomain = params.get('domain');
		const presetLocal = params.get('local_id') || params.get('localId');
		if (presetDomain) domain = presetDomain;
		if (presetLocal) localId = presetLocal;
	});
</script>

<div class="soul-register">
	<header class="soul-register__header">
		<div class="soul-register__title">
			<Heading level={2} size="xl">Register agent</Heading>
			<Text color="secondary">Publish DNS + HTTPS proofs, sign the challenge, and create the Safe-ready mint operation.</Text>
		</div>
		<div class="soul-register__actions">
			<Button variant="ghost" onclick={() => navigate('/portal/souls')}>Back</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">1. Generate proof</Heading>
		{/snippet}

		{#if beginError}
			<Alert variant="error" title="Registration start failed">{beginError}</Alert>
		{/if}

		<div class="soul-register__form">
			<TextField label="Domain" bind:value={domain} placeholder="example.com" />
			<TextField label="Local ID" bind:value={localId} placeholder="agent-alice" />
			<TextField label="Wallet" bind:value={walletAddress} placeholder="0x…" />
			<TextField label="Capabilities (comma-separated)" bind:value={capabilities} placeholder="social, commerce" />
			<div class="soul-register__row">
				<Button variant="solid" onclick={() => void handleBegin()} disabled={beginLoading}>Generate proof</Button>
				<Button variant="outline" onclick={() => void useConnectedWallet()} disabled={beginLoading}>Use connected wallet</Button>
			</div>
		</div>

		{#if beginLoading}
			<div class="soul-register__loading-inline">
				<Spinner size="sm" />
				<Text size="sm">Creating registration…</Text>
			</div>
		{/if}

		{#if beginResult}
			<div class="soul-register__meta">
				<Text size="sm" color="secondary">
					Registration <span class="soul-register__mono">{beginResult.registration.id}</span>
				</Text>
				<Text size="sm" color="secondary">
					Agent ID <span class="soul-register__mono">{beginResult.registration.agent_id}</span>
				</Text>
				<Text size="sm" color="secondary">Expires {beginResult.registration.expires_at || beginResult.wallet.expiresAt}</Text>
			</div>
		{/if}
	</Card>

	{#if beginResult}
		{@const begin = beginResult as SoulAgentRegistrationBeginResponse}
		<div class="soul-register__grid">
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">2. Publish proofs</Heading>
				{/snippet}
				<Text size="sm" color="secondary">
					Both DNS TXT and HTTPS well-known proofs must be published before verification will succeed.
				</Text>

				{#if dnsProof}
					<div class="soul-register__proof">
						<Text size="sm" color="secondary">DNS TXT</Text>
						<div class="soul-register__proof-row">
							<Text size="sm">Name</Text>
							<span class="soul-register__mono">{dnsProof.dns_name}</span>
							<CopyButton size="sm" text={dnsProof.dns_name || ''} />
						</div>
						<div class="soul-register__proof-row">
							<Text size="sm">Value</Text>
							<span class="soul-register__mono">{dnsProof.dns_value}</span>
							<CopyButton size="sm" text={dnsProof.dns_value || ''} />
						</div>
					</div>
				{/if}

				{#if httpsProof}
					<div class="soul-register__proof">
						<Text size="sm" color="secondary">HTTPS well-known</Text>
						<div class="soul-register__proof-row">
							<Text size="sm">URL</Text>
							<span class="soul-register__mono">{httpsProof.https_url}</span>
							<CopyButton size="sm" text={httpsProof.https_url || ''} />
						</div>
						<div class="soul-register__proof-row">
							<Text size="sm">Body</Text>
							<span class="soul-register__mono">{httpsProof.https_body}</span>
							<CopyButton size="sm" text={httpsProof.https_body || ''} />
						</div>
					</div>
				{/if}
			</Card>

			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">3. Sign and verify</Heading>
				{/snippet}

				{#if signError}
					<Alert variant="error" title="Signing failed">{signError}</Alert>
				{/if}
				{#if verifyError}
					<Alert variant="error" title="Verification failed">{verifyError}</Alert>
				{/if}

				<Text size="sm" color="secondary">Sign the challenge message with your agent wallet.</Text>
				<TextArea value={beginResult.wallet.message} readonly rows={6} />
				<div class="soul-register__row">
					<Button variant="outline" onclick={() => void signMessage()} disabled={signLoading}>Sign message</Button>
					{#if signature}
						<CopyButton size="sm" text={signature} />
					{/if}
				</div>

				{#if signLoading}
					<div class="soul-register__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Waiting for wallet…</Text>
					</div>
				{/if}

				{#if signature}
					<Text size="sm" color="secondary">
						Signature <span class="soul-register__mono">{signature.slice(0, 14)}…</span>
					</Text>
				{/if}

				<div class="soul-register__row soul-register__row--verify">
					<Button variant="solid" onclick={() => void verifyRegistration()} disabled={verifyLoading}>Verify + create mint operation</Button>
				</div>

				{#if verifyLoading}
					<div class="soul-register__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Verifying…</Text>
					</div>
				{/if}

				{#if verifyResult}
					<Card variant="outlined" padding="lg">
						{#snippet header()}
							<Heading level={4} size="lg">Result</Heading>
						{/snippet}

						<Text size="sm" color="secondary">
							Operation <span class="soul-register__mono">{verifyResult.operation.operation_id}</span>
						</Text>
						<div class="soul-register__row">
								<Button
									variant="outline"
									onclick={() => navigate(`/portal/souls/${begin.registration.agent_id}`)}
								>
									Open agent
								</Button>
							<CopyButton size="sm" text={verifyResult.operation.operation_id} />
						</div>

						{#if verifyResult.safe_tx}
							<DefinitionList>
								<DefinitionItem label="Safe" monospace>{verifyResult.safe_tx.safe_address}</DefinitionItem>
								<DefinitionItem label="To" monospace>{verifyResult.safe_tx.to}</DefinitionItem>
								<DefinitionItem label="Value" monospace>{verifyResult.safe_tx.value}</DefinitionItem>
								<DefinitionItem label="Data" monospace>{verifyResult.safe_tx.data}</DefinitionItem>
							</DefinitionList>
						{/if}
					</Card>
				{/if}
			</Card>
		</div>
	{/if}
</div>

<style>
	.soul-register {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.soul-register__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.soul-register__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.soul-register__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.soul-register__form {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.soul-register__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.soul-register__row--verify {
		margin-top: var(--gr-spacing-scale-4);
	}

	.soul-register__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.soul-register__meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		margin-top: var(--gr-spacing-scale-3);
	}

	.soul-register__grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
		gap: var(--gr-spacing-scale-4);
	}

	.soul-register__proof {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		margin-top: var(--gr-spacing-scale-4);
		padding-top: var(--gr-spacing-scale-3);
		border-top: 1px solid var(--gr-color-border);
	}

	.soul-register__proof-row {
		display: grid;
		grid-template-columns: 60px 1fr auto;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.soul-register__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
