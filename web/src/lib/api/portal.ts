import { fetchJson, jsonRequest } from './http';
import type { OperatorLoginResponse, WalletChallengeResponse } from './controlPlane';

export interface PortalMeResponse {
	username: string;
	role: string;
	display_name?: string;
	email?: string;
	method?: string;
}

export function portalWalletChallenge(address: string, chainId: number): Promise<WalletChallengeResponse> {
	return fetchJson<WalletChallengeResponse>('/api/v1/portal/auth/wallet/challenge', {
		method: 'POST',
		...jsonRequest({ address, chainId }),
	});
}

export function portalWalletLogin(input: {
	challengeId: string;
	address: string;
	signature: string;
	message: string;
	email?: string;
	display_name?: string;
}): Promise<OperatorLoginResponse> {
	return fetchJson<OperatorLoginResponse>('/api/v1/portal/auth/wallet/login', {
		method: 'POST',
		...jsonRequest(input),
	});
}

export function getPortalMe(token: string): Promise<PortalMeResponse> {
	return fetchJson<PortalMeResponse>('/api/v1/portal/me', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

