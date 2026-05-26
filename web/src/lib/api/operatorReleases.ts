/* ============================================================================
 * operatorReleases — M2.8 endpoint client (forward-compatible).
 *
 * Shape matches the documented contract in
 * `docs/provisioning-web-ui-rework-2026-05-24.md` Change 5.2:
 *
 *   GET /api/v1/operators/releases
 *   {
 *     "channels": [
 *       {
 *         "id": "lesser",
 *         "versions": [
 *           {
 *             "version": "v1.4.2",
 *             "released_at": "...",
 *             "is_latest": true,
 *             "is_breaking": false,
 *             "adoption": { "instances": 7, "of": 12, "percent": 58 }
 *           }
 *         ]
 *       },
 *       { "id": "lesser-body", "versions": [...] }
 *     ],
 *     "fleet_total": 12
 *   }
 *
 * Until M2.8 (issue #436) lands, `listOperatorReleases()` catches the
 * expected 404 / 501 and returns an `endpoint-pending` sentinel so the
 * /operator/releases page can render a "telemetry pending" placeholder
 * without blocking. Same fault-tolerance pattern as
 * `listOperatorInstancesDrift` (M2.3) and the M1.6 stack endpoint.
 *
 * Aligned to documented contract after PR #512 arch review 4363557132
 * Blocker 2.
 * ========================================================================== */

import { fetchJson } from './http';

/** Channel id = which release surface this entry belongs to. */
export type ReleaseChannelId = 'lesser' | 'lesser-body';

export interface ReleaseAdoption {
	/** Number of managed instances on this version. */
	instances: number;
	/** Total managed instances (fleet size). */
	of: number;
	/** Adoption percent (0-100). Server-computed: round(instances/of*100). */
	percent: number;
}

export interface ReleaseVersionEntry {
	version: string;
	released_at: string;
	is_latest?: boolean;
	is_breaking?: boolean;
	adoption?: ReleaseAdoption;
	/** Optional summary blurb the timeline can render under the version card. */
	summary?: string;
}

export interface ReleaseChannelData {
	id: ReleaseChannelId | string;
	versions: ReleaseVersionEntry[];
}

export interface ListOperatorReleasesResponse {
	channels: ReleaseChannelData[];
	fleet_total: number;
}

export type ListOperatorReleasesResult =
	| { kind: 'data'; data: ListOperatorReleasesResponse }
	| { kind: 'endpoint-pending'; status: number };

export async function listOperatorReleases(token: string): Promise<ListOperatorReleasesResult> {
	try {
		const data = await fetchJson<ListOperatorReleasesResponse>('/api/v1/operators/releases', {
			headers: {
				authorization: `Bearer ${token}`,
			},
		});
		return { kind: 'data', data };
	} catch (err) {
		const status = (err as { status?: number })?.status ?? 0;
		if (status === 404 || status === 501) {
			return { kind: 'endpoint-pending', status };
		}
		throw err;
	}
}

/** Extract versions for a specific channel id; empty list if not present. */
export function channelVersions(
	data: ListOperatorReleasesResponse | undefined,
	id: ReleaseChannelId,
): ReleaseVersionEntry[] {
	if (!data) return [];
	const found = data.channels.find((c) => c.id === id);
	return found?.versions ?? [];
}
