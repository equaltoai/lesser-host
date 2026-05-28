/**
 * M10 Souls Fixture — mock soul API.
 *
 * Returns deterministic soul-list data with realistic stage, anchor, and
 * tips values so the InstanceSouls component renders the design-aligned
 * table with fully populated rows.
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
			style_name?: string;
			renderer_address?: string;
			image?: string;
			selected?: boolean;
		}>;
	};
	anchor_assurance?: {
		state: 'hosted_offchain' | 'immutable_onchain';
		source: 'host_record' | 'onchain_receipt';
		capability_gate: false;
		mutable: boolean;
		revocable: boolean;
	};
	capabilities?: string[];
	status: string;
	lifecycle_status?: string;
	lifecycle_reason?: string;
	successor_agent_id?: string;
	predecessor_agent_id?: string;
	principal_address?: string;
	principal_signature?: string;
	principal_declaration?: string;
	principal_declared_at?: string;
	self_description_version?: number;
	mint_tx_hash?: string;
	minted_at?: string;
	updated_at?: string;
}

export interface SoulAgentReputation {
	agent_id: string;
	block_ref?: number;
	composite: number;
	economic: number;
	social: number;
	validation: number;
	trust: number;
	integrity?: number;
	tips_received: number;
	interactions: number;
	validations_passed: number;
	endorsements: number;
	flags: number;
	delegations_completed?: number;
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

// Design-aligned fixture data: 6 souls with varied stages, anchors, and tips.
// All bound to simulacrum.greater.website (matching the instance's domain).
const FIXTURE_AGENTS: SoulMineAgentItem[] = [
	{
		agent: {
			agent_id: 'agent-maeve-4f29abc123',
			domain: 'simulacrum.greater.website',
			local_id: 'maeve',
			wallet: '0x4f29abc123def4567890abc123def4567890f9c2',
			status: 'active',
			lifecycle_status: 'graduated',
			minted_at: new Date('2025-10-04').toISOString(),
			self_description_version: 3,
			anchor_assurance: {
				state: 'immutable_onchain',
				source: 'onchain_receipt',
				capability_gate: false,
				mutable: false,
				revocable: false,
			},
		},
		reputation: {
			agent_id: 'agent-maeve-4f29abc123',
			composite: 0.92,
			economic: 0.88,
			social: 0.95,
			validation: 0.91,
			trust: 0.94,
			tips_received: 18.40,
			interactions: 2840,
			validations_passed: 142,
			endorsements: 36,
			flags: 0,
		},
	},
	{
		agent: {
			agent_id: 'agent-atlas-b71f9c2a3d',
			domain: 'simulacrum.greater.website',
			local_id: 'atlas',
			wallet: '0x2b71f9c2a3df8810abc123def4567890abc1d2e',
			status: 'active',
			lifecycle_status: 'graduated',
			minted_at: new Date('2025-12-09').toISOString(),
			self_description_version: 2,
			anchor_assurance: {
				state: 'immutable_onchain',
				source: 'onchain_receipt',
				capability_gate: false,
				mutable: false,
				revocable: false,
			},
		},
		reputation: {
			agent_id: 'agent-atlas-b71f9c2a3d',
			composite: 0.78,
			economic: 0.72,
			social: 0.81,
			validation: 0.79,
			trust: 0.80,
			tips_received: 7.20,
			interactions: 1120,
			validations_passed: 67,
			endorsements: 18,
			flags: 1,
		},
	},
	{
		agent: {
			agent_id: 'agent-iris-8810f9c2a3',
			domain: 'simulacrum.greater.website',
			local_id: 'iris',
			wallet: '0x8810f9c2a3df4f29abc123def4567890abc1d2e',
			status: 'pending',
			lifecycle_status: 'in_review',
			minted_at: undefined,
			self_description_version: 1,
			anchor_assurance: {
				state: 'hosted_offchain',
				source: 'host_record',
				capability_gate: false,
				mutable: true,
				revocable: true,
			},
		},
		reputation: {
			agent_id: 'agent-iris-8810f9c2a3',
			composite: 0.45,
			economic: 0.30,
			social: 0.55,
			validation: 0.42,
			trust: 0.50,
			tips_received: 0,
			interactions: 240,
			validations_passed: 12,
			endorsements: 3,
			flags: 0,
		},
	},
	{
		agent: {
			agent_id: 'agent-hex-9c2a3df4f29',
			domain: 'simulacrum.greater.website',
			local_id: 'hex',
			wallet: '0x9c2a3df4f29abc123def4567890abc123def4f29',
			status: 'pending',
			lifecycle_status: 'requested',
			minted_at: undefined,
			self_description_version: 0,
			anchor_assurance: undefined,
		},
		reputation: undefined,
	},
	{
		agent: {
			agent_id: 'agent-ribbon-3df4f29abc1',
			domain: 'simulacrum.greater.website',
			local_id: 'ribbon',
			wallet: '0x3df4f29abc123def4567890abc123def4f29abc1',
			status: 'active',
			lifecycle_status: 'graduated',
			minted_at: new Date('2026-03-12').toISOString(),
			self_description_version: 2,
			anchor_assurance: {
				state: 'immutable_onchain',
				source: 'onchain_receipt',
				capability_gate: false,
				mutable: false,
				revocable: false,
			},
		},
		reputation: {
			agent_id: 'agent-ribbon-3df4f29abc1',
			composite: 0.85,
			economic: 0.82,
			social: 0.88,
			validation: 0.84,
			trust: 0.86,
			tips_received: 5.80,
			interactions: 1680,
			validations_passed: 89,
			endorsements: 24,
			flags: 0,
		},
	},
	{
		agent: {
			agent_id: 'agent-drone04-f29abc123de',
			domain: 'simulacrum.greater.website',
			local_id: 'drone-04',
			wallet: '0xf29abc123def4567890abc123def4f29abc123de',
			status: 'suspended',
			lifecycle_status: 'on_hold',
			lifecycle_reason: 'requires anchor refresh',
			minted_at: new Date('2026-04-11').toISOString(),
			self_description_version: 1,
			anchor_assurance: {
				state: 'hosted_offchain',
				source: 'host_record',
				capability_gate: false,
				mutable: true,
				revocable: true,
			},
		},
		reputation: {
			agent_id: 'agent-drone04-f29abc123de',
			composite: 0.35,
			economic: 0.28,
			social: 0.40,
			validation: 0.32,
			trust: 0.38,
			tips_received: 0,
			interactions: 120,
			validations_passed: 5,
			endorsements: 1,
			flags: 3,
		},
	},
];

export async function soulListMyAgents(_token: string): Promise<SoulMineAgentsResponse> {
	void _token;
	return { agents: FIXTURE_AGENTS, count: FIXTURE_AGENTS.length };
}
