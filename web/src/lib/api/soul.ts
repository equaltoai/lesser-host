import type { components } from '../greater/adapters/rest/generated/lesser-host-api.js';
import { fetchJson, jsonRequest } from './http';

// --- Public config + search ---

export interface SoulConfigReputationWeights {
	economic: number;
	social: number;
	validation: number;
	trust: number;
	integrity: number;
	communication: number;
}

export interface SoulConfigResponse {
	enabled: boolean;
	chain_id: number;
	registry_contract_address: string;
	admin_safe_address?: string;
	tx_mode?: string;
	supported_capabilities?: string[];
	reputation_weights?: SoulConfigReputationWeights;
}

export function soulPublicGetConfig(): Promise<SoulConfigResponse> {
	return fetchJson<SoulConfigResponse>('/api/v1/soul/config');
}

// --- v3 channels + preferences ---

export interface SoulContactPreferencesV3 {
	preferred: string;
	fallback?: string;
	availability: {
		schedule: string;
		timezone?: string;
		windows?: Array<{ days: string[]; startTime: string; endTime: string }>;
	};
	responseExpectation: { target: string; guarantee: string };
	rateLimits?: Record<string, unknown>;
	languages: string[];
	contentTypes?: string[];
	firstContact?: {
		requireSoul?: boolean;
		requireReputation?: number | null;
		introductionExpected?: boolean;
	};
}

export interface SoulPublicENSChannel {
	name: string;
	resolverAddress?: string;
	chain?: string;
}

export interface SoulPublicEmailChannel {
	address: string;
	capabilities: string[];
	protocols?: string[];
	verified: boolean;
	verifiedAt?: string;
	status?: string;
}

export interface SoulPublicPhoneChannel {
	number: string;
	capabilities: string[];
	provider?: string;
	verified: boolean;
	verifiedAt?: string;
	status?: string;
}

export interface SoulPublicAgentChannelsResponse {
	agentId: string;
	channels: {
		ens: SoulPublicENSChannel | null;
		email: SoulPublicEmailChannel | null;
		phone: SoulPublicPhoneChannel | null;
	};
	contactPreferences: SoulContactPreferencesV3 | null;
	updatedAt: string;
}

export function soulPublicGetAgentChannels(agentId: string): Promise<SoulPublicAgentChannelsResponse> {
	return fetchJson<SoulPublicAgentChannelsResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/channels`);
}

export interface SoulProvisionEmailBeginResponse {
	version: string;
	address: string;
	ens_name: string;
	digest_hex: string;
	issued_at: string;
	expected_version: number;
	next_version: number;
	registration: unknown;
}

export interface SoulProvisionEmailConfirmResponse {
	version: string;
	address: string;
	registration_version: number;
}

export function soulProvisionEmailBegin(token: string, agentId: string, input: { local_part?: string }): Promise<SoulProvisionEmailBeginResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulProvisionEmailBeginResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/channels/email/provision/begin`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export function soulProvisionEmailConfirm(
	token: string,
	agentId: string,
	input: { local_part?: string; issued_at: string; expected_version: number; self_attestation: string },
): Promise<SoulProvisionEmailConfirmResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulProvisionEmailConfirmResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/channels/email/provision`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export interface SoulProvisionPhoneBeginResponse {
	version: string;
	number: string;
	digest_hex: string;
	issued_at: string;
	expected_version: number;
	next_version: number;
	registration: unknown;
}

export interface SoulProvisionPhoneConfirmResponse {
	version: string;
	number: string;
	registration_version: number;
}

export function soulProvisionPhoneBegin(
	token: string,
	agentId: string,
	input: { country_code?: string; number?: string },
): Promise<SoulProvisionPhoneBeginResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulProvisionPhoneBeginResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/channels/phone/provision/begin`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export function soulProvisionPhoneConfirm(
	token: string,
	agentId: string,
	input: { number: string; issued_at: string; expected_version: number; self_attestation: string },
): Promise<SoulProvisionPhoneConfirmResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulProvisionPhoneConfirmResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/channels/phone/provision`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export function soulDeprovisionPhone(token: string, agentId: string): Promise<{ ok: boolean }> {
	return fetchJson<{ ok: boolean }>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/channels/phone`, {
		method: 'DELETE',
		headers: { authorization: `Bearer ${token}` },
	});
}

export interface SoulSearchResult {
	agent_id: string;
	domain: string;
	local_id: string;
}

export interface SoulSearchResponse {
	version: string;
	results: SoulSearchResult[];
	count: number;
	has_more: boolean;
	next_cursor?: string;
}

export function soulPublicSearch(input: {
	q?: string;
	domain?: string;
	capability?: string;
	claimLevel?: string;
	boundary?: string;
	status?: string;
	cursor?: string;
	limit?: number;
}): Promise<SoulSearchResponse> {
	const params = new URLSearchParams();
	if (input.q) params.set('q', input.q);
	if (input.domain) params.set('domain', input.domain);
	if (input.capability) params.set('capability', input.capability);
	if (input.claimLevel) params.set('claimLevel', input.claimLevel);
	if (input.boundary) params.set('boundary', input.boundary);
	if (input.status) params.set('status', input.status);
	if (input.cursor) params.set('cursor', input.cursor);
	if (input.limit != null) params.set('limit', String(input.limit));
	const qs = params.toString();
	return fetchJson<SoulSearchResponse>(`/api/v1/soul/search${qs ? `?${qs}` : ''}`);
}

export interface SoulAgentIdentity {
	agent_id: string;
	domain: string;
	local_id: string;
	wallet: string;
	token_id?: string;
	meta_uri?: string;
	capabilities?: string[];
	status: string;
	lifecycle_status?: string;
	lifecycle_reason?: string;
	successor_agent_id?: string;
	predecessor_agent_id?: string;
	principal_address?: string;
	self_description_version?: number;
	mint_tx_hash?: string;
	minted_at?: string;
	updated_at?: string;
}

export interface SoulAgentReputation {
	agent_id: string;
	block_ref?: number;
	composite: number;
	economic: number;
	social: number;
	validation: number;
	trust: number;
	integrity?: number;
	tips_received: number;
	interactions: number;
	validations_passed: number;
	endorsements: number;
	flags: number;
	delegations_completed?: number;
	boundary_violations?: number;
	failure_recoveries?: number;
	updated_at?: string;
}

export interface SoulMineAgentItem {
	agent: SoulAgentIdentity;
	reputation?: SoulAgentReputation;
}

export interface SoulMineAgentsResponse {
	agents: SoulMineAgentItem[];
	count: number;
}

export function soulListMyAgents(token: string): Promise<SoulMineAgentsResponse> {
	return fetchJson<SoulMineAgentsResponse>('/api/v1/soul/agents/mine', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export interface SoulRegistryProofInstructions {
	method: string;
	dns_name?: string;
	dns_value?: string;
	https_url?: string;
	https_body?: string;
}

export interface WalletChallengeResponse {
	id: string;
	username?: string;
	address: string;
	chainId: number;
	nonce: string;
	message: string;
	issuedAt: string;
	expiresAt: string;
}

export interface SoulAgentRegistration {
	id: string;
	username?: string;
	domain_raw?: string;
	domain_normalized: string;
	local_id_raw?: string;
	local_id: string;
	agent_id: string;
	wallet_address: string;
	capabilities?: string[];
	wallet_nonce?: string;
	wallet_message?: string;
	proof_token?: string;
	dns_verified?: boolean;
	https_verified?: boolean;
	wallet_verified?: boolean;
	verified_at?: string;
	status: string;
	created_at: string;
	updated_at: string;
	expires_at?: string;
	completed_at?: string;
}

export interface SoulAgentRegistrationBeginResponse {
	registration: SoulAgentRegistration;
	wallet: WalletChallengeResponse;
	proofs: SoulRegistryProofInstructions[];
}

export function soulAgentRegistrationBegin(
	token: string,
	input: { domain: string; local_id: string; wallet_address: string; capabilities?: string[] },
): Promise<SoulAgentRegistrationBeginResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulAgentRegistrationBeginResponse>('/api/v1/soul/agents/register/begin', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export interface SafeTxPayload {
	safe_address: string;
	to: string;
	value: string;
	data: string;
}

export interface SoulOperation {
	operation_id: string;
	kind: string;
	agent_id?: string;
	status: string;
	safe_payload?: string;
	exec_tx_hash?: string;
	exec_block_number?: number;
	exec_success?: boolean;
	receipt_json?: string;
	snapshot_json?: string;
	created_at: string;
	updated_at: string;
	executed_at?: string;
}

export interface SoulAgentMintOperationResponse {
	operation: SoulOperation;
	safe_tx?: SafeTxPayload;
}

export interface SoulAgentRegistrationVerifyResponse {
	registration: SoulAgentRegistration;
	operation: SoulOperation;
	safe_tx?: SafeTxPayload;
}

export function soulAgentRegistrationVerify(
	token: string,
	id: string,
	input: {
		signature: string;
		principal_address: string;
		principal_declaration: string;
		principal_signature: string;
		declared_at: string;
	},
): Promise<SoulAgentRegistrationVerifyResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulAgentRegistrationVerifyResponse>(`/api/v1/soul/agents/register/${encodeURIComponent(id)}/verify`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export interface SoulWalletRotationRequest {
	agent_id: string;
	username: string;
	current_wallet: string;
	new_wallet: string;
	nonce: string;
	deadline: number;
	digest_hex: string;
	spent: boolean;
	created_at: string;
	updated_at: string;
	expires_at: string;
	confirmed_at?: string;
}

export interface SoulWalletRotationTypedData {
	types: Record<string, Array<{ name: string; type: string }>>;
	primaryType: string;
	domain: { name: string; version: string; chainId: number; verifyingContract: string };
	message: { agentId: string; currentWallet: string; newWallet: string; nonce: string; deadline: string };
	digest_hex: string;
}

export interface SoulRotateWalletBeginResponse {
	rotation: SoulWalletRotationRequest;
	typed_data: SoulWalletRotationTypedData;
}

export function soulAgentRotateWalletBegin(token: string, agentId: string, newWalletAddress: string): Promise<SoulRotateWalletBeginResponse> {
	const req = jsonRequest({ new_wallet_address: newWalletAddress });
	return fetchJson<SoulRotateWalletBeginResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/rotate-wallet/begin`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export interface SoulRotateWalletConfirmResponse {
	operation: SoulOperation;
	safe_tx?: SafeTxPayload;
}

export function soulAgentRotateWalletConfirm(
	token: string,
	agentId: string,
	currentSignature: string,
	newSignature: string,
): Promise<SoulRotateWalletConfirmResponse> {
	const req = jsonRequest({ current_signature: currentSignature, new_signature: newSignature });
	return fetchJson<SoulRotateWalletConfirmResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/rotate-wallet/confirm`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export interface SoulUpdateRegistrationResponse {
	agent: SoulAgentIdentity;
	s3_key?: string;
	version?: number;
}

export function soulUpdateRegistration(
	token: string,
	agentId: string,
	input: { registration: unknown; expected_version?: number },
): Promise<SoulUpdateRegistrationResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulUpdateRegistrationResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/update-registration`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

// --- Communication (portal) ---

export type SoulAgentCommActivity =
	components['schemas']['SoulAgentCommActivityItem'];

export type SoulAgentCommActivityResponse =
	components['schemas']['SoulAgentCommActivityResponse'];

export type SoulAgentCommQueueItem =
	components['schemas']['SoulAgentCommQueueItem'];

export type SoulAgentCommQueueResponse =
	components['schemas']['SoulAgentCommQueueResponse'];

export type SoulCommStatusResponse =
	components['schemas']['SoulCommStatusResponse'];

export function soulAgentListCommActivity(token: string, agentId: string, limit: number = 50): Promise<SoulAgentCommActivityResponse> {
	const params = new URLSearchParams();
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulAgentCommActivityResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/comm/activity?${qs}`, {
		headers: { authorization: `Bearer ${token}` },
	});
}

export function soulAgentListCommQueue(token: string, agentId: string, limit: number = 50): Promise<SoulAgentCommQueueResponse> {
	const params = new URLSearchParams();
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulAgentCommQueueResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/comm/queue?${qs}`, {
		headers: { authorization: `Bearer ${token}` },
	});
}

export function soulAgentGetCommStatus(token: string, agentId: string, messageId: string): Promise<SoulCommStatusResponse> {
	return fetchJson<SoulCommStatusResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/comm/status/${encodeURIComponent(messageId)}`, {
		headers: { authorization: `Bearer ${token}` },
	});
}

export interface SoulPublicAgentResponse {
	version: string;
	agent: SoulAgentIdentity;
	reputation?: SoulAgentReputation;
}

export function soulPublicGetAgent(agentId: string): Promise<SoulPublicAgentResponse> {
	return fetchJson<SoulPublicAgentResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}`);
}

export function soulPublicGetRegistration(agentId: string): Promise<unknown> {
	return fetchJson<unknown>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/registration`);
}

export interface SoulPublicReputationResponse {
	version: string;
	reputation: SoulAgentReputation;
}

export function soulPublicGetReputation(agentId: string): Promise<SoulPublicReputationResponse> {
	return fetchJson<SoulPublicReputationResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/reputation`);
}

export interface SoulAgentValidationRecord {
	agent_id: string;
	challenge_id: string;
	challenge_type: string;
	validator_id: string;
	request?: string;
	response?: string;
	result: string;
	score: number;
	evaluated_at: string;
}

export interface SoulPublicValidationsResponse {
	version: string;
	validations: SoulAgentValidationRecord[];
	count: number;
	has_more: boolean;
	next_cursor?: string;
}

export function soulPublicGetValidations(agentId: string, cursor?: string, limit: number = 50): Promise<SoulPublicValidationsResponse> {
	const params = new URLSearchParams();
	if (cursor) params.set('cursor', cursor);
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulPublicValidationsResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/validations${qs ? `?${qs}` : ''}`);
}

export interface ListSoulOperationsResponse {
	operations: SoulOperation[];
	count: number;
}

export function listSoulOperations(token: string, status: string): Promise<ListSoulOperationsResponse> {
	const params = new URLSearchParams();
	if (status) params.set('status', status);
	const qs = params.toString();
	return fetchJson<ListSoulOperationsResponse>(`/api/v1/soul/operations${qs ? `?${qs}` : ''}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function getSoulOperation(token: string, id: string): Promise<SoulOperation> {
	return fetchJson<SoulOperation>(`/api/v1/soul/operations/${encodeURIComponent(id)}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function recordSoulOperationExecution(token: string, id: string, execTxHash: string): Promise<SoulOperation> {
	const req = jsonRequest({ exec_tx_hash: execTxHash });
	return fetchJson<SoulOperation>(`/api/v1/soul/operations/${encodeURIComponent(id)}/record-execution`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function soulGetAgentMintOperation(token: string, agentId: string): Promise<SoulAgentMintOperationResponse> {
	return fetchJson<SoulAgentMintOperationResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/mint-operation`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function soulRecordAgentMintExecution(token: string, agentId: string, execTxHash: string): Promise<SoulAgentMintOperationResponse> {
	const req = jsonRequest({ exec_tx_hash: execTxHash });
	return fetchJson<SoulAgentMintOperationResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/mint-operation/record-execution`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export interface PublishRootResponse {
	operation: SoulOperation;
	safe_tx?: SafeTxPayload;
	root: string;
	block_ref: number;
	count: number;
	snapshot_key: string;
	proofs_key: string;
	manifest_key: string;
}

export function publishSoulReputationRoot(token: string): Promise<PublishRootResponse> {
	return fetchJson<PublishRootResponse>('/api/v1/soul/reputation/publish', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function publishSoulValidationRoot(token: string): Promise<PublishRootResponse> {
	return fetchJson<PublishRootResponse>('/api/v1/soul/validation/publish', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

// --- v2: Boundaries ---

export interface SoulAgentBoundary {
	agent_id: string;
	boundary_id: string;
	category: string;
	statement: string;
	rationale?: string;
	added_in_version?: number;
	supersedes?: string;
	signature?: string;
	added_at: string;
}

export interface SoulPublicBoundariesResponse {
	version: string;
	boundaries: SoulAgentBoundary[];
	count: number;
	has_more: boolean;
	next_cursor?: string;
}

export function soulPublicGetBoundaries(agentId: string, cursor?: string, limit: number = 50): Promise<SoulPublicBoundariesResponse> {
	const params = new URLSearchParams();
	if (cursor) params.set('cursor', cursor);
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulPublicBoundariesResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/boundaries${qs ? `?${qs}` : ''}`);
}

export interface SoulAppendBoundaryResponse {
	boundary: SoulAgentBoundary;
}

export interface SoulAppendBoundaryBeginResponse {
	version: string;
	digest_hex: string;
	issued_at: string;
	expected_version: number;
	next_version: number;
}

export function soulAddBoundaryBegin(
	token: string,
	agentId: string,
	input: { boundary_id: string; category: string; statement: string; rationale?: string; supersedes?: string; signature?: string },
): Promise<SoulAppendBoundaryBeginResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulAppendBoundaryBeginResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/boundaries/begin`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export function soulAddBoundary(
	token: string,
	agentId: string,
	input: {
		boundary_id: string;
		category: string;
		statement: string;
		rationale?: string;
		supersedes?: string;
		signature?: string;
		issued_at: string;
		expected_version: number;
		self_attestation: string;
	},
): Promise<SoulAppendBoundaryResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulAppendBoundaryResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/boundaries`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

// --- v2: Continuity ---

export interface SoulAgentContinuityEntry {
	agent_id: string;
	type: string;
	summary: string;
	recovery?: string;
	references?: string[];
	signature?: string;
	timestamp: string;
}

export interface SoulPublicContinuityResponse {
	version: string;
	entries: SoulAgentContinuityEntry[];
	count: number;
	has_more: boolean;
	next_cursor?: string;
}

export function soulPublicGetContinuity(agentId: string, cursor?: string, limit: number = 50): Promise<SoulPublicContinuityResponse> {
	const params = new URLSearchParams();
	if (cursor) params.set('cursor', cursor);
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulPublicContinuityResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/continuity${qs ? `?${qs}` : ''}`);
}

export interface SoulAppendContinuityResponse {
	entry: SoulAgentContinuityEntry;
}

export function soulAppendContinuity(
	token: string,
	agentId: string,
	input: { type: string; timestamp: string; summary: string; recovery?: string; references?: string[]; signature: string },
): Promise<SoulAppendContinuityResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulAppendContinuityResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/continuity`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

// --- v2: Disputes ---

export interface SoulAgentDispute {
	agent_id: string;
	dispute_id: string;
	signal_ref?: string;
	evidence?: string;
	statement?: string;
	resolution?: string;
	status: string;
	created_at: string;
	resolved_at?: string;
}

export interface SoulPublicDisputesResponse {
	version: string;
	disputes: SoulAgentDispute[];
	count: number;
	has_more: boolean;
	next_cursor?: string;
}

export function soulPublicGetDisputes(agentId: string, cursor?: string, limit: number = 50): Promise<SoulPublicDisputesResponse> {
	const params = new URLSearchParams();
	if (cursor) params.set('cursor', cursor);
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulPublicDisputesResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/disputes${qs ? `?${qs}` : ''}`);
}

export function soulPublicGetDispute(agentId: string, disputeId: string): Promise<SoulAgentDispute> {
	return fetchJson<SoulAgentDispute>(
		`/api/v1/soul/agents/${encodeURIComponent(agentId)}/disputes/${encodeURIComponent(disputeId)}`,
	);
}

// --- v2: Relationships ---

export interface SoulAgentRelationship {
	from_agent_id: string;
	to_agent_id: string;
	type: string;
	context?: Record<string, unknown>;
	message?: string;
	signature?: string;
	created_at: string;
}

export interface SoulPublicRelationshipsResponse {
	version: string;
	relationships: SoulAgentRelationship[];
	count: number;
	has_more: boolean;
	next_cursor?: string;
}

export function soulPublicGetRelationships(
	agentId: string,
	type?: string,
	cursor?: string,
	limit: number = 50,
	taskType?: string,
): Promise<SoulPublicRelationshipsResponse> {
	const params = new URLSearchParams();
	if (type) params.set('type', type);
	if (taskType) params.set('taskType', taskType);
	if (cursor) params.set('cursor', cursor);
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulPublicRelationshipsResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/relationships${qs ? `?${qs}` : ''}`);
}

export interface SoulCreateRelationshipResponse {
	relationship: SoulAgentRelationship;
}

export function soulCreateRelationship(
	token: string,
	toAgentId: string,
	input: {
		from_agent_id: string;
		type: string;
		context?: Record<string, unknown>;
		message: string;
		created_at: string;
		signature: string;
	},
): Promise<SoulCreateRelationshipResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulCreateRelationshipResponse>(`/api/v1/soul/agents/${encodeURIComponent(toAgentId)}/relationships`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

// --- v2: Versions ---

export interface SoulAgentVersion {
	agent_id: string;
	version_number: number;
	registration_uri?: string;
	change_summary?: string;
	self_attestation?: string;
	created_at: string;
}

export interface SoulPublicVersionsResponse {
	version: string;
	versions: SoulAgentVersion[];
	count: number;
	has_more: boolean;
	next_cursor?: string;
}

export function soulPublicGetVersions(agentId: string, cursor?: string, limit: number = 50): Promise<SoulPublicVersionsResponse> {
	const params = new URLSearchParams();
	if (cursor) params.set('cursor', cursor);
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulPublicVersionsResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/versions${qs ? `?${qs}` : ''}`);
}

// --- v2: Capabilities (structured) ---

export interface SoulAgentCapability {
	capability: string;
	scope?: string;
	constraints?: Record<string, unknown>;
	claim_level: string;
	last_validated?: string;
	validation_ref?: string;
	degrades_to?: string;
}

export interface SoulPublicCapabilitiesResponse {
	version: string;
	capabilities: SoulAgentCapability[];
	count: number;
}

export function soulPublicGetCapabilities(agentId: string): Promise<SoulPublicCapabilitiesResponse> {
	return fetchJson<SoulPublicCapabilitiesResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/capabilities`);
}

// --- v2: Transparency ---

export function soulPublicGetTransparency(agentId: string): Promise<unknown> {
	return fetchJson<unknown>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/transparency`);
}

// --- v2: Failures ---

export interface SoulAgentFailure {
	agent_id: string;
	failure_id: string;
	failure_type: string;
	description?: string;
	impact?: string;
	recovery_ref?: string;
	status?: string;
	timestamp: string;
}

export interface SoulPublicFailuresResponse {
	version: string;
	failures: SoulAgentFailure[];
	count: number;
	has_more: boolean;
	next_cursor?: string;
}

export function soulPublicGetFailures(agentId: string, cursor?: string, limit: number = 50): Promise<SoulPublicFailuresResponse> {
	const params = new URLSearchParams();
	if (cursor) params.set('cursor', cursor);
	params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulPublicFailuresResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/failures${qs ? `?${qs}` : ''}`);
}

export function soulRecordFailure(
	token: string,
	agentId: string,
	input: { failure_id: string; failure_type: string; description: string; impact?: string },
): Promise<SoulAgentFailure> {
	const req = jsonRequest(input);
	return fetchJson<SoulAgentFailure>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/failures`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export function soulRecordRecovery(
	token: string,
	agentId: string,
	input: { failure_id: string; recovery_ref?: string },
): Promise<SoulAgentFailure> {
	const req = jsonRequest(input);
	return fetchJson<SoulAgentFailure>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/failures/recover`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

// --- v2: Sovereignty (self-suspend, archive, successor) ---

export function soulSelfSuspend(token: string, agentId: string, reason?: string): Promise<SoulAgentIdentity> {
	const req = jsonRequest({ reason });
	return fetchJson<SoulAgentIdentity>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/self-suspend`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export function soulSelfReinstate(token: string, agentId: string): Promise<SoulAgentIdentity> {
	return fetchJson<SoulAgentIdentity>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/self-reinstate`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}` },
	});
}

export interface SoulContinuityToSign {
	agent_id: string;
	type: string;
	timestamp: string;
	summary: string;
	references?: string[];
	digest_hex: string;
}

export interface SoulArchiveBeginResponse {
	version: string;
	entry: SoulContinuityToSign;
}

export function soulArchiveAgentBegin(token: string, agentId: string): Promise<SoulArchiveBeginResponse> {
	return fetchJson<SoulArchiveBeginResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/archive/begin`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}` },
	});
}

export function soulArchiveAgent(
	token: string,
	agentId: string,
	input: { reason?: string; timestamp: string; signature: string },
): Promise<SoulAgentIdentity> {
	const req = jsonRequest(input);
	return fetchJson<SoulAgentIdentity>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/archive`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export interface SoulDesignateSuccessorBeginResponse {
	version: string;
	predecessor_entry: SoulContinuityToSign;
	successor_entry: SoulContinuityToSign;
}

export function soulDesignateSuccessorBegin(
	token: string,
	agentId: string,
	successorAgentId: string,
): Promise<SoulDesignateSuccessorBeginResponse> {
	const req = jsonRequest({ successor_agent_id: successorAgentId });
	return fetchJson<SoulDesignateSuccessorBeginResponse>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/successor/begin`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

export function soulDesignateSuccessor(
	token: string,
	agentId: string,
	input: {
		successor_agent_id: string;
		reason?: string;
		timestamp: string;
		predecessor_signature: string;
		successor_signature: string;
	},
): Promise<SoulAgentIdentity> {
	const req = jsonRequest(input);
	return fetchJson<SoulAgentIdentity>(`/api/v1/soul/agents/${encodeURIComponent(agentId)}/successor`, {
		method: 'POST',
		headers: { authorization: `Bearer ${token}`, ...req.headers },
		body: req.body,
	});
}

// --- v2: Minting Conversation ---

export interface SoulMintConversation {
	agent_id: string;
	conversation_id: string;
	model: string;
	messages?: string;
	produced_declarations?: string;
	status: string;
	created_at: string;
	completed_at?: string;
}

export interface SoulAgentMintConversationsResponse {
	version: string;
	conversations: SoulMintConversation[];
	count: number;
}

export function soulGetMintConversation(token: string, registrationId: string, conversationId: string): Promise<SoulMintConversation> {
	return fetchJson<SoulMintConversation>(
		`/api/v1/soul/agents/register/${encodeURIComponent(registrationId)}/mint-conversation/${encodeURIComponent(conversationId)}`,
		{ headers: { authorization: `Bearer ${token}` } },
	);
}

export function soulCompleteMintConversation(token: string, registrationId: string, conversationId: string): Promise<SoulMintConversation> {
	return fetchJson<SoulMintConversation>(
		`/api/v1/soul/agents/register/${encodeURIComponent(registrationId)}/mint-conversation/${encodeURIComponent(conversationId)}/complete`,
		{ method: 'POST', headers: { authorization: `Bearer ${token}` } },
	);
}

export function soulAgentListMintConversations(token: string, agentId: string, limit?: number): Promise<SoulAgentMintConversationsResponse> {
	const params = new URLSearchParams();
	if (limit != null) params.set('limit', String(limit));
	const qs = params.toString();
	return fetchJson<SoulAgentMintConversationsResponse>(
		`/api/v1/soul/agents/${encodeURIComponent(agentId)}/mint-conversations${qs ? `?${qs}` : ''}`,
		{ headers: { authorization: `Bearer ${token}` } },
	);
}

export function soulAgentGetMintConversation(token: string, agentId: string, conversationId: string): Promise<SoulMintConversation> {
	return fetchJson<SoulMintConversation>(
		`/api/v1/soul/agents/${encodeURIComponent(agentId)}/mint-conversation/${encodeURIComponent(conversationId)}`,
		{ headers: { authorization: `Bearer ${token}` } },
	);
}

export function soulAgentCompleteMintConversation(token: string, agentId: string, conversationId: string): Promise<SoulMintConversation> {
	return fetchJson<SoulMintConversation>(
		`/api/v1/soul/agents/${encodeURIComponent(agentId)}/mint-conversation/${encodeURIComponent(conversationId)}/complete`,
		{ method: 'POST', headers: { authorization: `Bearer ${token}` } },
	);
}

export interface SoulMintConversationFinalizeBeginResponse {
	version: string;
	digest_hex: string;
	issued_at: string;
	expected_version: number;
	next_version: number;
	registration_preview?: unknown;
}

export function soulMintConversationFinalizeBegin(
	token: string,
	registrationId: string,
	conversationId: string,
	input: { boundary_signatures: Record<string, string> },
): Promise<SoulMintConversationFinalizeBeginResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulMintConversationFinalizeBeginResponse>(
		`/api/v1/soul/agents/register/${encodeURIComponent(registrationId)}/mint-conversation/${encodeURIComponent(conversationId)}/finalize/begin`,
		{
			method: 'POST',
			headers: { authorization: `Bearer ${token}`, ...req.headers },
			body: req.body,
		},
	);
}

export function soulAgentMintConversationFinalizeBegin(
	token: string,
	agentId: string,
	conversationId: string,
	input: { boundary_signatures: Record<string, string> },
): Promise<SoulMintConversationFinalizeBeginResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulMintConversationFinalizeBeginResponse>(
		`/api/v1/soul/agents/${encodeURIComponent(agentId)}/mint-conversation/${encodeURIComponent(conversationId)}/finalize/begin`,
		{
			method: 'POST',
			headers: { authorization: `Bearer ${token}`, ...req.headers },
			body: req.body,
		},
	);
}

export interface SoulMintConversationFinalizeResponse {
	version: string;
	agent: SoulAgentIdentity;
	published_version: number;
}

export function soulMintConversationFinalize(
	token: string,
	registrationId: string,
	conversationId: string,
	input: { boundary_signatures: Record<string, string>; issued_at: string; expected_version: number; self_attestation: string },
): Promise<SoulMintConversationFinalizeResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulMintConversationFinalizeResponse>(
		`/api/v1/soul/agents/register/${encodeURIComponent(registrationId)}/mint-conversation/${encodeURIComponent(conversationId)}/finalize`,
		{
			method: 'POST',
			headers: { authorization: `Bearer ${token}`, ...req.headers },
			body: req.body,
		},
	);
}

export function soulAgentMintConversationFinalize(
	token: string,
	agentId: string,
	conversationId: string,
	input: { boundary_signatures: Record<string, string>; issued_at: string; expected_version: number; self_attestation: string },
): Promise<SoulMintConversationFinalizeResponse> {
	const req = jsonRequest(input);
	return fetchJson<SoulMintConversationFinalizeResponse>(
		`/api/v1/soul/agents/${encodeURIComponent(agentId)}/mint-conversation/${encodeURIComponent(conversationId)}/finalize`,
		{
			method: 'POST',
			headers: { authorization: `Bearer ${token}`, ...req.headers },
			body: req.body,
		},
	);
}

export interface SoulMintConversationSSEInput {
	model?: string;
	conversation_id?: string;
	message: string;
}

export function soulStartMintConversationSSE(
	token: string,
	registrationId: string,
	input: SoulMintConversationSSEInput,
): EventSource | ReadableStream<string> {
	const url = `/api/v1/soul/agents/register/${encodeURIComponent(registrationId)}/mint-conversation`;
	const body = JSON.stringify(input);

	return soulMintConversationSSERequest(token, url, body);
}

export function soulStartAgentMintConversationSSE(
	token: string,
	agentId: string,
	input: SoulMintConversationSSEInput,
): EventSource | ReadableStream<string> {
	const url = `/api/v1/soul/agents/${encodeURIComponent(agentId)}/mint-conversation`;
	const body = JSON.stringify(input);

	return soulMintConversationSSERequest(token, url, body);
}

function soulSseErrorMessage(value: unknown, fallback: string): string {
	if (typeof value === 'string') {
		const trimmed = value.trim();
		return trimmed || fallback;
	}
	if (value == null) return fallback;
	if (typeof value !== 'object') {
		const asString = String(value).trim();
		return asString || fallback;
	}

	const record = value as Record<string, unknown>;
	for (const key of ['message', 'error', 'detail']) {
		const nested = soulSseErrorMessage(record[key], '');
		if (nested) return nested;
	}

	if (typeof record.code === 'string' && record.code.trim()) return record.code.trim();

	try {
		const json = JSON.stringify(value);
		return json && json !== '{}' ? json : fallback;
	} catch {
		return fallback;
	}
}

function soulMintConversationSSERequest(token: string, url: string, body: string): EventSource | ReadableStream<string> {
	const controller = new AbortController();
	const stream = new ReadableStream<string>({
		async start(streamController) {
			try {
				const res = await fetch(url, {
					method: 'POST',
					headers: {
						authorization: `Bearer ${token}`,
						'content-type': 'application/json',
						accept: 'text/event-stream',
					},
					body,
					signal: controller.signal,
				});

				if (!res.ok || !res.body) {
					const text = await res.text().catch(() => '');
					let message = soulSseErrorMessage(text, `HTTP ${res.status}`);
					try {
						message = soulSseErrorMessage(JSON.parse(text) as unknown, message);
					} catch {
						// Ignore non-JSON error bodies and fall back to the raw text.
					}
					streamController.enqueue(`event: error\ndata: ${JSON.stringify({ message })}\n\n`);
					streamController.close();
					return;
				}

				const reader = res.body.getReader();
				const decoder = new TextDecoder();

				while (true) {
					const { done, value } = await reader.read();
					if (done) break;
					streamController.enqueue(decoder.decode(value, { stream: true }));
				}
			} catch (err) {
				if (!controller.signal.aborted) {
					streamController.enqueue(`event: error\ndata: ${JSON.stringify({ message: String(err) })}\n\n`);
				}
			} finally {
				streamController.close();
			}
		},
		cancel() {
			controller.abort();
		},
	});

	return stream;
}
