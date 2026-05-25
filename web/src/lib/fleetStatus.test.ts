/* ============================================================================
 * Tests for fleet-status mapping.
 *
 * Locks `mapInstanceFleetStatus()` against backend-taxonomy drift. Each
 * case states the backend signal explicitly so a future change to
 * `internal/store/models/{instance,provision_job}.go` that flips an
 * assumption produces a test failure rather than a silently-wrong fleet
 * badge.
 *
 * Authoritative backend taxonomy mirrored here:
 *   provision_status: 'queued' | 'running' | 'ok' | 'error'
 *   status:           'active' | 'disabled' (+ forward-compat aliases)
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.11
 * Arch review: https://github.com/equaltoai/lesser-host/pull/499#pullrequestreview-4357356996
 * ========================================================================== */

import { describe, expect, it } from 'vitest';

import { mapInstanceFleetStatus } from './fleetStatus';

describe('mapInstanceFleetStatus — provision_status branch', () => {
	it('returns provisioning for queued provision', () => {
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'queued' })).toBe(
			'provisioning'
		);
	});

	it('returns provisioning for running provision', () => {
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'running' })).toBe(
			'provisioning'
		);
	});

	it('returns degraded for error provision', () => {
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'error' })).toBe(
			'degraded'
		);
	});

	it('returns degraded for failed provision (forward-compat alias)', () => {
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'failed' })).toBe(
			'degraded'
		);
	});

	it('falls through for ok provision (terminal success)', () => {
		// THIS is the case Arch's blocking review caught: the backend's
		// terminal success value is 'ok', not 'done'/'complete'/'completed',
		// so a successfully-provisioned active instance MUST resolve to
		// 'healthy' rather than 'provisioning'.
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'ok' })).toBe('healthy');
	});

	it('falls through for empty provision_status', () => {
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: '' })).toBe('healthy');
	});

	it('falls through for undefined provision_status', () => {
		expect(mapInstanceFleetStatus({ status: 'active' })).toBe('healthy');
	});

	it('falls through for legacy done/complete/completed (defensive compat)', () => {
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'done' })).toBe('healthy');
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'complete' })).toBe(
			'healthy'
		);
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'completed' })).toBe(
			'healthy'
		);
	});
});

describe('mapInstanceFleetStatus — status branch (after provision fall-through)', () => {
	it('maps active to healthy', () => {
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'ok' })).toBe('healthy');
	});

	it('maps disabled to offline (backend lifecycle pause)', () => {
		// THIS is the second case Arch's blocking review caught: the
		// backend lifecycle uses `status: 'disabled'` for paused instances,
		// not `suspended|paused|stopped`. Disabled MUST map to 'offline'.
		expect(mapInstanceFleetStatus({ status: 'disabled', provision_status: 'ok' })).toBe('offline');
	});

	it('maps suspended/paused/stopped to offline (forward-compat aliases)', () => {
		expect(mapInstanceFleetStatus({ status: 'suspended' })).toBe('offline');
		expect(mapInstanceFleetStatus({ status: 'paused' })).toBe('offline');
		expect(mapInstanceFleetStatus({ status: 'stopped' })).toBe('offline');
	});

	it('maps running/healthy to healthy (forward-compat aliases)', () => {
		expect(mapInstanceFleetStatus({ status: 'running' })).toBe('healthy');
		expect(mapInstanceFleetStatus({ status: 'healthy' })).toBe('healthy');
	});

	it('maps failed/error/degraded to degraded', () => {
		expect(mapInstanceFleetStatus({ status: 'failed' })).toBe('degraded');
		expect(mapInstanceFleetStatus({ status: 'error' })).toBe('degraded');
		expect(mapInstanceFleetStatus({ status: 'degraded' })).toBe('degraded');
	});

	it('maps warning to warning', () => {
		expect(mapInstanceFleetStatus({ status: 'warning' })).toBe('warning');
	});

	it('returns unknown for unrecognized status', () => {
		expect(mapInstanceFleetStatus({ status: 'fubar' })).toBe('unknown');
	});

	it('returns unknown for completely empty input (both fields missing)', () => {
		expect(mapInstanceFleetStatus({})).toBe('unknown');
	});
});

describe('mapInstanceFleetStatus — case-insensitivity', () => {
	it('uppercase status + provision_status still classify correctly', () => {
		expect(mapInstanceFleetStatus({ status: 'ACTIVE', provision_status: 'OK' })).toBe('healthy');
		expect(mapInstanceFleetStatus({ status: 'DISABLED', provision_status: 'OK' })).toBe('offline');
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'QUEUED' })).toBe(
			'provisioning'
		);
	});
});

describe('mapInstanceFleetStatus — provision branch precedence', () => {
	it('provision_status takes precedence over status when active provisioning', () => {
		// Even if lifecycle status is 'active', an in-flight provision job
		// must surface as 'provisioning' so operators see the in-flight
		// state.
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'running' })).toBe(
			'provisioning'
		);
	});

	it('provision_status takes precedence over status when error', () => {
		expect(mapInstanceFleetStatus({ status: 'active', provision_status: 'error' })).toBe(
			'degraded'
		);
	});
});
