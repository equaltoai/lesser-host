<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type {
		SoulPublicAgentResponse,
		SoulPublicValidationsResponse,
		SoulPublicBoundariesResponse,
		SoulPublicContinuityResponse,
		SoulPublicRelationshipsResponse,
		SoulPublicVersionsResponse,
		SoulPublicCapabilitiesResponse,
		SoulPublicFailuresResponse,
		SoulRotateWalletBeginResponse,
		SoulRotateWalletConfirmResponse,
	} from 'src/lib/api/soul';
	import {
		soulAgentRotateWalletBegin,
		soulAgentRotateWalletConfirm,
		soulPublicGetAgent,
		soulPublicGetRegistration,
		soulPublicGetValidations,
		soulPublicGetBoundaries,
		soulPublicGetContinuity,
		soulPublicGetRelationships,
		soulPublicGetVersions,
		soulPublicGetCapabilities,
		soulPublicGetTransparency,
		soulPublicGetFailures,
		soulAddBoundaryBegin,
		soulAddBoundary,
		soulSelfSuspend,
		soulSelfReinstate,
		soulArchiveAgentBegin,
		soulArchiveAgent,
		soulDesignateSuccessorBegin,
		soulDesignateSuccessor,
	} from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { getEthereumProvider, personalSign, requestAccounts, signTypedDataV4 } from 'src/lib/wallet/ethereum';
	import { keccak256Utf8Hex } from 'src/lib/wallet/keccak';
	import {
		Alert,
		Badge,
		Button,
		Card,
		CopyButton,
		DefinitionItem,
		DefinitionList,
		Heading,
		Select,
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
	let boundaries = $state<SoulPublicBoundariesResponse | null>(null);
	let continuity = $state<SoulPublicContinuityResponse | null>(null);
	let relationships = $state<SoulPublicRelationshipsResponse | null>(null);
	let versions = $state<SoulPublicVersionsResponse | null>(null);
	let capabilities = $state<SoulPublicCapabilitiesResponse | null>(null);
	let transparency = $state<unknown | null>(null);
	let failures = $state<SoulPublicFailuresResponse | null>(null);

	let activeSection = $state('identity');

	// Wallet rotation state
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

	// Sovereignty state
	let sovereigntyLoading = $state(false);
	let sovereigntyError = $state<string | null>(null);
	let suspendReason = $state('');
	let successorId = $state('');

	// Boundary creation state
	let boundaryAddError = $state<string | null>(null);
	let boundaryAddLoading = $state(false);
	let boundaryId = $state('');
	let boundaryCategory = $state('refusal');
	let boundaryStatement = $state('');
	let boundaryRationale = $state('');
	let boundarySupersedes = $state('');
	let boundarySignature = $state('');
	let boundarySignLoading = $state(false);

	const boundaryCategoryOptions = [
		{ value: 'refusal', label: 'Refusal' },
		{ value: 'scope_limit', label: 'Scope limit' },
		{ value: 'ethical_commitment', label: 'Ethical commitment' },
		{ value: 'circuit_breaker', label: 'Circuit breaker' },
	];

	// Relationship filter
	let relTypeFilter = $state('');
	const relTypeOptions = [
		{ value: '', label: 'All types' },
		{ value: 'endorsement', label: 'Endorsement' },
		{ value: 'delegation', label: 'Delegation' },
		{ value: 'collaboration', label: 'Collaboration' },
		{ value: 'trust_grant', label: 'Trust grant' },
		{ value: 'trust_revocation', label: 'Trust revocation' },
	];

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
		if (s === 'suspended' || s === 'self_suspended') return { variant: 'filled', color: 'error' };
		if (s === 'archived' || s === 'succeeded') return { variant: 'outlined', color: 'gray' };
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

	async function signBoundary() {
		boundaryAddError = null;
		boundarySignature = '';

		const stmt = boundaryStatement.trim();
		if (!stmt) {
			boundaryAddError = 'Statement is required.';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			boundaryAddError = 'No wallet detected.';
			return;
		}
		const wallet = agent?.agent?.wallet?.trim();
		if (!wallet) {
			boundaryAddError = 'Agent wallet is not available.';
			return;
		}

		boundarySignLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				boundaryAddError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			const digestHex = keccak256Utf8Hex(stmt);
			boundarySignature = await personalSign(provider, digestHex, wallet);
		} catch (err) {
			boundaryAddError = formatError(err);
		} finally {
			boundarySignLoading = false;
		}
	}

	async function submitBoundary() {
		boundaryAddError = null;

		const id = boundaryId.trim();
		const category = boundaryCategory.trim();
		const statement = boundaryStatement.trim();
		const rationale = boundaryRationale.trim();
		const supersedes = boundarySupersedes.trim();
		const signature = boundarySignature.trim();

		if (!id) {
			boundaryAddError = 'Boundary ID is required.';
			return;
		}
		if (!category) {
			boundaryAddError = 'Category is required.';
			return;
		}
		if (!statement) {
			boundaryAddError = 'Statement is required.';
			return;
		}
		if (!signature) {
			boundaryAddError = 'Signature is required. Sign the statement first.';
			return;
		}

		boundaryAddLoading = true;
		try {
			const begin = await soulAddBoundaryBegin(token, agentId, {
				boundary_id: id,
				category,
				statement,
				rationale: rationale || undefined,
				supersedes: supersedes || undefined,
				signature,
			});

			const provider = getEthereumProvider();
			if (!provider) {
				boundaryAddError = 'No wallet detected.';
				return;
			}
			const wallet = agent?.agent?.wallet?.trim();
			if (!wallet) {
				boundaryAddError = 'Agent wallet is not available.';
				return;
			}

			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				boundaryAddError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			const selfAttestation = await personalSign(provider, begin.digest_hex, wallet);

			await soulAddBoundary(token, agentId, {
				boundary_id: id,
				category,
				statement,
				rationale: rationale || undefined,
				supersedes: supersedes || undefined,
				signature,
				issued_at: begin.issued_at,
				expected_version: begin.expected_version,
				self_attestation: selfAttestation,
			});
			boundaryId = '';
			boundaryStatement = '';
			boundaryRationale = '';
			boundarySupersedes = '';
			boundarySignature = '';
			boundaries = await soulPublicGetBoundaries(agentId, undefined, 50);
			activeSection = 'boundaries';
		} catch (err) {
			if (await handleAuthError(err)) return;
			boundaryAddError = formatError(err);
		} finally {
			boundaryAddLoading = false;
		}
	}

	async function handleAuthError(err: unknown): Promise<boolean> {
		if ((err as Partial<ApiError>).status === 401) {
			await logout();
			navigate('/login');
			return true;
		}
		return false;
	}

	async function load() {
		errorMessage = null;
		agent = null;
		registration = null;
		validations = null;
		boundaries = null;
		continuity = null;
		relationships = null;
		versions = null;
		capabilities = null;
		transparency = null;
		failures = null;

		loading = true;
		try {
			agent = await soulPublicGetAgent(agentId);
			const settled = await Promise.allSettled([
				soulPublicGetRegistration(agentId),
				soulPublicGetValidations(agentId, undefined, 50),
				soulPublicGetBoundaries(agentId, undefined, 50),
				soulPublicGetContinuity(agentId, undefined, 50),
				soulPublicGetRelationships(agentId, undefined, undefined, 50),
				soulPublicGetVersions(agentId, undefined, 50),
				soulPublicGetCapabilities(agentId),
				soulPublicGetTransparency(agentId),
				soulPublicGetFailures(agentId, undefined, 50),
			]);
			registration = settled[0].status === 'fulfilled' ? settled[0].value : null;
			validations = settled[1].status === 'fulfilled' ? settled[1].value : null;
			boundaries = settled[2].status === 'fulfilled' ? settled[2].value : null;
			continuity = settled[3].status === 'fulfilled' ? settled[3].value : null;
			relationships = settled[4].status === 'fulfilled' ? settled[4].value : null;
			versions = settled[5].status === 'fulfilled' ? settled[5].value : null;
			capabilities = settled[6].status === 'fulfilled' ? settled[6].value : null;
			transparency = settled[7].status === 'fulfilled' ? settled[7].value : null;
			failures = settled[8].status === 'fulfilled' ? settled[8].value : null;
		} catch (err) {
			errorMessage = formatError(err);
		} finally {
			loading = false;
		}
	}

	async function loadRelationships() {
		try {
			relationships = await soulPublicGetRelationships(agentId, relTypeFilter || undefined, undefined, 50);
		} catch {
			// ignore
		}
	}

	// --- Wallet rotation ---
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
			if (await handleAuthError(err)) return;
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
			if (await handleAuthError(err)) return;
			rotationConfirmError = formatError(err);
		} finally {
			rotationConfirmLoading = false;
		}
	}

	// --- Sovereignty ---
	async function doSelfSuspend() {
		sovereigntyError = null;
		sovereigntyLoading = true;
		try {
			await soulSelfSuspend(token, agentId, suspendReason.trim() || undefined);
			suspendReason = '';
			await load();
		} catch (err) {
			if (await handleAuthError(err)) return;
			sovereigntyError = formatError(err);
		} finally {
			sovereigntyLoading = false;
		}
	}

	async function doSelfReinstate() {
		sovereigntyError = null;
		sovereigntyLoading = true;
		try {
			await soulSelfReinstate(token, agentId);
			await load();
		} catch (err) {
			if (await handleAuthError(err)) return;
			sovereigntyError = formatError(err);
		} finally {
			sovereigntyLoading = false;
		}
	}

	async function doArchive() {
		sovereigntyError = null;
		sovereigntyLoading = true;
		try {
			const begin = await soulArchiveAgentBegin(token, agentId);

			const provider = getEthereumProvider();
			if (!provider) {
				sovereigntyError = 'No wallet detected.';
				return;
			}
			const wallet = agent?.agent?.wallet?.trim();
			if (!wallet) {
				sovereigntyError = 'Agent wallet is not available.';
				return;
			}

			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				sovereigntyError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			const signature = await personalSign(provider, begin.entry.digest_hex, wallet);
			await soulArchiveAgent(token, agentId, { timestamp: begin.entry.timestamp, signature });
			await load();
		} catch (err) {
			if (await handleAuthError(err)) return;
			sovereigntyError = formatError(err);
		} finally {
			sovereigntyLoading = false;
		}
	}

	async function doDesignateSuccessor() {
		sovereigntyError = null;
		const sid = successorId.trim();
		if (!sid) {
			sovereigntyError = 'Successor agent ID is required.';
			return;
		}
		sovereigntyLoading = true;
		try {
			const begin = await soulDesignateSuccessorBegin(token, agentId, sid);

			const provider = getEthereumProvider();
			if (!provider) {
				sovereigntyError = 'No wallet detected.';
				return;
			}

			const predWallet = agent?.agent?.wallet?.trim();
			if (!predWallet) {
				sovereigntyError = 'Agent wallet is not available.';
				return;
			}

			// Sign predecessor continuity entry with current agent wallet.
			{
				const accounts = await requestAccounts(provider);
				const normalized = accounts.map((a) => a.toLowerCase());
				if (!normalized.includes(predWallet.toLowerCase())) {
					sovereigntyError = `Connected wallet does not match agent wallet (${predWallet}).`;
					return;
				}
			}
			const predecessorSignature = await personalSign(provider, begin.predecessor_entry.digest_hex, predWallet);

			// Sign successor continuity entry with successor agent wallet.
			const succ = await soulPublicGetAgent(sid);
			const succWallet = succ?.agent?.wallet?.trim();
			if (!succWallet) {
				sovereigntyError = 'Successor agent wallet is not available.';
				return;
			}
			{
				const accounts = await requestAccounts(provider);
				const normalized = accounts.map((a) => a.toLowerCase());
				if (!normalized.includes(succWallet.toLowerCase())) {
					sovereigntyError = `Connected wallet does not match successor wallet (${succWallet}).`;
					return;
				}
			}
			const successorSignature = await personalSign(provider, begin.successor_entry.digest_hex, succWallet);

			await soulDesignateSuccessor(token, agentId, {
				successor_agent_id: sid,
				timestamp: begin.predecessor_entry.timestamp,
				predecessor_signature: predecessorSignature,
				successor_signature: successorSignature,
			});
			successorId = '';
			await load();
		} catch (err) {
			if (await handleAuthError(err)) return;
			sovereigntyError = formatError(err);
		} finally {
			sovereigntyLoading = false;
		}
	}

	const sections = [
		{ id: 'identity', label: 'Identity' },
		{ id: 'reputation', label: 'Reputation' },
		{ id: 'capabilities', label: 'Capabilities' },
		{ id: 'boundaries', label: 'Boundaries' },
		{ id: 'continuity', label: 'Continuity' },
		{ id: 'relationships', label: 'Relationships' },
		{ id: 'versions', label: 'Versions' },
		{ id: 'validations', label: 'Validations' },
		{ id: 'failures', label: 'Failures' },
		{ id: 'transparency', label: 'Transparency' },
		{ id: 'registration', label: 'Registration' },
		{ id: 'sovereignty', label: 'Sovereignty' },
		{ id: 'wallet', label: 'Wallet' },
	];

	let lifecycleStatus = $derived(agent?.agent?.lifecycle_status || agent?.agent?.status || '');
	let isTerminal = $derived(lifecycleStatus === 'archived' || lifecycleStatus === 'succeeded');
	let publicOrigin = $derived(typeof window !== 'undefined' ? window.location.origin : '');

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
		{@const statusBadge = badgeForStatus(lifecycleStatus)}

		<nav class="soul-agent__nav">
			{#each sections as section (section.id)}
				<button
					class="soul-agent__nav-item"
					class:soul-agent__nav-item--active={activeSection === section.id}
					onclick={() => (activeSection = section.id)}
				>
					{section.label}
				</button>
			{/each}
		</nav>

		{#if activeSection === 'identity'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<div class="soul-agent__row">
						<div class="soul-agent__row-left">
							<Heading level={3} size="lg">Identity</Heading>
							<Badge variant={statusBadge.variant} color={statusBadge.color} size="sm">{lifecycleStatus || current.agent.status}</Badge>
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
					{#if current.agent.principal_address}
						<DefinitionItem label="Principal" monospace>{current.agent.principal_address}</DefinitionItem>
					{/if}
					<DefinitionItem label="Meta URI" monospace>{current.agent.meta_uri || '—'}</DefinitionItem>
					<DefinitionItem label="Well-known URI" monospace>
						https://{current.agent.domain}/.well-known/lesser-soul-agent
					</DefinitionItem>
					{#if publicOrigin}
						<DefinitionItem label="Public soul endpoint" monospace>{publicOrigin}/api/v1/soul/agents/{current.agent.agent_id}</DefinitionItem>
						<DefinitionItem label="Public registration endpoint" monospace>
							{publicOrigin}/api/v1/soul/agents/{current.agent.agent_id}/registration
						</DefinitionItem>
						<DefinitionItem label="MCP soulUri (suggestion)" monospace>
							{publicOrigin}/api/v1/soul/agents/{current.agent.agent_id}/registration
						</DefinitionItem>
					{/if}
					{#if current.agent.self_description_version}
						<DefinitionItem label="Version" monospace>v{current.agent.self_description_version}</DefinitionItem>
					{/if}
					{#if current.agent.lifecycle_reason}
						<DefinitionItem label="Lifecycle reason">{current.agent.lifecycle_reason}</DefinitionItem>
					{/if}
					{#if current.agent.successor_agent_id}
						<DefinitionItem label="Successor" monospace>{current.agent.successor_agent_id}</DefinitionItem>
					{/if}
					{#if current.agent.predecessor_agent_id}
						<DefinitionItem label="Predecessor" monospace>{current.agent.predecessor_agent_id}</DefinitionItem>
					{/if}
					<DefinitionItem label="Updated" monospace>{current.agent.updated_at || '—'}</DefinitionItem>
				</DefinitionList>
			</Card>
		{/if}

		{#if activeSection === 'reputation'}
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
						{#if current.reputation.integrity != null}
							<DefinitionItem label="Integrity" monospace>{current.reputation.integrity.toFixed(6)}</DefinitionItem>
						{/if}
						<DefinitionItem label="Block ref" monospace>{String(current.reputation.block_ref ?? '—')}</DefinitionItem>
						<DefinitionItem label="Tips received" monospace>{String(current.reputation.tips_received)}</DefinitionItem>
						<DefinitionItem label="Validations passed" monospace>{String(current.reputation.validations_passed)}</DefinitionItem>
						{#if current.reputation.delegations_completed != null}
							<DefinitionItem label="Delegations completed" monospace>{String(current.reputation.delegations_completed)}</DefinitionItem>
						{/if}
						{#if current.reputation.boundary_violations != null}
							<DefinitionItem label="Boundary violations" monospace>{String(current.reputation.boundary_violations)}</DefinitionItem>
						{/if}
						{#if current.reputation.failure_recoveries != null}
							<DefinitionItem label="Failure recoveries" monospace>{String(current.reputation.failure_recoveries)}</DefinitionItem>
						{/if}
						<DefinitionItem label="Updated" monospace>{current.reputation.updated_at || '—'}</DefinitionItem>
					</DefinitionList>
				{:else}
					<Alert variant="info" title="No reputation yet">
						<Text size="sm">Reputation is computed by the scheduled recompute job.</Text>
					</Alert>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'capabilities'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Capabilities</Heading>
				{/snippet}

				{#if capabilities?.capabilities?.length}
					<div class="soul-agent__list">
						{#each capabilities.capabilities as cap (cap.capability)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<Text size="sm" weight="medium">{cap.capability}</Text>
										<Text size="sm" color="secondary">Claim: {cap.claim_level}</Text>
										{#if cap.scope}
											<Text size="sm" color="secondary">Scope: {cap.scope}</Text>
										{/if}
										{#if cap.constraints}
											<Text size="sm" color="secondary">Constraints: {prettyJSON(cap.constraints)}</Text>
										{/if}
										{#if cap.degrades_to}
											<Text size="sm" color="secondary">Degrades to: {cap.degrades_to}</Text>
										{/if}
									</div>
									{#if cap.last_validated}
										<div class="soul-agent__item-right">
											<Text size="sm" color="secondary">Validated: {cap.last_validated}</Text>
										</div>
									{/if}
								</div>
							</Card>
						{/each}
					</div>
				{:else}
					<Alert variant="info" title="No capabilities">
						<Text size="sm">No capabilities declared.</Text>
					</Alert>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'boundaries'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Boundaries</Heading>
				{/snippet}

				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<Heading level={4} size="lg">Add boundary</Heading>
					{/snippet}

					{#if boundaryAddError}
						<Alert variant="error" title="Boundary">{boundaryAddError}</Alert>
					{/if}

					<div class="soul-agent__form">
						<TextField label="Boundary ID" bind:value={boundaryId} placeholder="boundary-1" />
						<Select
							options={boundaryCategoryOptions}
							value={boundaryCategory}
							onchange={(value: string) => {
								boundaryCategory = value;
							}}
						/>
						<TextArea bind:value={boundaryStatement} rows={4} placeholder="Write a concrete refusal or constraint…" />
						<TextArea bind:value={boundaryRationale} rows={3} placeholder="Rationale (optional) …" />
						<TextField label="Supersedes (optional)" bind:value={boundarySupersedes} placeholder="boundary-id-to-supersede" />

						<div class="soul-agent__row">
							<Button variant="outline" onclick={() => void signBoundary()} disabled={boundarySignLoading || boundaryAddLoading}>
								Sign statement
							</Button>
							{#if boundarySignature}
								<CopyButton size="sm" text={boundarySignature} />
							{/if}
						</div>

						<div class="soul-agent__row">
							<Button variant="solid" onclick={() => void submitBoundary()} disabled={boundaryAddLoading || boundarySignLoading}>
								Submit boundary
							</Button>
						</div>

						{#if boundarySignLoading || boundaryAddLoading}
							<div class="soul-agent__loading-inline">
								<Spinner size="sm" />
								<Text size="sm">{boundarySignLoading ? 'Waiting for wallet…' : 'Submitting…'}</Text>
							</div>
						{/if}
					</div>
				</Card>

				{#if boundaries?.boundaries?.length}
					<div class="soul-agent__list">
						{#each boundaries.boundaries as b (b.boundary_id)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<div class="soul-agent__row-left">
											<Text size="sm" weight="medium">{b.statement}</Text>
											<Badge variant="outlined" color="gray" size="sm">{b.category}</Badge>
										</div>
										{#if b.rationale}
											<Text size="sm" color="secondary">{b.rationale}</Text>
										{/if}
										{#if b.supersedes}
											<Text size="sm" color="secondary">Supersedes: {b.supersedes}</Text>
										{/if}
										{#if b.added_in_version != null}
											<Text size="sm" color="secondary">Added in version: v{b.added_in_version}</Text>
										{/if}
										<Text size="sm" color="secondary">Added: {b.added_at}</Text>
									</div>
									<div class="soul-agent__item-right">
										<CopyButton size="sm" text={b.boundary_id} />
									</div>
								</div>
							</Card>
						{/each}
					</div>
				{:else}
					<Alert variant="info" title="No boundaries">
						<Text size="sm">No boundaries declared for this agent.</Text>
					</Alert>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'continuity'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Continuity</Heading>
				{/snippet}

				{#if continuity?.entries?.length}
					<div class="soul-agent__list">
						{#each continuity.entries as entry, i (i)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<div class="soul-agent__row-left">
											<Badge variant="outlined" color="gray" size="sm">{entry.type}</Badge>
											<Text size="sm" color="secondary">{entry.timestamp}</Text>
										</div>
										<Text size="sm">{entry.summary}</Text>
										{#if entry.recovery}
											<Text size="sm" color="secondary">Recovery: {entry.recovery}</Text>
										{/if}
									</div>
								</div>
							</Card>
						{/each}
					</div>
				{:else}
					<Alert variant="info" title="No continuity entries">
						<Text size="sm">No continuity history for this agent.</Text>
					</Alert>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'relationships'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<div class="soul-agent__row">
						<Heading level={3} size="lg">Relationships</Heading>
						<div class="soul-agent__row-right">
							<Select
								options={relTypeOptions}
								value={relTypeFilter}
								onchange={(value: string) => {
									relTypeFilter = value;
									void loadRelationships();
								}}
							/>
						</div>
					</div>
				{/snippet}

				{#if relationships?.relationships?.length}
					<div class="soul-agent__list">
						{#each relationships.relationships as rel, i (i)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<div class="soul-agent__row-left">
											<Badge variant="outlined" color="gray" size="sm">{rel.type}</Badge>
											<Text size="sm" color="secondary">{rel.created_at}</Text>
										</div>
										<Text size="sm">From: <span class="soul-agent__mono">{rel.from_agent_id}</span></Text>
										{#if rel.message}
											<Text size="sm" color="secondary">{rel.message}</Text>
										{/if}
										{#if rel.context}
											<Text size="sm" color="secondary">Context: {JSON.stringify(rel.context)}</Text>
										{/if}
									</div>
								</div>
							</Card>
						{/each}
					</div>
				{:else}
					<Alert variant="info" title="No relationships">
						<Text size="sm">No relationship records for this agent.</Text>
					</Alert>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'versions'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Version History</Heading>
				{/snippet}

				{#if versions?.versions?.length}
					<div class="soul-agent__list">
						{#each versions.versions as ver (ver.version_number)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<Text size="sm" weight="medium">v{ver.version_number}</Text>
										{#if ver.change_summary}
											<Text size="sm" color="secondary">{ver.change_summary}</Text>
										{/if}
										<Text size="sm" color="secondary">Created: {ver.created_at}</Text>
									</div>
									{#if ver.registration_uri}
										<div class="soul-agent__item-right">
											<CopyButton size="sm" text={ver.registration_uri} />
										</div>
									{/if}
								</div>
							</Card>
						{/each}
					</div>
				{:else}
					<Alert variant="info" title="No versions">
						<Text size="sm">No version history.</Text>
					</Alert>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'validations'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Validations</Heading>
				{/snippet}

				{#if validations?.validations?.length}
					<div class="soul-agent__list">
						{#each validations.validations as v (v.challenge_id)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<Text size="sm" weight="medium">{v.challenge_type}</Text>
										<Text size="sm" color="secondary">Result: {v.result} (score {v.score})</Text>
										<Text size="sm" color="secondary">Evaluated: {v.evaluated_at}</Text>
									</div>
									<div class="soul-agent__item-right">
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
		{/if}

		{#if activeSection === 'failures'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Failures</Heading>
				{/snippet}

				{#if failures?.failures?.length}
					<div class="soul-agent__list">
						{#each failures.failures as f (f.failure_id)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<div class="soul-agent__row-left">
											<Text size="sm" weight="medium">{f.failure_type}</Text>
											<Badge variant={f.status === 'recovered' ? 'filled' : 'outlined'} color={f.status === 'recovered' ? 'success' : 'error'} size="sm">{f.status || 'open'}</Badge>
										</div>
										{#if f.description}
											<Text size="sm" color="secondary">{f.description}</Text>
										{/if}
										{#if f.impact}
											<Text size="sm" color="secondary">Impact: {f.impact}</Text>
										{/if}
										{#if f.recovery_ref}
											<Text size="sm" color="secondary">Recovery ref: {f.recovery_ref}</Text>
										{/if}
										<Text size="sm" color="secondary">{f.timestamp}</Text>
									</div>
									<div class="soul-agent__item-right">
										<CopyButton size="sm" text={f.failure_id} />
									</div>
								</div>
							</Card>
						{/each}
					</div>
				{:else}
					<Alert variant="info" title="No failures">
						<Text size="sm">No failure records for this agent.</Text>
					</Alert>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'transparency'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Transparency</Heading>
				{/snippet}

				{#if transparency}
					<TextArea value={prettyJSON(transparency)} readonly rows={12} />
				{:else}
					<Alert variant="info" title="No transparency data">
						<Text size="sm">No transparency information available.</Text>
					</Alert>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'registration'}
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
		{/if}

		{#if activeSection === 'sovereignty'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Sovereignty</Heading>
				{/snippet}

				{#if sovereigntyError}
					<Alert variant="error" title="Action failed">{sovereigntyError}</Alert>
				{/if}

				{#if isTerminal}
					<Alert variant="info" title="Terminal state">
						<Text size="sm">This agent is <strong>{lifecycleStatus}</strong> and no further lifecycle actions can be taken.</Text>
					</Alert>
				{:else}
					<div class="soul-agent__form">
						{#if lifecycleStatus === 'self_suspended'}
							<Text size="sm" color="secondary">Agent is self-suspended. You can reinstate it.</Text>
							<Button variant="solid" onclick={() => void doSelfReinstate()} disabled={sovereigntyLoading}>Reinstate</Button>
						{:else}
							<Text size="sm" color="secondary">Suspend this agent. It can be reinstated later.</Text>
							<TextField label="Reason (optional)" bind:value={suspendReason} placeholder="Why are you suspending?" />
							<Button variant="outline" onclick={() => void doSelfSuspend()} disabled={sovereigntyLoading}>Self-suspend</Button>
						{/if}
					</div>

					<div class="soul-agent__form">
						<Text size="sm" color="secondary">Permanently archive this agent (one-way). Requires a wallet signature.</Text>
						<Button variant="outline" onclick={() => void doArchive()} disabled={sovereigntyLoading}>Archive</Button>
					</div>

					<div class="soul-agent__form">
						<Text size="sm" color="secondary">
							Designate a successor agent (one-way, marks this agent as succeeded). Requires signatures from both wallets.
						</Text>
						<TextField label="Successor agent ID" bind:value={successorId} placeholder="0x…" />
						<Button variant="outline" onclick={() => void doDesignateSuccessor()} disabled={sovereigntyLoading}>Designate successor</Button>
					</div>
				{/if}

				{#if sovereigntyLoading}
					<div class="soul-agent__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Processing…</Text>
					</div>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'wallet'}
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
		{/if}
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

	.soul-agent__nav {
		display: flex;
		gap: var(--gr-spacing-scale-1);
		flex-wrap: wrap;
		border-bottom: 1px solid var(--gr-color-border-secondary, #e0e0e0);
		padding-bottom: var(--gr-spacing-scale-2);
	}

	.soul-agent__nav-item {
		padding: var(--gr-spacing-scale-2) var(--gr-spacing-scale-3);
		border: none;
		background: none;
		cursor: pointer;
		font-size: 0.875rem;
		color: var(--gr-color-text-secondary, #666);
		border-radius: var(--gr-radius-md, 6px);
		transition: background 0.15s, color 0.15s;
	}

	.soul-agent__nav-item:hover {
		background: var(--gr-color-bg-secondary, #f5f5f5);
		color: var(--gr-color-text-primary, #111);
	}

	.soul-agent__nav-item--active {
		background: var(--gr-color-bg-secondary, #f0f0f0);
		color: var(--gr-color-text-primary, #111);
		font-weight: 600;
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

	.soul-agent__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-3);
	}

	.soul-agent__item {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.soul-agent__item-left {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		flex: 1;
		min-width: 0;
	}

	.soul-agent__item-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.soul-agent__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
