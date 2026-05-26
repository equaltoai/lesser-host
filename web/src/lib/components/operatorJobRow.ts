/* ============================================================================
 * operatorJobRow — unified row shape for the Operator Provisioning list.
 *
 * Project 39 M2.3 (issue #429) requires the operator Provisioning list to
 * surface ProvisionJob + UpdateJob rows side by side with derived Kind
 * labels. Each row needs the same affordances (status badge, retry CTA
 * where applicable, view link) regardless of source, so we normalise both
 * into a single `OperatorJobRow` shape and let the page render through
 * the same loop.
 *
 * The derived `kind` is the JobKind discriminator used by JobKindBadge:
 *
 *   ProvisionJob              → 'provision'
 *   UpdateJob (kind='lesser') → 'update-lesser'
 *   UpdateJob (kind='body' /
 *              body_only)     → 'update-body'
 *   UpdateJob (kind='mcp' /
 *              mcp_only)      → 'wire-mcp'
 *
 * Aligned with arch review 4363557132 Blocker 4 on PR #512.
 * ========================================================================== */

import type {
	OperatorProvisionJobListItem,
	OperatorUpdateJobListItem,
} from 'src/lib/api/operatorProvisioning';
import type { JobKind } from './JobKindBadge.svelte';
import { deriveProvisionJobKind, deriveUpdateJobKind } from './jobKind';

export type OperatorJobSource = 'provision' | 'update';

export interface OperatorJobRow {
	id: string;
	source: OperatorJobSource;
	kind: JobKind;
	instance_slug: string;
	status: string;
	step?: string;
	attempts?: number;
	max_attempts?: number;
	run_id?: string;
	/** Real CodeBuild console URL if the backend supplies one. */
	run_url?: string;
	request_id?: string;
	error_code?: string;
	error_message?: string;
	created_at: string;
	updated_at: string;
	/** SPA path where the operator can view full details. */
	detail_path: string;
	/** True if the row supports a retry CTA on error (ProvisionJob only today). */
	retryable: boolean;
}

/** Normalise a ProvisionJob list item into the unified row shape. */
export function provisionJobToRow(job: OperatorProvisionJobListItem): OperatorJobRow {
	return {
		id: job.id,
		source: 'provision',
		kind: deriveProvisionJobKind(),
		instance_slug: job.instance_slug,
		status: job.status,
		step: job.step,
		attempts: job.attempts,
		max_attempts: job.max_attempts,
		run_id: job.run_id,
		// ProvisionJob does not yet expose a real run_url; the eventual
		// backend field will populate this and the timeline log link.
		run_url: undefined,
		request_id: job.request_id,
		error_code: job.error_code,
		error_message: job.error_message,
		created_at: job.created_at,
		updated_at: job.updated_at,
		detail_path: `/operator/provisioning/jobs/${job.id}`,
		retryable: true,
	};
}

/** Normalise an UpdateJob list item into the unified row shape. */
export function updateJobToRow(job: OperatorUpdateJobListItem): OperatorJobRow {
	const kind = deriveUpdateJobKind(job);
	// Prefer the most specific run_url available. The UpdateJob response
	// already exposes per-phase URLs (deploy / body / mcp); fall back to
	// the top-level `run_url` if no phase-specific one is present.
	const runUrl =
		job.deploy_run_url ||
		job.body_run_url ||
		job.mcp_run_url ||
		job.run_url ||
		undefined;
	// `active_phase` is the canonical "current step" for update jobs; fall
	// back to the legacy `step` field if set.
	const step = job.active_phase || job.failed_phase || job.step || undefined;
	return {
		id: job.id,
		source: 'update',
		kind,
		instance_slug: job.instance_slug,
		status: job.status,
		step,
		attempts: job.attempts,
		max_attempts: job.max_attempts,
		run_id: job.run_id,
		run_url: runUrl,
		request_id: job.request_id,
		error_code: job.error_code,
		error_message: job.error_message,
		created_at: job.created_at,
		updated_at: job.updated_at,
		// Per-slug detail page surfaces UpdateJob history at the customer-portal
		// instance detail view; operator's view of per-instance updates routes
		// through the existing /operator/instances/{slug} support page.
		detail_path: `/operator/instances/${job.instance_slug}`,
		retryable: false,
	};
}

/**
 * Merge provision rows and update rows into a single sorted feed, newest
 * `updated_at` first. Stable on equal timestamps.
 */
export function mergeJobRows(
	provisions: OperatorProvisionJobListItem[],
	updates: OperatorUpdateJobListItem[],
): OperatorJobRow[] {
	const rows: OperatorJobRow[] = [
		...provisions.map(provisionJobToRow),
		...updates.map(updateJobToRow),
	];
	rows.sort((a, b) => {
		// Descending by updated_at; ISO-8601 lexicographic sort = chronological.
		if (a.updated_at < b.updated_at) return 1;
		if (a.updated_at > b.updated_at) return -1;
		return 0;
	});
	return rows;
}
