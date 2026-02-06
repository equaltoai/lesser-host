import { fetchJson } from './http';

export interface AttestationResponse {
	id: string;
	jws: string;
	header?: unknown;
	payload: unknown;
}

export function getAttestation(id: string): Promise<AttestationResponse> {
	return fetchJson<AttestationResponse>(`/attestations/${encodeURIComponent(id)}`, {
		method: 'GET',
	});
}

export function lookupAttestation(input: {
	actor_uri: string;
	object_uri: string;
	content_hash: string;
	module: string;
	policy_version: string;
}): Promise<AttestationResponse> {
	const qs = new URLSearchParams({
		actor_uri: input.actor_uri,
		object_uri: input.object_uri,
		content_hash: input.content_hash,
		module: input.module,
		policy_version: input.policy_version,
	});
	return fetchJson<AttestationResponse>(`/attestations?${qs.toString()}`, {
		method: 'GET',
	});
}

export interface JWKS {
	keys: Array<{ kid?: string; kty?: string; alg?: string; use?: string; [key: string]: unknown }>;
}

export function getJWKS(): Promise<JWKS> {
	return fetchJson<JWKS>('/.well-known/jwks.json', { method: 'GET' });
}

