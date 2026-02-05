import { fetchJson, jsonRequest } from './http';
import type { OperatorLoginResponse } from './controlPlane';

export interface WebAuthnBeginResponse {
	publicKey: Record<string, unknown>;
	challenge: string;
}

export interface WebAuthnCredentialSummary {
	id: string;
	name: string;
	created_at: string;
	last_used_at: string;
}

export interface WebAuthnCredentialsResponse {
	credentials: WebAuthnCredentialSummary[];
}

export function webAuthnRegisterBegin(token: string): Promise<WebAuthnBeginResponse> {
	return fetchJson<WebAuthnBeginResponse>('/api/v1/auth/webauthn/register/begin', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function webAuthnRegisterFinish(
	token: string,
	input: { challenge: string; response: Record<string, unknown>; credential_name: string },
): Promise<{ ok: boolean }> {
	return fetchJson<{ ok: boolean }>('/api/v1/auth/webauthn/register/finish', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			'content-type': 'application/json',
		},
		body: JSON.stringify(input),
	});
}

export function webAuthnLoginBegin(username: string): Promise<WebAuthnBeginResponse> {
	return fetchJson<WebAuthnBeginResponse>('/api/v1/auth/webauthn/login/begin', {
		method: 'POST',
		...jsonRequest({ username }),
	});
}

export function webAuthnLoginFinish(input: {
	username: string;
	challenge: string;
	response: Record<string, unknown>;
	device_name?: string;
}): Promise<OperatorLoginResponse> {
	return fetchJson<OperatorLoginResponse>('/api/v1/auth/webauthn/login/finish', {
		method: 'POST',
		...jsonRequest(input),
	});
}

export function webAuthnCredentials(token: string): Promise<WebAuthnCredentialsResponse> {
	return fetchJson<WebAuthnCredentialsResponse>('/api/v1/auth/webauthn/credentials', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function webAuthnDeleteCredential(token: string, credentialId: string): Promise<{ ok: boolean }> {
	return fetchJson<{ ok: boolean }>(`/api/v1/auth/webauthn/credentials/${encodeURIComponent(credentialId)}`, {
		method: 'DELETE',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function webAuthnUpdateCredential(
	token: string,
	credentialId: string,
	name: string,
): Promise<{ ok: boolean }> {
	return fetchJson<{ ok: boolean }>(`/api/v1/auth/webauthn/credentials/${encodeURIComponent(credentialId)}`, {
		method: 'PUT',
		headers: {
			authorization: `Bearer ${token}`,
			'content-type': 'application/json',
		},
		body: JSON.stringify({ name }),
	});
}

