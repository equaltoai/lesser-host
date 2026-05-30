import { mount } from 'svelte';
import { tick } from 'svelte';
import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('src/lib/api/portalInstances', () => ({
	portalCreateInstance: vi.fn(),
	portalListInstances: vi.fn(),
}));

vi.mock('src/lib/api/portalUsage', () => ({
	portalGetBudgetMonth: vi.fn(),
}));

vi.mock('src/lib/router', async () => {
	const actual = await vi.importActual<typeof import('src/lib/router')>('src/lib/router');
	return { ...actual, navigate: vi.fn() };
});

import PortalFleet from './PortalFleet.svelte';
import type { InstanceResponse, ListInstancesResponse } from 'src/lib/api/portalInstances';
import { portalListInstances } from 'src/lib/api/portalInstances';
import { portalGetBudgetMonth } from 'src/lib/api/portalUsage';
import { logout } from 'src/lib/auth/logout';
import { clearPortalFleetState, portalFleetInstances } from 'src/lib/portalFleetState';
import { clearSession, setSession } from 'src/lib/session';

const mockListInstances = vi.mocked(portalListInstances);
const mockGetBudgetMonth = vi.mocked(portalGetBudgetMonth);

function customerSession(token = 'test-token') {
	return {
		tokenType: 'Bearer' as const,
		token,
		expiresAt: new Date(Date.now() + 3600_000).toISOString(),
		username: 'wallet-user',
		role: 'customer',
		method: 'wallet',
	};
}

function makeInstance(overrides: Partial<InstanceResponse> = {}): InstanceResponse {
	return {
		slug: 'alpha',
		status: 'ok',
		hosted_region: 'us-east-1',
		lesser_version: 'v1.0.0',
		translation_enabled: false,
		hosted_previews_enabled: false,
		link_safety_enabled: false,
		renders_enabled: false,
		render_policy: '',
		overage_policy: '',
		moderation_enabled: false,
		moderation_trigger: '',
		moderation_virality_min: 0,
		ai_enabled: false,
		ai_model_set: '',
		ai_batching_mode: '',
		ai_batch_max_items: 0,
		ai_batch_max_total_bytes: 0,
		ai_pricing_multiplier_bps: 0,
		ai_max_inflight_jobs: 0,
		created_at: new Date().toISOString(),
		...overrides,
	};
}

async function flushAsync(): Promise<void> {
	await tick();
	await Promise.resolve();
	await new Promise((resolve) => setTimeout(resolve, 0));
	await tick();
}

describe('PortalFleet', () => {
	beforeEach(() => {
		mockListInstances.mockReset();
		mockGetBudgetMonth.mockReset();
		sessionStorage.clear();
		clearSession();
		clearPortalFleetState();
		document.body.innerHTML = '';
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		document.body.innerHTML = '';
		clearSession();
		clearPortalFleetState();
	});

	it('does not repopulate portal fleet command state when a slow load resolves after logout', async () => {
		const token = 'old-session-token';
		setSession(customerSession(token));

		let resolveList!: (value: ListInstancesResponse) => void;
		mockListInstances.mockReturnValue(
			new Promise((resolve) => {
				resolveList = resolve;
			}),
		);
		mockGetBudgetMonth.mockResolvedValue({
			instance_slug: 'alpha',
			month: '2026-05',
			included_credits: 100,
			used_credits: 10,
		});
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(
				new Response(JSON.stringify({ ok: true }), {
					status: 200,
					headers: { 'content-type': 'application/json' },
				}),
			),
		);

		const target = document.createElement('div');
		document.body.appendChild(target);
		mount(PortalFleet, { target, props: { token } });

		await tick();
		expect(mockListInstances).toHaveBeenCalledWith(token);
		expect(get(portalFleetInstances)).toEqual([]);

		await logout();
		expect(get(portalFleetInstances)).toEqual([]);

		resolveList({ instances: [makeInstance()], count: 1 });
		await flushAsync();

		expect(mockGetBudgetMonth).not.toHaveBeenCalled();
		expect(get(portalFleetInstances)).toEqual([]);
	});
});
