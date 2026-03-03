<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { SoulAgentRegistrationBeginResponse, SoulAgentRegistrationVerifyResponse, SoulMintConversation } from 'src/lib/api/soul';
	import {
		soulAgentRegistrationBegin,
		soulAgentRegistrationVerify,
		soulCompleteMintConversation,
		soulGetMintConversation,
		soulStartMintConversationSSE,
	} from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { getEthereumProvider, personalSign, requestAccounts } from 'src/lib/wallet/ethereum';
	import { keccak256Utf8Hex } from 'src/lib/wallet/keccak';
	import {
		Alert,
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

	let principalAddress = $state('');
	let principalDeclaration = $state("I accept responsibility for this agent's behavior.");
	let principalSignature = $state('');
	let principalDeclaredAt = $state('');
	let principalSignLoading = $state(false);
	let principalSignError = $state<string | null>(null);

	let verifyLoading = $state(false);
	let verifyError = $state<string | null>(null);
	let verifyResult = $state<SoulAgentRegistrationVerifyResponse | null>(null);

	const dnsProof = $derived.by(() => beginResult?.proofs?.find((p) => p.method === 'dns_txt') || null);
	const httpsProof = $derived.by(() => beginResult?.proofs?.find((p) => p.method === 'https_well_known') || null);

	// --- Minting conversation (Phase 2) ---

	type MintMessage = { role: 'user' | 'assistant'; content: string };

	let mintModel = $state('anthropic:claude-sonnet-4-20250514');
	const mintModelOptions = [
		{ value: 'anthropic:claude-sonnet-4-20250514', label: 'Anthropic — Claude Sonnet 4' },
		{ value: 'openai:gpt-4.1-mini', label: 'OpenAI — GPT-4.1 mini' },
	];

	let mintConversationId = $state('');
	let mintConversation = $state<SoulMintConversation | null>(null);
	let mintMessages = $state<MintMessage[]>([]);
	let mintUserMessage = $state('');
	let mintAssistantPartial = $state('');
	let mintStreaming = $state(false);
	let mintError = $state<string | null>(null);
	let mintCompleteLoading = $state(false);
	let mintCompleteError = $state<string | null>(null);
	let mintProducedDeclarations = $state<unknown | null>(null);

	function mintStorageKey(registrationId: string): string {
		return `soul_mint_conversation:${registrationId}`;
	}

	function parseMintMessages(raw?: string): MintMessage[] {
		if (!raw) return [];
		try {
			const parsed = JSON.parse(raw) as unknown;
			if (!Array.isArray(parsed)) return [];
			const out: MintMessage[] = [];
			for (const item of parsed) {
				if (!item || typeof item !== 'object') continue;
				const role = String((item as { role?: unknown }).role || '').toLowerCase();
				const content = String((item as { content?: unknown }).content || '');
				if ((role === 'user' || role === 'assistant') && content) {
					out.push({ role, content });
				}
			}
			return out;
		} catch {
			return [];
		}
	}

	function parseProducedDeclarations(raw?: string): unknown | null {
		if (!raw) return null;
		try {
			return JSON.parse(raw);
		} catch {
			return raw;
		}
	}

	async function loadMintConversationFromStorage(registrationId: string) {
		mintError = null;
		mintCompleteError = null;
		mintProducedDeclarations = null;
		mintConversation = null;
		mintMessages = [];
		mintAssistantPartial = '';

		try {
			const stored = localStorage.getItem(mintStorageKey(registrationId));
			const cid = (stored || '').trim();
			if (!cid) return;
			mintConversationId = cid;
			await refreshMintConversation(registrationId, cid);
		} catch {
			// ignore (e.g. storage disabled)
		}
	}

	async function refreshMintConversation(registrationId: string, conversationId: string) {
		mintError = null;
		try {
			mintConversation = await soulGetMintConversation(token, registrationId, conversationId);
			mintMessages = parseMintMessages(mintConversation.messages);
			mintProducedDeclarations = parseProducedDeclarations(mintConversation.produced_declarations);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			mintError = formatError(err);
		}
	}

	function resetMintConversation(registrationId: string) {
		mintConversationId = '';
		mintConversation = null;
		mintMessages = [];
		mintUserMessage = '';
		mintAssistantPartial = '';
		mintError = null;
		mintCompleteError = null;
		mintProducedDeclarations = null;
		try {
			localStorage.removeItem(mintStorageKey(registrationId));
		} catch {
			// ignore
		}
	}

	function parseSseEvent(raw: string): { event: string; data: unknown } | null {
		const lines = raw
			.split('\n')
			.map((l) => l.trimEnd())
			.filter(Boolean);
		let eventName = 'message';
		const dataLines: string[] = [];
		for (const line of lines) {
			if (line.startsWith('event:')) {
				eventName = line.slice('event:'.length).trim() || 'message';
				continue;
			}
			if (line.startsWith('data:')) {
				dataLines.push(line.slice('data:'.length).trim());
			}
		}
		const dataStr = dataLines.join('\n').trim();
		if (!dataStr) return null;
		try {
			return { event: eventName, data: JSON.parse(dataStr) as unknown };
		} catch {
			return { event: eventName, data: dataStr };
		}
	}

	async function sendMintMessage(registrationId: string) {
		mintError = null;
		mintCompleteError = null;

		const msg = mintUserMessage.trim();
		if (!msg) {
			mintError = 'Message is required.';
			return;
		}
		mintUserMessage = '';
		mintMessages = [...mintMessages, { role: 'user', content: msg }];
		mintAssistantPartial = '';

		mintStreaming = true;
		try {
			const streamOrSource = soulStartMintConversationSSE(token, registrationId, {
				model: mintConversationId ? undefined : mintModel,
				conversation_id: mintConversationId || undefined,
				message: msg,
			});
			if (!streamOrSource || typeof (streamOrSource as ReadableStream<string>).getReader !== 'function') {
				mintError = 'Streaming not supported in this environment.';
				return;
			}

			const reader = (streamOrSource as ReadableStream<string>).getReader();
			let buffer = '';
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += value || '';

				while (true) {
					const idx = buffer.indexOf('\n\n');
					if (idx === -1) break;
					const raw = buffer.slice(0, idx);
					buffer = buffer.slice(idx + 2);
					const parsed = parseSseEvent(raw);
					if (!parsed) continue;

					if (parsed.event === 'conversation_start') {
						const d = parsed.data as { conversation_id?: unknown; model?: unknown };
						const cid = String(d.conversation_id || '').trim();
						if (cid) {
							mintConversationId = cid;
							try {
								localStorage.setItem(mintStorageKey(registrationId), cid);
							} catch {
								// ignore
							}
						}
					}

					if (parsed.event === 'delta') {
						const d = parsed.data as { text?: unknown };
						const t = String(d.text || '');
						if (t) mintAssistantPartial += t;
					}

					if (parsed.event === 'conversation_done') {
						const d = parsed.data as { full_response?: unknown };
						const full = String(d.full_response || '');
						const assistantText = full || mintAssistantPartial;
						if (assistantText) mintMessages = [...mintMessages, { role: 'assistant', content: assistantText }];
						mintAssistantPartial = '';

						// Sync persisted record for history + produced declarations.
						if (mintConversationId) {
							await refreshMintConversation(registrationId, mintConversationId);
						}
					}

					if (parsed.event === 'error') {
						const d = parsed.data as { error?: unknown; message?: unknown };
						mintError = String(d.error || d.message || 'stream error');
					}
				}
			}
		} catch (err) {
			mintError = formatError(err);
		} finally {
			mintStreaming = false;
		}
	}

	async function completeMintConversation(registrationId: string) {
		mintCompleteError = null;
		if (!mintConversationId) {
			mintCompleteError = 'No conversation in progress.';
			return;
		}
		mintCompleteLoading = true;
		try {
			mintConversation = await soulCompleteMintConversation(token, registrationId, mintConversationId);
			mintProducedDeclarations = parseProducedDeclarations(mintConversation.produced_declarations);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			mintCompleteError = formatError(err);
		} finally {
			mintCompleteLoading = false;
		}
	}

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function prettyJSON(value: unknown): string {
		if (value == null) return '';
		try {
			return JSON.stringify(value, null, 2);
		} catch {
			return String(value);
		}
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
		principalSignError = null;
		verifyError = null;
		beginResult = null;
		verifyResult = null;
		signature = '';
		principalSignature = '';
		principalDeclaredAt = '';
		mintConversationId = '';
		mintConversation = null;
		mintMessages = [];
		mintAssistantPartial = '';
		mintError = null;
		mintCompleteError = null;
		mintProducedDeclarations = null;

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
			const res = await soulAgentRegistrationBegin(token, {
				domain: nextDomain,
				local_id: nextLocal,
				wallet_address: nextWallet,
				capabilities: parseCapabilities(capabilities),
			});
			beginResult = res;
			principalAddress = nextWallet;
			principalDeclaration = `I accept responsibility for the lesser-soul agent ${res.registration.domain_normalized}/${res.registration.local_id} (agentId: ${res.registration.agent_id}).`;
			await loadMintConversationFromStorage(res.registration.id);
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

	async function signPrincipal() {
		principalSignError = null;
		principalSignature = '';

		const provider = getEthereumProvider();
		if (!provider) {
			principalSignError = 'No wallet detected. Install or enable a wallet extension.';
			return;
		}

		const addr = principalAddress.trim();
		if (!addr) {
			principalSignError = 'Principal address is required.';
			return;
		}

		const decl = principalDeclaration.trim();
		if (!decl) {
			principalSignError = 'Declaration is required.';
			return;
		}

		principalSignLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(addr.toLowerCase())) {
				principalSignError = 'Connected wallet does not match the principal address above.';
				return;
			}

			const digestHex = keccak256Utf8Hex(decl);
			principalSignature = await personalSign(provider, digestHex, addr);
			principalDeclaredAt = new Date().toISOString();
		} catch (err) {
			principalSignError = formatError(err);
		} finally {
			principalSignLoading = false;
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
		if (!principalAddress.trim()) {
			verifyError = 'Principal address is required.';
			return;
		}
		if (!principalDeclaration.trim()) {
			verifyError = 'Principal declaration is required.';
			return;
		}
		if (!principalSignature.trim()) {
			verifyError = 'Principal signature is required. Sign the declaration first.';
			return;
		}

		verifyLoading = true;
		try {
			verifyResult = await soulAgentRegistrationVerify(token, beginResult.registration.id, {
				signature,
				principal_address: principalAddress.trim(),
				principal_declaration: principalDeclaration.trim(),
				principal_signature: principalSignature.trim(),
				declared_at: principalDeclaredAt || new Date().toISOString(),
			});
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
				{#if principalSignError}
					<Alert variant="error" title="Principal signing failed">{principalSignError}</Alert>
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

				<Text size="sm" color="secondary">
					Sign the responsibility statement with your principal wallet.
				</Text>
				<TextField label="Principal wallet" bind:value={principalAddress} placeholder="0x…" />
				<TextArea label="Principal declaration" bind:value={principalDeclaration} rows={4} />
				<div class="soul-register__row">
					<Button variant="outline" onclick={() => void signPrincipal()} disabled={principalSignLoading}>
						Sign principal
					</Button>
					{#if principalSignature}
						<CopyButton size="sm" text={principalSignature} />
					{/if}
				</div>

				{#if principalSignLoading}
					<div class="soul-register__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Waiting for principal wallet…</Text>
					</div>
				{/if}

				{#if principalSignature}
					<Text size="sm" color="secondary">
						Principal signature <span class="soul-register__mono">{principalSignature.slice(0, 14)}…</span>
					</Text>
				{/if}

				<div class="soul-register__row soul-register__row--verify">
					<Button
						variant="solid"
						onclick={() => void verifyRegistration()}
						disabled={verifyLoading || !signature || !principalSignature}
					>
						Verify + create mint operation
					</Button>
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

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">4. Minting conversation (Phase 2, optional)</Heading>
			{/snippet}

			<Text size="sm" color="secondary">
				Stream a guided conversation to produce structured declarations (selfDescription, capabilities, boundaries, transparency). You can complete the conversation to store
				the produced declarations on the conversation record.
			</Text>

			{#if mintError}
				<Alert variant="error" title="Mint conversation">{mintError}</Alert>
			{/if}
			{#if mintCompleteError}
				<Alert variant="error" title="Complete conversation">{mintCompleteError}</Alert>
			{/if}

			<div class="soul-register__row soul-register__mint-controls">
				<Select
					options={mintModelOptions}
					value={mintModel}
					disabled={Boolean(mintConversationId)}
					onchange={(value: string) => {
						mintModel = value;
					}}
				/>

				{#if mintConversationId}
					<Text size="sm" color="secondary">
						Conversation <span class="soul-register__mono">{mintConversationId}</span>
					</Text>
					<CopyButton size="sm" text={mintConversationId} />
				{/if}
			</div>

			<div class="soul-register__row">
				<Button variant="outline" onclick={() => resetMintConversation(begin.registration.id)} disabled={mintStreaming || mintCompleteLoading}>
					Start new
				</Button>
				<Button
					variant="outline"
					onclick={() => void refreshMintConversation(begin.registration.id, mintConversationId)}
					disabled={!mintConversationId || mintStreaming}
				>
					Refresh
				</Button>
				<Button
					variant="solid"
					onclick={() => void completeMintConversation(begin.registration.id)}
					disabled={!mintConversationId || mintStreaming || mintCompleteLoading}
				>
					Complete
				</Button>
			</div>

			{#if mintCompleteLoading}
				<div class="soul-register__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Completing…</Text>
				</div>
			{/if}

			{#if mintConversation?.model}
				<Text size="sm" color="secondary">
					Model: <span class="soul-register__mono">{mintConversation.model}</span> ({mintConversation.status})
				</Text>
			{/if}

			{#if mintMessages.length || mintAssistantPartial}
				<div class="soul-register__mint-thread">
					{#each mintMessages as m, i (i)}
						<Card variant="outlined" padding="md">
							<Text size="sm" weight="medium">{m.role}</Text>
							<Text size="sm" color="secondary" style="white-space: pre-wrap">{m.content}</Text>
						</Card>
					{/each}
					{#if mintAssistantPartial}
						<Card variant="outlined" padding="md">
							<Text size="sm" weight="medium">assistant (streaming)</Text>
							<Text size="sm" color="secondary" style="white-space: pre-wrap">{mintAssistantPartial}</Text>
						</Card>
					{/if}
				</div>
			{:else}
				<Alert variant="info" title="No conversation yet">
					<Text size="sm">Send the first message to start a new conversation.</Text>
				</Alert>
			{/if}

			<div class="soul-register__form">
				<TextArea bind:value={mintUserMessage} rows={3} placeholder="Ask about self-description, capabilities, boundaries, or transparency…" />
				<div class="soul-register__row">
					<Button variant="solid" onclick={() => void sendMintMessage(begin.registration.id)} disabled={mintStreaming}>
						Send
					</Button>
				</div>

				{#if mintStreaming}
					<div class="soul-register__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Streaming…</Text>
					</div>
				{/if}
			</div>

			{#if mintProducedDeclarations}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<Heading level={4} size="lg">Produced declarations</Heading>
					{/snippet}
					<TextArea value={prettyJSON(mintProducedDeclarations)} readonly rows={14} />
				</Card>
			{/if}
		</Card>
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

	.soul-register__mint-controls {
		margin-top: var(--gr-spacing-scale-4);
	}

	.soul-register__mint-thread {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}
</style>
