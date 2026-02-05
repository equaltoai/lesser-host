import { fetchJson } from './http';

export interface OperatorMeResponse {
	username: string;
	role: string;
	display_name?: string;
}

export function getOperatorMe(token: string): Promise<OperatorMeResponse> {
	return fetchJson<OperatorMeResponse>('/api/v1/operators/me', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

