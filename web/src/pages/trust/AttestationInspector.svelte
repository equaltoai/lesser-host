<!--
@component
Public attestation inspector — re-skinned for the M2.1 design-token surface.

Project 39 M3.6 (issue #450). The publicly-reachable attestation viewer at
`/attestations/{id}`. Re-skin is presentational only per the trust-and-safety
walk (24dcd86) — Dimension 1 (no auth change), Dimension 2 (no JWS decode
path change), Dimension 3 (client-rendered; ISR deferred), Dimension 4
(MarkdownRenderer mandatory-sanitization path preserved by intentional
absence — no markdown surface is introduced; `pretty()` JSON-stringifies
the header / payload into `TextArea readonly`, which Svelte escapes safely).

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles / third-party origins; the
  `webCsp` byte-string remains unchanged; the inspector renders entirely
  via host's own bundle.
- Trust-API instance-auth untouched: this is the public read surface; no
  instance-key path; no internal-only fields surfaced (the response shape
  from `/attestations/{id}` already redacts; nothing read here that wasn't
  already in the public response).
- No client-side caching beyond browser HTTP cache: same fetch on every
  load; no IndexedDB / localStorage / SW caching layer added.
- Multi-tenant isolation: attestations are public artifacts; no tenant
  data is accessed.

Behavior preserved:
- Same `getAttestation(id)` + `getJWKS()` endpoints; same JWS decode +
  header-kid + JWKS-kid surface; same Refresh button; same `Back` link;
  same TextArea-readonly render of header + payload JSON.
- No new endpoints; no new fields surfaced.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M3.6
Trust walk: docs/trust-and-safety-web-ui-rework-2026-05-24.md (24dcd86)
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { AttestationResponse, JWKS } from 'src/lib/api/trust';
	import { getAttestation, getJWKS } from 'src/lib/api/trust';
	import { linkProps, navigate } from 'src/lib/router';
	import { StatCard, SummaryStrip } from 'src/lib/shell';
	import type { StatCardStatus } from 'src/lib/shell';
	import { Alert, Button, Card, CopyButton, DefinitionItem, DefinitionList, Heading, Link, Spinner, Text, TextArea } from 'src/lib/ui';

	let { id } = $props<{ id: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let attestation = $state<AttestationResponse | null>(null);
	let jwks = $state<JWKS | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function pretty(value: unknown): string {
		try {
			return JSON.stringify(value, null, 2);
		} catch {
			return String(value);
		}
	}

	function headerKid(): string {
		const h = attestation?.header;
		if (!h || typeof h !== 'object') return '';
		const kid = (h as Record<string, unknown>).kid;
		return typeof kid === 'string' ? kid : '';
	}

	function jwksKids(): string[] {
		const keys = jwks?.keys ?? [];
		const kids = keys.map((k) => (typeof k.kid === 'string' ? k.kid : '')).filter(Boolean);
		return Array.from(new Set(kids));
	}

	/**
	 * Surface whether the header's kid is present in the JWKS the inspector
	 * loaded alongside the attestation. A `success` tone tells the reader
	 * "this attestation's signing key is in the published JWKS"; a `warning`
	 * tone (kid present, JWKS missing) tells them the JWKS read failed but
	 * the attestation itself loaded; `default` for the loading / no-data
	 * state. Verification is the operator's responsibility; this card is a
	 * fast visual signal, not a verifier.
	 */
	function kidPresenceStatus(): StatCardStatus {
		if (!attestation) return 'default';
		const headerK = headerKid();
		if (!headerK) return 'default';
		if (!jwks) return 'warning';
		return jwksKids().includes(headerK) ? 'success' : 'warning';
	}

	function kidPresenceLabel(): string {
		if (!attestation) return '—';
		const headerK = headerKid();
		if (!headerK) return 'no kid';
		if (!jwks) return 'JWKS unavailable';
		return jwksKids().includes(headerK) ? 'kid in JWKS' : 'kid not in JWKS';
	}

	async function load() {
		errorMessage = null;
		attestation = null;
		jwks = null;

		loading = true;
		try {
			const [a, j] = await Promise.all([getAttestation(id), getJWKS().catch(() => null)]);
			attestation = a;
			jwks = j;
		} catch (err) {
			errorMessage = formatError(err);
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="trust-att">
	<header class="trust-att__header">
		<div class="trust-att__title">
			<Heading level={2} size="xl">Attestation</Heading>
			<Text color="secondary"><span class="trust-att__mono">{id}</span></Text>
		</div>
		<div class="trust-att__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
			<Link {...linkProps('/trust')} variant="ghost">Back</Link>
		</div>
	</header>

	{#if loading}
		<div class="trust-att__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Attestation">{errorMessage}</Alert>
	{:else if attestation}
		<!--
			Fast visual signal: does the attestation's header kid appear in
			the public JWKS the inspector loaded alongside it? This is a
			lookup, not a verification — the reader still runs the JWS
			verification per the instructions below. `warning` covers both
			"kid not found in JWKS" and "JWKS unavailable" so the inspector
			never silently implies a verified posture it can't claim.
		-->
		<SummaryStrip label="Inspector state" columns={1} gap="md">
			<StatCard label="Header kid presence" value={kidPresenceLabel()} status={kidPresenceStatus()} />
		</SummaryStrip>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<div class="trust-att__row">
					<Heading level={3} size="lg">Overview</Heading>
					<CopyButton size="sm" text={attestation?.id ?? ''} />
				</div>
			{/snippet}
			<DefinitionList>
				<DefinitionItem label="ID" monospace>{attestation.id}</DefinitionItem>
				<DefinitionItem label="Header kid" monospace>{headerKid() || '—'}</DefinitionItem>
				<DefinitionItem label="JWKS kids" monospace>{jwksKids().join(', ') || '—'}</DefinitionItem>
			</DefinitionList>
			<div class="trust-att__row">
				<CopyButton size="sm" text={attestation.jws} labels={{ default: 'Copy JWS' }} variant="icon-text" />
			</div>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Verification</Heading>
			{/snippet}
			<Text size="sm" color="secondary">
				Verify the JWS using <span class="trust-att__mono">/.well-known/jwks.json</span>, matching the header’s
				<span class="trust-att__mono">kid</span>.
			</Text>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Header</Heading>
			{/snippet}
			<!--
				JSON content is rendered through TextArea readonly. Svelte
				escapes the string body; no Markdown or HTML interpretation
				path is introduced here, which preserves the mandatory-
				sanitization invariant the trust-and-safety walk asserts
				for the inspector (Dimension 4).
			-->
			<TextArea value={pretty(attestation.header)} readonly rows={10} />
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Payload</Heading>
			{/snippet}
			<TextArea value={pretty(attestation.payload)} readonly rows={14} />
		</Card>
	{:else}
		<Alert variant="warning" title="No data">
			<Text size="sm">No attestation response.</Text>
		</Alert>
	{/if}
</div>

<style>
	.trust-att {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.trust-att__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.trust-att__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.trust-att__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.trust-att__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.trust-att__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-3);
	}

	/* Canonical mono token chain — see UserApprovals.svelte (M3.1) for rationale. */
	.trust-att__mono {
		font-family:
			var(--gr-typography-fontFamily-mono),
			ui-monospace,
			SFMono-Regular,
			Menlo,
			Monaco,
			Consolas,
			'Liberation Mono',
			'Courier New',
			monospace;
	}
</style>
