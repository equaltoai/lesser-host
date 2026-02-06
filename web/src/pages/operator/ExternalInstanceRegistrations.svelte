<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListExternalInstanceRegistrationsResponse } from 'src/lib/api/operators';
	import {
		approveExternalInstanceRegistration,
		listExternalInstanceRegistrations,
		rejectExternalInstanceRegistration,
	} from 'src/lib/api/operators';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, Heading, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let data = $state<ListExternalInstanceRegistrationsResponse | null>(null);

	let actingId = $state<string | null>(null);
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
			data = await listExternalInstanceRegistrations(token);
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

	async function act(username: string, id: string, action: 'approve' | 'reject') {
		actionError = null;
		actingId = id;
		try {
			if (action === 'approve') {
				await approveExternalInstanceRegistration(token, username, id);
			} else {
				await rejectExternalInstanceRegistration(token, username, id);
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
			actingId = null;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="op-external">
	<header class="op-external__header">
		<div class="op-external__title">
			<Heading level={2} size="xl">External instance registrations</Heading>
			<Text color="secondary">Approve or reject non-managed instance registrations.</Text>
		</div>
		<div class="op-external__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	{#if loading}
		<div class="op-external__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="External instance registrations">{errorMessage}</Alert>
	{:else if data && data.registrations.length === 0}
		<Alert variant="info" title="No registrations">
			<Text size="sm">No pending external instance registrations.</Text>
		</Alert>
	{:else if data}
		{#if actionError}
			<Alert variant="error" title="Action failed">{actionError}</Alert>
		{/if}

		<div class="op-external__list">
			{#each data.registrations as reg (reg.id)}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<div class="op-external__row">
							<div class="op-external__row-left">
								<Heading level={3} size="lg">
									<span class="op-external__mono">{reg.slug}</span>
								</Heading>
								<Badge variant="outlined" color="warning" size="sm">pending</Badge>
							</div>
							<div class="op-external__row-right">
								<CopyButton size="sm" text={`${reg.username}:${reg.id}`} />
							</div>
						</div>
					{/snippet}

					<div class="op-external__meta">
						<Text size="sm" color="secondary">
							user <span class="op-external__mono">{reg.username}</span> · id
							<span class="op-external__mono">{reg.id}</span>
						</Text>
						<Text size="sm" color="secondary">
							created <span class="op-external__mono">{reg.created_at}</span>
						</Text>
						{#if reg.note}
							<Text size="sm" color="secondary">{reg.note}</Text>
						{/if}
					</div>

					<div class="op-external__row">
						<Button
							variant="solid"
							onclick={() => void act(reg.username, reg.id, 'approve')}
							disabled={actingId === reg.id}
						>
							Approve
						</Button>
						<Button
							variant="outline"
							onclick={() => void act(reg.username, reg.id, 'reject')}
							disabled={actingId === reg.id}
						>
							Reject
						</Button>
						<Button variant="ghost" onclick={() => navigate(`/operator/instances/${reg.slug}`)}>
							Open instance
						</Button>
						{#if actingId === reg.id}
							<div class="op-external__loading-inline">
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
	.op-external {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-external__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-external__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-external__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-external__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-external__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4);
	}

	.op-external__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.op-external__row-left {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-external__row-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-external__meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-external__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-external__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>

