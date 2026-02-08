<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import { listExternalInstanceRegistrations, listPortalUserApprovals, listVanityDomainRequests } from 'src/lib/api/operators';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, DefinitionItem, DefinitionList, Heading, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);

	let vanityCount = $state<number | null>(null);
	let externalCount = $state<number | null>(null);
	let userCount = $state<number | null>(null);

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
		vanityCount = null;
		externalCount = null;
		userCount = null;

		loading = true;
		try {
			const [vanity, external, users] = await Promise.all([
				listVanityDomainRequests(token),
				listExternalInstanceRegistrations(token),
				listPortalUserApprovals(token),
			]);
			vanityCount = vanity.count ?? vanity.requests?.length ?? 0;
			externalCount = external.count ?? external.registrations?.length ?? 0;
			userCount = users.count ?? users.users?.length ?? 0;
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

	onMount(() => {
		void load();
	});
</script>

<div class="op-dashboard">
	<header class="op-dashboard__header">
		<div class="op-dashboard__title">
			<Heading level={2} size="xl">Dashboard</Heading>
			<Text color="secondary">Approvals and support tools.</Text>
		</div>
		<div class="op-dashboard__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	{#if loading}
		<div class="op-dashboard__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Operator dashboard">{errorMessage}</Alert>
	{:else}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Queues</Heading>
			{/snippet}
			<DefinitionList>
				<DefinitionItem label="Vanity domain requests" monospace>{String(vanityCount ?? 0)}</DefinitionItem>
				<DefinitionItem label="Portal user approvals" monospace>{String(userCount ?? 0)}</DefinitionItem>
				<DefinitionItem label="External instance registrations" monospace>{String(externalCount ?? 0)}</DefinitionItem>
			</DefinitionList>

			<div class="op-dashboard__row">
				<Button variant="outline" onclick={() => navigate('/operator/approvals/domains')}>Review domains</Button>
				<Button variant="outline" onclick={() => navigate('/operator/approvals/users')}>Review users</Button>
				<Button variant="outline" onclick={() => navigate('/operator/approvals/external-instances')}>
					Review external registrations
				</Button>
				<Button variant="outline" onclick={() => navigate('/operator/provisioning/jobs')}>Provisioning jobs</Button>
				<Button variant="outline" onclick={() => navigate('/operator/instances')}>Instance search</Button>
				<Button variant="outline" onclick={() => navigate('/operator/tip-registry')}>Tip registry</Button>
				<Button variant="outline" onclick={() => navigate('/operator/audit')}>Audit log</Button>
			</div>
		</Card>
	{/if}
</div>

<style>
	.op-dashboard {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-dashboard__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-dashboard__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-dashboard__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-dashboard__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-dashboard__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}
</style>
