<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { AttestationResponse, JWKS } from 'src/lib/api/trust';
	import { getAttestation, getJWKS } from 'src/lib/api/trust';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, CopyButton, DefinitionItem, DefinitionList, Heading, Spinner, Text, TextArea } from 'src/lib/ui';

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
			<Button variant="ghost" onclick={() => navigate('/trust')}>Back</Button>
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

	.trust-att__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
