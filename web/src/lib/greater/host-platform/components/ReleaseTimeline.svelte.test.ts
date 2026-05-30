/**
 * ReleaseTimeline link-safety tests.
 *
 * @license AGPL-3.0-only
 */
import { mount } from 'svelte';
import { describe, expect, it } from 'vitest';

import ReleaseTimeline from './ReleaseTimeline.svelte';
import type { ReleaseTimelineItem } from '../types.js';

function releaseWithHref(href: string): ReleaseTimelineItem {
	return {
		id: href,
		version: 'v1.2.3',
		channel: 'stable',
		date: '2026-05-30',
		status: 'shipped',
		href,
	};
}

function mountTimeline(releases: ReleaseTimelineItem[]) {
	const target = document.createElement('div');
	document.body.appendChild(target);
	mount(ReleaseTimeline, {
		target,
		props: {
			label: 'Release history',
			releases,
		},
	});
	return target;
}

describe('ReleaseTimeline release-note href safety', () => {
	it('renders valid https release notes links', () => {
		const target = mountTimeline([releaseWithHref('https://example.com/releases/v1.2.3')]);

		const link = target.querySelector<HTMLAnchorElement>(
			'.gr-host-platform-release-timeline__link',
		);
		expect(link).not.toBeNull();
		expect(link?.textContent?.trim()).toBe('Release notes');
		expect(link?.getAttribute('href')).toBe('https://example.com/releases/v1.2.3');
	});

	it.each(['javascript:alert(1)', 'data:text/html,<script>alert(1)</script>', 'vbscript:msgbox(1)'])(
		'rejects non-http(s) release notes href %s',
		(href) => {
			const target = mountTimeline([releaseWithHref(href)]);
			const renderedHrefs = [...target.querySelectorAll<HTMLAnchorElement>('a')].map(
				(anchor) => anchor.getAttribute('href') ?? '',
			);

			expect(target.querySelector('.gr-host-platform-release-timeline__link')).toBeNull();
			expect(renderedHrefs).not.toContain(href);
			expect(renderedHrefs.every((renderedHref) => renderedHref === '' || /^https?:\/\//i.test(renderedHref))).toBe(true);
		},
	);
});
