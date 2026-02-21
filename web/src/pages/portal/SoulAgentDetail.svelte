<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type {
		SoulPublicAgentResponse,
		SoulPublicValidationsResponse,
		SoulRotateWalletBeginResponse,
		SoulRotateWalletConfirmResponse,
	} from 'src/lib/api/soul';
	import {
		soulAgentRotateWalletBegin,
		soulAgentRotateWalletConfirm,
		soulPublicGetAgent,
		soulPublicGetRegistration,
		soulPublicGetValidations,
	} from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { getEthereumProvider, requestAccounts, signTypedDataV4 } from 'src/lib/wallet/ethereum';
	import {
		Alert,
		Badge,
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

	let { token, agentId } = $props<{ token: string; agentId: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let agent = $state<SoulPublicAgentResponse | null>(null);
	let registration = $state<unknown | null>(null);
	let validations = $state<SoulPublicValidationsResponse | null>(null);

	let rotationNewWallet = $state('');
	let rotationBeginLoading = $state(false);
	let rotationBeginError = $state<string | null>(null);
	let rotationBeginResult = $state<SoulRotateWalletBeginResponse | null>(null);

	let sigCurrent = $state('');
	let sigNew = $state('');
	let signCurrentLoading = $state(false);
	let signNewLoading = $state(false);
	let signError = $state<string | null>(null);

	let rotationConfirmLoading = $state(false);
	let rotationConfirmError = $state<string | null>(null);
	let rotationConfirmResult = $state<SoulRotateWalletConfirmResponse | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function badgeForStatus(status: string): { variant: 'outlined' | 'filled'; color: 'success' | 'warning' | 'error' | 'gray' } {
		const s = (status || '').toLowerCase();
		if (s === 'active') return { variant: 'filled', color: 'success' };
		if (s === 'pending') return { variant: 'outlined', color: 'warning' };
		if (s === 'suspended') return { variant: 'filled', color: 'error' };
		return { variant: 'outlined', color: 'gray' };
	}

	function prettyJSON(value: unknown): string {
		if (value == null) return '';
		try {
			return JSON.stringify(value, null, 2);
		} catch {
			return String(value);
		}
	}

	async function load() {
		errorMessage = null;
		agent = null;
		registration = null;
		validations = null;

		loading = true;
		try {
			agent = await soulPublicGetAgent(agentId);
			registration = await soulPublicGetRegistration(agentId).catch(() => null);
			validations = await soulPublicGetValidations(agentId, undefined, 50).catch(() => null);
		} catch (err) {
			errorMessage = formatError(err);
		} finally {
			loading = false;
		}
	}

	async function beginRotation() {
		rotationBeginError = null;
		signError = null;
		rotationConfirmError = null;
		rotationConfirmResult = null;
		rotationBeginResult = null;
		sigCurrent = '';
		sigNew = '';

		const newWallet = rotationNewWallet.trim();
		if (!newWallet) {
			rotationBeginError = 'New wallet is required.';
			return;
		}

		rotationBeginLoading = true;
		try {
			rotationBeginResult = await soulAgentRotateWalletBegin(token, agentId, newWallet);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			rotationBeginError = formatError(err);
		} finally {
			rotationBeginLoading = false;
		}
	}

	async function signWithCurrentWallet() {
		signError = null;
		sigCurrent = '';

		if (!rotationBeginResult) {
			signError = 'Begin rotation first.';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			signError = 'No wallet detected.';
			return;
		}

		const currentWallet = rotationBeginResult.rotation.current_wallet;
		signCurrentLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(currentWallet.toLowerCase())) {
				signError = `Connected wallet does not match current wallet (${currentWallet}).`;
				return;
			}

			sigCurrent = await signTypedDataV4(provider, currentWallet, rotationBeginResult.typed_data);
		} catch (err) {
			signError = formatError(err);
		} finally {
			signCurrentLoading = false;
		}
	}

	async function signWithNewWallet() {
		signError = null;
		sigNew = '';

		if (!rotationBeginResult) {
			signError = 'Begin rotation first.';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			signError = 'No wallet detected.';
			return;
		}

		const newWallet = rotationBeginResult.rotation.new_wallet;
		signNewLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(newWallet.toLowerCase())) {
				signError = `Connected wallet does not match new wallet (${newWallet}).`;
				return;
			}

			sigNew = await signTypedDataV4(provider, newWallet, rotationBeginResult.typed_data);
		} catch (err) {
			signError = formatError(err);
		} finally {
			signNewLoading = false;
		}
	}

	async function confirmRotation() {
		rotationConfirmError = null;
		rotationConfirmResult = null;

		if (!rotationBeginResult) {
			rotationConfirmError = 'Begin rotation first.';
			return;
		}
		if (!sigCurrent || !sigNew) {
			rotationConfirmError = 'Both signatures are required.';
			return;
		}

		rotationConfirmLoading = true;
		try {
			rotationConfirmResult = await soulAgentRotateWalletConfirm(token, agentId, sigCurrent, sigNew);
			await load();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			rotationConfirmError = formatError(err);
		} finally {
			rotationConfirmLoading = false;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="soul-agent">
	<header class="soul-agent__header">
		<div class="soul-agent__title">
			<Heading level={2} size="xl">Agent</Heading>
			<Text color="secondary"><span class="soul-agent__mono">{agentId}</span></Text>
		</div>
		<div class="soul-agent__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate('/portal/souls')}>Back</Button>
		</div>
	</header>

	{#if loading}
		<div class="soul-agent__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Agent">{errorMessage}</Alert>
	{:else if agent}
		{@const current = agent as SoulPublicAgentResponse}
		{@const statusBadge = badgeForStatus(current.agent.status)}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<div class="soul-agent__row">
					<div class="soul-agent__row-left">
						<Heading level={3} size="lg">Identity</Heading>
						<Badge variant={statusBadge.variant} color={statusBadge.color} size="sm">{current.agent.status}</Badge>
					</div>
					<div class="soul-agent__row-right">
						<CopyButton size="sm" text={current.agent.agent_id} />
					</div>
				</div>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Domain" monospace>{current.agent.domain}</DefinitionItem>
				<DefinitionItem label="Local ID" monospace>{current.agent.local_id}</DefinitionItem>
				<DefinitionItem label="Wallet" monospace>{current.agent.wallet}</DefinitionItem>
				<DefinitionItem label="Meta URI" monospace>{current.agent.meta_uri || '—'}</DefinitionItem>
				<DefinitionItem label="Capabilities" monospace>{(current.agent.capabilities || []).join(', ') || '—'}</DefinitionItem>
				<DefinitionItem label="Updated" monospace>{current.agent.updated_at || '—'}</DefinitionItem>
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Reputation</Heading>
			{/snippet}

			{#if current.reputation}
				<DefinitionList>
					<DefinitionItem label="Composite" monospace>{current.reputation.composite.toFixed(6)}</DefinitionItem>
					<DefinitionItem label="Economic" monospace>{current.reputation.economic.toFixed(6)}</DefinitionItem>
					<DefinitionItem label="Social" monospace>{current.reputation.social.toFixed(6)}</DefinitionItem>
					<DefinitionItem label="Validation" monospace>{current.reputation.validation.toFixed(6)}</DefinitionItem>
					<DefinitionItem label="Trust" monospace>{current.reputation.trust.toFixed(6)}</DefinitionItem>
					<DefinitionItem label="Block ref" monospace>{String(current.reputation.block_ref ?? '—')}</DefinitionItem>
					<DefinitionItem label="Tips received" monospace>{String(current.reputation.tips_received)}</DefinitionItem>
					<DefinitionItem label="Validations passed" monospace>{String(current.reputation.validations_passed)}</DefinitionItem>
					<DefinitionItem label="Updated" monospace>{current.reputation.updated_at || '—'}</DefinitionItem>
				</DefinitionList>
			{:else}
				<Alert variant="info" title="No reputation yet">
					<Text size="sm">Reputation is computed by the scheduled recompute job.</Text>
				</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Registration</Heading>
			{/snippet}

			{#if registration}
				<TextArea value={prettyJSON(registration)} readonly rows={12} />
			{:else}
				<Alert variant="info" title="No registration file">
					<Text size="sm">Registration file not found (yet).</Text>
				</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Validations</Heading>
			{/snippet}

			{#if validations?.validations?.length}
				<div class="soul-agent__validations">
					{#each validations.validations as v (v.challenge_id)}
						<Card variant="outlined" padding="md">
							<div class="soul-agent__validation">
								<div class="soul-agent__validation-left">
									<Text size="sm" weight="medium">{v.challenge_type}</Text>
									<Text size="sm" color="secondary">Result: {v.result} (score {v.score})</Text>
									<Text size="sm" color="secondary">Evaluated: {v.evaluated_at}</Text>
								</div>
								<div class="soul-agent__validation-right">
									<CopyButton size="sm" text={v.challenge_id} />
								</div>
							</div>
						</Card>
					{/each}
				</div>
			{:else}
				<Alert variant="info" title="No validations">
					<Text size="sm">No validation history for this agent.</Text>
				</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Wallet rotation</Heading>
			{/snippet}

			<Text size="sm" color="secondary">
				Rotation requires signatures from both the current wallet and the new wallet (EIP-712 typed data).
			</Text>

			{#if rotationBeginError}
				<Alert variant="error" title="Begin rotation failed">{rotationBeginError}</Alert>
			{/if}
			{#if signError}
				<Alert variant="error" title="Signing failed">{signError}</Alert>
			{/if}
			{#if rotationConfirmError}
				<Alert variant="error" title="Confirm rotation failed">{rotationConfirmError}</Alert>
			{/if}

			<div class="soul-agent__form">
				<TextField label="New wallet" bind:value={rotationNewWallet} placeholder="0x…" />
				<div class="soul-agent__row">
					<Button variant="solid" onclick={() => void beginRotation()} disabled={rotationBeginLoading}>Begin</Button>
				</div>
			</div>

			{#if rotationBeginLoading}
				<div class="soul-agent__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Starting rotation…</Text>
				</div>
			{/if}

			{#if rotationBeginResult}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<Heading level={4} size="lg">Rotation request</Heading>
					{/snippet}
					<DefinitionList>
						<DefinitionItem label="Current wallet" monospace>{rotationBeginResult.rotation.current_wallet}</DefinitionItem>
						<DefinitionItem label="New wallet" monospace>{rotationBeginResult.rotation.new_wallet}</DefinitionItem>
						<DefinitionItem label="Nonce" monospace>{rotationBeginResult.rotation.nonce}</DefinitionItem>
						<DefinitionItem label="Deadline" monospace>{String(rotationBeginResult.rotation.deadline)}</DefinitionItem>
						<DefinitionItem label="Digest" monospace>{rotationBeginResult.typed_data.digest_hex}</DefinitionItem>
					</DefinitionList>

					<div class="soul-agent__row soul-agent__row--sign">
						<Button variant="outline" onclick={() => void signWithCurrentWallet()} disabled={signCurrentLoading}>Sign as current wallet</Button>
						{#if sigCurrent}
							<CopyButton size="sm" text={sigCurrent} />
						{/if}
					</div>
					<div class="soul-agent__row soul-agent__row--sign">
						<Button variant="outline" onclick={() => void signWithNewWallet()} disabled={signNewLoading}>Sign as new wallet</Button>
						{#if sigNew}
							<CopyButton size="sm" text={sigNew} />
						{/if}
					</div>

					<div class="soul-agent__row soul-agent__row--confirm">
						<Button variant="solid" onclick={() => void confirmRotation()} disabled={rotationConfirmLoading}>Confirm + create operation</Button>
					</div>

					{#if rotationConfirmLoading}
						<div class="soul-agent__loading-inline">
							<Spinner size="sm" />
							<Text size="sm">Confirming…</Text>
						</div>
					{/if}

					{#if rotationConfirmResult}
						<Card variant="outlined" padding="lg">
							{#snippet header()}
								<Heading level={5} size="lg">Operation</Heading>
							{/snippet}
							<Text size="sm" color="secondary">
								Operation <span class="soul-agent__mono">{rotationConfirmResult.operation.operation_id}</span>
							</Text>
							<div class="soul-agent__row">
								<CopyButton size="sm" text={rotationConfirmResult.operation.operation_id} />
							</div>

							{#if rotationConfirmResult.safe_tx}
								<DefinitionList>
									<DefinitionItem label="Safe" monospace>{rotationConfirmResult.safe_tx.safe_address}</DefinitionItem>
									<DefinitionItem label="To" monospace>{rotationConfirmResult.safe_tx.to}</DefinitionItem>
									<DefinitionItem label="Value" monospace>{rotationConfirmResult.safe_tx.value}</DefinitionItem>
									<DefinitionItem label="Data" monospace>{rotationConfirmResult.safe_tx.data}</DefinitionItem>
								</DefinitionList>
							{/if}
						</Card>
					{/if}
				</Card>
			{/if}
		</Card>
	{:else}
		<Alert variant="warning" title="Not found">
			<Text size="sm">Agent not found.</Text>
		</Alert>
	{/if}
</div>

<style>
	.soul-agent {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.soul-agent__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.soul-agent__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.soul-agent__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.soul-agent__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.soul-agent__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-3);
	}

	.soul-agent__row--sign {
		justify-content: flex-start;
	}

	.soul-agent__row--confirm {
		margin-top: var(--gr-spacing-scale-4);
	}

	.soul-agent__row-left {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.soul-agent__row-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.soul-agent__form {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.soul-agent__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.soul-agent__validations {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-3);
	}

	.soul-agent__validation {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.soul-agent__validation-left {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.soul-agent__validation-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.soul-agent__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
