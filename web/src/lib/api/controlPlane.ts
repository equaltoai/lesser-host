import { fetchJson } from './http';

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

export function getSetupStatus(): Promise<SetupStatusResponse> {
	return fetchJson<SetupStatusResponse>('/setup/status');
}

