<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListPortalUserApprovalsResponse } from 'src/lib/api/operators';
	import { approvePortalUser, listPortalUserApprovals, rejectPortalUser } from 'src/lib/api/operators';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, Heading, Spinner, Text, TextArea } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let data = $state<ListPortalUserApprovalsResponse | null>(null);

	let reviewNote = $state('');
	let actingUser = $state<string | null>(null);
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
			data = await listPortalUserApprovals(token);
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

	async function act(username: string, action: 'approve' | 'reject') {
		actionError = null;
		actingUser = username;
		try {
			const note = reviewNote.trim();
			if (action === 'approve') {
				await approvePortalUser(token, username, note || undefined);
			} else {
				await rejectPortalUser(token, username, note || undefined);
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
			actingUser = null;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="op-users">
	<header class="op-users__header">
		<div class="op-users__title">
			<Heading level={2} size="xl">Portal user approvals</Heading>
			<Text color="secondary">Approve or reject new portal accounts.</Text>
		</div>
		<div class="op-users__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Review note</Heading>
		{/snippet}
		<Text size="sm" color="secondary">Optional note recorded with approve/reject actions.</Text>
		<div class="op-users__form">
			<TextArea bind:value={reviewNote} placeholder="Optional note…" />
		</div>
	</Card>

	{#if loading}
		<div class="op-users__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Portal user approvals">{errorMessage}</Alert>
	{:else if data && data.users.length === 0}
		<Alert variant="info" title="No approvals">
			<Text size="sm">No pending portal users.</Text>
		</Alert>
	{:else if data}
		{#if actionError}
			<Alert variant="error" title="Action failed">{actionError}</Alert>
		{/if}

		<div class="op-users__list">
			{#each data.users as user (user.username)}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<div class="op-users__row">
							<div class="op-users__row-left">
								<Heading level={3} size="lg">
									<span class="op-users__mono">{user.username}</span>
								</Heading>
								<Badge variant="outlined" color="warning" size="sm">pending</Badge>
							</div>
							<div class="op-users__row-right">
								<CopyButton size="sm" text={user.username} />
							</div>
						</div>
					{/snippet}

					<div class="op-users__meta">
						<Text size="sm" color="secondary">
							wallet <span class="op-users__mono">{user.wallet_address || '—'}</span>
							{#if user.wallet_address}
								<CopyButton size="sm" text={user.wallet_address} />
							{/if}
						</Text>
						<Text size="sm" color="secondary">
							display name <span class="op-users__mono">{user.display_name || '—'}</span>
							· email <span class="op-users__mono">{user.email || '—'}</span>
						</Text>
						<Text size="sm" color="secondary">
							created <span class="op-users__mono">{user.created_at}</span>
						</Text>
					</div>

					<div class="op-users__row">
						<Button
							variant="solid"
							onclick={() => void act(user.username, 'approve')}
							disabled={actingUser === user.username}
						>
							Approve
						</Button>
						<Button
							variant="outline"
							onclick={() => void act(user.username, 'reject')}
							disabled={actingUser === user.username}
						>
							Reject
						</Button>
						{#if actingUser === user.username}
							<div class="op-users__loading-inline">
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
	.op-users {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-users__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-users__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-users__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-users__form {
		margin-top: var(--gr-spacing-scale-4);
		display: grid;
		gap: var(--gr-spacing-scale-3);
	}

	.op-users__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-users__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
	}

	.op-users__row {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.op-users__row-left {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-users__row-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-users__meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		margin: var(--gr-spacing-scale-3) 0;
	}

	.op-users__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-users__mono {
		font-family: var(--gr-font-family-mono);
	}
</style>
