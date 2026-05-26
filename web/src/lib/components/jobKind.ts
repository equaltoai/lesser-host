/* ============================================================================
 * jobKind — derive the JobKind discriminator for the Operator Console.
 *
 * Project 39 M2.3 (issue #429). The provisioning walk's kind table:
 *
 *   - ProvisionJob                            → 'provision'
 *   - UpdateJob with kind === 'mcp' or
 *     mcp_only === true                       → 'wire-mcp'
 *   - UpdateJob with kind === 'body' or
 *     body_only === true                      → 'update-body'
 *   - UpdateJob with kind === 'lesser' or
 *     (no body_only / mcp_only flag)          → 'update-lesser'
 *
 * The kind is UI-derived; the underlying records carry distinguishing
 * fields but not the unified label. Keeping the derivation in one
 * helper avoids the same conditional pasted across list and detail
 * surfaces.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.3
 * ========================================================================== */

import type { JobKind } from './JobKindBadge.svelte';

/**
 * Minimal discriminator shape — enough to derive the kind without
 * coupling this helper to a specific API response type. Both
 * `OperatorProvisionJobListItem` and `UpdateJobResponse` satisfy this.
 */
export interface KindDiscriminator {
	/** Optional discriminator field present on UpdateJob; absent on ProvisionJob. */
	kind?: string;
	/** Optional boolean discriminators on UpdateJob (mcp/body specialised). */
	mcp_only?: boolean;
	body_only?: boolean;
}

/**
 * Derive the JobKind from an UpdateJob-shaped record. ProvisionJobs
 * should be tagged externally via `deriveProvisionJobKind()` because
 * the field name `kind` is unused on that resource.
 */
export function deriveUpdateJobKind(input: KindDiscriminator): JobKind {
	const k = (input.kind || '').toLowerCase();
	if (k === 'mcp' || k === 'wire-mcp' || k === 'mcp-wire' || input.mcp_only) return 'wire-mcp';
	if (k === 'body' || k === 'update-body' || input.body_only) return 'update-body';
	if (k === 'lesser' || k === 'update-lesser' || k === '') return 'update-lesser';
	return 'unknown';
}

/** ProvisionJob is always 'provision' (first-time per-slug). */
export function deriveProvisionJobKind(): JobKind {
	return 'provision';
}
