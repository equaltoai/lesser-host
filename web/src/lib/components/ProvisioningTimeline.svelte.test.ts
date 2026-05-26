/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

import type { JobKind } from './JobKindBadge.svelte';
import {
	AUTO_QUEUED_WIRE_MCP_LABEL,
	AUTO_QUEUED_WIRE_MCP_STEP,
	classifySteps,
	isAutoQueuedStep,
	stepLabel,
	stepsForKind,
} from './ProvisioningTimeline.svelte';

/*
 * M2.13 unit coverage for the ProvisioningTimeline helpers.
 *
 * Focused on three contracts:
 *   1. stepsForKind enumerates the right step list per kind and only
 *      appends the auto-queued gated sentinel to `update-body`.
 *   2. classifySteps marks the gated step `auto-queued` regardless of
 *      job state, never mistakes it for the active step, and preserves
 *      pre-existing classification rules for real steps.
 *   3. The sentinel is opaque from the outside: callers reach for it
 *      via isAutoQueuedStep + stepLabel, never by matching the raw
 *      string.
 */

describe('ProvisioningTimeline.stepsForKind', () => {
	it('enumerates the provision step machine verbatim from provisionworker', () => {
		const steps = stepsForKind('provision', undefined);
		expect(steps).toEqual([
			'queued',
			'account.create',
			'account.create.poll',
			'account.move',
			'account.assumeRole',
			'dns.childZone',
			'dns.parentDelegation',
			'instance.config',
			'deploy.start',
			'deploy.wait',
			'receipt.ingest',
			'body.deploy.start',
			'body.deploy.wait',
			'deploy.mcp.start',
			'deploy.mcp.wait',
			'done',
		]);
	});

	it('enumerates update-lesser as deploy → verify with no gated step', () => {
		const steps = stepsForKind('update-lesser', undefined);
		expect(steps).toEqual(['deploy', 'verify']);
		expect(steps.some(isAutoQueuedStep)).toBe(false);
	});

	it('enumerates update-body as body → verify → auto-queued wire-mcp gated step', () => {
		const steps = stepsForKind('update-body', undefined);
		expect(steps).toEqual(['body', 'verify', AUTO_QUEUED_WIRE_MCP_STEP]);
		expect(steps.filter(isAutoQueuedStep)).toHaveLength(1);
		// The gated step is always terminal.
		expect(steps[steps.length - 1]).toBe(AUTO_QUEUED_WIRE_MCP_STEP);
	});

	it('enumerates wire-mcp as mcp → verify with no gated step', () => {
		const steps = stepsForKind('wire-mcp', undefined);
		expect(steps).toEqual(['mcp', 'verify']);
		expect(steps.some(isAutoQueuedStep)).toBe(false);
	});

	it('falls back to a single node for unknown kind when activeStep is set', () => {
		const steps = stepsForKind('unknown', 'something.unmapped');
		expect(steps).toEqual(['something.unmapped']);
	});

	it('returns an empty list for unknown kind without an active step', () => {
		const steps = stepsForKind('unknown', undefined);
		expect(steps).toEqual([]);
	});

	it('appends an unknown active step BEFORE the auto-queued gated step on update-body', () => {
		// A future backend phase that this UI doesn't know about. The gated
		// step must remain visually terminal so the operator still reads it
		// as a downstream consequence.
		const steps = stepsForKind('update-body', 'body.deploy.future-phase');
		expect(steps).toEqual([
			'body',
			'verify',
			'body.deploy.future-phase',
			AUTO_QUEUED_WIRE_MCP_STEP,
		]);
	});

	it('does not append a known active step (already in the enumerated list)', () => {
		const steps = stepsForKind('update-body', 'body');
		expect(steps).toEqual(['body', 'verify', AUTO_QUEUED_WIRE_MCP_STEP]);
	});

	it('matches the active step case-insensitively before deciding to append', () => {
		const steps = stepsForKind('update-lesser', 'DEPLOY');
		expect(steps).toEqual(['deploy', 'verify']);
	});
});

describe('ProvisioningTimeline.classifySteps', () => {
	it('marks all real update-body steps active/pending/auto-queued during body deploy', () => {
		const steps = stepsForKind('update-body', 'body');
		expect(classifySteps(steps, 'body', 'running')).toEqual([
			'active',
			'pending',
			'auto-queued',
		]);
	});

	it('keeps the gated step auto-queued even when the job has succeeded', () => {
		const steps = stepsForKind('update-body', undefined);
		// status='ok' would normally backfill every real step as completed.
		expect(classifySteps(steps, undefined, 'ok')).toEqual([
			'completed',
			'completed',
			'auto-queued',
		]);
	});

	it('keeps the gated step auto-queued when the job has failed', () => {
		const steps = stepsForKind('update-body', 'body');
		expect(classifySteps(steps, 'body', 'error')).toEqual([
			'failed',
			'pending',
			'auto-queued',
		]);
	});

	it('never marks the gated sentinel as the active step', () => {
		// Even if the backend somehow returned the sentinel as the active
		// step (it would not — the sentinel is UI-only), classification
		// refuses to elect it as active because the sentinel is filtered
		// out of the findIndex pool.
		const steps = stepsForKind('update-body', undefined);
		const result = classifySteps(steps, AUTO_QUEUED_WIRE_MCP_STEP, 'running');
		// activeIndex resolves to -1 → real steps go pending, gated stays auto-queued.
		expect(result).toEqual(['pending', 'pending', 'auto-queued']);
	});

	it('matches active step case-insensitively', () => {
		const steps = stepsForKind('update-lesser', 'DEPLOY');
		expect(classifySteps(steps, 'DEPLOY', 'running')).toEqual(['active', 'pending']);
	});

	it('marks all steps pending when status is empty and no active step', () => {
		const steps = stepsForKind('wire-mcp', undefined);
		expect(classifySteps(steps, undefined, '')).toEqual(['pending', 'pending']);
	});
});

describe('ProvisioningTimeline sentinel helpers', () => {
	it('stepLabel folds the auto-queued sentinel to its human label', () => {
		expect(stepLabel(AUTO_QUEUED_WIRE_MCP_STEP)).toBe(AUTO_QUEUED_WIRE_MCP_LABEL);
	});

	it('stepLabel passes through real step names unchanged', () => {
		expect(stepLabel('body')).toBe('body');
		expect(stepLabel('deploy.wait')).toBe('deploy.wait');
	});

	it('isAutoQueuedStep distinguishes the sentinel from real step names', () => {
		expect(isAutoQueuedStep(AUTO_QUEUED_WIRE_MCP_STEP)).toBe(true);
		expect(isAutoQueuedStep('body')).toBe(false);
		expect(isAutoQueuedStep('verify')).toBe(false);
		expect(isAutoQueuedStep('mcp')).toBe(false);
	});

	it('sentinel string cannot collide with real backend step constants', () => {
		// The colon separator distinguishes the UI sentinel from the
		// dotted backend constants (e.g. body.deploy.start, deploy.mcp.wait).
		// Guard against accidental sentinel rename to a dotted form.
		expect(AUTO_QUEUED_WIRE_MCP_STEP).toContain(':');
		expect(AUTO_QUEUED_WIRE_MCP_STEP).not.toContain('.');
	});
});

describe('ProvisioningTimeline.svelte source — M2.13 acceptance', () => {
	// Source-level assertions guard the component's visible contract:
	// the auto-queued pill, the gated indicator styling, and the
	// `data-state="auto-queued"` analytics hook.
	const source = readFileSync(
		join(process.cwd(), 'src/lib/components/ProvisioningTimeline.svelte'),
		'utf8',
	);
	const kinds: JobKind[] = ['provision', 'update-lesser', 'update-body', 'wire-mcp', 'unknown'];

	it('enumerates a STEPS_BY_KIND entry for every JobKind', () => {
		for (const kind of kinds) {
			expect(source).toContain(`'${kind}':`);
		}
	});

	it('renders an "Auto-queued" pill for the gated step', () => {
		expect(source).toContain("timeline__pill--auto-queued");
		expect(source).toContain('>Auto-queued<');
	});

	it('declares the auto-queued indicator style block', () => {
		expect(source).toContain('.timeline__step--auto-queued .timeline__indicator');
	});
});
