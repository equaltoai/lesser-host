import { fetchJson, jsonRequest } from './http';

export interface SetupStatusResponse {
	control_plane_state: 'locked' | 'active';
	locked: boolean;
	finalize_allowed: boolean;
	bootstrapped_at?: string;

	bootstrap_wallet_address_set: boolean;
	bootstrap_wallet_address?: string;

	primary_admin_set: boolean;
	primary_admin_username?: string;

	stage: string;
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

export interface SetupBootstrapVerifyResponse {
	token_type: string;
	token: string;
	expires_at: string;
}

export interface SetupCreateAdminResponse {
	username: string;
}

export interface OperatorLoginResponse {
	token_type: string;
	token: string;
	expires_at: string;
	username: string;
	role: string;
	method: string;
}

export interface SetupFinalizeResponse {
	locked: boolean;
	bootstrapped_at?: string;
}

export function getSetupStatus(): Promise<SetupStatusResponse> {
	return fetchJson<SetupStatusResponse>('/setup/status');
}

export function setupBootstrapChallenge(address: string, chainId: number): Promise<WalletChallengeResponse> {
	return fetchJson<WalletChallengeResponse>('/setup/bootstrap/challenge', {
		method: 'POST',
		...jsonRequest({ address, chainId }),
	});
}

export function setupBootstrapVerify(input: {
	challengeId: string;
	address: string;
	signature: string;
	message: string;
}): Promise<SetupBootstrapVerifyResponse> {
	return fetchJson<SetupBootstrapVerifyResponse>('/setup/bootstrap/verify', {
		method: 'POST',
		...jsonRequest(input),
	});
}

export function walletChallenge(input: {
	username: string;
	address: string;
	chainId: number;
}): Promise<WalletChallengeResponse> {
	return fetchJson<WalletChallengeResponse>('/auth/wallet/challenge', {
		method: 'POST',
		...jsonRequest(input),
	});
}

export function walletLogin(input: {
	challengeId: string;
	address: string;
	signature: string;
	message: string;
}): Promise<OperatorLoginResponse> {
	return fetchJson<OperatorLoginResponse>('/auth/wallet/login', {
		method: 'POST',
		...jsonRequest(input),
	});
}

export function setupCreateAdmin(
	setupSessionToken: string,
	input: {
		username: string;
		displayName?: string;
		wallet: { challengeId: string; address: string; signature: string; message: string };
	},
): Promise<SetupCreateAdminResponse> {
	return fetchJson<SetupCreateAdminResponse>('/setup/admin', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${setupSessionToken}`,
			'content-type': 'application/json',
		},
		body: JSON.stringify(input),
	});
}

export function setupFinalize(operatorToken: string): Promise<SetupFinalizeResponse> {
	return fetchJson<SetupFinalizeResponse>('/setup/finalize', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${operatorToken}`,
		},
	});
}
