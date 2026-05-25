/* ============================================================================
 * Fleet-status mapping (host web/)
 *
 * Pure function `mapInstanceFleetStatus()` projects the host backend's
 * `InstanceResponse.status` + `InstanceResponse.provision_status` onto the
 * greater-components `FleetCardStatus` taxonomy used by the fleet card
 * badge + accessible label.
 *
 * Authoritative backend taxonomy (canonical sources in this repo):
 *
 *   `internal/store/models/instance.go`:
 *     InstanceStatusActive   = "active"
 *     InstanceStatusDisabled = "disabled"
 *
 *   `internal/store/models/provision_job.go`:
 *     ProvisionJobStatusQueued  = "queued"
 *     ProvisionJobStatusRunning = "running"
 *     ProvisionJobStatusOK      = "ok"     // terminal success
 *     ProvisionJobStatusError   = "error"
 *
 * Mapping rules (matched in order):
 *
 *   1. provision_status === 'queued' | 'running'   → 'provisioning'
 *   2. provision_status === 'error' | 'failed'     → 'degraded'
 *      (`'failed'` retained as forward-compatibility for any future
 *      backend change toward a more descriptive failure value)
 *   3. provision_status ∈ {'ok','done','complete','completed',''}
 *                                                  → fall through to
 *                                                    `status` branch
 *      (`'ok'` is the backend's terminal success; `done|complete|completed`
 *      are retained for defensive compat with any older instances or
 *      manual-overrides; empty / unset means no provision job recorded.)
 *   4. status === 'active' | 'running' | 'healthy'     → 'healthy'
 *   5. status === 'disabled' | 'suspended' | 'paused' |
 *                'stopped'                              → 'offline'
 *      (`'disabled'` is the backend's lifecycle pause state; the others
 *      are forward-compat aliases.)
 *   6. status === 'failed' | 'error' | 'degraded'      → 'degraded'
 *   7. status === 'warning'                            → 'warning'
 *   8. else                                            → 'unknown'
 *
 * Extracted from PortalFleet.svelte's inline `mapStatus()` so it can be
 * tested independently (`fleetStatus.test.ts`) and locked against future
 * backend taxonomy drift. Arch review on PR #499 flagged the original
 * inline mapping as treating `'ok'` as still-provisioning; this extraction
 * + test fixes that.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.11
 * Issue: equaltoai/lesser-host#391
 * Arch review: https://github.com/equaltoai/lesser-host/pull/499#pullrequestreview-4357356996
 * ========================================================================== */

import type { FleetCardStatus } from 'src/lib/greater/host-platform';

/**
 * Minimal projection of `InstanceResponse` needed for the status mapping.
 *
 * Both fields are deliberately optional here — even though `InstanceResponse`
 * declares `status: string` as required, defensive coding (the function
 * guards with `?? ''` already) plus test ergonomics (`{}` as
 * "completely empty input" without an annoying cast) justify the
 * permissive shape. The wire-side schema stays the source of truth for
 * what the backend actually sends.
 */
export interface FleetStatusInput {
	status?: string;
	provision_status?: string;
}

/**
 * Map a host backend instance row onto the FleetCard status badge value.
 *
 * Case-insensitive on both input fields. Returns `'unknown'` rather than
 * guessing for unrecognized states, so the badge text matches the
 * underlying state honestly.
 *
 * @public
 */
export function mapInstanceFleetStatus(inst: FleetStatusInput): FleetCardStatus {
	const provision = (inst.provision_status ?? '').toLowerCase();
	switch (provision) {
		case 'queued':
		case 'running':
			return 'provisioning';
		case 'error':
		case 'failed':
			return 'degraded';
		// 'ok' (terminal success), 'done'/'complete'/'completed' (defensive
		// legacy compat), and '' (no provision job recorded) fall through
		// to the status branch below.
		default:
			break;
	}

	const status = (inst.status ?? '').toLowerCase();
	switch (status) {
		case 'active':
		case 'running':
		case 'healthy':
			return 'healthy';
		case 'disabled':
		case 'suspended':
		case 'paused':
		case 'stopped':
			return 'offline';
		case 'failed':
		case 'error':
		case 'degraded':
			return 'degraded';
		case 'warning':
			return 'warning';
		default:
			return 'unknown';
	}
}
