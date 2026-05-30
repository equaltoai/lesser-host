<!--
@component
PortalFleet — Project 39 M0.11 fleet view.

Renders a grid of greater-components `FleetCard` for the authenticated
customer's instances, consuming the existing
`GET /api/v1/portal/instances` endpoint and, per-instance, the
existing budget endpoint
`GET /api/v1/portal/instances/{slug}/budgets/{YYYY-MM}` for the
current UTC month. Cost data is BUDGET-ONLY here per the M0 scope —
real-time cost telemetry is deferred to M3.

Posture invariants:
  - Multi-tenant isolation: consumes only the existing per-owner-
    scoped portal endpoints; no cross-tenant reads.
  - Trust-API instance-auth untouched.
  - Strict-no-inline-CSP safe: no inline event handlers, no inline
    styles, no third-party origins.
  - Fail-quiet on per-instance budget reads: if the budget endpoint
    404s (no budget set for this month), the FleetCard simply renders
    without a cost gauge rather than failing the whole fleet load.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.11
Issue: equaltoai/lesser-host#391
-->

<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { get } from 'svelte/store';

	import type { ApiError } from 'src/lib/api/http';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import { portalCreateInstance, portalListInstances } from 'src/lib/api/portalInstances';
	import type { BudgetMonthResponse } from 'src/lib/api/portalUsage';
	import { portalGetBudgetMonth } from 'src/lib/api/portalUsage';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Button, Heading, Link, Spinner, Text, TextField } from 'src/lib/ui';
	import FleetCard from 'src/lib/components/FleetCard.svelte';
	import CostGauge from 'src/lib/components/CostGauge.svelte';
	import type { FleetCardMetadataItem } from 'src/lib/greater/host-platform';
	import { clearPortalFleetState, portalFleetInstances } from 'src/lib/portalFleetState';
	import { mapInstanceFleetStatus } from 'src/lib/fleetStatus';
	import { session } from 'src/lib/session';

	let { token } = $props<{ token: string }>();

	const slugRE = /^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$/;

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let instances = $state<InstanceResponse[]>([]);
	let budgets = $state<Record<string, BudgetMonthResponse>>({});

	let createSlug = $state('');
	let createLoading = $state(false);
	let createError = $state<string | null>(null);
	let loadGeneration = 0;
	let destroyed = false;

	function currentMonthUTC(): string {
		return new Date().toISOString().slice(0, 7);
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

	// Backend status taxonomy mapping lives in `src/lib/fleetStatus.ts` so
	// it can be tested independently. See that module's docstring for the
	// authoritative mapping rules (backend sources: internal/store/models/
	// instance.go + provision_job.go).

	function buildMetadata(inst: InstanceResponse): FleetCardMetadataItem[] {
		const rows: FleetCardMetadataItem[] = [];
		if (inst.hosted_base_domain) rows.push({ key: 'Domain', value: inst.hosted_base_domain });
		if (inst.lesser_version) rows.push({ key: 'Lesser', value: inst.lesser_version });
		if (inst.lesser_body_version) rows.push({ key: 'Body', value: inst.lesser_body_version });
		if (inst.created_at) {
			// Human-readable date only (UTC); strict-CSP-safe formatting via toISOString slice.
			rows.push({ key: 'Created', value: inst.created_at.slice(0, 10) });
		}
		return rows;
	}

	/**
	 * Compute CostGauge thresholds. Default behavior is sensible: warning
	 * at 75% of included credits, danger at 95%. The CostGauge component
	 * auto-elevates status when these thresholds are crossed, and never
	 * communicates state by color alone (it pairs the colored arc with
	 * an icon glyph + accessible text).
	 */
	const COST_THRESHOLDS = { warning: 0.75, danger: 0.95 } as const;

	function isLoadCurrent(loadId: number, loadToken: string): boolean {
		return !destroyed && loadId === loadGeneration && get(session)?.token === loadToken;
	}

	async function loadFleet() {
		const loadId = (loadGeneration += 1);
		const loadToken = token;
		errorMessage = null;
		instances = [];
		budgets = {};
		// Clear the shared fleet snapshot during a refetch so a stale
		// snapshot never produces palette entries for instances the
		// current user no longer has access to.
		clearPortalFleetState();

		loading = true;
		try {
			const res = await portalListInstances(loadToken);
			if (!isLoadCurrent(loadId, loadToken)) return;

			const list = res.instances ?? [];

			// Fetch budgets in parallel; fail-quiet per-instance so one
			// missing-budget 404 doesn't fail the whole fleet view.
			const month = currentMonthUTC();
			const settlements = await Promise.allSettled(
				list.map((inst) => portalGetBudgetMonth(loadToken, inst.slug, month))
			);
			if (!isLoadCurrent(loadId, loadToken)) return;

			const map: Record<string, BudgetMonthResponse> = {};
			for (let i = 0; i < settlements.length; i += 1) {
				const result = settlements[i];
				const inst = list[i];
				if (!inst) continue;
				if (result.status === 'fulfilled') {
					map[inst.slug] = result.value;
				}
				// 404 / other failures: leave map[slug] unset; FleetCard
				// will simply omit the cost slot for that instance.
			}
			instances = list;
			budgets = map;
			// Publish the projection that PortalShell's CommandPalette
			// uses to build per-instance "Open <slug>" entries. Narrow
			// the projection to slug + a couple of low-sensitivity fields
			// so growth in InstanceResponse doesn't widen what the palette
			// observes. This write is guarded after all awaits so logout or
			// user-switch during an in-flight load cannot repopulate stale
			// fleet metadata.
			portalFleetInstances.set(
				list.map((inst) => ({
					slug: inst.slug,
					hosted_region: inst.hosted_region,
					lesser_version: inst.lesser_version,
				}))
			);
		} catch (err) {
			if (!isLoadCurrent(loadId, loadToken)) return;
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			errorMessage = formatError(err);
		} finally {
			if (!destroyed && loadId === loadGeneration) {
				loading = false;
			}
		}
	}

	async function createInstance() {
		createError = null;

		const slug = createSlug.trim().toLowerCase();
		if (!slug) {
			createError = 'Slug is required.';
			return;
		}
		if (!slugRE.test(slug)) {
			createError =
				'Slug must be 1–63 chars, lowercase letters/numbers, and hyphens (cannot start/end with hyphen).';
			return;
		}

		createLoading = true;
		try {
			const inst = await portalCreateInstance(token, slug);
			createSlug = '';
			await loadFleet();
			navigate(`/portal/instances/${inst.slug}`);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			const maybe = err as Partial<ApiError>;
			if (maybe.status === 403 && typeof maybe.message === 'string') {
				const lower = maybe.message.toLowerCase();
				if (lower.includes('approval rejected')) {
					createError = 'Your account was rejected. Contact support if you believe this is a mistake.';
					return;
				}
				if (lower.includes('approval required')) {
					createError =
						'Your account is pending approval. Instance creation and provisioning are blocked until an admin approves your user.';
					return;
				}
			}
			createError = formatError(err);
		} finally {
			createLoading = false;
		}
	}

	onMount(() => {
		void loadFleet();
	});

	onDestroy(() => {
		destroyed = true;
		loadGeneration += 1;
	});
</script>

<div class="portal-fleet">
	<header class="portal-fleet__header">
		<div class="portal-fleet__heading">
			<Heading level={1} size="2xl">Fleet</Heading>
			<Text size="sm" color="secondary">
				Managed lesser.host instances. Cost shows current-month budget consumption (real-time
				telemetry is deferred to M3).
			</Text>
		</div>
		<div class="portal-fleet__header-actions">
			<Button variant="outline" onclick={() => void loadFleet()} disabled={loading}>
				Refresh
			</Button>
		</div>
	</header>

	<section class="portal-fleet__create" aria-label="Create new instance">
		<Heading level={2} size="lg">Create instance</Heading>
		<Text size="sm" color="secondary">
			Reserve a slug to start provisioning managed hosting.
		</Text>
		<div class="portal-fleet__create-row">
			<TextField label="Slug" bind:value={createSlug} placeholder="my-instance" />
			<Button variant="solid" onclick={() => void createInstance()} disabled={createLoading}>
				Create
			</Button>
		</div>
		{#if createError}
			<Alert variant="error" title="Create failed">{createError}</Alert>
		{/if}
	</section>

	{#if loading}
		<div class="portal-fleet__loading">
			<Spinner size="md" />
			<Text>Loading fleet…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load /api/v1/portal/instances">{errorMessage}</Alert>
	{:else if instances.length === 0}
		<Alert variant="info" title="No instances yet">
			<Text size="sm">Create your first instance above to get started.</Text>
		</Alert>
	{:else}
		<ul class="portal-fleet__grid" aria-label="Instance fleet">
			{#each instances as inst (inst.slug)}
				{@const budget = budgets[inst.slug]}
				<li class="portal-fleet__grid-item">
					<FleetCard
						name={inst.slug}
						slug={inst.slug}
						region={inst.hosted_region}
						version={inst.lesser_version}
						status={mapInstanceFleetStatus(inst)}
						metadata={buildMetadata(inst)}
						variant="elevated"
					>
						{#snippet cost()}
							{#if budget && budget.included_credits > 0}
								<CostGauge
									current={budget.used_credits}
									limit={budget.included_credits}
									currency="credits"
									thresholds={COST_THRESHOLDS}
									label="Budget"
								/>
							{:else}
								<Text size="sm" color="secondary">No budget set for {currentMonthUTC()}</Text>
							{/if}
						{/snippet}
						{#snippet actions()}
							<Link {...linkProps(`/portal/instances/${inst.slug}`)} variant="default">
								Open
							</Link>
						{/snippet}
					</FleetCard>
				</li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	.portal-fleet {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-6) 0;
	}

	.portal-fleet__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.portal-fleet__heading {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		max-width: 60ch;
	}

	.portal-fleet__header-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.portal-fleet__create {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		padding: var(--gr-spacing-scale-4);
		border: 1px solid var(--gr-semantic-border-default, var(--gr-color-gray-200));
		border-radius: var(--gr-radii-md, 0.375rem);
		background: var(--gr-semantic-background-surface, var(--gr-color-base-white));
	}

	.portal-fleet__create-row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: flex-end;
		flex-wrap: wrap;
	}

	.portal-fleet__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.portal-fleet__grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(420px, 100%), 1fr));
		gap: var(--gr-spacing-scale-4);
		list-style: none;
		padding: 0;
		margin: 0;
	}

	.portal-fleet__grid-item {
		display: contents;
	}
</style>
