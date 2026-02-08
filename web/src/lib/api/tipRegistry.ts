import { fetchJson, jsonRequest } from './http';

export interface TipRegistryOperation {
	id: string;
	kind: string;
	chain_id: number;
	contract_address: string;
	tx_mode?: string;
	safe_address?: string;
	domain_raw?: string;
	domain_normalized?: string;
	host_id_hex?: string;
	wallet_address?: string;
	host_fee_bps?: number;
	active?: boolean;
	token_address?: string;
	token_allowed?: boolean;
	tx_to?: string;
	tx_data?: string;
	tx_value?: string;
	safe_tx_hash?: string;
	exec_tx_hash?: string;
	exec_block_number?: number;
	exec_success?: boolean;
	receipt_json?: string;
	snapshot_json?: string;
	status: string;
	created_at: string;
	updated_at: string;
	proposed_at?: string;
	executed_at?: string;
}

export interface SafeTxPayload {
	safe_address: string;
	to: string;
	value: string;
	data: string;
}

export interface TipHostRegistration {
	id: string;
	kind: string;
	domain_raw?: string;
	domain_normalized: string;
	host_id_hex: string;
	chain_id: number;
	wallet_type: string;
	wallet_address: string;
	host_fee_bps: number;
	tx_mode?: string;
	safe_address?: string;
	wallet_nonce?: string;
	wallet_message?: string;
	dns_token?: string;
	http_token?: string;
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

export interface TipRegistryWalletChallenge {
	id: string;
	address: string;
	chainId: number;
	nonce: string;
	message: string;
	issuedAt: string;
	expiresAt: string;
}

export interface TipRegistryProofInstructions {
	method: string;
	dns_name?: string;
	dns_value?: string;
	https_url?: string;
	https_body?: string;
}

export interface TipRegistryRegistrationBeginResponse {
	registration: TipHostRegistration;
	wallet: TipRegistryWalletChallenge;
	proofs: TipRegistryProofInstructions[];
}

export interface TipRegistryRegistrationVerifyResponse {
	registration: TipHostRegistration;
	operation: TipRegistryOperation;
	safe_tx?: SafeTxPayload;
}

export interface CreateTipRegistryOperationResponse {
	operation: TipRegistryOperation;
	safe_tx?: SafeTxPayload;
}

export interface EnsureTipRegistryHostNoopResponse {
	noop: boolean;
	domain_normalized: string;
}

export interface ListTipRegistryOperationsResponse {
	operations: TipRegistryOperation[];
	count: number;
}

export function beginTipRegistryRegistration(input: {
	kind?: string;
	domain: string;
	wallet_address: string;
	host_fee_bps: number;
}): Promise<TipRegistryRegistrationBeginResponse> {
	const req = jsonRequest(input);
	return fetchJson<TipRegistryRegistrationBeginResponse>('/api/v1/tip-registry/registrations/begin', {
		method: 'POST',
		...req,
	});
}

export function verifyTipRegistryRegistration(
	id: string,
	signature: string,
	proofs?: string[],
): Promise<TipRegistryRegistrationVerifyResponse> {
	const req = jsonRequest({ signature, proofs });
	return fetchJson<TipRegistryRegistrationVerifyResponse>(
		`/api/v1/tip-registry/registrations/${encodeURIComponent(id)}/verify`,
		{
			method: 'POST',
			...req,
		},
	);
}

export function listTipRegistryOperations(token: string, status?: string): Promise<ListTipRegistryOperationsResponse> {
	const qs = new URLSearchParams();
	if (status) qs.set('status', status);
	const url = qs.toString() ? `/api/v1/tip-registry/operations?${qs.toString()}` : '/api/v1/tip-registry/operations';

	return fetchJson<ListTipRegistryOperationsResponse>(url, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function getTipRegistryOperation(token: string, id: string): Promise<TipRegistryOperation> {
	return fetchJson<TipRegistryOperation>(`/api/v1/tip-registry/operations/${encodeURIComponent(id)}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function recordTipRegistryOperationExecution(
	token: string,
	id: string,
	execTxHash: string,
): Promise<TipRegistryOperation> {
	const req = jsonRequest({ exec_tx_hash: execTxHash });
	return fetchJson<TipRegistryOperation>(`/api/v1/tip-registry/operations/${encodeURIComponent(id)}/record-execution`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function ensureTipRegistryHost(
	token: string,
	domain: string,
): Promise<CreateTipRegistryOperationResponse | EnsureTipRegistryHostNoopResponse> {
	return fetchJson<CreateTipRegistryOperationResponse | EnsureTipRegistryHostNoopResponse>(
		`/api/v1/tip-registry/hosts/${encodeURIComponent(domain)}/ensure`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
}

export function setTipRegistryHostActive(
	token: string,
	domain: string,
	active: boolean,
): Promise<CreateTipRegistryOperationResponse> {
	const req = jsonRequest({ active });
	return fetchJson<CreateTipRegistryOperationResponse>(
		`/api/v1/tip-registry/hosts/${encodeURIComponent(domain)}/active`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
				...req.headers,
			},
			body: req.body,
		},
	);
}

export function setTipRegistryTokenAllowed(
	token: string,
	tokenAddress: string,
	allowed: boolean,
): Promise<CreateTipRegistryOperationResponse> {
	const req = jsonRequest({ token_address: tokenAddress, allowed });
	return fetchJson<CreateTipRegistryOperationResponse>('/api/v1/tip-registry/tokens/allowlist', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}
