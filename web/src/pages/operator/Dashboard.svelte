<!--
@component
Operator Dashboard — re-skinned for the M2.1 dark warm-charcoal chrome.

Project 39 M2.2 (issue #428). Replaces the single "Queues" DefinitionList
with a 3-column metric strip + quick-actions block so the dashboard reads
as a control surface on the dark chrome rather than a flat property list.

Behavior preserved:
- Same three queue counts (vanity domains, portal users, external regs)
  loaded in parallel.
- Same 401 → logout / login navigation guard.
- Same Refresh CTA on the header.

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles / third-party origins.
- Multi-tenant isolation: dashboard never reads tenant content; only the
  three operator approval queue counts are surfaced.
- Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.2
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import { listExternalInstanceRegistrations, listPortalUserApprovals, listVanityDomainRequests } from 'src/lib/api/operators';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { StatCard, SummaryStrip } from 'src/lib/shell';
	import type { StatCardStatus } from 'src/lib/shell';
	import { Alert, Button, Card, Heading, Link, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);

	let vanityCount = $state<number | null>(null);
	let externalCount = $state<number | null>(null);
	let userCount = $state<number | null>(null);

	/**
	 * Map a queue count to a StatCard tone so non-zero queues read as
	 * "needs attention" against the dark chrome. Null (unloaded) and 0
	 * both render as the neutral default; >0 reads as warning (amber).
	 */
	function queueStatus(count: number | null): StatCardStatus {
		if (count == null) return 'default';
		if (count > 0) return 'warning';
		return 'success';
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
		<SummaryStrip label="Operator approval queues" columns={3} gap="md">
			<StatCard
				label="Vanity domain requests"
				value={String(vanityCount ?? 0)}
				status={queueStatus(vanityCount)}
			/>
			<StatCard
				label="Portal user approvals"
				value={String(userCount ?? 0)}
				status={queueStatus(userCount)}
			/>
			<StatCard
				label="External instance registrations"
				value={String(externalCount ?? 0)}
				status={queueStatus(externalCount)}
			/>
		</SummaryStrip>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Quick actions</Heading>
			{/snippet}
			<Text size="sm" color="secondary">
				Operator approval queues, provisioning, instance search, tip registry, and audit log.
			</Text>
			<div class="op-dashboard__row">
				<Link {...linkProps('/operator/approvals/domains')} variant="default">Review domains</Link>
				<Link {...linkProps('/operator/approvals/users')} variant="default">Review users</Link>
				<Link {...linkProps('/operator/approvals/external-instances')} variant="default">
					Review external registrations
				</Link>
				<Link {...linkProps('/operator/provisioning/jobs')} variant="default">Provisioning jobs</Link>
				<Link {...linkProps('/operator/instances')} variant="default">Instance search</Link>
				<Link {...linkProps('/operator/tip-registry')} variant="default">Tip registry</Link>
				<Link {...linkProps('/operator/audit')} variant="default">Audit log</Link>
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
