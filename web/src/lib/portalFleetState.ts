/* ============================================================================
 * Portal fleet shared state (host web/)
 *
 * Module-scoped `writable` store that bridges `PortalFleet.svelte` (the
 * fleet view that owns the data fetch) and `PortalShell.svelte` (the
 * surrounding shell that owns the CommandPalette). PortalFleet writes
 * the loaded instances here on every successful fetch; PortalShell
 * subscribes and builds per-instance "Open <slug>" command palette
 * entries from the snapshot.
 *
 * Why a module store and not Svelte 5 context:
 *   - The fleet view is a child of the shell in render order, but
 *     contexts set by children aren't visible to parents (context
 *     descends, it doesn't ascend).
 *   - A module-scoped store is read identically in both components,
 *     matches the existing `session.ts` precedent, and stays trivially
 *     testable.
 *
 * Lifecycle:
 *   - PortalFleet calls `portalFleetInstances.set([...])` after
 *     `portalListInstances` succeeds (or `.set([])` when the user has
 *     no instances or load fails — fail-quiet for the palette).
 *   - PortalShell subscribes via the standard `$` rune.
 *   - The store is reset to `[]` on logout via `clearPortalFleetState()`
 *     so an old user's slugs never leak into a new user's palette.
 *
 * Posture:
 *   - The store holds only the customer's own slugs (read via the
 *     existing per-owner-scoped portal endpoint) — no multi-tenant
 *     leakage shape.
 *   - No raw API keys or tokens are written here.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.11
 * Issue: equaltoai/lesser-host#391
 * ========================================================================== */

import { writable } from 'svelte/store';

/**
 * Minimal projection of the fleet for command-palette command building.
 * Keep this surface narrow so the palette has a stable contract even
 * when the upstream `InstanceResponse` shape grows new fields.
 */
export interface PortalFleetEntry {
	slug: string;
	hosted_region?: string;
	lesser_version?: string;
}

/**
 * Module-scoped writable store with the current fleet snapshot. Empty
 * array when the user is signed out, has no instances, or the fleet
 * load has not yet completed (or failed).
 */
export const portalFleetInstances = writable<PortalFleetEntry[]>([]);

/**
 * Reset the store. Call from the logout flow so an old session's
 * slugs never appear in a new session's command palette.
 */
export function clearPortalFleetState(): void {
	portalFleetInstances.set([]);
}
