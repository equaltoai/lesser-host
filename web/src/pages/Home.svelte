<script lang="ts">
	import { onMount } from 'svelte';
	import type { SetupStatusResponse } from 'src/lib/api/controlPlane';
	import { getSetupStatus } from 'src/lib/api/controlPlane';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { Alert, Button, Container, DefinitionItem, DefinitionList, Heading, Spinner, Text } from 'src/lib/ui';

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let status = $state<SetupStatusResponse | null>(null);

	async function loadStatus() {
		loading = true;
		errorMessage = null;
		try {
			status = await getSetupStatus();
		} catch (err) {
			if (err instanceof Error) {
				errorMessage = err.message;
			} else {
				errorMessage = 'failed to load /setup/status';
			}
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void loadStatus();
	});

	function defaultRouteForRole(role: string): string {
		if (role === 'admin' || role === 'operator') return '/operator';
		return '/portal';
	}

	async function handleLogout() {
		await logout();
		navigate('/login');
	}
</script>

<Container size="lg" gutter="lg">
	<div class="home">
		<header class="home__header">
			<div class="home__title">
				<Heading level={1}>lesser.host</Heading>
				<Text size="sm" color="secondary">Portal + operator console</Text>
			</div>

			<div class="home__actions">
				<Button variant="outline" onclick={() => void loadStatus()} disabled={loading}>
					Refresh
				</Button>
				<Button variant="ghost" onclick={() => navigate('/setup')}>Setup</Button>
				<Button variant="ghost" onclick={() => navigate('/tip-registry/register')}>Register host</Button>
				<Button variant="ghost" onclick={() => navigate('/trust')}>Trust</Button>
				{#if $session}
					<Button variant="ghost" onclick={() => navigate('/account')}>Account</Button>
					<Button variant="ghost" onclick={() => navigate(defaultRouteForRole($session.role))}>
						Continue
					</Button>
					<Button
						variant="ghost"
						onclick={() => void handleLogout()}
					>
						Logout
					</Button>
				{:else}
					<Button variant="solid" onclick={() => navigate('/login')}>Sign in</Button>
				{/if}
			</div>
		</header>

			{#if $session}
				<Alert variant="info" title="Session">
					<Text size="sm">
						Signed in as <strong>{$session.username}</strong> (<strong>{$session.role}</strong>) · expires
						<strong>{$session.expiresAt}</strong>
					</Text>
				</Alert>
			{/if}

		{#if loading}
			<div class="home__loading">
				<Spinner size="md" />
				<Text>Loading setup status…</Text>
			</div>
		{:else if errorMessage}
			<Alert variant="error" title="Failed to load setup status">{errorMessage}</Alert>
		{:else if status}
			<Alert
				variant={status.locked ? 'warning' : 'success'}
				title={status.locked ? 'Control plane locked' : 'Control plane active'}
			>
				<Text size="sm">
					Stage: <strong>{status.stage || 'unknown'}</strong>
				</Text>
			</Alert>

			<DefinitionList>
				<DefinitionItem label="Stage" monospace>{status.stage}</DefinitionItem>
				<DefinitionItem label="State" monospace>{status.control_plane_state}</DefinitionItem>
				<DefinitionItem label="Locked" monospace>{status.locked ? 'true' : 'false'}</DefinitionItem>
				<DefinitionItem label="Finalize allowed" monospace>
					{status.finalize_allowed ? 'true' : 'false'}
				</DefinitionItem>
				<DefinitionItem label="Bootstrapped at" monospace>
					{status.bootstrapped_at || '—'}
				</DefinitionItem>
				<DefinitionItem label="Bootstrap wallet set" monospace>
					{status.bootstrap_wallet_address_set ? 'true' : 'false'}
				</DefinitionItem>
				<DefinitionItem label="Bootstrap wallet" monospace>
					{status.bootstrap_wallet_address || '—'}
				</DefinitionItem>
				<DefinitionItem label="Primary admin set" monospace>
					{status.primary_admin_set ? 'true' : 'false'}
				</DefinitionItem>
				<DefinitionItem label="Primary admin" monospace>
					{status.primary_admin_username || '—'}
				</DefinitionItem>
			</DefinitionList>
		{:else}
			<Alert variant="warning" title="No data">No response from /setup/status.</Alert>
		{/if}
	</div>
</Container>

<style>
	.home {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.home__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.home__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.home__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.home__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}
</style>
