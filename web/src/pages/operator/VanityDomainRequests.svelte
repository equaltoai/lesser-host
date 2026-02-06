<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListVanityDomainRequestsResponse } from 'src/lib/api/operators';
	import { approveVanityDomainRequest, listVanityDomainRequests, rejectVanityDomainRequest } from 'src/lib/api/operators';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, Heading, Spinner, Text, TextArea } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let data = $state<ListVanityDomainRequestsResponse | null>(null);

	let reviewNote = $state('');
	let actingDomain = $state<string | null>(null);
	let actionError = $state<string | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function load() {
		errorMessage = null;
		actionError = null;
		data = null;

		loading = true;
		try {
			data = await listVanityDomainRequests(token);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			errorMessage = formatError(err);
		} finally {
			loading = false;
		}
	}

	async function act(domain: string, action: 'approve' | 'reject') {
		actionError = null;
		actingDomain = domain;
		try {
			const note = reviewNote.trim();
			if (action === 'approve') {
				await approveVanityDomainRequest(token, domain, note || undefined);
			} else {
				await rejectVanityDomainRequest(token, domain, note || undefined);
			}
			await load();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			actionError = formatError(err);
		} finally {
			actingDomain = null;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="op-requests">
	<header class="op-requests__header">
		<div class="op-requests__title">
			<Heading level={2} size="xl">Vanity domain requests</Heading>
			<Text color="secondary">Approve or reject verified vanity domains.</Text>
		</div>
		<div class="op-requests__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Review note</Heading>
		{/snippet}
		<Text size="sm" color="secondary">Optional note recorded with approve/reject actions.</Text>
		<div class="op-requests__form">
			<TextArea bind:value={reviewNote} placeholder="Optional note…" />
		</div>
	</Card>

	{#if loading}
		<div class="op-requests__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Vanity domain requests">{errorMessage}</Alert>
	{:else if data && data.requests.length === 0}
		<Alert variant="info" title="No requests">
			<Text size="sm">No pending vanity domain requests.</Text>
		</Alert>
	{:else if data}
		{#if actionError}
			<Alert variant="error" title="Action failed">{actionError}</Alert>
		{/if}

		<div class="op-requests__list">
			{#each data.requests as req (req.domain)}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<div class="op-requests__row">
							<div class="op-requests__row-left">
								<Heading level={3} size="lg">
									<span class="op-requests__mono">{req.domain}</span>
								</Heading>
								<Badge variant="outlined" color="warning" size="sm">pending</Badge>
							</div>
							<div class="op-requests__row-right">
								<CopyButton size="sm" text={req.domain} />
							</div>
						</div>
					{/snippet}

					<div class="op-requests__meta">
						<Text size="sm" color="secondary">
							instance <span class="op-requests__mono">{req.instance_slug}</span>
							{#if req.requested_by}
								· requested by <span class="op-requests__mono">{req.requested_by}</span>
							{/if}
						</Text>
						<Text size="sm" color="secondary">
							verified <span class="op-requests__mono">{req.verified_at || '—'}</span> · requested
							<span class="op-requests__mono">{req.requested_at || req.created_at}</span>
						</Text>
					</div>

					<div class="op-requests__row">
						<Button
							variant="solid"
							onclick={() => void act(req.domain, 'approve')}
							disabled={actingDomain === req.domain}
						>
							Approve
						</Button>
						<Button
							variant="outline"
							onclick={() => void act(req.domain, 'reject')}
							disabled={actingDomain === req.domain}
						>
							Reject
						</Button>
						<Button variant="ghost" onclick={() => navigate(`/operator/instances/${req.instance_slug}`)}>
							Open instance
						</Button>
						{#if actingDomain === req.domain}
							<div class="op-requests__loading-inline">
								<Spinner size="sm" />
								<Text size="sm">Working…</Text>
							</div>
						{/if}
					</div>
				</Card>
			{/each}
		</div>
	{:else}
		<Alert variant="warning" title="No data">
			<Text size="sm">No response from approval endpoints.</Text>
		</Alert>
	{/if}
</div>

<style>
	.op-requests {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-requests__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-requests__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-requests__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-requests__form {
		margin-top: var(--gr-spacing-scale-4);
		display: grid;
		gap: var(--gr-spacing-scale-3);
	}

	.op-requests__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-requests__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4);
	}

	.op-requests__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.op-requests__row-left {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-requests__row-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-requests__meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-requests__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-requests__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
