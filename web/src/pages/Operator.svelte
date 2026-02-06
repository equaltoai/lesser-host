<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { OperatorMeResponse } from 'src/lib/api/operators';
	import { getOperatorMe } from 'src/lib/api/operators';
	import { logout } from 'src/lib/auth/logout';
	import { currentPath, navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { Alert, Button, Card, Container, Heading, Spinner, Text } from 'src/lib/ui';

	import Dashboard from 'src/pages/operator/Dashboard.svelte';
	import ExternalInstanceRegistrations from 'src/pages/operator/ExternalInstanceRegistrations.svelte';
	import InstanceSupport from 'src/pages/operator/InstanceSupport.svelte';
	import ProvisioningJobDetail from 'src/pages/operator/ProvisioningJobDetail.svelte';
	import ProvisioningJobs from 'src/pages/operator/ProvisioningJobs.svelte';
	import VanityDomainRequests from 'src/pages/operator/VanityDomainRequests.svelte';

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let me = $state<OperatorMeResponse | null>(null);

	type OperatorRoute =
		| { kind: 'dashboard' }
		| { kind: 'vanityDomains' }
		| { kind: 'externalRegistrations' }
		| { kind: 'provisioningJobs' }
		| { kind: 'provisioningJobDetail'; id: string }
		| { kind: 'instances' }
		| { kind: 'instanceDetail'; slug: string }
		| { kind: 'notFound' };

	const operatorRoute = $derived.by<OperatorRoute>(() => {
		const path = $currentPath;
		if (!path.startsWith('/operator')) return { kind: 'dashboard' };

		const rest = path.slice('/operator'.length);
		const parts = rest.split('/').filter(Boolean);

		if (parts.length === 0) return { kind: 'dashboard' };
		if (parts[0] === 'approvals') {
			if (parts[1] === 'domains') return { kind: 'vanityDomains' };
			if (parts[1] === 'external-instances') return { kind: 'externalRegistrations' };
			return { kind: 'notFound' };
		}
		if (parts[0] === 'instances') {
			if (parts[1]) return { kind: 'instanceDetail', slug: parts[1] };
			return { kind: 'instances' };
		}
		if (parts[0] === 'provisioning') {
			if (parts[1] === 'jobs') {
				if (parts[2]) return { kind: 'provisioningJobDetail', id: parts[2] };
				return { kind: 'provisioningJobs' };
			}
			if (parts.length === 1) return { kind: 'provisioningJobs' };
			return { kind: 'notFound' };
		}
		return { kind: 'notFound' };
	});

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function loadMe() {
		errorMessage = null;
		me = null;

		const current = $session;
		if (!current) {
			navigate('/login');
			return;
		}

		if (current.role !== 'admin' && current.role !== 'operator') {
			errorMessage = 'Operator session required.';
			return;
		}

		loading = true;
		try {
			me = await getOperatorMe(current.token);
		} catch (err) {
			const message = formatError(err);
			errorMessage = message;
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
			}
		} finally {
			loading = false;
		}
	}

	async function handleLogout() {
		await logout();
		navigate('/login');
	}

	onMount(() => {
		void loadMe();
	});
</script>

<Container size="lg" gutter="lg">
	<div class="operator">
		<header class="operator__header">
			<div class="operator__title">
				<Heading level={1}>Operator console</Heading>
				<Text color="secondary">Admin/operator access.</Text>
			</div>
		<div class="operator__actions">
			<Button variant="outline" onclick={() => void loadMe()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate('/operator')}>Dashboard</Button>
			<Button variant="ghost" onclick={() => navigate('/operator/approvals/domains')}>Domains</Button>
			<Button variant="ghost" onclick={() => navigate('/operator/approvals/external-instances')}>External regs</Button>
			<Button variant="ghost" onclick={() => navigate('/operator/provisioning/jobs')}>Provisioning</Button>
			<Button variant="ghost" onclick={() => navigate('/operator/instances')}>Instances</Button>
			<Button variant="ghost" onclick={() => navigate('/account')}>Account</Button>
			<Button variant="ghost" onclick={() => navigate('/portal')}>Portal</Button>
			<Button
				variant="ghost"
					onclick={() => void handleLogout()}
				>
					Logout
				</Button>
			</div>
		</header>

		{#if loading}
			<div class="operator__loading">
				<Spinner size="md" />
				<Text>Loading…</Text>
			</div>
		{:else if errorMessage}
			<Alert variant="error" title="Operator console">{errorMessage}</Alert>
		{:else if me}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={2} size="xl">Who am I?</Heading>
				{/snippet}
				<div class="operator__me">
					<Text size="sm">
						Username: <span class="operator__mono">{me.username}</span>
					</Text>
					<Text size="sm">
						Role: <span class="operator__mono">{me.role}</span>
					</Text>
					<Text size="sm">
						Display name: <span class="operator__mono">{me.display_name || '—'}</span>
					</Text>
				</div>
			</Card>
		{:else}
			<Alert variant="warning" title="No data">
				<Text size="sm">No response from /api/v1/operators/me.</Text>
			</Alert>
		{/if}

		{#if $session && me}
			{#if operatorRoute.kind === 'dashboard'}
				<Dashboard token={$session.token} />
			{:else if operatorRoute.kind === 'vanityDomains'}
				<VanityDomainRequests token={$session.token} />
			{:else if operatorRoute.kind === 'externalRegistrations'}
				<ExternalInstanceRegistrations token={$session.token} />
			{:else if operatorRoute.kind === 'provisioningJobs'}
				<ProvisioningJobs token={$session.token} />
			{:else if operatorRoute.kind === 'provisioningJobDetail'}
				<ProvisioningJobDetail token={$session.token} id={operatorRoute.id} />
			{:else if operatorRoute.kind === 'instances'}
				<InstanceSupport token={$session.token} />
			{:else if operatorRoute.kind === 'instanceDetail'}
				<InstanceSupport token={$session.token} slug={operatorRoute.slug} />
			{:else}
				<Alert variant="warning" title="Not found">
					<Text size="sm">Unknown operator path.</Text>
				</Alert>
			{/if}
		{/if}

		{#if $session}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={2} size="xl">Session</Heading>
				{/snippet}
				<Text size="sm">
					Method: <span class="operator__mono">{$session.method || '—'}</span>
				</Text>
				<Text size="sm">
					Wallet: <span class="operator__mono">{$session.walletAddress || '—'}</span>
				</Text>
				<Text size="sm">
					Expires: <span class="operator__mono">{$session.expiresAt}</span>
				</Text>
			</Card>
		{/if}
	</div>
</Container>

<style>
	.operator {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.operator__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.operator__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.operator__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.operator__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.operator__me {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.operator__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
