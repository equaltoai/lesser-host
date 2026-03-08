<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import { getPortalMe } from 'src/lib/api/portal';
	import { portalListInstances } from 'src/lib/api/portalInstances';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import type {
		SoulConfigResponse,
		SoulAgentRegistrationBeginResponse,
		SoulAgentRegistrationVerifyResponse,
		SoulMintConversation,
		SoulMintConversationFinalizeBeginResponse,
		SoulMintConversationFinalizeResponse,
	} from 'src/lib/api/soul';
	import {
		soulAgentRegistrationBegin,
		soulAgentRegistrationVerify,
		soulCompleteMintConversation,
		soulGetMintConversation,
		soulMintConversationFinalize,
		soulMintConversationFinalizeBegin,
		soulPublicGetConfig,
		soulRecordAgentMintExecution,
		soulStartMintConversationSSE,
	} from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { MarkdownRenderer } from 'src/lib/greater/content';
	import { navigate } from 'src/lib/router';
	import {
		ensureAccounts,
		getChainId,
		getEthereumProvider,
		personalSign,
		requestAccounts,
		sendEthereumTransaction,
		switchEthereumChain,
		waitForEthereumTransactionReceipt,
	} from 'src/lib/wallet/ethereum';
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
	let portalUsername = $state('');

	let instances = $state<InstanceResponse[]>([]);
	let instancesLoading = $state(false);
	let instancesError = $state<string | null>(null);
	let selectedInstanceSlug = $state('');

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
	let showVerifySafePayload = $state(false);
	let soulConfig = $state<SoulConfigResponse | null>(null);
	let mintDirectLoading = $state(false);
	let mintDirectError = $state<string | null>(null);
	let mintDirectNotice = $state<string | null>(null);
	let mintDirectTxHash = $state('');

	const dnsProof = $derived.by(() => beginResult?.proofs?.find((p) => p.method === 'dns_txt') || null);
	const httpsProof = $derived.by(() => beginResult?.proofs?.find((p) => p.method === 'https_well_known') || null);
	const verifyUsesSafe = $derived.by(() => Boolean(verifyResult?.safe_tx?.safe_address?.trim()));
	const verifyMintExecuted = $derived.by(() => (verifyResult?.operation?.status || '').toLowerCase() === 'executed');

	// --- Minting conversation (Phase 2) ---

	type MintMessage = { role: 'user' | 'assistant'; content: string };

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
	const mintBoundaryCount = $derived(extractProducedBoundaries(mintProducedDeclarations).length);
	const mintFinalizePromptCount = $derived(mintBoundaryCount > 0 ? mintBoundaryCount + 1 : 0);

	const selectedInstance = $derived.by(
		() => instances.find((instance) => instance.slug === selectedInstanceSlug) || null
	);
	const instanceOptions = $derived.by(() =>
		instances.map((instance) => {
			const managedDomain = instance.managed_lesser_domain?.trim() || instance.hosted_base_domain?.trim() || '';
			return {
				value: instance.slug,
				label: managedDomain ? `${instance.slug} (${managedDomain})` : instance.slug,
			};
		})
	);
	const managedProofsSatisfied = $derived.by(
		() => Boolean(beginResult?.registration?.dns_verified && beginResult?.registration?.https_verified)
	);

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

	type ProducedBoundary = { id: string; statement: string };

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

	async function loadMintConversationFromStorage(registrationId: string) {
		mintError = null;
		mintCompleteError = null;
		mintFinalizeError = null;
		mintFinalizeBegin = null;
		mintFinalizeResult = null;
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
		mintFinalizeError = null;
		mintFinalizeBegin = null;
		mintFinalizeResult = null;
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
						mintError = parseSseErrorMessage(parsed.data);
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

	async function finalizeMintConversation(registrationId: string) {
		mintFinalizeError = null;
		mintFinalizeBegin = null;
		mintFinalizeResult = null;

		if (!verifyResult) {
			mintFinalizeError = 'Verify the registration first (Step 3) to create the pending agent identity.';
			return;
		}
		if (!mintConversationId) {
			mintFinalizeError = 'No completed conversation to finalize.';
			return;
		}
		if (!mintProducedDeclarations) {
			mintFinalizeError = 'Complete the mint conversation first to produce declarations.';
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

		const wallet = beginResult?.registration?.wallet_address?.trim() || walletAddress.trim();
		if (!wallet) {
			mintFinalizeError = 'Agent wallet address is required.';
			return;
		}

		mintFinalizeLoading = true;
		try {
			const accounts = await ensureAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				mintFinalizeError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			const boundarySignatures: Record<string, string> = {};
			for (const b of boundaries) {
				const digestHex = keccak256Utf8Hex(b.statement);
				boundarySignatures[b.id] = await personalSign(provider, digestHex, wallet);
			}

			mintFinalizeBegin = await soulMintConversationFinalizeBegin(token, registrationId, mintConversationId, {
				boundary_signatures: boundarySignatures,
			});

			const selfAttestation = await personalSign(provider, mintFinalizeBegin.digest_hex, wallet);

			mintFinalizeResult = await soulMintConversationFinalize(token, registrationId, mintConversationId, {
				boundary_signatures: boundarySignatures,
				issued_at: mintFinalizeBegin.issued_at,
				expected_version: mintFinalizeBegin.expected_version,
				self_attestation: selfAttestation,
			});
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

	function preferredDomainForInstance(instance: InstanceResponse | null | undefined): string {
		if (!instance) return '';
		return instance.managed_lesser_domain?.trim() || instance.hosted_base_domain?.trim() || '';
	}

	function applySelectedInstance(slug: string) {
		selectedInstanceSlug = slug;
		const instance = instances.find((item) => item.slug === slug);
		const preferredDomain = preferredDomainForInstance(instance);
		if (preferredDomain) {
			domain = preferredDomain;
		}
	}

	function inferWalletAddressFromUsername(username: string): string {
		const normalized = username.trim().toLowerCase();
		if (!normalized.startsWith('wallet-')) return '';

		const hex = normalized.slice('wallet-'.length);
		return /^0x[0-9a-f]{40}$/.test(hex) ? hex : '';
	}

	async function populateWalletAddress() {
		const provider = getEthereumProvider();
		if (provider) {
			try {
				const accounts = (await provider.request({ method: 'eth_accounts' })) as unknown;
				if (Array.isArray(accounts) && accounts.length > 0) {
					walletAddress = String(accounts[0]);
					return;
				}
			} catch {
				// Ignore silent wallet probes and fall back to the portal username.
			}
		}

		const inferred = inferWalletAddressFromUsername(portalUsername);
		if (inferred) {
			walletAddress = inferred;
		}
	}

	async function loadRegistrationDefaults() {
		instancesLoading = true;
		instancesError = null;

		try {
			const [me, list] = await Promise.all([getPortalMe(token), portalListInstances(token)]);
			portalUsername = me.username || '';
			instances = list.instances || [];

			const params = new URLSearchParams(window.location.search);
			const presetSlug = params.get('slug')?.trim() || '';
			const presetDomain = params.get('domain')?.trim() || '';
			const presetLocal = params.get('local_id') || params.get('localId') || '';

			if (presetLocal) {
				localId = presetLocal;
			} else if (!localId.trim()) {
				localId = 'agent-0';
			}

			if (presetDomain) {
				domain = presetDomain;
			}

			let selected =
				(presetSlug && instances.find((instance) => instance.slug === presetSlug)) ||
				(presetDomain &&
					instances.find((instance) => preferredDomainForInstance(instance).toLowerCase() === presetDomain.toLowerCase())) ||
				(instances.length === 1 ? instances[0] : null);

			if (selected) {
				applySelectedInstance(selected.slug);
			}

			if (!walletAddress.trim()) {
				await populateWalletAddress();
			}
			if (!capabilities.trim()) {
				capabilities = 'social';
			}
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			instancesError = formatError(err);
		} finally {
			instancesLoading = false;
		}
	}

	async function loadSoulConfig() {
		try {
			soulConfig = await soulPublicGetConfig();
		} catch {
			soulConfig = null;
		}
	}

	async function executeDirectMint(agentId: string) {
		mintDirectError = null;
		mintDirectNotice = null;

		const payload = verifyResult?.safe_tx;
		if (!payload) {
			mintDirectError = 'No mint transaction payload is available yet.';
			return;
		}
		if (payload.safe_address?.trim()) {
			mintDirectError = 'This mint still requires Safe execution.';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			mintDirectError = 'No wallet detected. Install or enable a wallet extension.';
			return;
		}

		const wallet = beginResult?.registration?.wallet_address?.trim() || walletAddress.trim();
		if (!wallet) {
			mintDirectError = 'Agent wallet address is required.';
			return;
		}

		mintDirectLoading = true;
		let txHash = '';
		try {
			const accounts = await ensureAccounts(provider);
			const normalized = accounts.map((account) => account.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				mintDirectError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			const expectedChainId = soulConfig?.chain_id;
			if (expectedChainId) {
				const currentChainId = await getChainId(provider);
				if (currentChainId !== expectedChainId) {
					mintDirectNotice = `Switching wallet to chain ${expectedChainId}…`;
					await switchEthereumChain(provider, expectedChainId);
				}
			}

			mintDirectNotice = 'Sending mint transaction from the connected wallet…';
			txHash = await sendEthereumTransaction(provider, {
				from: wallet,
				to: payload.to,
				value: payload.value,
				data: payload.data,
			});
			mintDirectTxHash = txHash;

			mintDirectNotice = 'Transaction submitted. Waiting for Sepolia confirmation…';
			await waitForEthereumTransactionReceipt(provider, txHash, 10 * 60 * 1000, 3000);

			mintDirectNotice = 'Transaction confirmed. Recording execution in lesser-host…';
			const updated = await soulRecordAgentMintExecution(token, agentId, txHash);
			if (verifyResult) {
				verifyResult = {
					...verifyResult,
					operation: updated.operation,
					safe_tx: updated.safe_tx ?? verifyResult.safe_tx,
				};
			}
			mintDirectNotice = 'Mint confirmed onchain and recorded. You can continue the profile conversation now.';
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			const base = formatError(err);
			mintDirectError = txHash
				? `${base}. Transaction sent as ${txHash}. You can recover it from the agent detail page if needed.`
				: base;
		} finally {
			mintDirectLoading = false;
		}
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
		mintFinalizeError = null;
		mintFinalizeBegin = null;
		mintFinalizeResult = null;
		mintProducedDeclarations = null;
		mintDirectError = null;
		mintDirectNotice = null;
		mintDirectTxHash = '';

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
		showVerifySafePayload = false;
		mintDirectError = null;
		mintDirectNotice = null;
		mintDirectTxHash = '';

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
		void loadRegistrationDefaults();
		void loadSoulConfig();
	});
</script>

<div class="soul-register">
	<header class="soul-register__header">
		<div class="soul-register__title">
			<Heading level={2} size="xl">Register agent</Heading>
			<Text color="secondary">
				Choose your managed instance, confirm the wallet, and prepare the onchain mint from your connected wallet.
			</Text>
		</div>
		<div class="soul-register__actions">
			<Button variant="ghost" onclick={() => navigate('/portal/souls')}>Back</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">1. Start registration</Heading>
		{/snippet}

		{#if beginError}
			<Alert variant="error" title="Registration start failed">{beginError}</Alert>
		{/if}
		{#if instancesError}
			<Alert variant="error" title="Failed to load instance defaults">{instancesError}</Alert>
		{/if}

		<div class="soul-register__form">
			{#if instances.length > 0}
				<div class="soul-register__field">
					<Text size="sm">Instance</Text>
					<Select options={instanceOptions} value={selectedInstanceSlug} onchange={(value: string) => applySelectedInstance(value)} />
					{#if selectedInstance}
						<Text size="sm" color="secondary">
							Using {preferredDomainForInstance(selectedInstance)} for this {selectedInstance.status} managed instance.
						</Text>
					{/if}
				</div>
			{/if}
			<TextField label="Domain" bind:value={domain} placeholder="example.com" />
			<TextField label="Local ID" bind:value={localId} placeholder="agent-alice" />
			<TextField label="Wallet" bind:value={walletAddress} placeholder="0x…" />
			<TextField label="Capabilities (comma-separated)" bind:value={capabilities} placeholder="social, commerce" />
			<div class="soul-register__row">
				<Button variant="solid" onclick={() => void handleBegin()} disabled={beginLoading || instancesLoading}>Start registration</Button>
				<Button variant="outline" onclick={() => void useConnectedWallet()} disabled={beginLoading}>Use connected wallet</Button>
			</div>
		</div>

		{#if instancesLoading || beginLoading}
			<div class="soul-register__loading-inline">
				<Spinner size="sm" />
				<Text size="sm">{instancesLoading ? 'Loading your instance…' : 'Creating registration…'}</Text>
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
					<Heading level={3} size="lg">2. Domain ownership</Heading>
				{/snippet}
				{#if managedProofsSatisfied}
					<Alert variant="success" title="Managed instance ownership already verified">
						This agent is using your provisioned managed instance domain, so lesser-host already trusts the DNS and HTTPS surface.
						No manual proof publishing is required for this registration.
					</Alert>
				{:else}
					<Text size="sm" color="secondary">
						Both DNS TXT and HTTPS well-known proofs must be published before verification will succeed.
					</Text>
				{/if}

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
						Verify + prepare mint
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
							<Heading level={4} size="lg">{verifyMintExecuted ? 'Mint completed' : 'Mint transaction ready'}</Heading>
						{/snippet}

						{#if verifyMintExecuted}
							<Alert variant="success" title="Soul mint recorded">
								<Text size="sm">
									The mint transaction has been confirmed onchain and recorded in lesser-host. You can continue the
									profile conversation now or open the agent page to review the live identity.
								</Text>
							</Alert>
						{:else if verifyUsesSafe}
							<Alert variant="warning" title="One more step is still required">
								<Text size="sm">
									This environment still expects Safe execution. Execute the mint transaction, wait for it to land
									onchain, then record the execution tx hash from the agent page to activate it.
								</Text>
							</Alert>
						{:else}
							<Alert variant="info" title="Ready to mint from your wallet">
								<Text size="sm">
									The agent is verified and the mint transaction is ready. Send it from the connected agent wallet
									below, and lesser-host will record the execution after it confirms onchain.
								</Text>
							</Alert>
						{/if}

						<DefinitionList>
							<DefinitionItem label="Operation" monospace>{verifyResult.operation.operation_id}</DefinitionItem>
							<DefinitionItem label="Status" monospace>{verifyResult.operation.status}</DefinitionItem>
						</DefinitionList>

						{#if verifyUsesSafe}
							<div class="soul-register__steps">
								<Text size="sm">1. Open the agent detail page.</Text>
								<Text size="sm">2. Execute the Safe transaction there or in your Safe workflow.</Text>
								<Text size="sm">3. Paste the mined tx hash back into the pending mint panel to activate the soul.</Text>
							</div>
						{:else if !verifyMintExecuted}
							<div class="soul-register__steps">
								<Text size="sm">1. Use the connected agent wallet to submit the mint transaction.</Text>
								<Text size="sm">2. Keep this page open while lesser-host waits for Sepolia confirmation.</Text>
								<Text size="sm">3. If needed, recover the tx later from the agent detail page.</Text>
							</div>
						{/if}

						{#if mintDirectNotice}
							<Alert variant="info" title="Direct mint">{mintDirectNotice}</Alert>
						{/if}
						{#if mintDirectError}
							<Alert variant="error" title="Direct mint">{mintDirectError}</Alert>
						{/if}

						<div class="soul-register__row">
							{#if !verifyUsesSafe && !verifyMintExecuted && verifyResult.safe_tx}
								<Button variant="solid" onclick={() => void executeDirectMint(begin.registration.agent_id)} disabled={mintDirectLoading}>
									{mintDirectLoading ? 'Minting…' : 'Mint now with connected wallet'}
								</Button>
							{/if}
							<Button variant="solid" onclick={() => navigate(`/portal/souls/${begin.registration.agent_id}`)}>
								{verifyUsesSafe || !verifyMintExecuted ? 'Open mint recovery' : 'Open agent'}
							</Button>
							<Button variant="outline" onclick={() => navigate(`/portal/souls/${begin.registration.agent_id}/mint`)}>
								Complete profile
							</Button>
							<CopyButton size="sm" text={verifyResult.operation.operation_id} />
						</div>

						{#if mintDirectLoading}
							<div class="soul-register__loading-inline">
								<Spinner size="sm" />
								<Text size="sm">Waiting for wallet confirmation and chain settlement…</Text>
							</div>
						{/if}
						{#if mintDirectTxHash}
							<div class="soul-register__row">
								<Text size="sm" color="secondary">
									Execution tx <span class="soul-register__mono">{mintDirectTxHash}</span>
								</Text>
								<CopyButton size="sm" text={mintDirectTxHash} />
							</div>
						{/if}

						{#if verifyResult.safe_tx}
							<div class="soul-register__row">
								<Button variant="outline" onclick={() => (showVerifySafePayload = !showVerifySafePayload)}>
									{showVerifySafePayload ? 'Hide transaction data' : 'Show transaction data'}
								</Button>
								<CopyButton size="sm" text={prettyJSON(verifyResult.safe_tx)} />
							</div>
							{#if showVerifySafePayload}
								<DefinitionList>
									<DefinitionItem label="To" monospace>{verifyResult.safe_tx.to}</DefinitionItem>
									<DefinitionItem label="Value" monospace>{verifyResult.safe_tx.value}</DefinitionItem>
									{#if verifyResult.safe_tx.safe_address}
										<DefinitionItem label="Safe" monospace>{verifyResult.safe_tx.safe_address}</DefinitionItem>
									{/if}
								</DefinitionList>
								<TextArea readonly rows={6} value={verifyResult.safe_tx.data} label="Transaction data" />
							{/if}
						{/if}
					</Card>
				{/if}
			</Card>
		</div>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">4. Profile conversation (after mint, optional)</Heading>
			{/snippet}

			<Text size="sm" color="secondary">
				After the mint operation is created, use this guided conversation to draft the published profile:
				selfDescription, capabilities, boundaries, and transparency. Completing the conversation stores the produced
				declarations on the conversation record.
			</Text>

			{#if mintError}
				<Alert variant="error" title="Profile conversation">{mintError}</Alert>
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
					Start fresh
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
							{#if m.role === 'assistant'}
								<div class="soul-register__markdown">
									<MarkdownRenderer content={m.content} />
								</div>
							{:else}
								<div class="soul-register__plain">{m.content}</div>
							{/if}
						</Card>
					{/each}
					{#if mintAssistantPartial}
						<Card variant="outlined" padding="md">
							<Text size="sm" weight="medium">assistant (streaming)</Text>
							<div class="soul-register__markdown">
								<MarkdownRenderer content={mintAssistantPartial} />
							</div>
						</Card>
					{/if}
				</div>
			{:else}
				<Alert variant="info" title="No conversation yet">
					<Text size="sm">Send the first message to start the profile conversation.</Text>
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
						<Heading level={4} size="lg">Drafted profile</Heading>
					{/snippet}
					<TextArea value={prettyJSON(mintProducedDeclarations)} readonly rows={14} />
				</Card>

				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<Heading level={4} size="lg">Publish profile</Heading>
					{/snippet}

					<Text size="sm" color="secondary">
						This signs each boundary statement, then signs the full v2 registration self-attestation and publishes
						the first profile version to the registry.
					</Text>
					{#if mintFinalizePromptCount > 0}
						<Text size="sm" color="secondary">
							Current draft: {mintBoundaryCount} boundary signatures + 1 final publication signature = {mintFinalizePromptCount} wallet prompts.
						</Text>
					{/if}

					{#if !verifyResult}
						<Alert variant="info" title="Verify first">
							<Text size="sm">Complete Step 3 (verify) before finalizing, so the pending agent identity exists.</Text>
						</Alert>
					{/if}

					{#if mintFinalizeError}
						<Alert variant="error" title="Publish profile">{mintFinalizeError}</Alert>
					{/if}

					<div class="soul-register__row">
						<Button
							variant="solid"
							onclick={() => void finalizeMintConversation(begin.registration.id)}
							disabled={!verifyResult || mintFinalizeLoading || mintStreaming || mintCompleteLoading}
						>
							Publish profile
						</Button>
					</div>

					{#if mintFinalizeLoading}
						<div class="soul-register__loading-inline">
							<Spinner size="sm" />
							<Text size="sm">Finalizing…</Text>
						</div>
					{/if}

					{#if mintFinalizeBegin}
						<DefinitionList>
							<DefinitionItem label="Issued at" monospace>{mintFinalizeBegin.issued_at}</DefinitionItem>
							<DefinitionItem label="Expected version" monospace>{String(mintFinalizeBegin.expected_version)}</DefinitionItem>
							<DefinitionItem label="Next version" monospace>{String(mintFinalizeBegin.next_version)}</DefinitionItem>
							<DefinitionItem label="Digest" monospace>{mintFinalizeBegin.digest_hex}</DefinitionItem>
						</DefinitionList>
					{/if}

					{#if mintFinalizeResult}
						<Alert variant="success" title="Published">
							<Text size="sm">Published v2 registration version {mintFinalizeResult.published_version}.</Text>
						</Alert>
						<div class="soul-register__row">
							<Button variant="outline" onclick={() => navigate(`/portal/souls/${begin.registration.agent_id}`)}>Open agent</Button>
							<Button variant="solid" onclick={() => navigate(`/portal/souls/${begin.registration.agent_id}/mint`)}>
								Review profile
							</Button>
						</div>
					{/if}
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

	.soul-register__field {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
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

	.soul-register__steps {
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

	.soul-register__plain {
		white-space: pre-wrap;
		color: var(--gr-semantic-foreground-primary);
		font: inherit;
		line-height: 1.5;
	}

	.soul-register__markdown {
		color: var(--gr-semantic-foreground-primary);
	}

	.soul-register__markdown :global(p:first-child),
	.soul-register__markdown :global(ul:first-child),
	.soul-register__markdown :global(ol:first-child),
	.soul-register__markdown :global(blockquote:first-child),
	.soul-register__markdown :global(pre:first-child) {
		margin-top: 0;
	}

	.soul-register__markdown :global(p:last-child),
	.soul-register__markdown :global(ul:last-child),
	.soul-register__markdown :global(ol:last-child),
	.soul-register__markdown :global(blockquote:last-child),
	.soul-register__markdown :global(pre:last-child) {
		margin-bottom: 0;
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
