<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type {
		SoulMintConversation,
		SoulMintConversationFinalizeBeginResponse,
		SoulMintConversationFinalizeResponse,
		SoulPublicAgentResponse,
	} from 'src/lib/api/soul';
	import {
		soulAgentCompleteMintConversation,
		soulAgentGetMintConversation,
		soulAgentListMintConversations,
		soulAgentMintConversationFinalize,
		soulAgentMintConversationFinalizeBegin,
		soulPublicGetAgent,
		soulStartAgentMintConversationSSE,
	} from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { MarkdownRenderer } from 'src/lib/greater/content';
	import { navigate } from 'src/lib/router';
	import { ensureAccounts, getEthereumProvider, personalSign } from 'src/lib/wallet/ethereum';
	import { keccak256Utf8Hex } from 'src/lib/wallet/keccak';
	import { Alert, Button, Card, CopyButton, DefinitionItem, DefinitionList, Heading, Select, Spinner, Text, TextArea } from 'src/lib/ui';

	let { token, agentId } = $props<{ token: string; agentId: string }>();

	type MintMessage = { role: 'user' | 'assistant'; content: string };
	type ProducedBoundary = { id: string; statement: string };

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let agent = $state<SoulPublicAgentResponse | null>(null);
	let conversations = $state<SoulMintConversation[]>([]);

	let mintModel = $state('anthropic:claude-sonnet-4-6');
	const mintModelOptions = [
		{ value: 'anthropic:claude-sonnet-4-6', label: 'Anthropic — Claude Sonnet 4.6' },
		{ value: 'openai:gpt-5.4', label: 'OpenAI — GPT-5.4' },
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

	let mintFinalizeLoading = $state(false);
	let mintFinalizeError = $state<string | null>(null);
	let mintFinalizeBegin = $state<SoulMintConversationFinalizeBeginResponse | null>(null);
	let mintFinalizeResult = $state<SoulMintConversationFinalizeResponse | null>(null);

	const lifecycleStatus = $derived.by(() => {
		const current = agent?.agent;
		return current?.lifecycle_status || current?.status || '';
	});

	const publishedVersion = $derived(agent?.agent?.self_description_version ?? 0);
	const mintBoundaryCount = $derived(extractProducedBoundaries(mintProducedDeclarations).length);
	const mintFinalizePromptCount = $derived(mintBoundaryCount > 0 ? mintBoundaryCount + 1 : 0);

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
				if ((role === 'user' || role === 'assistant') && content) out.push({ role, content });
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

	function extractProducedBoundaries(value: unknown): ProducedBoundary[] {
		if (!value || typeof value !== 'object') return [];
		const boundaries = (value as { boundaries?: unknown }).boundaries;
		if (!Array.isArray(boundaries)) return [];

		const out: ProducedBoundary[] = [];
		for (const item of boundaries) {
			if (!item || typeof item !== 'object') continue;
			const id = String((item as { id?: unknown }).id || '').trim();
			const statement = String((item as { statement?: unknown }).statement || '').trim();
			if (id && statement) out.push({ id, statement });
		}
		return out;
	}

	function parseSseEvent(raw: string): { event: string; data: unknown } | null {
		const lines = raw
			.split('\n')
			.map((line) => line.trimEnd())
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

	function parseSseErrorMessage(value: unknown): string {
		if (typeof value === 'string') {
			const trimmed = value.trim();
			return trimmed || 'stream error';
		}
		if (value == null) return 'stream error';
		if (typeof value !== 'object') {
			const asString = String(value).trim();
			return asString || 'stream error';
		}

		const record = value as Record<string, unknown>;
		for (const key of ['message', 'error', 'detail']) {
			const nested = parseSseErrorMessage(record[key]);
			if (nested !== 'stream error') return nested;
		}
		if (typeof record.code === 'string' && record.code.trim()) return record.code.trim();

		try {
			const json = JSON.stringify(value);
			return json && json !== '{}' ? json : 'stream error';
		} catch {
			return 'stream error';
		}
	}

	function resetMintConversation() {
		mintConversationId = '';
		mintConversation = null;
		mintMessages = [];
		mintUserMessage = '';
		mintAssistantPartial = '';
		mintError = null;
		mintCompleteError = null;
		mintFinalizeError = null;
		mintFinalizeBegin = null;
		mintFinalizeResult = null;
		mintProducedDeclarations = null;
	}

	async function refreshMintConversation(conversationId: string) {
		mintError = null;
		try {
			mintConversation = await soulAgentGetMintConversation(token, agentId, conversationId);
			mintConversationId = conversationId;
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

	async function load() {
		errorMessage = null;
		mintError = null;
		loading = true;
		try {
			const [agentRes, convRes] = await Promise.all([
				soulPublicGetAgent(agentId),
				soulAgentListMintConversations(token, agentId, 20),
			]);
			agent = agentRes;
			const nextConversations = convRes.conversations || [];
			conversations = nextConversations;

			const nextConversation =
				nextConversations.find((item) => item.status === 'in_progress') ||
				nextConversations.find((item) => item.status === 'completed') ||
				null;

			if (nextConversation?.conversation_id) {
				await refreshMintConversation(nextConversation.conversation_id);
			} else {
				resetMintConversation();
			}
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			errorMessage = formatError(err);
		} finally {
			loading = false;
		}
	}

	async function sendMintMessage() {
		mintError = null;
		mintCompleteError = null;
		mintFinalizeError = null;

		if ((agent?.agent?.self_description_version || 0) > 0) {
			mintError = 'This agent already published its self-description. Use registration updates for later edits.';
			return;
		}

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
			let streamFailed = false;
			const streamOrSource = soulStartAgentMintConversationSSE(token, agentId, {
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
						const data = parsed.data as { conversation_id?: unknown };
						const conversationId = String(data.conversation_id || '').trim();
						if (conversationId) mintConversationId = conversationId;
					}
					if (parsed.event === 'delta') {
						const data = parsed.data as { text?: unknown };
						const text = String(data.text || '');
						if (text) mintAssistantPartial += text;
					}
					if (parsed.event === 'conversation_done') {
						const data = parsed.data as { full_response?: unknown };
						const full = String(data.full_response || '');
						const assistantText = full || mintAssistantPartial;
						if (assistantText) mintMessages = [...mintMessages, { role: 'assistant', content: assistantText }];
						mintAssistantPartial = '';
						if (mintConversationId) {
							await refreshMintConversation(mintConversationId);
						}
					}
					if (parsed.event === 'error') {
						mintError = parseSseErrorMessage(parsed.data);
						streamFailed = true;
					}
				}
			}
			if (!streamFailed) {
				await load();
			}
		} catch (err) {
			mintError = formatError(err);
		} finally {
			mintStreaming = false;
		}
	}

	async function completeMintConversation() {
		mintCompleteError = null;
		if (!mintConversationId) {
			mintCompleteError = 'No conversation in progress.';
			return;
		}
		mintCompleteLoading = true;
		try {
			mintConversation = await soulAgentCompleteMintConversation(token, agentId, mintConversationId);
			mintProducedDeclarations = parseProducedDeclarations(mintConversation.produced_declarations);
			await load();
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

	async function finalizeMintConversation() {
		mintFinalizeError = null;
		mintFinalizeBegin = null;
		mintFinalizeResult = null;

		const currentAgent = agent?.agent;
		if (!currentAgent) {
			mintFinalizeError = 'Agent is not loaded yet.';
			return;
		}
		const currentVersion = currentAgent.self_description_version ?? 0;
		if (currentVersion > 0) {
			mintFinalizeError = `This agent already published v${currentVersion}.`;
			return;
		}
		if (!mintConversationId) {
			mintFinalizeError = 'No completed conversation to finalize.';
			return;
		}
		if (!mintProducedDeclarations) {
			mintFinalizeError = 'Complete the conversation first to produce declarations.';
			return;
		}

		const boundaries = extractProducedBoundaries(mintProducedDeclarations);
		if (!boundaries.length) {
			mintFinalizeError = 'No boundaries found in produced declarations.';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			mintFinalizeError = 'No wallet detected. Install or enable a wallet extension.';
			return;
		}

		const wallet = currentAgent.wallet?.trim() || '';
		if (!wallet) {
			mintFinalizeError = 'Agent wallet address is required.';
			return;
		}

		mintFinalizeLoading = true;
		try {
			const accounts = await ensureAccounts(provider);
			const normalized = accounts.map((account) => account.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				mintFinalizeError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			const boundarySignatures: Record<string, string> = {};
			for (const boundary of boundaries) {
				const digestHex = keccak256Utf8Hex(boundary.statement);
				boundarySignatures[boundary.id] = await personalSign(provider, digestHex, wallet);
			}

			mintFinalizeBegin = await soulAgentMintConversationFinalizeBegin(token, agentId, mintConversationId, {
				boundary_signatures: boundarySignatures,
			});

			const selfAttestation = await personalSign(provider, mintFinalizeBegin.digest_hex, wallet);

			mintFinalizeResult = await soulAgentMintConversationFinalize(token, agentId, mintConversationId, {
				boundary_signatures: boundarySignatures,
				issued_at: mintFinalizeBegin.issued_at,
				expected_version: mintFinalizeBegin.expected_version,
				self_attestation: selfAttestation,
			});
			await load();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			mintFinalizeError = formatError(err);
		} finally {
			mintFinalizeLoading = false;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="soul-mint">
	<header class="soul-mint__header">
		<div class="soul-mint__title">
			<Heading level={2} size="xl">Complete profile</Heading>
			<Text color="secondary"><span class="soul-mint__mono">{agentId}</span></Text>
		</div>
		<div class="soul-mint__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/souls/${agentId}`)}>Back to agent</Button>
		</div>
	</header>

	{#if loading}
		<div class="soul-mint__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Profile conversation">{errorMessage}</Alert>
	{:else if agent}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Agent</Heading>
			{/snippet}
			<DefinitionList>
				<DefinitionItem label="Domain" monospace>{agent.agent.domain}</DefinitionItem>
				<DefinitionItem label="Local ID" monospace>{agent.agent.local_id}</DefinitionItem>
				<DefinitionItem label="Wallet" monospace>{agent.agent.wallet}</DefinitionItem>
				<DefinitionItem label="Status" monospace>{lifecycleStatus}</DefinitionItem>
				<DefinitionItem label="Published version" monospace>{agent.agent.self_description_version || '—'}</DefinitionItem>
			</DefinitionList>

			{#if publishedVersion > 0}
				<Alert variant="info" title="Profile already published">
					This agent already published its self-description as version v{publishedVersion}.
					Use the registration update tools for later edits.
				</Alert>
			{:else}
				<Alert variant="info" title="Pick up where you left off">
					The mint step is already done. This conversation is the follow-up profile step where you draft and publish
					the self-description, capabilities, boundaries, and transparency record for this agent.
				</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Profile conversation</Heading>
			{/snippet}

			{#if mintError}
				<Alert variant="error" title="Conversation error">{mintError}</Alert>
			{/if}
			{#if mintCompleteError}
				<Alert variant="error" title="Complete conversation">{mintCompleteError}</Alert>
			{/if}
			{#if mintFinalizeError}
				<Alert variant="error" title="Publish profile">{mintFinalizeError}</Alert>
			{/if}

			<div class="soul-mint__controls">
				<Select
					options={mintModelOptions}
					value={mintModel}
					disabled={Boolean(mintConversationId)}
					onchange={(value: string) => {
						mintModel = value;
					}}
				/>
				<Button variant="outline" onclick={() => resetMintConversation()} disabled={mintStreaming || mintCompleteLoading || mintFinalizeLoading}>
					Start fresh
				</Button>
				<Button variant="outline" onclick={() => void refreshMintConversation(mintConversationId)} disabled={!mintConversationId || mintStreaming}>
					Refresh
				</Button>
				<Button
					variant="outline"
					onclick={() => void completeMintConversation()}
					disabled={!mintConversationId || mintStreaming || mintCompleteLoading || publishedVersion > 0}
				>
					Complete conversation
				</Button>
			</div>

			{#if conversations.length > 0}
				<div class="soul-mint__history">
					<Text size="sm" color="secondary">Saved conversations: {conversations.length}</Text>
					<div class="soul-mint__history-list">
						{#each conversations as conversation (conversation.conversation_id)}
							<Button
								variant={conversation.conversation_id === mintConversationId ? 'solid' : 'outline'}
								size="sm"
								onclick={() => void refreshMintConversation(conversation.conversation_id)}
								disabled={mintStreaming}
							>
								{conversation.status === 'in_progress' ? 'Resume' : 'Open'} {conversation.conversation_id.slice(0, 10)}
							</Button>
						{/each}
					</div>
				</div>
			{/if}

			{#if mintConversationId}
				<div class="soul-mint__meta">
					<Text size="sm" color="secondary">
						Conversation <span class="soul-mint__mono">{mintConversationId}</span>
					</Text>
					<CopyButton size="sm" text={mintConversationId} />
				</div>
			{/if}

			{#if mintConversation?.model}
				<Text size="sm" color="secondary">
					Model: <span class="soul-mint__mono">{mintConversation.model}</span> ({mintConversation.status})
				</Text>
			{/if}

			<div class="soul-mint__messages">
				{#if mintMessages.length === 0 && !mintAssistantPartial}
					<Text size="sm" color="secondary">No profile conversation yet. Send a first message to begin.</Text>
				{/if}
				{#each mintMessages as msg, i (`${msg.role}-${i}`)}
					<div class={`soul-mint__bubble soul-mint__bubble--${msg.role}`}>
						<Text size="sm" weight="medium">{msg.role === 'assistant' ? 'Assistant' : 'You'}</Text>
						{#if msg.role === 'assistant'}
							<div class="soul-mint__markdown">
								<MarkdownRenderer content={msg.content} />
							</div>
						{:else}
							<div class="soul-mint__plain">{msg.content}</div>
						{/if}
					</div>
				{/each}
				{#if mintAssistantPartial}
					<div class="soul-mint__bubble soul-mint__bubble--assistant">
						<Text size="sm" weight="medium">Assistant</Text>
						<div class="soul-mint__markdown">
							<MarkdownRenderer content={mintAssistantPartial} />
						</div>
					</div>
				{/if}
			</div>

			<TextArea label="Your message" bind:value={mintUserMessage} rows={5} placeholder="Describe the agent, its purpose, limits, and how it should present itself." />
			<div class="soul-mint__row">
				<Button variant="solid" onclick={() => void sendMintMessage()} disabled={mintStreaming || publishedVersion > 0}>
					Send message
				</Button>
				{#if mintStreaming}
					<div class="soul-mint__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Thinking…</Text>
					</div>
				{/if}
			</div>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Drafted profile</Heading>
			{/snippet}

			{#if mintProducedDeclarations}
				<TextArea value={prettyJSON(mintProducedDeclarations)} readonly rows={16} />
				{#if mintFinalizePromptCount > 0}
					<Text size="sm" color="secondary">
						Publishing currently requires one wallet signature per boundary plus one final publication signature.
						This draft will prompt MetaMask {mintFinalizePromptCount} times.
					</Text>
				{/if}
				<div class="soul-mint__row">
					<Button
						variant="solid"
						onclick={() => void finalizeMintConversation()}
						disabled={mintFinalizeLoading || publishedVersion > 0}
					>
						Finalize and publish
					</Button>
					{#if mintFinalizeLoading}
						<div class="soul-mint__loading-inline">
							<Spinner size="sm" />
							<Text size="sm">Waiting for signatures to publish…</Text>
						</div>
					{/if}
				</div>
			{:else}
				<Text size="sm" color="secondary">Complete the conversation to generate the structured profile declaration for publication.</Text>
			{/if}

			{#if mintFinalizeBegin}
				<DefinitionList>
					<DefinitionItem label="Expected version" monospace>{mintFinalizeBegin.expected_version}</DefinitionItem>
					<DefinitionItem label="Next version" monospace>{mintFinalizeBegin.next_version}</DefinitionItem>
					<DefinitionItem label="Issued at" monospace>{mintFinalizeBegin.issued_at}</DefinitionItem>
				</DefinitionList>
			{/if}

			{#if mintFinalizeResult}
				<Alert variant="success" title="Profile published">
					Published version v{mintFinalizeResult.published_version} for this agent.
				</Alert>
			{/if}
		</Card>
	{/if}
</div>

<style>
	.soul-mint {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.soul-mint__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.soul-mint__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.soul-mint__actions,
	.soul-mint__controls,
	.soul-mint__row,
	.soul-mint__meta {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.soul-mint__loading,
	.soul-mint__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.soul-mint__messages {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		margin: var(--gr-spacing-scale-3) 0;
	}

	.soul-mint__history {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		margin-top: var(--gr-spacing-scale-2);
	}

	.soul-mint__history-list {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		flex-wrap: wrap;
	}

	.soul-mint__bubble {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		padding: var(--gr-spacing-scale-3);
		border-radius: var(--gr-radius-lg);
		border: 1px solid var(--gr-border-subtle);
		background: var(--gr-surface-subtle);
	}

	.soul-mint__bubble--assistant {
		background: var(--gr-surface-primary-soft);
	}

	.soul-mint__plain {
		white-space: pre-wrap;
		color: var(--gr-semantic-foreground-primary);
		font: inherit;
		line-height: 1.5;
	}

	.soul-mint__markdown {
		color: var(--gr-semantic-foreground-primary);
	}

	.soul-mint__markdown :global(p:first-child),
	.soul-mint__markdown :global(ul:first-child),
	.soul-mint__markdown :global(ol:first-child),
	.soul-mint__markdown :global(blockquote:first-child),
	.soul-mint__markdown :global(pre:first-child) {
		margin-top: 0;
	}

	.soul-mint__markdown :global(p:last-child),
	.soul-mint__markdown :global(ul:last-child),
	.soul-mint__markdown :global(ol:last-child),
	.soul-mint__markdown :global(blockquote:last-child),
	.soul-mint__markdown :global(pre:last-child) {
		margin-bottom: 0;
	}

	.soul-mint__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
