/**
 * M2 Shell Fixture — mock portal API (portalMe).
 *
 * Returns a realistic PortalMeResponse so the sidebar user chip renders
 * with display_name, role, and method. This mock is aliased into the
 * `src/lib/api/portal` import path by the fixture Vite config and is
 * NEVER loaded by any customer portal route.
 *
 * @license AGPL-3.0-only
 */

export interface PortalMeResponse {
	username: string;
	role: string;
	display_name?: string;
	email?: string;
	method?: string;
}

const FIXTURE_ME: PortalMeResponse = {
	username: 'alice',
	role: 'customer',
	method: 'wallet',
	display_name: 'Alice Johnson',
};

export function getPortalMe(_token?: string): Promise<PortalMeResponse> {
	void _token;
	return Promise.resolve(FIXTURE_ME);
}
