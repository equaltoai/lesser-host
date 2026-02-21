import { fetchJson, jsonRequest } from './http';

export interface SoulAgentIdentity {
	agent_id: string;
	domain: string;
	local_id: string;
	wallet: string;
	token_id?: string;
	meta_uri?: string;
	capabilities?: string[];
	status: string;
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
	tips_received: number;
	interactions: number;
	validations_passed: number;
	endorsements: number;
	flags: number;
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

export interface SoulAgentRegistrationVerifyResponse {
	registration: SoulAgentRegistration;
	operation: SoulOperation;
	safe_tx?: SafeTxPayload;
}

export function soulAgentRegistrationVerify(token: string, id: string, signature: string): Promise<SoulAgentRegistrationVerifyResponse> {
	const req = jsonRequest({ signature });
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
