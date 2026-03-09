<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type {
		SoulMineAgentItem,
		SoulAgentMintOperationResponse,
		SoulPublicAgentResponse,
		SoulPublicAgentChannelsResponse,
		SoulConfigResponse,
		SoulPublicValidationsResponse,
		SoulPublicBoundariesResponse,
		SoulPublicContinuityResponse,
		SoulPublicRelationshipsResponse,
		SoulPublicVersionsResponse,
		SoulPublicCapabilitiesResponse,
		SoulPublicFailuresResponse,
		SoulRotateWalletBeginResponse,
		SoulRotateWalletConfirmResponse,
		SoulUpdateRegistrationResponse,
		SoulProvisionEmailBeginResponse,
		SoulProvisionEmailConfirmResponse,
		SoulProvisionPhoneBeginResponse,
		SoulProvisionPhoneConfirmResponse,
		SoulAgentCommActivityResponse,
		SoulAgentCommQueueResponse,
		SoulCommStatusResponse,
	} from 'src/lib/api/soul';
	import {
		soulAgentRotateWalletBegin,
		soulAgentRotateWalletConfirm,
		soulGetAgentMintOperation,
		soulAppendContinuity,
		soulCreateRelationship,
		soulListMyAgents,
		soulPublicGetConfig,
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
		soulPublicGetAgentChannels,
		soulProvisionEmailBegin,
		soulProvisionEmailConfirm,
		soulProvisionPhoneBegin,
		soulProvisionPhoneConfirm,
		soulRecordAgentMintExecution,
		soulDeprovisionPhone,
		soulAgentListCommActivity,
		soulAgentListCommQueue,
		soulAgentGetCommStatus,
		soulAddBoundaryBegin,
		soulAddBoundary,
		soulSelfSuspend,
		soulSelfReinstate,
		soulArchiveAgentBegin,
		soulArchiveAgent,
		soulDesignateSuccessorBegin,
		soulDesignateSuccessor,
		soulUpdateRegistration,
	} from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import {
		ensureAccounts,
		getChainId,
		getEthereumProvider,
		personalSign,
		requestAccounts,
		sendEthereumTransaction,
		signTypedDataV4,
		switchEthereumChain,
		waitForEthereumTransactionReceipt,
	} from 'src/lib/wallet/ethereum';
	import { jcsCanonicalize } from 'src/lib/wallet/jcs';
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
	let channels = $state<SoulPublicAgentChannelsResponse | null>(null);
	let mintOperation = $state<SoulAgentMintOperationResponse | null>(null);
	let mintOperationLoading = $state(false);
	let mintOperationError = $state<string | null>(null);
	let mintExecTxHash = $state('');
	let mintRecordLoading = $state(false);
	let mintRecordError = $state<string | null>(null);
	let showMintSafePayload = $state(false);
	let soulConfig = $state<SoulConfigResponse | null>(null);
	let mintDirectLoading = $state(false);
	let mintDirectError = $state<string | null>(null);
	let mintDirectNotice = $state<string | null>(null);
	let mintDirectTxHash = $state('');

	// Channel provisioning state
	let emailLocalPart = $state('');
	let emailProvisionBegin = $state<SoulProvisionEmailBeginResponse | null>(null);
	let emailProvisionSignature = $state('');
	let emailProvisionError = $state<string | null>(null);
	let emailProvisionBeginLoading = $state(false);
	let emailProvisionSignLoading = $state(false);
	let emailProvisionConfirmLoading = $state(false);
	let emailProvisionResult = $state<SoulProvisionEmailConfirmResponse | null>(null);

	let phoneCountryCode = $state('');
	let phoneDesiredNumber = $state('');
	let phoneProvisionBegin = $state<SoulProvisionPhoneBeginResponse | null>(null);
	let phoneProvisionSignature = $state('');
	let phoneProvisionError = $state<string | null>(null);
	let phoneProvisionBeginLoading = $state(false);
	let phoneProvisionSignLoading = $state(false);
	let phoneProvisionConfirmLoading = $state(false);
	let phoneProvisionResult = $state<SoulProvisionPhoneConfirmResponse | null>(null);
	let phoneDeprovisionLoading = $state(false);

	// Contact preferences update state (patches the registration file)
	let prefsDraft = $state('');
	let prefsUpdateError = $state<string | null>(null);
	let prefsUpdateLoading = $state(false);
	let prefsCanonical = $state('');
	let prefsDigestHex = $state('');
	let prefsSignature = $state('');
	let prefsSignLoading = $state(false);
	let prefsExpectedVersion = $state<number | null>(null);
	let prefsFrozenDraft = $state<string>('');
	let prefsUpdateResult = $state<SoulUpdateRegistrationResponse | null>(null);

	// Communication debug state
	let commActivity = $state<SoulAgentCommActivityResponse | null>(null);
	let commQueue = $state<SoulAgentCommQueueResponse | null>(null);
	let commError = $state<string | null>(null);
	let commStatusLoadingId = $state<string | null>(null);
	let commStatuses = $state<Record<string, SoulCommStatusResponse | null>>({});

	let activeSection = $state('identity');

	let myAgents = $state<SoulMineAgentItem[]>([]);
	let myAgentsError = $state<string | null>(null);

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

	// Continuity append state
	let continuityAddError = $state<string | null>(null);
	let continuityAddLoading = $state(false);
	let continuityType = $state('migration');
	let continuityTimestamp = $state(new Date().toISOString());
	let continuitySummary = $state('');
	let continuityRecovery = $state('');
	let continuityReferencesRaw = $state('');
	let continuityCanonical = $state('');
	let continuityDigestHex = $state('');
	let continuitySignature = $state('');
	let continuitySignLoading = $state(false);

	const boundaryCategoryOptions = [
		{ value: 'refusal', label: 'Refusal' },
		{ value: 'scope_limit', label: 'Scope limit' },
		{ value: 'ethical_commitment', label: 'Ethical commitment' },
		{ value: 'circuit_breaker', label: 'Circuit breaker' },
	];

	const continuityTypeOptions = [
		{ value: 'capability_acquired', label: 'Capability acquired' },
		{ value: 'capability_deprecated', label: 'Capability deprecated' },
		{ value: 'significant_failure', label: 'Significant failure' },
		{ value: 'recovery', label: 'Recovery' },
		{ value: 'boundary_added', label: 'Boundary added' },
		{ value: 'migration', label: 'Migration' },
		{ value: 'model_change', label: 'Model change' },
		{ value: 'relationship_formed', label: 'Relationship formed' },
		{ value: 'relationship_ended', label: 'Relationship ended' },
		{ value: 'self_suspension', label: 'Self suspension' },
		{ value: 'archived', label: 'Archived' },
		{ value: 'succession_declared', label: 'Succession declared' },
		{ value: 'succession_received', label: 'Succession received' },
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
	const relCreateTypeOptions = relTypeOptions.filter((o) => o.value);

	// Relationship creation state (creates relationship ABOUT this agent, signed by the "from" agent).
	let relAddError = $state<string | null>(null);
	let relAddLoading = $state(false);
	let relFromAgentId = $state('');
	let relType = $state('endorsement');
	let relMessage = $state('');
	let relContextRaw = $state('');
	let relCreatedAt = $state(new Date().toISOString());
	let relCanonical = $state('');
	let relDigestHex = $state('');
	let relSignature = $state('');
	let relSignLoading = $state(false);

	// Registration update state
	let regDraft = $state('');
	let regUpdateError = $state<string | null>(null);
	let regUpdateLoading = $state(false);
	let regCanonical = $state('');
	let regDigestHex = $state('');
	let regSignature = $state('');
	let regSignLoading = $state(false);
	let regExpectedVersion = $state<number | null>(null);
	let regFrozenDraft = $state<string>('');
	let regUpdateResult = $state<SoulUpdateRegistrationResponse | null>(null);

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

	function parseReferences(raw: string): string[] {
		const parts = raw
			.split(/\r?\n/g)
			.map((s) => s.trim())
			.filter(Boolean);
		return parts.length ? parts : [];
	}

	function computeContinuityDigest(): { type: string; timestamp: string; summary: string; recovery?: string; references?: string[] } | null {
		continuityAddError = null;
		continuityCanonical = '';
		continuityDigestHex = '';

		const type = continuityType.trim().toLowerCase();
		const summary = continuitySummary.trim();
		const recovery = continuityRecovery.trim();
		const refs = parseReferences(continuityReferencesRaw);

		if (!type) {
			continuityAddError = 'Type is required.';
			return null;
		}
		if (!summary) {
			continuityAddError = 'Summary is required.';
			return null;
		}

		let ts = continuityTimestamp.trim();
		if (!ts) {
			ts = new Date().toISOString();
			continuityTimestamp = ts;
		}
		const parsed = new Date(ts);
		if (Number.isNaN(parsed.getTime())) {
			continuityAddError = 'Timestamp must be a valid RFC3339 date-time.';
			return null;
		}
		const timestamp = parsed.toISOString();

		const unsigned: Record<string, unknown> = { type, timestamp, summary };
		if (recovery) unsigned.recovery = recovery;
		if (refs.length) unsigned.references = refs;

		try {
			continuityCanonical = jcsCanonicalize(unsigned);
		} catch (err) {
			continuityAddError = formatError(err);
			return null;
		}
		continuityDigestHex = keccak256Utf8Hex(continuityCanonical);

		return {
			type,
			timestamp,
			summary,
			recovery: recovery || undefined,
			references: refs.length ? refs : undefined,
		};
	}

	async function signContinuity() {
		continuityAddError = null;
		continuitySignature = '';

		const prepared = computeContinuityDigest();
		if (!prepared) return;

		const provider = getEthereumProvider();
		if (!provider) {
			continuityAddError = 'No wallet detected.';
			return;
		}

		const wallet = agent?.agent?.wallet?.trim();
		if (!wallet) {
			continuityAddError = 'Agent wallet is not available.';
			return;
		}

		continuitySignLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				continuityAddError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			continuitySignature = await personalSign(provider, continuityDigestHex, wallet);
		} catch (err) {
			continuityAddError = formatError(err);
		} finally {
			continuitySignLoading = false;
		}
	}

	async function submitContinuity() {
		continuityAddError = null;

		const prepared = computeContinuityDigest();
		if (!prepared) return;
		if (!continuitySignature.trim()) {
			continuityAddError = 'Signature is required. Sign the digest first.';
			return;
		}

		continuityAddLoading = true;
		try {
			await soulAppendContinuity(token, agentId, {
				...prepared,
				signature: continuitySignature.trim(),
			});

			continuityType = 'migration';
			continuityTimestamp = new Date().toISOString();
			continuitySummary = '';
			continuityRecovery = '';
			continuityReferencesRaw = '';
			continuityCanonical = '';
			continuityDigestHex = '';
			continuitySignature = '';

			continuity = await soulPublicGetContinuity(agentId, undefined, 50);
			activeSection = 'continuity';
		} catch (err) {
			if (await handleAuthError(err)) return;
			continuityAddError = formatError(err);
		} finally {
			continuityAddLoading = false;
		}
	}

	function parseContextJSON(raw: string): Record<string, unknown> | undefined {
		const trimmed = raw.trim();
		if (!trimmed) return undefined;
		let parsed: unknown;
		try {
			parsed = JSON.parse(trimmed) as unknown;
		} catch {
			throw new Error('Context must be valid JSON.');
		}
		if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
			throw new Error('Context must be a JSON object.');
		}
		return parsed as Record<string, unknown>;
	}

	function computeRelationshipDigest(): { from_agent_id: string; type: string; context?: Record<string, unknown>; message: string; created_at: string } | null {
		relAddError = null;
		relCanonical = '';
		relDigestHex = '';

		const fromAgent = relFromAgentId.trim().toLowerCase();
		const toAgent = agentId.trim().toLowerCase();
		const type = relType.trim().toLowerCase();
		const message = relMessage.trim();

		if (!fromAgent) {
			relAddError = 'From agent is required.';
			return null;
		}
		if (!type) {
			relAddError = 'Type is required.';
			return null;
		}
		if (!message) {
			relAddError = 'Message is required.';
			return null;
		}

		const parsed = new Date(relCreatedAt.trim() || new Date().toISOString());
		if (Number.isNaN(parsed.getTime())) {
			relAddError = 'created_at must be a valid RFC3339 date-time.';
			return null;
		}
		const created_at = parsed.toISOString();
		relCreatedAt = created_at;

		let context: Record<string, unknown> | undefined;
		try {
			context = parseContextJSON(relContextRaw);
		} catch (err) {
			relAddError = formatError(err);
			return null;
		}

		const unsigned: Record<string, unknown> = {
			kind: 'soul_relationship',
			version: '1',
			fromAgentId: fromAgent,
			toAgentId: toAgent,
			type,
			message,
			createdAt: created_at,
		};
		if (context) unsigned.context = context;

		try {
			relCanonical = jcsCanonicalize(unsigned);
		} catch (err) {
			relAddError = formatError(err);
			return null;
		}
		relDigestHex = keccak256Utf8Hex(relCanonical);

		return {
			from_agent_id: fromAgent,
			type,
			context,
			message,
			created_at,
		};
	}

	async function signRelationship() {
		relAddError = null;
		relSignature = '';

		const prepared = computeRelationshipDigest();
		if (!prepared) return;

		const provider = getEthereumProvider();
		if (!provider) {
			relAddError = 'No wallet detected.';
			return;
		}

		const fromItem = myAgents.find((it) => it.agent.agent_id.toLowerCase() === prepared.from_agent_id.toLowerCase());
		const wallet = fromItem?.agent?.wallet?.trim();
		if (!wallet) {
			relAddError = 'From-agent wallet is not available. Select one of your agents.';
			return;
		}

		relSignLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				relAddError = `Connected wallet does not match from-agent wallet (${wallet}).`;
				return;
			}

			relSignature = await personalSign(provider, relDigestHex, wallet);
		} catch (err) {
			relAddError = formatError(err);
		} finally {
			relSignLoading = false;
		}
	}

	async function submitRelationship() {
		relAddError = null;

		const prepared = computeRelationshipDigest();
		if (!prepared) return;
		if (!relSignature.trim()) {
			relAddError = 'Signature is required. Sign the digest first.';
			return;
		}

		relAddLoading = true;
		try {
			await soulCreateRelationship(token, agentId, {
				...prepared,
				signature: relSignature.trim(),
			});

			relType = 'endorsement';
			relFromAgentId = '';
			relMessage = '';
			relContextRaw = '';
			relCreatedAt = new Date().toISOString();
			relCanonical = '';
			relDigestHex = '';
			relSignature = '';

			await loadRelationships();
			activeSection = 'relationships';
		} catch (err) {
			if (await handleAuthError(err)) return;
			relAddError = formatError(err);
		} finally {
			relAddLoading = false;
		}
	}

	function loadRegistrationDraft() {
		regUpdateError = null;
		regUpdateResult = null;
		regCanonical = '';
		regDigestHex = '';
		regSignature = '';
		regFrozenDraft = '';
		regExpectedVersion = agent?.agent?.self_description_version ?? null;
		regDraft = registration ? prettyJSON(registration) : '';
	}

	function computeUpdateRegistrationDigest(): { registration: Record<string, unknown>; expected_version?: number } | null {
		regUpdateError = null;
		regCanonical = '';
		regDigestHex = '';
		regUpdateResult = null;

		const raw = regDraft.trim();
		if (!raw) {
			regUpdateError = 'Registration JSON is required.';
			return null;
		}

		let parsed: unknown;
		try {
			parsed = JSON.parse(raw) as unknown;
		} catch {
			regUpdateError = 'Registration must be valid JSON.';
			return null;
		}
		if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
			regUpdateError = 'Registration must be a JSON object.';
			return null;
		}

		const reg = parsed as Record<string, unknown>;
		const attAny = reg.attestations as unknown;
		if (!attAny || typeof attAny !== 'object' || Array.isArray(attAny)) {
			regUpdateError = 'Registration must include attestations object.';
			return null;
		}

		const unsigned = JSON.parse(JSON.stringify(reg)) as Record<string, unknown>;
		const unsignedAtt = unsigned.attestations as Record<string, unknown>;
		delete unsignedAtt.selfAttestation;

		try {
			regCanonical = jcsCanonicalize(unsigned);
		} catch (err) {
			regUpdateError = formatError(err);
			return null;
		}
		regDigestHex = keccak256Utf8Hex(regCanonical);

		regFrozenDraft = regDraft;
		regExpectedVersion = agent?.agent?.self_description_version ?? null;

		return { registration: reg, expected_version: regExpectedVersion ?? undefined };
	}

	async function signUpdateRegistration() {
		regUpdateError = null;
		regSignature = '';

		const prepared = computeUpdateRegistrationDigest();
		if (!prepared) return;

		const provider = getEthereumProvider();
		if (!provider) {
			regUpdateError = 'No wallet detected.';
			return;
		}

		const wallet = agent?.agent?.wallet?.trim();
		if (!wallet) {
			regUpdateError = 'Agent wallet is not available.';
			return;
		}

		regSignLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				regUpdateError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			regSignature = await personalSign(provider, regDigestHex, wallet);
		} catch (err) {
			regUpdateError = formatError(err);
		} finally {
			regSignLoading = false;
		}
	}

	async function submitUpdateRegistration() {
		regUpdateError = null;

		if (regFrozenDraft.trim() && regDraft.trim() !== regFrozenDraft.trim()) {
			regUpdateError = 'Draft changed after digest computation. Recompute the digest before publishing.';
			return;
		}

		const prepared = computeUpdateRegistrationDigest();
		if (!prepared) return;
		if (!regSignature.trim()) {
			regUpdateError = 'Signature is required. Sign the digest first.';
			return;
		}

		const registrationObj = prepared.registration;
		const att = registrationObj.attestations as Record<string, unknown>;
		att.selfAttestation = regSignature.trim();

		regUpdateLoading = true;
		try {
			regUpdateResult = await soulUpdateRegistration(token, agentId, {
				registration: registrationObj,
				expected_version: prepared.expected_version,
			});

			await load();
			loadRegistrationDraft();
			activeSection = 'registration';
		} catch (err) {
			if (await handleAuthError(err)) return;
			regUpdateError = formatError(err);
		} finally {
			regUpdateLoading = false;
		}
	}

	// --- Channels + preferences ---

	async function beginEmailProvision() {
		emailProvisionError = null;
		emailProvisionResult = null;
		emailProvisionSignature = '';
		emailProvisionBegin = null;

		emailProvisionBeginLoading = true;
		try {
			emailProvisionBegin = await soulProvisionEmailBegin(token, agentId, {
				local_part: emailLocalPart.trim() || undefined,
			});
		} catch (err) {
			if (await handleAuthError(err)) return;
			emailProvisionError = formatError(err);
		} finally {
			emailProvisionBeginLoading = false;
		}
	}

	async function signEmailProvision() {
		emailProvisionError = null;
		emailProvisionSignature = '';

		if (!emailProvisionBegin) {
			emailProvisionError = 'Begin provisioning first.';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			emailProvisionError = 'No wallet detected.';
			return;
		}

		const wallet = agent?.agent?.wallet?.trim();
		if (!wallet) {
			emailProvisionError = 'Agent wallet is not available.';
			return;
		}

		emailProvisionSignLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				emailProvisionError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			emailProvisionSignature = await personalSign(provider, emailProvisionBegin.digest_hex, wallet);
		} catch (err) {
			emailProvisionError = formatError(err);
		} finally {
			emailProvisionSignLoading = false;
		}
	}

	async function confirmEmailProvision() {
		emailProvisionError = null;
		emailProvisionResult = null;

		if (!emailProvisionBegin) {
			emailProvisionError = 'Begin provisioning first.';
			return;
		}
		if (!emailProvisionSignature.trim()) {
			emailProvisionError = 'Signature is required.';
			return;
		}

		emailProvisionConfirmLoading = true;
		try {
			emailProvisionResult = await soulProvisionEmailConfirm(token, agentId, {
				local_part: emailLocalPart.trim() || undefined,
				issued_at: emailProvisionBegin.issued_at,
				expected_version: emailProvisionBegin.expected_version,
				self_attestation: emailProvisionSignature.trim(),
			});
			emailLocalPart = '';
			emailProvisionSignature = '';
			emailProvisionBegin = null;
			await load();
			activeSection = 'channels';
		} catch (err) {
			if (await handleAuthError(err)) return;
			emailProvisionError = formatError(err);
		} finally {
			emailProvisionConfirmLoading = false;
		}
	}

	async function beginPhoneProvision() {
		phoneProvisionError = null;
		phoneProvisionResult = null;
		phoneProvisionSignature = '';
		phoneProvisionBegin = null;

		phoneProvisionBeginLoading = true;
		try {
			phoneProvisionBegin = await soulProvisionPhoneBegin(token, agentId, {
				country_code: phoneCountryCode.trim() || undefined,
				number: phoneDesiredNumber.trim() || undefined,
			});
			phoneDesiredNumber = phoneProvisionBegin.number;
		} catch (err) {
			if (await handleAuthError(err)) return;
			phoneProvisionError = formatError(err);
		} finally {
			phoneProvisionBeginLoading = false;
		}
	}

	async function signPhoneProvision() {
		phoneProvisionError = null;
		phoneProvisionSignature = '';

		if (!phoneProvisionBegin) {
			phoneProvisionError = 'Begin provisioning first.';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			phoneProvisionError = 'No wallet detected.';
			return;
		}

		const wallet = agent?.agent?.wallet?.trim();
		if (!wallet) {
			phoneProvisionError = 'Agent wallet is not available.';
			return;
		}

		phoneProvisionSignLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				phoneProvisionError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			phoneProvisionSignature = await personalSign(provider, phoneProvisionBegin.digest_hex, wallet);
		} catch (err) {
			phoneProvisionError = formatError(err);
		} finally {
			phoneProvisionSignLoading = false;
		}
	}

	async function confirmPhoneProvision() {
		phoneProvisionError = null;
		phoneProvisionResult = null;

		if (!phoneProvisionBegin) {
			phoneProvisionError = 'Begin provisioning first.';
			return;
		}
		if (!phoneProvisionSignature.trim()) {
			phoneProvisionError = 'Signature is required.';
			return;
		}

		phoneProvisionConfirmLoading = true;
		try {
			phoneProvisionResult = await soulProvisionPhoneConfirm(token, agentId, {
				number: phoneDesiredNumber.trim() || phoneProvisionBegin.number,
				issued_at: phoneProvisionBegin.issued_at,
				expected_version: phoneProvisionBegin.expected_version,
				self_attestation: phoneProvisionSignature.trim(),
			});
			phoneProvisionSignature = '';
			phoneProvisionBegin = null;
			await load();
			activeSection = 'channels';
		} catch (err) {
			if (await handleAuthError(err)) return;
			phoneProvisionError = formatError(err);
		} finally {
			phoneProvisionConfirmLoading = false;
		}
	}

	async function doPhoneDeprovision() {
		phoneProvisionError = null;
		phoneDeprovisionLoading = true;
		try {
			await soulDeprovisionPhone(token, agentId);
			await load();
		} catch (err) {
			if (await handleAuthError(err)) return;
			phoneProvisionError = formatError(err);
		} finally {
			phoneDeprovisionLoading = false;
		}
	}

	function loadPreferencesDraft() {
		prefsUpdateError = null;
		prefsUpdateResult = null;
		prefsCanonical = '';
		prefsDigestHex = '';
		prefsSignature = '';
		prefsFrozenDraft = '';
		prefsExpectedVersion = agent?.agent?.self_description_version ?? null;

		const current = channels?.contactPreferences;
		prefsDraft = current ? prettyJSON(current) : '';
	}

	function computeUpdatePreferencesDigest(): { registration: Record<string, unknown>; expected_version?: number } | null {
		prefsUpdateError = null;
		prefsCanonical = '';
		prefsDigestHex = '';
		prefsUpdateResult = null;

		if (!registration || typeof registration !== 'object' || Array.isArray(registration)) {
			prefsUpdateError = 'Registration is not loaded yet.';
			return null;
		}

		const prefsRaw = prefsDraft.trim();
		let prefsObj: unknown = null;
		if (prefsRaw) {
			try {
				prefsObj = JSON.parse(prefsRaw) as unknown;
			} catch {
				prefsUpdateError = 'Contact preferences must be valid JSON.';
				return null;
			}
			if (!prefsObj || typeof prefsObj !== 'object' || Array.isArray(prefsObj)) {
				prefsUpdateError = 'Contact preferences must be a JSON object.';
				return null;
			}
		}

		const reg = JSON.parse(JSON.stringify(registration)) as Record<string, unknown>;
		const attAny = reg.attestations as unknown;
		if (!attAny || typeof attAny !== 'object' || Array.isArray(attAny)) {
			prefsUpdateError = 'Registration must include attestations object.';
			return null;
		}

		reg.version = '3';
		if (prefsObj) reg.contactPreferences = prefsObj as Record<string, unknown>;
		else delete reg.contactPreferences;

		const unsigned = JSON.parse(JSON.stringify(reg)) as Record<string, unknown>;
		const unsignedAtt = unsigned.attestations as Record<string, unknown>;
		delete unsignedAtt.selfAttestation;

		try {
			prefsCanonical = jcsCanonicalize(unsigned);
		} catch (err) {
			prefsUpdateError = formatError(err);
			return null;
		}
		prefsDigestHex = keccak256Utf8Hex(prefsCanonical);

		prefsFrozenDraft = prefsDraft;
		prefsExpectedVersion = agent?.agent?.self_description_version ?? null;

		return { registration: reg, expected_version: prefsExpectedVersion ?? undefined };
	}

	async function signUpdatePreferences() {
		prefsUpdateError = null;
		prefsSignature = '';

		const prepared = computeUpdatePreferencesDigest();
		if (!prepared) return;

		const provider = getEthereumProvider();
		if (!provider) {
			prefsUpdateError = 'No wallet detected.';
			return;
		}

		const wallet = agent?.agent?.wallet?.trim();
		if (!wallet) {
			prefsUpdateError = 'Agent wallet is not available.';
			return;
		}

		prefsSignLoading = true;
		try {
			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(wallet.toLowerCase())) {
				prefsUpdateError = `Connected wallet does not match agent wallet (${wallet}).`;
				return;
			}

			prefsSignature = await personalSign(provider, prefsDigestHex, wallet);
		} catch (err) {
			prefsUpdateError = formatError(err);
		} finally {
			prefsSignLoading = false;
		}
	}

	async function submitUpdatePreferences() {
		prefsUpdateError = null;

		if (prefsFrozenDraft.trim() && prefsDraft.trim() !== prefsFrozenDraft.trim()) {
			prefsUpdateError = 'Draft changed after digest computation. Recompute the digest before publishing.';
			return;
		}

		const prepared = computeUpdatePreferencesDigest();
		if (!prepared) return;
		if (!prefsSignature.trim()) {
			prefsUpdateError = 'Signature is required. Sign the digest first.';
			return;
		}

		const registrationObj = prepared.registration;
		const att = registrationObj.attestations as Record<string, unknown>;
		att.selfAttestation = prefsSignature.trim();

		prefsUpdateLoading = true;
		try {
			prefsUpdateResult = await soulUpdateRegistration(token, agentId, {
				registration: registrationObj,
				expected_version: prepared.expected_version,
			});
			await load();
			loadPreferencesDraft();
			activeSection = 'channels';
		} catch (err) {
			if (await handleAuthError(err)) return;
			prefsUpdateError = formatError(err);
		} finally {
			prefsUpdateLoading = false;
		}
	}

	async function fetchCommStatus(messageId: string) {
		commError = null;
		commStatusLoadingId = messageId;
		try {
			const status = await soulAgentGetCommStatus(token, agentId, messageId);
			commStatuses = { ...commStatuses, [messageId]: status };
		} catch (err) {
			if (await handleAuthError(err)) return;
			commError = formatError(err);
		} finally {
			commStatusLoadingId = null;
		}
	}

	async function loadMintOperation() {
		mintOperation = null;
		mintOperationError = null;
		showMintSafePayload = false;

		const status = (agent?.agent?.lifecycle_status || agent?.agent?.status || '').toLowerCase();
		if (status !== 'pending' || agent?.agent?.mint_tx_hash) return;

		mintOperationLoading = true;
		try {
			mintOperation = await soulGetAgentMintOperation(token, agentId);
		} catch (err) {
			if (await handleAuthError(err)) return;
			mintOperationError = formatError(err);
		} finally {
			mintOperationLoading = false;
		}
	}

	async function recordMintExecutionHash(txHash: string): Promise<void> {
		mintOperation = await soulRecordAgentMintExecution(token, agentId, txHash);
		mintExecTxHash = '';
		await load();
	}

	async function recordMintExecution() {
		mintRecordError = null;
		const txHash = mintExecTxHash.trim();
		if (!txHash) {
			mintRecordError = 'Execution tx hash is required.';
			return;
		}

		mintRecordLoading = true;
		try {
			await recordMintExecutionHash(txHash);
		} catch (err) {
			if (await handleAuthError(err)) return;
			mintRecordError = formatError(err);
		} finally {
			mintRecordLoading = false;
		}
	}

	async function loadSoulConfig() {
		try {
			soulConfig = await soulPublicGetConfig();
		} catch {
			soulConfig = null;
		}
	}

	async function executeDirectMint() {
		mintDirectError = null;
		mintDirectNotice = null;

		const payload = mintOperation?.safe_tx;
		if (!payload) {
			mintDirectError = 'No mint transaction payload is available.';
			return;
		}
		if (payload.safe_address?.trim()) {
			mintDirectError = 'This mint still requires Safe execution.';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			mintDirectError = 'No wallet detected.';
			return;
		}

		const wallet = agent?.agent?.wallet?.trim();
		if (!wallet) {
			mintDirectError = 'Agent wallet is not available.';
			return;
		}

		mintDirectLoading = true;
		mintRecordError = null;
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
			mintExecTxHash = txHash;

			mintDirectNotice = 'Transaction submitted. Waiting for Sepolia confirmation…';
			await waitForEthereumTransactionReceipt(provider, txHash, 10 * 60 * 1000, 3000);

			mintDirectNotice = 'Transaction confirmed. Recording execution in lesser-host…';
			await recordMintExecutionHash(txHash);
			mintDirectNotice = 'Mint confirmed onchain and recorded.';
		} catch (err) {
			if (await handleAuthError(err)) return;
			const base = formatError(err);
			mintDirectError = txHash
				? `${base}. Transaction sent as ${txHash}. You can still record it manually below if needed.`
				: base;
		} finally {
			mintDirectLoading = false;
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
		channels = null;
		mintOperation = null;
		mintOperationError = null;
		mintRecordError = null;
		showMintSafePayload = false;
		commActivity = null;
		commQueue = null;
		commError = null;

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
				soulPublicGetAgentChannels(agentId),
				soulAgentListCommActivity(token, agentId, 50),
				soulAgentListCommQueue(token, agentId, 50),
			]);
			for (const result of settled) {
				if (result.status === 'rejected') {
					if (await handleAuthError(result.reason)) return;
				}
			}
			registration = settled[0].status === 'fulfilled' ? settled[0].value : null;
			validations = settled[1].status === 'fulfilled' ? settled[1].value : null;
			boundaries = settled[2].status === 'fulfilled' ? settled[2].value : null;
			continuity = settled[3].status === 'fulfilled' ? settled[3].value : null;
			relationships = settled[4].status === 'fulfilled' ? settled[4].value : null;
			versions = settled[5].status === 'fulfilled' ? settled[5].value : null;
			capabilities = settled[6].status === 'fulfilled' ? settled[6].value : null;
			transparency = settled[7].status === 'fulfilled' ? settled[7].value : null;
			failures = settled[8].status === 'fulfilled' ? settled[8].value : null;
			channels = settled[9].status === 'fulfilled' ? settled[9].value : null;
			commActivity = settled[10].status === 'fulfilled' ? settled[10].value : null;
			commQueue = settled[11].status === 'fulfilled' ? settled[11].value : null;
			if (settled[10].status === 'rejected') commError = formatError(settled[10].reason);
			if (!commError && settled[11].status === 'rejected') commError = formatError(settled[11].reason);

			if (!regDraft.trim() && registration) {
				loadRegistrationDraft();
			}
			if (!prefsDraft.trim() && channels?.contactPreferences) {
				prefsDraft = prettyJSON(channels.contactPreferences);
			}
			await loadMintOperation();
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

	async function loadMyAgents() {
		myAgentsError = null;
		try {
			const res = await soulListMyAgents(token);
			myAgents = res.agents ?? [];
			if (!relFromAgentId) {
				const first = myAgents.find((it) => it?.agent?.agent_id && it.agent.agent_id.toLowerCase() !== agentId.toLowerCase());
				if (first?.agent?.agent_id) relFromAgentId = first.agent.agent_id;
			}
		} catch (err) {
			if (await handleAuthError(err)) return;
			myAgentsError = formatError(err);
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
		{ id: 'channels', label: 'Channels' },
		{ id: 'communication', label: 'Communication' },
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
	let needsMintRecovery = $derived(lifecycleStatus === 'pending' && !agent?.agent?.mint_tx_hash);
	let mintUsesSafe = $derived(Boolean(mintOperation?.safe_tx?.safe_address?.trim()));

	onMount(() => {
		void load();
		void loadMyAgents();
		void loadSoulConfig();
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
							{#if !current.agent.self_description_version}
								<Button variant="solid" onclick={() => navigate(`/portal/souls/${current.agent.agent_id}/mint`)}>
									Complete profile
								</Button>
							{/if}
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

				{#if needsMintRecovery}
					<div class="soul-agent__identity-followup">
						<Alert variant="warning" title={mintUsesSafe ? 'Mint still needs execution' : 'Mint still needs wallet confirmation'}>
							<Text size="sm">
								{#if mintUsesSafe}
									The profile can be finished before minting, but this soul stays pending until the mint transaction is
									executed onchain and the execution tx hash is recorded here.
								{:else}
									The profile can be finished before minting, but this soul stays pending until the connected agent
									wallet submits the mint transaction and lesser-host records the confirmed execution.
								{/if}
							</Text>

							{#if mintOperationLoading}
								<div class="soul-agent__loading-inline">
									<Spinner size="sm" />
									<Text size="sm">Loading mint operation…</Text>
								</div>
							{:else if mintOperationError}
								<Alert variant="error" title="Pending mint">{mintOperationError}</Alert>
							{:else if mintOperation}
								<DefinitionList>
									<DefinitionItem label="Operation" monospace>{mintOperation.operation.operation_id}</DefinitionItem>
									<DefinitionItem label="Status" monospace>{mintOperation.operation.status}</DefinitionItem>
								</DefinitionList>

								{#if mintUsesSafe}
									<div class="soul-agent__steps">
										<Text size="sm">1. Execute the Safe transaction below in your Safe workflow.</Text>
										<Text size="sm">2. Wait for the transaction to mine on Sepolia.</Text>
										<Text size="sm">3. Paste the execution tx hash here to activate the soul.</Text>
									</div>
								{:else}
									<div class="soul-agent__steps">
										<Text size="sm">1. Submit the mint transaction from the connected agent wallet.</Text>
										<Text size="sm">2. Keep this page open while lesser-host waits for Sepolia confirmation.</Text>
										<Text size="sm">3. Use the manual tx hash field only if automatic recording does not finish.</Text>
									</div>
								{/if}

								{#if mintDirectNotice}
									<Alert variant="info" title="Direct mint">{mintDirectNotice}</Alert>
								{/if}
								{#if mintDirectError}
									<Alert variant="error" title="Direct mint">{mintDirectError}</Alert>
								{/if}

								<div class="soul-agent__row">
									{#if !mintUsesSafe && mintOperation.safe_tx}
										<Button variant="solid" onclick={() => void executeDirectMint()} disabled={mintDirectLoading || mintRecordLoading}>
											{mintDirectLoading ? 'Minting…' : 'Mint now with connected wallet'}
										</Button>
									{/if}
									{#if mintOperation.safe_tx}
										<Button variant="outline" onclick={() => (showMintSafePayload = !showMintSafePayload)}>
											{showMintSafePayload ? 'Hide transaction data' : 'Show transaction data'}
										</Button>
										<CopyButton size="sm" text={prettyJSON(mintOperation.safe_tx)} />
									{/if}
									<CopyButton size="sm" text={mintOperation.operation.operation_id} />
								</div>

								{#if mintDirectLoading}
									<div class="soul-agent__loading-inline">
										<Spinner size="sm" />
										<Text size="sm">Waiting for wallet confirmation and chain settlement…</Text>
									</div>
								{/if}
								{#if mintDirectTxHash}
									<div class="soul-agent__row">
										<Text size="sm" color="secondary">
											Execution tx <span class="soul-agent__mono">{mintDirectTxHash}</span>
										</Text>
										<CopyButton size="sm" text={mintDirectTxHash} />
									</div>
								{/if}

								{#if mintOperation.safe_tx && showMintSafePayload}
									<DefinitionList>
										<DefinitionItem label="To" monospace>{mintOperation.safe_tx.to}</DefinitionItem>
										<DefinitionItem label="Value" monospace>{mintOperation.safe_tx.value}</DefinitionItem>
										{#if mintOperation.safe_tx.safe_address}
											<DefinitionItem label="Safe" monospace>{mintOperation.safe_tx.safe_address}</DefinitionItem>
										{/if}
									</DefinitionList>
									<TextArea readonly rows={6} label="Transaction data" value={mintOperation.safe_tx.data} />
								{/if}

								<div class="soul-agent__form">
									<TextField label="Execution tx hash" bind:value={mintExecTxHash} placeholder="0x…" />
									<Button variant="solid" onclick={() => void recordMintExecution()} disabled={mintRecordLoading}>
										Record execution
									</Button>
								</div>

								{#if mintRecordLoading}
									<div class="soul-agent__loading-inline">
										<Spinner size="sm" />
										<Text size="sm">Recording execution…</Text>
									</div>
								{/if}
								{#if mintRecordError}
									<Alert variant="error" title="Record execution">{mintRecordError}</Alert>
								{/if}
							{/if}
						</Alert>
					</div>
				{/if}

				{#if !current.agent.self_description_version}
					<div class="soul-agent__identity-followup">
						<Alert variant="info" title="Profile setup still needs one step">
							<Text size="sm">
								The agent has already been created. The remaining step is the profile conversation that drafts and
								publishes the self-description, capabilities, boundaries, and transparency record.
							</Text>
							<div class="soul-agent__row">
								<Button variant="solid" onclick={() => navigate(`/portal/souls/${current.agent.agent_id}/mint`)}>
									Complete profile
								</Button>
							</div>
						</Alert>
					</div>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'channels'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Channels</Heading>
				{/snippet}

				{#if channels}
					<DefinitionList>
						<DefinitionItem label="ENS" monospace>{channels.channels.ens?.name || '—'}</DefinitionItem>
						<DefinitionItem label="Email" monospace>{channels.channels.email?.address || '—'}</DefinitionItem>
						<DefinitionItem label="Email verified" monospace>
							{channels.channels.email ? (channels.channels.email.verified ? 'yes' : 'no') : '—'}
						</DefinitionItem>
						<DefinitionItem label="Phone" monospace>{channels.channels.phone?.number || '—'}</DefinitionItem>
						<DefinitionItem label="Phone verified" monospace>
							{channels.channels.phone ? (channels.channels.phone.verified ? 'yes' : 'no') : '—'}
						</DefinitionItem>
						<DefinitionItem label="Updated" monospace>{channels.updatedAt}</DefinitionItem>
					</DefinitionList>
				{:else}
					<Alert variant="info" title="Channels">
						<Text size="sm">No channel data loaded.</Text>
					</Alert>
				{/if}

				<div class="soul-agent__divider"></div>

				<Heading level={4} size="base">Provision email</Heading>
				<Text size="sm" color="secondary">Creates a managed mailbox on <span class="soul-agent__mono">lessersoul.ai</span>.</Text>

				{#if emailProvisionError}
					<Alert variant="error" title="Email provisioning">{emailProvisionError}</Alert>
				{/if}
				{#if emailProvisionResult?.registration_version}
					<Alert variant="success" title="Email provisioned">
						<Text size="sm">Published version: v{emailProvisionResult.registration_version}</Text>
					</Alert>
				{/if}

				<div class="soul-agent__form">
					<TextField label="Local part (optional)" bind:value={emailLocalPart} placeholder="local-id" />
					<div class="soul-agent__row">
						<Button
							variant="outline"
							onclick={() => void beginEmailProvision()}
							disabled={emailProvisionBeginLoading || emailProvisionConfirmLoading}
						>
							Begin
						</Button>
						<Button
							variant="outline"
							onclick={() => void signEmailProvision()}
							disabled={!emailProvisionBegin || emailProvisionSignLoading || emailProvisionConfirmLoading}
						>
							Sign
						</Button>
						<Button
							variant="solid"
							onclick={() => void confirmEmailProvision()}
							disabled={!emailProvisionBegin || !emailProvisionSignature.trim() || emailProvisionConfirmLoading}
						>
							Confirm
						</Button>
					</div>
				</div>

				{#if emailProvisionBegin}
					<DefinitionList>
						<DefinitionItem label="Address" monospace>{emailProvisionBegin.address}</DefinitionItem>
						<DefinitionItem label="ENS name" monospace>{emailProvisionBegin.ens_name}</DefinitionItem>
						<DefinitionItem label="Expected version" monospace>{emailProvisionBegin.expected_version}</DefinitionItem>
						<DefinitionItem label="Digest" monospace>{emailProvisionBegin.digest_hex}</DefinitionItem>
					</DefinitionList>
				{/if}
				{#if emailProvisionSignature}
					<TextArea label="Signature" value={emailProvisionSignature} readonly rows={2} />
				{/if}
				{#if emailProvisionBeginLoading || emailProvisionConfirmLoading}
					<div class="soul-agent__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">{emailProvisionBeginLoading ? 'Preparing…' : 'Provisioning…'}</Text>
					</div>
				{/if}

				<div class="soul-agent__divider"></div>

				<Heading level={4} size="base">Provision phone</Heading>
				<Text size="sm" color="secondary">Orders a Telnyx number and publishes it to the v3 registration file.</Text>

				{#if phoneProvisionError}
					<Alert variant="error" title="Phone provisioning">{phoneProvisionError}</Alert>
				{/if}
				{#if phoneProvisionResult?.registration_version}
					<Alert variant="success" title="Phone provisioned">
						<Text size="sm">Published version: v{phoneProvisionResult.registration_version}</Text>
					</Alert>
				{/if}

				<div class="soul-agent__form">
					<div class="soul-agent__row soul-agent__row--stretch">
						<TextField label="Country code (optional)" bind:value={phoneCountryCode} placeholder="US" />
						<TextField label="Number (optional)" bind:value={phoneDesiredNumber} placeholder="+1…" />
					</div>
					<div class="soul-agent__row">
						<Button
							variant="outline"
							onclick={() => void beginPhoneProvision()}
							disabled={phoneProvisionBeginLoading || phoneProvisionConfirmLoading}
						>
							Begin
						</Button>
						<Button
							variant="outline"
							onclick={() => void signPhoneProvision()}
							disabled={!phoneProvisionBegin || phoneProvisionSignLoading || phoneProvisionConfirmLoading}
						>
							Sign
						</Button>
						<Button
							variant="solid"
							onclick={() => void confirmPhoneProvision()}
							disabled={!phoneProvisionBegin || !phoneProvisionSignature.trim() || phoneProvisionConfirmLoading}
						>
							Confirm
						</Button>
						{#if channels?.channels?.phone?.number}
							<Button variant="outline" onclick={() => void doPhoneDeprovision()} disabled={phoneDeprovisionLoading}>
								Deprovision
							</Button>
						{/if}
					</div>
				</div>

				{#if phoneProvisionBegin}
					<DefinitionList>
						<DefinitionItem label="Number" monospace>{phoneProvisionBegin.number}</DefinitionItem>
						<DefinitionItem label="Expected version" monospace>{phoneProvisionBegin.expected_version}</DefinitionItem>
						<DefinitionItem label="Digest" monospace>{phoneProvisionBegin.digest_hex}</DefinitionItem>
					</DefinitionList>
				{/if}
				{#if phoneProvisionSignature}
					<TextArea label="Signature" value={phoneProvisionSignature} readonly rows={2} />
				{/if}
				{#if phoneProvisionBeginLoading || phoneProvisionConfirmLoading || phoneDeprovisionLoading}
					<div class="soul-agent__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">
							{phoneProvisionBeginLoading ? 'Preparing…' : phoneDeprovisionLoading ? 'Deprovisioning…' : 'Provisioning…'}
						</Text>
					</div>
				{/if}
			</Card>

			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<div class="soul-agent__row">
						<div class="soul-agent__row-left">
							<Heading level={3} size="lg">Contact preferences</Heading>
						</div>
						<div class="soul-agent__row-right">
							<Button variant="outline" onclick={() => loadPreferencesDraft()} disabled={prefsUpdateLoading}>
								Load current
							</Button>
						</div>
					</div>
				{/snippet}

				{#if prefsUpdateError}
					<Alert variant="error" title="Update failed">{prefsUpdateError}</Alert>
				{/if}
				{#if prefsUpdateResult?.version}
					<Alert variant="success" title="Published">
						<Text size="sm">Published version: v{prefsUpdateResult.version}</Text>
					</Alert>
				{/if}

				<div class="soul-agent__form">
					<TextArea label="Contact preferences JSON" bind:value={prefsDraft} rows={14} />
					<div class="soul-agent__row">
						<Button variant="outline" onclick={() => void computeUpdatePreferencesDigest()} disabled={prefsUpdateLoading || prefsSignLoading}>
							Compute digest
						</Button>
						<Button variant="outline" onclick={() => void signUpdatePreferences()} disabled={prefsUpdateLoading || prefsSignLoading}>
							Sign
						</Button>
						<Button variant="solid" onclick={() => void submitUpdatePreferences()} disabled={prefsUpdateLoading}>Publish</Button>
					</div>
				</div>

				{#if prefsDigestHex}
					<DefinitionList>
						<DefinitionItem label="Expected version" monospace>{prefsExpectedVersion ?? '—'}</DefinitionItem>
						<DefinitionItem label="Digest" monospace>{prefsDigestHex}</DefinitionItem>
					</DefinitionList>
				{/if}
				{#if prefsCanonical}
					<TextArea label="Canonical payload (JCS, unsigned)" value={prefsCanonical} readonly rows={8} />
				{/if}
				{#if prefsSignature}
					<TextArea label="Signature" value={prefsSignature} readonly rows={2} />
				{/if}

				{#if prefsUpdateLoading}
					<div class="soul-agent__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Publishing…</Text>
					</div>
				{/if}
			</Card>
		{/if}

		{#if activeSection === 'communication'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Communication</Heading>
				{/snippet}

				{#if commError}
					<Alert variant="error" title="Communication">{commError}</Alert>
				{/if}

				<Heading level={4} size="base">Recent activity</Heading>
				{#if commActivity?.activities?.length}
					<div class="soul-agent__list">
						{#each commActivity.activities as act (act.activity_id + act.timestamp)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<Text size="sm" weight="medium">{act.direction} · {act.channel_type} · {act.action || '—'}</Text>
										<Text size="sm" color="secondary">
											<span class="soul-agent__mono">{act.timestamp}</span>
											{#if act.counterparty}
												· <span class="soul-agent__mono">{act.counterparty}</span>
											{/if}
										</Text>
										{#if act.message_id}
											<Text size="sm" color="secondary">
												message <span class="soul-agent__mono">{act.message_id}</span>
											</Text>
										{/if}
										{#if act.in_reply_to}
											<Text size="sm" color="secondary">
												inReplyTo <span class="soul-agent__mono">{act.in_reply_to}</span>
											</Text>
										{/if}
										{#if act.boundary_check}
											<Text size="sm" color="secondary">boundary {act.boundary_check}</Text>
										{/if}
										{#if act.preference_respected != null}
											<Text size="sm" color="secondary">prefs {act.preference_respected ? 'respected' : 'ignored'}</Text>
										{/if}
									</div>
									<div class="soul-agent__item-right">
										{#if act.direction === 'outbound' && act.message_id}
											<Button
												size="sm"
												variant="outline"
												onclick={() => void fetchCommStatus(act.message_id as string)}
												disabled={commStatusLoadingId === act.message_id}
											>
												{commStatusLoadingId === act.message_id ? 'Loading…' : 'Status'}
											</Button>
										{/if}
									</div>
								</div>

								{#if act.message_id && commStatuses[act.message_id]}
									{@const st = commStatuses[act.message_id] as SoulCommStatusResponse}
									<DefinitionList>
										<DefinitionItem label="Status" monospace>{st.status}</DefinitionItem>
										<DefinitionItem label="Provider" monospace>{st.provider || '—'}</DefinitionItem>
										<DefinitionItem label="Provider msg ID" monospace>{st.providerMessageId || '—'}</DefinitionItem>
										{#if st.errorCode}
											<DefinitionItem label="Error code" monospace>{st.errorCode}</DefinitionItem>
										{/if}
										{#if st.errorMessage}
											<DefinitionItem label="Error message">{st.errorMessage}</DefinitionItem>
										{/if}
										{#if st.replyBody}
											<DefinitionItem label="Reply transcript">{st.replyBody}</DefinitionItem>
										{/if}
										{#if st.replyConfidence != null}
											<DefinitionItem label="Reply confidence" monospace>{st.replyConfidence}</DefinitionItem>
										{/if}
										{#if st.replyMessageId}
											<DefinitionItem label="Reply msg ID" monospace>{st.replyMessageId}</DefinitionItem>
										{/if}
										{#if st.replyReceivedAt}
											<DefinitionItem label="Reply received" monospace>{st.replyReceivedAt}</DefinitionItem>
										{/if}
										<DefinitionItem label="Created" monospace>{st.createdAt}</DefinitionItem>
										<DefinitionItem label="Updated" monospace>{st.updatedAt || '—'}</DefinitionItem>
									</DefinitionList>
								{/if}
							</Card>
						{/each}
					</div>
				{:else}
					<Text size="sm" color="secondary">No communication activity recorded yet.</Text>
				{/if}

				<div class="soul-agent__divider"></div>

				<Heading level={4} size="base">Queued inbound</Heading>
				{#if commQueue?.items?.length}
					<div class="soul-agent__list">
						{#each commQueue.items as item (item.message_id + item.scheduled_delivery_time)}
							<Card variant="outlined" padding="md">
								<div class="soul-agent__item">
									<div class="soul-agent__item-left">
										<Text size="sm" weight="medium">{item.channel_type} · {item.status}</Text>
										<Text size="sm" color="secondary">
											received <span class="soul-agent__mono">{item.received_at}</span> · scheduled <span class="soul-agent__mono">{item.scheduled_delivery_time}</span>
										</Text>
										{#if item.from_address || item.from_number}
											<Text size="sm" color="secondary">
												from <span class="soul-agent__mono">{item.from_address || item.from_number}</span>
											</Text>
										{/if}
										{#if item.subject}
											<Text size="sm" color="secondary">subject {item.subject}</Text>
										{/if}
									</div>
								</div>
							</Card>
						{/each}
					</div>
				{:else}
					<Text size="sm" color="secondary">No queued inbound messages.</Text>
				{/if}
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

				<Card variant="outlined" padding="md">
					{#snippet header()}
						<Heading level={4} size="lg">Append entry</Heading>
					{/snippet}

					{#if continuityAddError}
						<Alert variant="error" title="Failed to append continuity entry">{continuityAddError}</Alert>
					{/if}

					<div class="soul-agent__form">
						<Select options={continuityTypeOptions} value={continuityType} onchange={(value: string) => (continuityType = value)} />
						<TextField label="Timestamp (RFC3339)" bind:value={continuityTimestamp} />
						<TextArea label="Summary" bind:value={continuitySummary} rows={3} />
						<TextArea label="Recovery (optional)" bind:value={continuityRecovery} rows={3} />
						<TextArea label="References (one per line, optional)" bind:value={continuityReferencesRaw} rows={3} />
						<div class="soul-agent__row">
							<Button
								variant="outline"
								onclick={() => void computeContinuityDigest()}
								disabled={continuityAddLoading || continuitySignLoading}
							>
								Compute digest
							</Button>
							<Button variant="outline" onclick={() => void signContinuity()} disabled={continuityAddLoading || continuitySignLoading}>
								Sign
							</Button>
							<Button variant="solid" onclick={() => void submitContinuity()} disabled={continuityAddLoading}>Append</Button>
						</div>
					</div>

					{#if continuityDigestHex}
						<DefinitionList>
							<DefinitionItem label="Digest" monospace>{continuityDigestHex}</DefinitionItem>
						</DefinitionList>
					{/if}
					{#if continuityCanonical}
						<TextArea label="Canonical payload (JCS)" value={continuityCanonical} readonly rows={6} />
					{/if}
					{#if continuitySignature}
						<TextArea label="Signature" value={continuitySignature} readonly rows={2} />
					{/if}

					{#if continuityAddLoading}
						<div class="soul-agent__loading-inline">
							<Spinner size="sm" />
							<Text size="sm">Appending…</Text>
						</div>
					{/if}
				</Card>

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

				{@const fromAgentOptions = myAgents
					.filter((it) => it?.agent?.agent_id && it.agent.agent_id.toLowerCase() !== agentId.toLowerCase())
					.map((it) => ({ value: it.agent.agent_id, label: `${it.agent.domain}/${it.agent.local_id}` }))}

				<Card variant="outlined" padding="md">
					{#snippet header()}
						<Heading level={4} size="lg">Create relationship</Heading>
					{/snippet}

					<Text size="sm" color="secondary">
						Creates a relationship record about this agent (the “to” agent). The signature must come from the “from” agent’s wallet.
					</Text>

					{#if myAgentsError}
						<Alert variant="error" title="Failed to load your agents">{myAgentsError}</Alert>
					{/if}
					{#if relAddError}
						<Alert variant="error" title="Failed to create relationship">{relAddError}</Alert>
					{/if}

					<div class="soul-agent__form">
						<Select options={fromAgentOptions} value={relFromAgentId} onchange={(value: string) => (relFromAgentId = value)} />
						<Select options={relCreateTypeOptions} value={relType} onchange={(value: string) => (relType = value)} />
						<TextArea label="Message" bind:value={relMessage} rows={3} />
						<TextArea label="Context (JSON object, optional)" bind:value={relContextRaw} rows={4} />
						<TextField label="created_at (RFC3339)" bind:value={relCreatedAt} />
						<div class="soul-agent__row">
							<Button variant="outline" onclick={() => void computeRelationshipDigest()} disabled={relAddLoading || relSignLoading}>
								Compute digest
							</Button>
							<Button variant="outline" onclick={() => void signRelationship()} disabled={relAddLoading || relSignLoading}>
								Sign
							</Button>
							<Button variant="solid" onclick={() => void submitRelationship()} disabled={relAddLoading}>Create</Button>
						</div>
					</div>

					{#if relDigestHex}
						<DefinitionList>
							<DefinitionItem label="Digest" monospace>{relDigestHex}</DefinitionItem>
						</DefinitionList>
					{/if}
					{#if relCanonical}
						<TextArea label="Canonical payload (JCS)" value={relCanonical} readonly rows={6} />
					{/if}
					{#if relSignature}
						<TextArea label="Signature" value={relSignature} readonly rows={2} />
					{/if}

					{#if relAddLoading}
						<div class="soul-agent__loading-inline">
							<Spinner size="sm" />
							<Text size="sm">Creating…</Text>
						</div>
					{/if}
				</Card>

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

				{#if regUpdateError}
					<Alert variant="error" title="Update failed">{regUpdateError}</Alert>
				{/if}
				{#if regUpdateResult?.version}
					<Alert variant="success" title="Published">
						<Text size="sm">Published version: v{regUpdateResult.version}</Text>
					</Alert>
				{/if}

				<div class="soul-agent__form">
					<TextArea label="Registration JSON" bind:value={regDraft} rows={16} />
					<div class="soul-agent__row">
						<Button variant="outline" onclick={() => loadRegistrationDraft()} disabled={regUpdateLoading}>Load current</Button>
						<Button variant="outline" onclick={() => void computeUpdateRegistrationDigest()} disabled={regUpdateLoading || regSignLoading}>
							Compute digest
						</Button>
						<Button variant="outline" onclick={() => void signUpdateRegistration()} disabled={regUpdateLoading || regSignLoading}>
							Sign
						</Button>
						<Button variant="solid" onclick={() => void submitUpdateRegistration()} disabled={regUpdateLoading}>Publish</Button>
					</div>
				</div>

				{#if regDigestHex}
					<DefinitionList>
						<DefinitionItem label="Expected version" monospace>{regExpectedVersion ?? '—'}</DefinitionItem>
						<DefinitionItem label="Digest" monospace>{regDigestHex}</DefinitionItem>
					</DefinitionList>
				{/if}
				{#if regCanonical}
					<TextArea label="Canonical payload (JCS, unsigned)" value={regCanonical} readonly rows={8} />
				{/if}
				{#if regSignature}
					<TextArea label="Signature" value={regSignature} readonly rows={2} />
				{/if}

				{#if regUpdateLoading}
					<div class="soul-agent__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Publishing…</Text>
					</div>
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

	.soul-agent__row--stretch {
		justify-content: flex-start;
	}

	.soul-agent__row--stretch > :global(*) {
		flex: 1;
		min-width: 220px;
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

	.soul-agent__divider {
		height: 1px;
		background: var(--gr-color-border-secondary, #e0e0e0);
		margin-top: var(--gr-spacing-scale-5);
	}

	.soul-agent__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.soul-agent__steps {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
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
