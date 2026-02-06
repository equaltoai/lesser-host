import { describe, expect, it } from 'vitest';

import type { InstanceResponse } from 'src/lib/api/portalInstances';
import {
	buildUpdateInstanceConfigRequest,
	configFormFromInstance,
	validateInstanceConfigForm,
} from './instanceConfig';

function fakeInstance(overrides: Partial<InstanceResponse> = {}): InstanceResponse {
	return {
		slug: 'demo',
		status: 'active',
		hosted_previews_enabled: true,
		link_safety_enabled: true,
		renders_enabled: true,
		render_policy: 'suspicious',
		overage_policy: 'block',
		moderation_enabled: true,
		moderation_trigger: 'on_reports',
		moderation_virality_min: 0,
		ai_enabled: false,
		ai_model_set: 'default',
		ai_batching_mode: 'none',
		ai_batch_max_items: 10,
		ai_batch_max_total_bytes: 1024,
		ai_pricing_multiplier_bps: 10_000,
		ai_max_inflight_jobs: 10,
		created_at: '2026-01-01T00:00:00Z',
		...overrides,
	} as InstanceResponse;
}

describe('instanceConfig', () => {
	it('round-trips a valid instance into a valid request', () => {
		const form = configFormFromInstance(fakeInstance());
		const errors = validateInstanceConfigForm(form);
		expect(errors).toEqual({});

		const req = buildUpdateInstanceConfigRequest(form);
		expect(req.render_policy).toBe('suspicious');
		expect(req.ai_model_set).toBe('default');
		expect(req.ai_batch_max_items).toBe(10);
	});

	it('flags invalid numeric fields', () => {
		const form = configFormFromInstance(fakeInstance());
		form.ai_batch_max_items = '0';
		form.ai_pricing_multiplier_bps = '1000001';
		form.moderation_virality_min = '-1';

		const errors = validateInstanceConfigForm(form);
		expect(errors.ai_batch_max_items).toBeDefined();
		expect(errors.ai_pricing_multiplier_bps).toBeDefined();
		expect(errors.moderation_virality_min).toBeDefined();
	});
});

