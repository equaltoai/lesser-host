<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import { portalCreateInstance, portalListInstances } from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, Heading, Spinner, Text, TextField } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	const slugRE = /^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$/;

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let instances = $state<InstanceResponse[]>([]);

	let createSlug = $state('');
	let createLoading = $state(false);
	let createError = $state<string | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function loadInstances() {
		errorMessage = null;
		instances = [];

		loading = true;
		try {
			const res = await portalListInstances(token);
			instances = res.instances ?? [];
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
			await loadInstances();
			navigate(`/portal/instances/${inst.slug}`);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			const maybe = err as Partial<ApiError>;
			if (maybe.status === 403 && typeof maybe.message === 'string' && maybe.message.includes('approval')) {
				createError =
					'Your account is pending approval. Instance creation and provisioning are blocked until an admin approves your user.';
				return;
			}
			createError = formatError(err);
		} finally {
			createLoading = false;
		}
	}

	onMount(() => {
		void loadInstances();
	});
</script>

<div class="instances">
	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={2} size="xl">Instances</Heading>
		{/snippet}

		<Text size="sm" color="secondary">
			Create a slug to reserve <code>{'{slug}'}</code> and start provisioning managed hosting.
		</Text>

		<div class="instances__create">
			<TextField label="Slug" bind:value={createSlug} placeholder="my-instance" />
			<div class="instances__row">
				<Button variant="solid" onclick={() => void createInstance()} disabled={createLoading}>
					Create instance
				</Button>
				<Button variant="outline" onclick={() => void loadInstances()} disabled={loading}>
					Refresh
				</Button>
			</div>
		</div>

		{#if createError}
			<Alert variant="error" title="Create failed">{createError}</Alert>
		{/if}
	</Card>

	{#if loading}
		<div class="instances__loading">
			<Spinner size="md" />
			<Text>Loading instances…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load /api/v1/portal/instances">{errorMessage}</Alert>
	{:else if instances.length === 0}
		<Alert variant="info" title="No instances">
			<Text size="sm">Create your first instance to get started.</Text>
		</Alert>
	{:else}
		<div class="instances__list">
			{#each instances as inst (inst.slug)}
				<Card variant="outlined" padding="md">
					<div class="instances__item">
						<div class="instances__item-meta">
							<Text size="sm" weight="medium">{inst.slug}</Text>
							<Text size="sm" color="secondary">Status: {inst.status}</Text>
							{#if inst.provision_status}
								<Text size="sm" color="secondary">Provisioning: {inst.provision_status}</Text>
							{/if}
						</div>
						<div class="instances__item-actions">
							<Button variant="outline" onclick={() => navigate(`/portal/instances/${inst.slug}`)}>
								Open
							</Button>
						</div>
					</div>
				</Card>
			{/each}
		</div>
	{/if}
</div>

<style>
	.instances {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.instances__create {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.instances__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.instances__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.instances__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
	}

	.instances__item {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.instances__item-meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		min-width: min(520px, 100%);
	}

	.instances__item-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}
</style>
