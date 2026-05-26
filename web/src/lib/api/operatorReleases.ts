/* ============================================================================
 * operatorReleases — M2.8 endpoint client (forward-compatible).
 *
 * Project 39 M2.5 (issue #431) consumes the `/api/v1/operators/releases`
 * aggregation endpoint shipped in M2.8 (issue #436). Until M2.8 lands,
 * `listOperatorReleases()` catches the expected 404 / 501 and returns an
 * `endpoint-pending` sentinel so the /operator/releases page can render
 * a "release telemetry pending" placeholder without blocking.
 *
 * Adoption: clients receive `adoption_pct` (0-100) per version so the
 * release-timeline component can render an adoption bar.
 *
 * Same fault-tolerance pattern as `listOperatorInstancesDrift` (M2.3)
 * and the M1.6 stack endpoint (mem-06120131ee628046).
 * ========================================================================== */

import { fetchJson } from './http';

/** Channel = which release surface this entry belongs to. */
export type ReleaseChannel = 'lesser' | 'lesser-body';

export interface ReleaseEntry {
	version: string;
	released_at: string;
	is_latest?: boolean;
	is_breaking?: boolean;
	/** Adoption percentage across managed instances (0..100). */
	adoption_pct?: number;
	/** Optional summary blurb the timeline can render under the version card. */
	summary?: string;
}

export interface ReleaseChannelData {
	channel: ReleaseChannel;
	entries: ReleaseEntry[];
}

export interface ListOperatorReleasesResponse {
	channels: ReleaseChannelData[];
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

/** Extract entries for a specific channel; empty list if not present. */
export function channelEntries(
	data: ListOperatorReleasesResponse | undefined,
	channel: ReleaseChannel,
): ReleaseEntry[] {
	if (!data) return [];
	const found = data.channels.find((c) => c.channel === channel);
	return found?.entries ?? [];
}
