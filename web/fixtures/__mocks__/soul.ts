/**
 * M6 Instance Overview Fixture — mock soul API (soulListMyAgents).
 *
 * Returns 2 bound souls so the Souls preview in InstanceOverview renders
 * the design state rather than a fetch error. This mock is aliased into
 * the `src/lib/api/soul` import path by the fixture Vite config and is
 * NEVER loaded by any customer portal route.
 *
 * @license AGPL-3.0-only
 */

export interface SoulAgentIdentity {
	agent_id: string;
	domain: string;
	local_id: string;
	ens_name?: string;
	wallet: string;
	token_id?: string;
	meta_uri?: string;
	avatar?: {
		token_uri?: string;
		image?: string;
		current_style_id?: number;
		current_style_name?: string;
		current_renderer_address?: string;
		styles?: Array<{
			style_id: number;
			style_name: string;
		}>;
	};
}

export interface SoulAgentReputation {
	tips_received?: number;
	helpful_count?: number;
	boundary_violations?: number;
	failure_recoveries?: number;
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

const FIXTURE_AGENTS: SoulMineAgentsResponse = {
	agents: [
		{
			agent: {
				agent_id: 'agent-sim-001',
				domain: 'simulacrum.greater.website',
				local_id: 'assistant',
				wallet: '0xabc1230000000000000000000000000000000001',
				token_id: '1',
				avatar: {
					token_uri: 'https://example.lesser.host/avatar/1.svg',
					image: 'https://example.lesser.host/avatar/1.png',
					current_style_id: 1,
					current_style_name: 'default',
				},
			},
			reputation: {
				tips_received: 18.4,
				helpful_count: 142,
				updated_at: new Date().toISOString(),
			},
		},
		{
			agent: {
				agent_id: 'agent-sim-002',
				domain: 'simulacrum.greater.website',
				local_id: 'moderator',
				wallet: '0xabc1230000000000000000000000000000000002',
				token_id: '2',
			},
			reputation: {
				tips_received: 7.2,
				helpful_count: 53,
				updated_at: new Date().toISOString(),
			},
		},
	],
	count: 2,
};

export function soulListMyAgents(_token: string): Promise<SoulMineAgentsResponse> {
	void _token;
	return Promise.resolve(FIXTURE_AGENTS);
}
