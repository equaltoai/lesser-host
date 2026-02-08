<script lang="ts">
	import { onMount } from 'svelte';
	import type { ApiError } from 'src/lib/api/http';
	import type {
		TipRegistryRegistrationBeginResponse,
		TipRegistryRegistrationVerifyResponse,
	} from 'src/lib/api/tipRegistry';
	import { beginTipRegistryRegistration, verifyTipRegistryRegistration } from 'src/lib/api/tipRegistry';
	import { getEthereumProvider, personalSign, requestAccounts } from 'src/lib/wallet/ethereum';
	import {
		Alert,
		Button,
		Card,
		Container,
		CopyButton,
		Heading,
		Select,
		Spinner,
		Text,
		TextArea,
		TextField,
	} from 'src/lib/ui';

	type ProofFlags = {
		dns: boolean;
		https: boolean;
	};

	let domain = $state('');
	let walletAddress = $state('');
	let hostFeeBps = $state('500');
	let kind = $state('register_host');

	let beginLoading = $state(false);
	let beginError = $state<string | null>(null);
	let beginResult = $state<TipRegistryRegistrationBeginResponse | null>(null);

	let signature = $state('');
	let signLoading = $state(false);
	let signError = $state<string | null>(null);

	let verifyLoading = $state(false);
	let verifyError = $state<string | null>(null);
	let verifyResult = $state<TipRegistryRegistrationVerifyResponse | null>(null);

	let proofs = $state<ProofFlags>({ dns: true, https: false });

	const dnsProof = $derived.by(() =>
		beginResult?.proofs?.find((proof) => proof.method === 'dns_txt') || null,
	);
	const httpsProof = $derived.by(() =>
		beginResult?.proofs?.find((proof) => proof.method === 'https_well_known') || null,
	);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function parseFee(value: string): number | null {
		const parsed = Number.parseInt(value.trim(), 10);
		if (!Number.isFinite(parsed)) return null;
		return parsed;
	}

	async function handleBegin() {
		beginError = null;
		verifyError = null;
		signError = null;
		beginResult = null;
		verifyResult = null;
		signature = '';

		const nextDomain = domain.trim();
		const nextWallet = walletAddress.trim();
		const fee = parseFee(hostFeeBps);

		if (!nextDomain) {
			beginError = 'Domain is required.';
			return;
		}
		if (!nextWallet) {
			beginError = 'Wallet address is required.';
			return;
		}
		if (fee === null) {
			beginError = 'Host fee bps must be a number.';
			return;
		}
		if (fee < 0 || fee > 500) {
			beginError = 'Host fee bps must be between 0 and 500.';
			return;
		}

		beginLoading = true;
		try {
			beginResult = await beginTipRegistryRegistration({
				kind,
				domain: nextDomain,
				wallet_address: nextWallet,
				host_fee_bps: fee,
			});

			proofs = { dns: true, https: false };
		} catch (err) {
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

		const selected: string[] = [];
		if (proofs.dns) selected.push('dns_txt');
		if (proofs.https) selected.push('https_well_known');
		if (selected.length === 0) {
			verifyError = 'Select at least one proof method.';
			return;
		}

		verifyLoading = true;
		try {
			verifyResult = await verifyTipRegistryRegistration(beginResult.registration.id, signature, selected);
		} catch (err) {
			verifyError = formatError(err);
		} finally {
			verifyLoading = false;
		}
	}

	onMount(() => {
		const params = new URLSearchParams(window.location.search);
		const presetDomain = params.get('domain');
		if (presetDomain) domain = presetDomain;
	});
</script>

<Container size="lg" gutter="lg">
	<div class="tip-register">
		<header class="tip-register__header">
			<div class="tip-register__title">
				<Heading level={1}>Tip registry registration</Heading>
				<Text color="secondary">Verify a domain and create the Safe payload to register it.</Text>
			</div>
		</header>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={2} size="xl">1. Generate proof</Heading>
			{/snippet}

			{#if beginError}
				<Alert variant="error" title="Registration start failed">{beginError}</Alert>
			{/if}

			<div class="tip-register__form">
				<TextField label="Domain" bind:value={domain} placeholder="example.com" />
				<TextField label="Host wallet" bind:value={walletAddress} placeholder="0x…" />
				<TextField label="Host fee (bps)" bind:value={hostFeeBps} placeholder="0" />
				<div class="tip-register__field">
					<Text size="sm">Registration type</Text>
					<Select
						bind:value={kind}
						options={[
							{ value: 'register_host', label: 'register_host' },
							{ value: 'update_host', label: 'update_host' },
						]}
					/>
				</div>
				<div class="tip-register__row">
					<Button variant="solid" onclick={() => void handleBegin()} disabled={beginLoading}>
						Generate proof
					</Button>
					<Button variant="outline" onclick={() => void useConnectedWallet()} disabled={beginLoading}>
						Use connected wallet
					</Button>
				</div>
			</div>

			{#if beginLoading}
				<div class="tip-register__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Creating registration…</Text>
				</div>
			{/if}

			{#if beginResult}
				<div class="tip-register__meta">
					<Text size="sm" color="secondary">
						Registration <span class="tip-register__mono">{beginResult.registration.id}</span>
					</Text>
					<Text size="sm" color="secondary">
						Expires {beginResult.registration.expires_at || beginResult.wallet.expiresAt}
					</Text>
				</div>
			{/if}
		</Card>

		{#if beginResult}
			<div class="tip-register__grid">
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<Heading level={3} size="lg">2. Publish proof</Heading>
					{/snippet}

					<Text size="sm" color="secondary">
						Publish at least one proof below. DNS TXT is the simplest path.
					</Text>

					{#if dnsProof}
						<div class="tip-register__proof">
							<Text size="sm" color="secondary">DNS TXT</Text>
							<div class="tip-register__proof-row">
								<Text size="sm">Name</Text>
								<span class="tip-register__mono">{dnsProof.dns_name}</span>
								<CopyButton size="sm" text={dnsProof.dns_name} />
							</div>
							<div class="tip-register__proof-row">
								<Text size="sm">Value</Text>
								<span class="tip-register__mono">{dnsProof.dns_value}</span>
								<CopyButton size="sm" text={dnsProof.dns_value} />
							</div>
						</div>
					{/if}

					{#if httpsProof}
						<div class="tip-register__proof">
							<Text size="sm" color="secondary">HTTPS well-known</Text>
							<div class="tip-register__proof-row">
								<Text size="sm">URL</Text>
								<span class="tip-register__mono">{httpsProof.https_url}</span>
								<CopyButton size="sm" text={httpsProof.https_url} />
							</div>
							<div class="tip-register__proof-row">
								<Text size="sm">Body</Text>
								<span class="tip-register__mono">{httpsProof.https_body}</span>
								<CopyButton size="sm" text={httpsProof.https_body} />
							</div>
						</div>
					{/if}

					<div class="tip-register__proof-pick">
						<label class="tip-register__checkbox">
							<input type="checkbox" bind:checked={proofs.dns} />
							<span>Verify DNS TXT</span>
						</label>
						<label class="tip-register__checkbox">
							<input type="checkbox" bind:checked={proofs.https} />
							<span>Verify HTTPS well-known</span>
						</label>
					</div>
				</Card>

				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<Heading level={3} size="lg">3. Sign wallet message</Heading>
					{/snippet}

					{#if signError}
						<Alert variant="error" title="Signature failed">{signError}</Alert>
					{/if}

					<TextArea label="Message to sign" value={beginResult.wallet.message} readonly rows={8} />

					<div class="tip-register__row">
						<Button variant="solid" onclick={() => void signMessage()} disabled={signLoading}>
							Sign message
						</Button>
						<CopyButton variant="icon-text" size="sm" text={beginResult.wallet.message} labels={{ default: 'Copy' }} />
					</div>

					{#if signature}
						<Text size="sm" color="secondary">
							Signature captured.
						</Text>
					{/if}
				</Card>
			</div>

			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">4. Verify + Safe payload</Heading>
				{/snippet}

				{#if verifyError}
					<Alert variant="error" title="Verification failed">{verifyError}</Alert>
				{/if}

				<div class="tip-register__row">
					<Button variant="solid" onclick={() => void verifyRegistration()} disabled={verifyLoading}>
						Verify now
					</Button>
				</div>

				{#if verifyLoading}
					<div class="tip-register__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Verifying proofs…</Text>
					</div>
				{/if}

				{#if verifyResult}
					<Text size="sm" color="secondary">
						Operation <span class="tip-register__mono">{verifyResult.operation.id}</span>
					</Text>

					{#if verifyResult.safe_tx}
						<div class="tip-register__safe">
							<div class="tip-register__proof-row">
								<Text size="sm">Safe</Text>
								<span class="tip-register__mono">{verifyResult.safe_tx.safe_address}</span>
								<CopyButton size="sm" text={verifyResult.safe_tx.safe_address} />
							</div>
							<div class="tip-register__proof-row">
								<Text size="sm">To</Text>
								<span class="tip-register__mono">{verifyResult.safe_tx.to}</span>
								<CopyButton size="sm" text={verifyResult.safe_tx.to} />
							</div>
							<div class="tip-register__proof-row">
								<Text size="sm">Value</Text>
								<span class="tip-register__mono">{verifyResult.safe_tx.value}</span>
								<CopyButton size="sm" text={verifyResult.safe_tx.value} />
							</div>
							<div class="tip-register__proof-row">
								<Text size="sm">Data</Text>
								<span class="tip-register__mono tip-register__mono--wrap">{verifyResult.safe_tx.data}</span>
								<CopyButton size="sm" text={verifyResult.safe_tx.data} />
							</div>
						</div>
					{/if}
				{/if}
			</Card>
		{/if}
	</div>
</Container>

<style>
	.tip-register {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.tip-register__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.tip-register__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.tip-register__form {
		display: grid;
		gap: var(--gr-spacing-scale-4);
	}

	.tip-register__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.tip-register__field {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.tip-register__meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		margin-top: var(--gr-spacing-scale-3);
	}

	.tip-register__grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
		gap: var(--gr-spacing-scale-4);
	}

	.tip-register__proof {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.tip-register__proof-row {
		display: grid;
		grid-template-columns: minmax(80px, 120px) 1fr auto;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.tip-register__proof-pick {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-4);
	}

	.tip-register__checkbox {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		font-size: var(--gr-typography-fontSize-sm);
	}

	.tip-register__safe {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.tip-register__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.tip-register__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
		font-size: var(--gr-typography-fontSize-sm);
	}

	.tip-register__mono--wrap {
		word-break: break-all;
	}

	@media (max-width: 720px) {
		.tip-register__proof-row {
			grid-template-columns: 1fr;
		}
	}
</style>
