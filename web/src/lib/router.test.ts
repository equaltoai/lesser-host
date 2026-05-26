/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

// router.ts touches window.location at module-load time (the CURRENT_BASE_PATH
// constant captures the path-at-load). Behavioral testing under jsdom is
// awkward because the module is evaluated once with a fixed pathname, and the
// SAFE_APP_BASE_PATH branch only triggers when the harness is at /safe-app/*
// at module import. Source-text guards lock the public-API contract instead,
// matching the pattern used elsewhere in the repo (InstanceDetail.svelte.test
// .ts, MarkdownRenderer.svelte.test.ts) until DOM-render testing is brought
// in as a separate scoped change.

const source = readFileSync(join(process.cwd(), 'src/lib/router.ts'), 'utf8');

describe('router.linkProps — greater-components Link helper', () => {
	it('is exported from router.ts so consumer pages can import it', () => {
		expect(source).toMatch(/export\s+function\s+linkProps\s*\(/);
	});

	it('takes a single path argument typed as string', () => {
		expect(source).toMatch(/export\s+function\s+linkProps\(to:\s*string\)/);
	});

	it('returns an object with href + onnavigate, matching greater LinkProps shape', () => {
		// The return type literal must match what Link.svelte's `onnavigate`
		// expects: (ev: MouseEvent, href: string) => void. If greater-components
		// ever changes the Link callback signature, this assertion is the local
		// canary that tells us to update the helper too.
		expect(source).toMatch(/href:\s*string;?/);
		expect(source).toMatch(/onnavigate:\s*\(ev:\s*MouseEvent,\s*href:\s*string\)\s*=>\s*void/);
	});

	it('applies base-path to href so middle/Cmd-click "open in new tab" lands on the correct URL', () => {
		// The whole point of linkProps over inline `navigate(...)` is that the
		// rendered <a href> must include the SAFE_APP_BASE_PATH prefix when the
		// app is mounted under /safe-app. Otherwise modifier-key clicks open the
		// wrong URL in a new tab.
		expect(source).toMatch(/href:\s*withBasePath\(normalizePath\(to\)\)/);
	});

	it('preventDefault + navigate inside onnavigate so the SPA router intercepts unmodified left-clicks', () => {
		// The onnavigate body must call preventDefault before navigate so the
		// browser does not perform native navigation on top of the SPA route.
		// navigate(to) — not navigate(href) — so the base-path stays applied
		// exactly once by navigate's own withBasePath call.
		const fnStart = source.indexOf('export function linkProps');
		expect(fnStart).toBeGreaterThan(0);
		const fnRegion = source.slice(fnStart, fnStart + 800);
		expect(fnRegion).toMatch(/ev\.preventDefault\(\)/);
		expect(fnRegion).toMatch(/navigate\(to\)/);
	});

	it('lives next to navigate() in router.ts for discoverability', () => {
		// Co-locating linkProps with navigate keeps the SPA-router contract in
		// one place. If future maintenance moves linkProps elsewhere, the
		// rationale needs to be documented; this assertion is the canary.
		const navIdx = source.indexOf('export function navigate(');
		const linkIdx = source.indexOf('export function linkProps(');
		expect(navIdx).toBeGreaterThan(0);
		expect(linkIdx).toBeGreaterThan(navIdx);
	});
});
