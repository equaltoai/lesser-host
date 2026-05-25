#!/usr/bin/env node
/* ============================================================================
 * Build-time verifier: no inline HTML (SEC-6 input).
 *
 * Walks `web/dist/**\/*.html` and fails the build (exit code 1) when any of
 * the following CSP relaxation vectors appear:
 *
 *   1. `<script>...</script>` with non-empty body content (inline script)
 *   2. `<style>...</style>` anywhere (inline style block)
 *   3. inline event handler attributes (e.g. `onclick=`, `onload=`)
 *   4. `<script src="data:...">` (data-URL script source)
 *   5. non-same-origin `<script src=...>` or `<link rel=stylesheet href=...>`
 *      URLs (absolute URLs that don't resolve back to the same origin)
 *
 * Same-origin definition: relative URLs (`/assets/...`, `./foo.js`) AND
 * absolute URLs whose origin matches `EXPECTED_ORIGIN` (the deployed host
 * domain — defaults to `https://lesser.host`; can be overridden via the
 * `LESSER_HOST_ORIGIN` env var for stage-specific runs). Protocol-relative
 * URLs (`//example.com/foo.js`) are treated as cross-origin because the
 * deployed CSP doesn't allow them.
 *
 * The verifier is intentionally pessimistic: a partial-match regex is used
 * to catch any potential CSP relaxation, even when the HTML is well-formed
 * Vite output. Any failure is a hard build error — strict-no-inline-CSP
 * posture is non-negotiable for host's single-origin contract.
 *
 * Run order: after `vite build` produces `web/dist/`, before the build
 * step completes. Wired into `web/package.json` `build` script after
 * `vite build && node ./scripts/build-sidecars.mjs`.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.15
 * Issue: equaltoai/lesser-host#395
 * ========================================================================== */

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, relative, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(__dirname, '..');
const distDir = join(webRoot, 'dist');

const EXPECTED_ORIGIN = process.env.LESSER_HOST_ORIGIN ?? 'https://lesser.host';

/**
 * Walk `dir` and collect every regular file ending in `.html` under it.
 * Returns absolute paths.
 */
function collectHtmlFiles(dir) {
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
			out.push(...collectHtmlFiles(full));
		} else if (entry.isFile() && entry.name.endsWith('.html')) {
			out.push(full);
		}
	}
	return out;
}

/**
 * Same-origin classification for a script src or link href:
 *   - `/...`, `./...`, `../...`, bare filename → relative (allowed)
 *   - `<EXPECTED_ORIGIN>...` → allowed
 *   - anything else (other schemes, other origins, protocol-relative) → cross-origin
 *   - `data:` is treated as a separate violation by the caller (cheaper signal)
 */
function isSameOriginUrl(value) {
	const v = value.trim();
	if (v === '') return false;
	if (v.startsWith('data:')) return false; // caller separately flags
	if (v.startsWith('//')) return false; // protocol-relative
	if (v.startsWith('/') || v.startsWith('./') || v.startsWith('../')) return true;
	if (!v.includes('://')) {
		// Bare filename or fragment / anchor — same-document is same-origin.
		return true;
	}
	try {
		const url = new URL(v);
		const expected = new URL(EXPECTED_ORIGIN);
		return url.origin === expected.origin;
	} catch {
		return false;
	}
}

function recordViolation(violations, file, message) {
	violations.push(`${relative(webRoot, file)}: ${message}`);
}

/**
 * Inline-script detector. Matches `<script ...>BODY</script>` where BODY
 * is non-empty (ignoring whitespace). Vite's emitted script tags use
 * `<script type="module" crossorigin src="..."></script>` with empty body —
 * those pass. Inline `<script>console.log(...)</script>` fails.
 */
function checkInlineScripts(html) {
	const violations = [];
	const SCRIPT_RE = /<script\b([^>]*)>([\s\S]*?)<\s*\/\s*script\b[^>]*>/gi;
	let match;
	while ((match = SCRIPT_RE.exec(html)) !== null) {
		const attrs = match[1] ?? '';
		const body = match[2] ?? '';
		if (body.trim() !== '') {
			violations.push(`inline <script> body (${body.length} chars): ${body.trim().slice(0, 80)}…`);
		}
		const dataMatch = attrs.match(/\bsrc\s*=\s*["']?(data:[^"' >]+)/i);
		if (dataMatch) {
			violations.push(`<script src="data:..."> (CSP violation): ${dataMatch[1].slice(0, 80)}…`);
		}
		const srcMatch = attrs.match(/\bsrc\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s"'<>]+))/i);
		if (srcMatch) {
			const src = srcMatch[1] ?? srcMatch[2] ?? srcMatch[3];
			if (!src.startsWith('data:') && !isSameOriginUrl(src)) {
				violations.push(`<script src="${src.slice(0, 80)}"> is not same-origin`);
			}
		}
	}
	return violations;
}

/**
 * Inline-style detector. Any `<style>...</style>` block fails because Vite
 * emits styles as `<link rel="stylesheet">` references; a residual <style>
 * tag in the output means something injected inline CSS.
 */
function checkInlineStyles(html) {
	const violations = [];
	const STYLE_RE = /<style\b[^>]*>([\s\S]*?)<\s*\/\s*style\b[^>]*>/gi;
	let match;
	while ((match = STYLE_RE.exec(html)) !== null) {
		const body = (match[1] ?? '').trim();
		violations.push(`inline <style> block (${body.length} chars): ${body.slice(0, 80)}…`);
	}
	return violations;
}

/**
 * Inline event-handler attribute detector. Matches `on<lowercase>="..."`
 * on any element. This catches `onclick=`, `onload=`, `onerror=`, etc.
 * The deployed CSP rejects them at runtime; flagging at build time is
 * earlier feedback.
 */
function checkInlineEventHandlers(html) {
	const violations = [];
	// Match `on<letters>="..."` or `on<letters>='...'`, case-insensitive.
	// Must be preceded by whitespace inside a tag to avoid matching things
	// like `onload` in text content.
	const EVT_RE = /<[a-zA-Z][^>]*?\s(on[a-z]+)\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'<>]+)/gi;
	let match;
	while ((match = EVT_RE.exec(html)) !== null) {
		const handler = match[1];
		violations.push(`inline event handler attribute: ${handler}=`);
	}
	return violations;
}

/**
 * Stylesheet link-href same-origin check.
 */
function checkStylesheetOrigins(html) {
	const violations = [];
	// Match <link ... rel="stylesheet" ... href="..."> in any attribute order.
	const LINK_RE = /<link\b([^>]*)\/?>/gi;
	let match;
	while ((match = LINK_RE.exec(html)) !== null) {
		const attrs = match[1] ?? '';
		const relMatch = attrs.match(/\brel\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s"'<>]+))/i);
		if (!relMatch) continue;
		const rel = (relMatch[1] ?? relMatch[2] ?? relMatch[3] ?? '').toLowerCase();
		if (rel !== 'stylesheet' && rel !== 'preload' && rel !== 'modulepreload') continue;
		const hrefMatch = attrs.match(/\bhref\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s"'<>]+))/i);
		if (!hrefMatch) continue;
		const href = hrefMatch[1] ?? hrefMatch[2] ?? hrefMatch[3] ?? '';
		if (!isSameOriginUrl(href)) {
			violations.push(`<link rel="${rel}" href="${href.slice(0, 80)}"> is not same-origin`);
		}
	}
	return violations;
}

function checkFile(file, violations) {
	let html;
	try {
		html = readFileSync(file, 'utf8');
	} catch (err) {
		recordViolation(violations, file, `read failed: ${err instanceof Error ? err.message : String(err)}`);
		return;
	}
	for (const v of checkInlineScripts(html)) recordViolation(violations, file, v);
	for (const v of checkInlineStyles(html)) recordViolation(violations, file, v);
	for (const v of checkInlineEventHandlers(html)) recordViolation(violations, file, v);
	for (const v of checkStylesheetOrigins(html)) recordViolation(violations, file, v);
}

function main() {
	if (!statSyncSafe(distDir)?.isDirectory()) {
		console.error(`[verify-no-inline-html] dist directory not found at ${distDir}; run npm run build first`);
		process.exit(2);
	}
	const files = collectHtmlFiles(distDir);
	if (files.length === 0) {
		console.error('[verify-no-inline-html] no HTML files found under dist/');
		process.exit(2);
	}
	const violations = [];
	for (const file of files) checkFile(file, violations);
	if (violations.length > 0) {
		console.error(`[verify-no-inline-html] FAIL — ${violations.length} violation(s):`);
		for (const v of violations) console.error(`  • ${v}`);
		console.error(
			'\nStrict single-origin CSP posture forbids inline scripts/styles, inline event handlers, data: script sources, and non-same-origin script/stylesheet URLs.',
		);
		process.exit(1);
	}
	// console.warn is the lint-allowed surface for build-script status output.
	console.warn(`[verify-no-inline-html] PASS — scanned ${files.length} HTML file(s); no inline CSP relaxations.`);
}

function statSyncSafe(path) {
	try {
		return statSync(path);
	} catch {
		return null;
	}
}

main();
