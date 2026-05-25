#!/usr/bin/env node
/* ============================================================================
 * Build-time verifier: OAC form transport integrity (SEC-7 input).
 *
 * Enforces three invariants on the built `web/dist/` output:
 *
 *   A. Every `<form data-facetheory-oac-form>` in any built HTML has:
 *        - same-origin action (relative URL OR matching EXPECTED_ORIGIN)
 *        - mutating method (POST | PUT | PATCH | DELETE — never GET / HEAD)
 *
 *   B. The marker attribute MUST NOT appear on forms whose action targets
 *      one of host's bearer-auth API path prefixes:
 *        - /api/*
 *        - /auth/*
 *        - /setup/*
 *        - /.well-known/*
 *        - /attestations/*
 *      These origins are NOT OAC-signed at the CloudFront layer (per the
 *      M0.13 composition tests). Marking a bearer-auth form with
 *      `data-facetheory-oac-form` would route a SigV4-signed request to
 *      an origin that doesn't accept signed requests, breaking auth.
 *      The marker is marker-scoped (per the FaceTheory coordination
 *      finding), not path-scoped — so FaceTheory itself does NOT filter
 *      these paths. This verifier is the build-time gate.
 *
 *   C. The client bundle that mounts the OAC transport must import
 *      `startAwsOacFormTransport` from `@theory-cloud/facetheory/client`.
 *      The verifier scans the built `dist/assets/*.js` files (the Vite
 *      client output) for that identifier and fails if no built asset
 *      references it. Catches the case where `web/src/client/oac-form.ts`
 *      is silently removed or tree-shaken away.
 *
 * Run order: after `vite build` produces `web/dist/`. Wired into
 * `web/package.json` `build` script in series with
 * `verify-no-inline-html.mjs`.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.16
 * Issue: equaltoai/lesser-host#396
 * ========================================================================== */

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, relative, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(__dirname, '..');
const distDir = join(webRoot, 'dist');

const EXPECTED_ORIGIN = process.env.LESSER_HOST_ORIGIN ?? 'https://lesser.host';
const MARKER_ATTR = 'data-facetheory-oac-form';
// Minifiers rename the `startAwsOacFormTransport` import identifier, but
// the unique literal strings inside its body survive: the marker attribute
// (queried via `document.querySelector(...)`) and the SigV4 content-hash
// HTTP header (injected onto every signed request). Either signal in the
// built bundle proves the transport is wired. We check BOTH so that a
// future refactor that removes one but not the other still trips.
const TRANSPORT_SIGNALS = Object.freeze(['data-facetheory-oac-form', 'x-amz-content-sha256']);

const BEARER_AUTH_PREFIXES = Object.freeze([
	'/api/',
	'/auth/',
	'/setup/',
	'/.well-known/',
	'/attestations/',
	// Exact-match equivalents so e.g. `/attestations` (no trailing slash)
	// also fails — host's CloudFront behavior includes both the bare and
	// wildcard form for several of these.
	'/attestations',
]);

const MUTATING_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

function collectFilesByExtension(dir, ext) {
	const out = [];
	let entries;
	try {
		entries = readdirSync(dir, { withFileTypes: true });
	} catch {
		return out;
	}
	for (const entry of entries) {
		const full = join(dir, entry.name);
		if (entry.isDirectory()) {
			out.push(...collectFilesByExtension(full, ext));
		} else if (entry.isFile() && entry.name.endsWith(ext)) {
			out.push(full);
		}
	}
	return out;
}

function isSameOriginUrl(value) {
	const v = value.trim();
	if (v === '') return false;
	if (v.startsWith('data:')) return false;
	if (v.startsWith('//')) return false;
	if (v.startsWith('/') || v.startsWith('./') || v.startsWith('../')) return true;
	if (!v.includes('://')) return true;
	try {
		return new URL(v).origin === new URL(EXPECTED_ORIGIN).origin;
	} catch {
		return false;
	}
}

function actionPath(value) {
	const v = value.trim();
	if (v === '') return '';
	if (v.startsWith('/') && !v.startsWith('//')) return v;
	if (v.startsWith('./') || v.startsWith('../')) {
		// Resolve against root for prefix-check purposes; close enough for
		// the bearer-auth prefix detection.
		try {
			return new URL(v, EXPECTED_ORIGIN).pathname;
		} catch {
			return v;
		}
	}
	try {
		return new URL(v).pathname;
	} catch {
		return v;
	}
}

function actionMatchesBearerAuthPrefix(action) {
	const path = actionPath(action);
	return BEARER_AUTH_PREFIXES.some((prefix) => path === prefix || path.startsWith(prefix));
}

function recordViolation(violations, file, message) {
	violations.push(`${relative(webRoot, file)}: ${message}`);
}

function checkFormsInHtml(file, html, violations) {
	const FORM_RE = /<form\b([^>]*)>/gi;
	let match;
	while ((match = FORM_RE.exec(html)) !== null) {
		const attrs = match[1] ?? '';
		const hasMarker = new RegExp(`\\b${MARKER_ATTR}\\b`, 'i').test(attrs);
		if (!hasMarker) continue;
		const actionMatch =
			attrs.match(/\baction\s*=\s*"([^"]*)"/i) ?? attrs.match(/\baction\s*=\s*'([^']*)'/i);
		const action = actionMatch?.[1] ?? '';
		if (!action) {
			recordViolation(
				violations,
				file,
				`<form ${MARKER_ATTR}> has no action= attribute (cannot verify same-origin / non-bearer-auth)`,
			);
			continue;
		}
		if (!isSameOriginUrl(action)) {
			recordViolation(
				violations,
				file,
				`<form ${MARKER_ATTR} action="${action.slice(0, 80)}"> is not same-origin`,
			);
		}
		if (actionMatchesBearerAuthPrefix(action)) {
			recordViolation(
				violations,
				file,
				`<form ${MARKER_ATTR} action="${action.slice(0, 80)}"> targets a bearer-auth API path — must NOT be marked (CloudFront origin is not OAC-signed for those paths)`,
			);
		}
		const methodMatch =
			attrs.match(/\bmethod\s*=\s*"([^"]*)"/i) ?? attrs.match(/\bmethod\s*=\s*'([^']*)'/i);
		const method = (methodMatch?.[1] ?? '').toUpperCase();
		if (!MUTATING_METHODS.has(method)) {
			recordViolation(
				violations,
				file,
				`<form ${MARKER_ATTR} method="${method || '(none)'}"> is not a mutating method (POST/PUT/PATCH/DELETE required)`,
			);
		}
	}
}

function checkClientBundleHasTransport(jsFiles, violations) {
	const missing = new Set(TRANSPORT_SIGNALS);
	for (const file of jsFiles) {
		if (missing.size === 0) break;
		let body;
		try {
			body = readFileSync(file, 'utf8');
		} catch {
			continue;
		}
		for (const signal of [...missing]) {
			if (body.includes(signal)) missing.delete(signal);
		}
	}
	if (missing.size > 0) {
		violations.push(
			`no built bundle under dist/assets/ references the OAC form transport signals: missing [${[...missing].join(', ')}]. Verify web/src/client/oac-form.ts imports startAwsOacFormTransport from @theory-cloud/facetheory/client and is referenced from web/src/client/face-client.ts.`,
		);
	}
}

function main() {
	if (!statSyncSafe(distDir)?.isDirectory()) {
		console.error(`[verify-oac-form-integrity] dist directory not found at ${distDir}; run npm run build first`);
		process.exit(2);
	}
	const violations = [];

	// (A) + (B): form marker checks across every built HTML.
	const htmlFiles = collectFilesByExtension(distDir, '.html');
	if (htmlFiles.length === 0) {
		console.error('[verify-oac-form-integrity] no HTML files found under dist/');
		process.exit(2);
	}
	for (const file of htmlFiles) {
		let html;
		try {
			html = readFileSync(file, 'utf8');
		} catch (err) {
			recordViolation(violations, file, `read failed: ${err instanceof Error ? err.message : String(err)}`);
			continue;
		}
		checkFormsInHtml(file, html, violations);
	}

	// (C): client bundle contains the transport identifier.
	const jsFiles = collectFilesByExtension(join(distDir, 'assets'), '.js');
	checkClientBundleHasTransport(jsFiles, violations);

	if (violations.length > 0) {
		console.error(`[verify-oac-form-integrity] FAIL — ${violations.length} violation(s):`);
		for (const v of violations) console.error(`  • ${v}`);
		console.error(
			'\nOAC form transport invariants: marked forms must be same-origin + mutating-method, bearer-auth API paths must NOT carry the marker, and the client bundle must wire startAwsOacFormTransport.',
		);
		process.exit(1);
	}
	console.warn(
		`[verify-oac-form-integrity] PASS — scanned ${htmlFiles.length} HTML + ${jsFiles.length} JS file(s); OAC form transport integrity holds.`,
	);
}

function statSyncSafe(path) {
	try {
		return statSync(path);
	} catch {
		return null;
	}
}

main();
