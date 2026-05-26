<!--
@component
Trust — public trust portal (attestation lookup + inspector entry).

M1.3 re-skin: renders on the new shell (greater-components `PageFrame` +
`PageTitle` + `Panel`) and consumes DS tokens through the existing
`--gr-*` bridge. Routing, lookup behavior, and attestation-inspector
delegation are preserved byte-for-byte from the pre-M1 implementation;
only the visual layer changes.

Posture invariants preserved:
  - Strict-no-inline-CSP safe.
  - Trust-API instance-auth contract NOT touched here — Trust UI is
    a public-read surface that calls the same `lookupAttestation` API
    client. SEC-9 change-lock not engaged.
  - Attestation integrity preserved: the AttestationInspector subroute
    is delegated unchanged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M1.3
Issue: equaltoai/lesser-host#412
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import { lookupAttestation } from 'src/lib/api/trust';
	import { currentPath, linkProps, navigate } from 'src/lib/router';
	import { Alert, Button, Link, Spinner, Text, TextField } from 'src/lib/ui';
	import { PageFrame, PageTitle, Panel } from 'src/lib/shell';

	import AttestationInspector from 'src/pages/trust/AttestationInspector.svelte';

	type TrustRoute =
		| { kind: 'home' }
		| { kind: 'attestation'; id: string }
		| { kind: 'notFound' };

	const trustRoute = $derived.by<TrustRoute>(() => {
		const path = $currentPath;
		if (!path.startsWith('/trust')) return { kind: 'home' };

		const rest = path.slice('/trust'.length);
		const parts = rest.split('/').filter(Boolean);
		if (parts.length === 0) return { kind: 'home' };
		if (parts[0] === 'attestations' && parts[1]) return { kind: 'attestation', id: parts[1] };
		return { kind: 'notFound' };
	});

	let idInput = $state('');

	let actorUri = $state('');
	let objectUri = $state('');
	let contentHash = $state('');
	let module = $state('');
	let policyVersion = $state('');

	let lookupLoading = $state(false);
	let lookupError = $state<string | null>(null);

	function normalizeId(input: string): string | null {
		const trimmed = input.trim().toLowerCase();
		if (!trimmed) return null;
		return trimmed;
	}

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function openById() {
		const normalized = normalizeId(idInput);
		if (!normalized) return;
		navigate(`/trust/attestations/${normalized}`);
	}

	async function lookup() {
		lookupError = null;

		const a = actorUri.trim();
		const o = objectUri.trim();
		const c = contentHash.trim();
		const m = module.trim();
		const p = policyVersion.trim();

		if (!a || !o || !c || !m || !p) {
			lookupError = 'All lookup fields are required.';
			return;
		}

		lookupLoading = true;
		try {
			const res = await lookupAttestation({
				actor_uri: a,
				object_uri: o,
				content_hash: c,
				module: m,
				policy_version: p,
			});
			navigate(`/trust/attestations/${res.id}`);
		} catch (err) {
			lookupError = formatError(err);
		} finally {
			lookupLoading = false;
		}
	}

	onMount(() => {
		if (trustRoute.kind === 'attestation') {
			idInput = trustRoute.id;
		}
	});
</script>

<PageFrame width="default">
	{#snippet header()}
		<PageTitle
			eyebrow="Trust"
			title="Trust"
			description="Attestations and evidence inspection."
		>
			{#snippet actions()}
				<Link {...linkProps('/')} variant="ghost">Home</Link>
			{/snippet}
		</PageTitle>
	{/snippet}

	{#if trustRoute.kind === 'home'}
		<Panel title="Fetch by id" headerLevel={2}>
			<Text size="sm" color="secondary">
				Note: raw attestation JSON lives at <span class="trust__mono">/attestations/*</span>. This UI
				uses <span class="trust__mono">/trust/*</span> to avoid routing conflicts.
			</Text>
			<div class="trust__form">
				<TextField label="Attestation id" bind:value={idInput} placeholder="64-char hex" />
				<Button variant="solid" onclick={openById}>Open</Button>
			</div>
		</Panel>

		<Panel title="Lookup" headerLevel={2}>
			<Text size="sm" color="secondary">
				Lookup by
				<span class="trust__mono">(actor_uri, object_uri, content_hash, module, policy_version)</span>.
			</Text>
			<div class="trust__grid">
				<TextField label="actor_uri" bind:value={actorUri} placeholder="did:..." />
				<TextField label="object_uri" bind:value={objectUri} placeholder="https://..." />
				<TextField label="content_hash" bind:value={contentHash} placeholder="sha256:..." />
				<TextField label="module" bind:value={module} placeholder="link_safety_basic" />
				<TextField label="policy_version" bind:value={policyVersion} placeholder="v1" />
			</div>
			<div class="trust__row">
				<Button variant="solid" onclick={() => void lookup()} disabled={lookupLoading}>Lookup</Button>
				{#if lookupLoading}
					<div class="trust__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Searching…</Text>
					</div>
				{/if}
			</div>
			{#if lookupError}
				<Alert variant="error" title="Lookup failed">{lookupError}</Alert>
			{/if}
		</Panel>
	{:else if trustRoute.kind === 'attestation'}
		<AttestationInspector id={trustRoute.id} />
	{:else}
		<Alert variant="warning" title="Not found">
			<Text size="sm">Unknown trust path.</Text>
		</Alert>
	{/if}
</PageFrame>

<style>
	.trust__form {
		display: flex;
		gap: var(--ds-space-3);
		align-items: flex-end;
		margin-top: var(--ds-space-4);
		flex-wrap: wrap;
	}

	.trust__grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
		gap: var(--ds-space-3);
		margin-top: var(--ds-space-4);
	}

	.trust__row {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		margin-top: var(--ds-space-4);
		flex-wrap: wrap;
	}

	.trust__loading-inline {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
	}

	.trust__mono {
		font-family: var(--ds-font-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
	}
</style>
