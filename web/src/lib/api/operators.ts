import { fetchJson, jsonRequest } from './http';

export interface OperatorMeResponse {
	username: string;
	role: string;
	display_name?: string;
}

export interface VanityDomainRequest {
	domain: string;
	domain_raw?: string;
	instance_slug: string;
	requested_by?: string;
	status: string;
	verified_at?: string;
	requested_at?: string;
	reviewed_by?: string;
	reviewed_at?: string;
	note?: string;
	created_at: string;
	updated_at: string;
}

export interface ListVanityDomainRequestsResponse {
	requests: VanityDomainRequest[];
	count: number;
}

export interface ExternalInstanceRegistration {
	id: string;
	username: string;
	slug: string;
	status: string;
	reviewed_by?: string;
	reviewed_at?: string;
	note?: string;
	created_at: string;
	updated_at: string;
}

export interface ListExternalInstanceRegistrationsResponse {
	registrations: ExternalInstanceRegistration[];
	count: number;
}

export interface ExternalInstanceRegistrationResponse {
	registration: ExternalInstanceRegistration;
}

export interface PortalUserApproval {
	username: string;
	role: string;
	approved: boolean;
	approval_status: string;
	reviewed_by?: string;
	reviewed_at?: string;
	approval_note?: string;
	display_name?: string;
	email?: string;
	created_at: string;
	wallet_address?: string;
}

export interface ListPortalUserApprovalsResponse {
	users: PortalUserApproval[];
	count: number;
}

export function getOperatorMe(token: string): Promise<OperatorMeResponse> {
	return fetchJson<OperatorMeResponse>('/api/v1/operators/me', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function listVanityDomainRequests(token: string): Promise<ListVanityDomainRequestsResponse> {
	return fetchJson<ListVanityDomainRequestsResponse>('/api/v1/operators/vanity-domain-requests', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function approveVanityDomainRequest(
	token: string,
	domain: string,
	note?: string,
): Promise<VanityDomainRequest> {
	const req = note ? jsonRequest({ note }) : null;
	return fetchJson<VanityDomainRequest>(
		`/api/v1/operators/vanity-domain-requests/${encodeURIComponent(domain)}/approve`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
				...(req?.headers ?? {}),
			},
			body: req?.body,
		},
	);
}

export function rejectVanityDomainRequest(
	token: string,
	domain: string,
	note?: string,
): Promise<VanityDomainRequest> {
	const req = note ? jsonRequest({ note }) : null;
	return fetchJson<VanityDomainRequest>(
		`/api/v1/operators/vanity-domain-requests/${encodeURIComponent(domain)}/reject`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
				...(req?.headers ?? {}),
			},
			body: req?.body,
		},
	);
}

export function listExternalInstanceRegistrations(token: string): Promise<ListExternalInstanceRegistrationsResponse> {
	return fetchJson<ListExternalInstanceRegistrationsResponse>('/api/v1/operators/external-instances/registrations', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function approveExternalInstanceRegistration(
	token: string,
	username: string,
	id: string,
): Promise<ExternalInstanceRegistration> {
	return fetchJson<ExternalInstanceRegistrationResponse>(
		`/api/v1/operators/external-instances/registrations/${encodeURIComponent(username)}/${encodeURIComponent(id)}/approve`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	).then((res) => res.registration);
}

export function rejectExternalInstanceRegistration(
	token: string,
	username: string,
	id: string,
): Promise<ExternalInstanceRegistration> {
	return fetchJson<ExternalInstanceRegistrationResponse>(
		`/api/v1/operators/external-instances/registrations/${encodeURIComponent(username)}/${encodeURIComponent(id)}/reject`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	).then((res) => res.registration);
}

export function listPortalUserApprovals(
	token: string,
	status?: string,
): Promise<ListPortalUserApprovalsResponse> {
	const qs = status ? `?status=${encodeURIComponent(status)}` : '';
	return fetchJson<ListPortalUserApprovalsResponse>(`/api/v1/operators/portal-users${qs}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function approvePortalUser(token: string, username: string, note?: string): Promise<PortalUserApproval> {
	const req = note ? jsonRequest({ note }) : null;
	return fetchJson<PortalUserApproval>(`/api/v1/operators/portal-users/${encodeURIComponent(username)}/approve`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...(req?.headers ?? {}),
		},
		body: req?.body,
	});
}

export function rejectPortalUser(token: string, username: string, note?: string): Promise<PortalUserApproval> {
	const req = note ? jsonRequest({ note }) : null;
	return fetchJson<PortalUserApproval>(`/api/v1/operators/portal-users/${encodeURIComponent(username)}/reject`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...(req?.headers ?? {}),
		},
		body: req?.body,
	});
}
