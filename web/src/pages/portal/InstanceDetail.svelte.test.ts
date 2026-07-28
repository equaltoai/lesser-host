/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { mount, unmount } from 'svelte';
import { tick } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { InstanceResponse, ProvisionJobResponse } from 'src/lib/api/portalInstances';

import InstanceDetail from './InstanceDetail.svelte';

const source = readFileSync(join(process.cwd(), 'src/pages/portal/InstanceDetail.svelte'), 'utf8');

afterEach(() => {
	vi.restoreAllMocks();
	document.body.innerHTML = '';
});

describe('InstanceDetail managed update controls', () => {
	it('allows blank Lesser versions for configuration apply and key rotation', () => {
		expect(source).toContain('allowBlankLesserVersion?: boolean;');
		expect(source).toContain('allowBlank: options?.allowBlankLesserVersion ?? false');
		expect(source).toContain('allowBlankLesserVersion: true');

		const applyConfig = source.indexOf('Apply configuration');
		const rotateKey = source.indexOf('Rotate instance key');
		const firstAllowBlank = source.indexOf('allowBlankLesserVersion: true');
		const secondAllowBlank = source.indexOf('allowBlankLesserVersion: true', firstAllowBlank + 1);

		expect(firstAllowBlank).toBeGreaterThanOrEqual(0);
		expect(secondAllowBlank).toBeGreaterThan(firstAllowBlank);
		expect(firstAllowBlank).toBeLessThan(applyConfig);
		expect(secondAllowBlank).toBeLessThan(rotateKey);
	});
});

describe('InstanceDetail managed provisioning availability', () => {
	it('uses the backend managed flag consistently for provisioning and updates', () => {
		expect(source).toContain('function isManagedInstance(inst: InstanceResponse | null): boolean');
		expect(source).toContain('{:else if isManagedInstance(instance)}');
		expect(source).toContain('{@const managed = isManagedInstance(instance)}');
		expect(source).not.toContain('hosted_account_id && instance.hosted_region && instance.hosted_base_domain');
	});

	it('shows the Start provisioning form for a fresh managed instance with no region or account yet', async () => {
		const component = await renderInstanceDetail(
			makeInstance({
				managed: true,
				hosted_base_domain: 'fresh.greater.website',
				hosted_region: '',
				hosted_account_id: '',
				provision_status: '',
			}),
		);

		await waitForText(component.target, 'Not started');
		expect(component.target.textContent).toContain('Start managed provisioning');
		expect(component.target.textContent).toContain('Admin username');
		expect(component.target.textContent).toContain('Region (optional)');
		expect(component.target.textContent).toContain('Lesser version (optional)');
		expect(buttonText(component.target)).toContain('Start provisioning');
		expect(component.target.textContent).not.toContain('Externally registered');

		unmount(component.instance);
	});

	it('keeps externally registered instances on the external-only branch', async () => {
		const component = await renderInstanceDetail(
			makeInstance({
				managed: false,
				hosted_base_domain: '',
				hosted_region: '',
				hosted_account_id: '',
				provision_status: '',
			}),
		);

		await waitForText(component.target, 'Externally registered');
		expect(component.target.textContent).toContain('Managed provisioning does not apply');
		expect(buttonText(component.target)).not.toContain('Start provisioning');
		expect(component.target.textContent).not.toContain('Not started');

		unmount(component.instance);
	});

	it('renders provisioning job details after provisioning has started', async () => {
		const component = await renderInstanceDetail(
			makeInstance({
				managed: true,
				hosted_base_domain: 'fresh.greater.website',
				hosted_region: 'us-east-1',
				provision_status: 'queued',
				provision_job_id: 'job-queued',
			}),
			makeProvisionJob({ status: 'queued' }),
		);

		await waitForText(component.target, 'Base domain');
		expect(component.target.textContent).toContain('queued');
		expect(component.target.textContent).toContain('Base domain');
		expect(component.target.textContent).toContain('fresh.greater.website');
		expect(component.target.textContent).not.toContain('Not started');
		expect(component.target.textContent).not.toContain('Externally registered');

		unmount(component.instance);
	});
});

function makeInstance(overrides: Partial<InstanceResponse> = {}): InstanceResponse {
	return {
		slug: 'fresh',
		owner: 'alice',
		status: 'active',
		managed: false,
		translation_enabled: false,
		hosted_previews_enabled: true,
		link_safety_enabled: true,
		renders_enabled: true,
		render_policy: 'suspicious',
		overage_policy: 'block',
		moderation_enabled: false,
		moderation_trigger: 'on_reports',
		moderation_virality_min: 0,
		ai_enabled: false,
		ai_model_set: 'gpt-5.6-luna',
		ai_batching_mode: 'none',
		ai_batch_max_items: 8,
		ai_batch_max_total_bytes: 65_536,
		ai_pricing_multiplier_bps: 10_000,
		ai_max_inflight_jobs: 200,
		created_at: '2026-05-30T00:00:00Z',
		...overrides,
	};
}

function makeProvisionJob(overrides: Partial<ProvisionJobResponse> = {}): ProvisionJobResponse {
	return {
		id: 'job-queued',
		instance_slug: 'fresh',
		status: 'queued',
		step: 'account',
		base_domain: 'fresh.greater.website',
		admin_username: 'fresh',
		created_at: '2026-05-30T00:00:00Z',
		updated_at: '2026-05-30T00:01:00Z',
		...overrides,
	};
}

function jsonResponse(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json' },
	});
}

async function renderInstanceDetail(instance: InstanceResponse, provisioningJob: ProvisionJobResponse | null = null) {
	installInstanceDetailFetchMocks(instance, provisioningJob);

	const target = document.createElement('div');
	document.body.appendChild(target);
	const component = mount(InstanceDetail, { target, props: { token: 'tok_lh28', slug: instance.slug } });
	await tick();
	return { instance: component, target };
}

function installInstanceDetailFetchMocks(instance: InstanceResponse, provisioningJob: ProvisionJobResponse | null) {
	const slug = instance.slug;
	vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL) => {
		const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
		const url = new URL(raw, 'http://localhost');
		const path = url.pathname;

		if (path === `/api/v1/portal/instances/${encodeURIComponent(slug)}`) {
			return Promise.resolve(jsonResponse(instance));
		}
		if (path === `/api/v1/portal/instances/${encodeURIComponent(slug)}/domains`) {
			const domain = (instance.hosted_base_domain || '').trim();
			return Promise.resolve(
				jsonResponse({
					domains: domain
						? [
								{
									domain,
									instance_slug: slug,
									type: 'primary',
									status: 'verified',
									verification_method: instance.managed ? 'managed' : 'manual',
									created_at: '2026-05-30T00:00:00Z',
									updated_at: '2026-05-30T00:00:00Z',
								},
							]
						: [],
					count: domain ? 1 : 0,
				}),
			);
		}
		if (path === `/api/v1/portal/instances/${encodeURIComponent(slug)}/provision`) {
			if (!provisioningJob) {
				return Promise.resolve(jsonResponse({ message: 'not found' }, 404));
			}
			return Promise.resolve(jsonResponse(provisioningJob));
		}
		if (path === `/api/v1/portal/instances/${encodeURIComponent(slug)}/updates`) {
			return Promise.resolve(jsonResponse({ jobs: [], count: 0 }));
		}
		if (path === `/api/v1/portal/instances/${encodeURIComponent(slug)}/stack`) {
			return Promise.resolve(jsonResponse({ message: 'not found' }, 404));
		}
		return Promise.resolve(jsonResponse({ message: `unexpected ${path}` }, 500));
	});
}

async function waitForText(target: HTMLElement, expected: string) {
	for (let i = 0; i < 30; i += 1) {
		await tick();
		await new Promise((resolve) => setTimeout(resolve, 0));
		if (target.textContent?.includes(expected)) {
			return;
		}
	}
	expect(target.textContent).toContain(expected);
}

function buttonText(target: HTMLElement): string {
	return Array.from(target.querySelectorAll('button'))
		.map((button) => button.textContent || '')
		.join('\n');
}
