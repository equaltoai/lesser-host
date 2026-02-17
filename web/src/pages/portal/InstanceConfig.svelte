<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import { portalGetInstance, portalUpdateInstanceConfig } from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import type { InstanceConfigForm } from 'src/lib/instanceConfig';
	import {
		buildUpdateInstanceConfigRequest,
		configFormFromInstance,
		validateInstanceConfigForm,
	} from 'src/lib/instanceConfig';
	import { Alert, Button, Card, Heading, Select, Spinner, Switch, Text, TextField } from 'src/lib/ui';

	let { token, slug } = $props<{ token: string; slug: string }>();

	let loading = $state(false);
	let saving = $state(false);
	let errorMessage = $state<string | null>(null);
	let successMessage = $state<string | null>(null);

	let instance = $state<InstanceResponse | null>(null);
	let form = $state<InstanceConfigForm | null>(null);
	let fieldErrors = $state<Record<string, string>>({});

	let ackRenderAlways = $state(false);
	let ackAI = $state(false);

	const requiresRenderAlwaysAck = $derived(Boolean(form?.renders_enabled && form?.render_policy === 'always'));
	const requiresAIAck = $derived(Boolean(form?.ai_enabled));
	const canSave = $derived(
		!saving &&
			!!form &&
			Object.keys(fieldErrors).length === 0 &&
			(!requiresRenderAlwaysAck || ackRenderAlways) &&
			(!requiresAIAck || ackAI),
	);

	$effect(() => {
		if (!form) {
			fieldErrors = {};
			return;
		}
		fieldErrors = validateInstanceConfigForm(form);
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

	function applyServerFieldErrors(message: string) {
		const next: Record<string, string> = {};
		const lowered = message.toLowerCase();
		if (lowered.includes('render_policy')) next.render_policy = message;
		if (lowered.includes('overage_policy')) next.overage_policy = message;
		if (lowered.includes('moderation_trigger')) next.moderation_trigger = message;
		if (lowered.includes('moderation_virality_min')) next.moderation_virality_min = message;
		if (lowered.includes('ai_model_set')) next.ai_model_set = message;
		if (lowered.includes('ai_batching_mode')) next.ai_batching_mode = message;
		if (lowered.includes('ai_batch_max_items')) next.ai_batch_max_items = message;
		if (lowered.includes('ai_batch_max_total_bytes')) next.ai_batch_max_total_bytes = message;
		if (lowered.includes('ai_pricing_multiplier_bps')) next.ai_pricing_multiplier_bps = message;
		if (lowered.includes('ai_max_inflight_jobs')) next.ai_max_inflight_jobs = message;
		if (Object.keys(next).length > 0) {
			fieldErrors = { ...fieldErrors, ...next };
		}
	}

	async function load() {
		errorMessage = null;
		successMessage = null;
		instance = null;
		form = null;
		ackAI = false;
		ackRenderAlways = false;

		loading = true;
		try {
			const res = await portalGetInstance(token, slug);
			instance = res;
			form = configFormFromInstance(res);
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

	async function save() {
		errorMessage = null;
		successMessage = null;

		if (!form) return;

		const errors = validateInstanceConfigForm(form);
		fieldErrors = errors;
		if (Object.keys(errors).length > 0) {
			errorMessage = 'Fix validation errors before saving.';
			return;
		}
		if (requiresRenderAlwaysAck && !ackRenderAlways) {
			errorMessage = 'Acknowledge render policy cost warning before saving.';
			return;
		}
		if (requiresAIAck && !ackAI) {
			errorMessage = 'Acknowledge AI cost warning before saving.';
			return;
		}

		saving = true;
		try {
			const updated = await portalUpdateInstanceConfig(token, slug, buildUpdateInstanceConfigRequest(form));
			instance = updated;
			form = configFormFromInstance(updated);
			successMessage = 'Saved.';
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			const message = formatError(err);
			errorMessage = message;
			applyServerFieldErrors(message);
		} finally {
			saving = false;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="config">
	<header class="config__header">
		<div class="config__title">
			<Heading level={2} size="xl">Configuration</Heading>
			<Text color="secondary">
				Instance <span class="config__mono">{slug}</span>.
			</Text>
		</div>
		<div class="config__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading || saving}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}`)}>Back</Button>
			<Button variant="solid" onclick={() => void save()} disabled={!canSave}>Save</Button>
		</div>
	</header>

	{#if loading}
		<div class="config__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Configuration">{errorMessage}</Alert>
	{:else if successMessage}
		<Alert variant="success" title="Configuration">{successMessage}</Alert>
	{/if}

	{#if instance && form}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Features</Heading>
			{/snippet}

			<div class="config__grid">
				<div class="config__toggle">
					<Switch bind:checked={form.hosted_previews_enabled} label="Hosted previews" />
					<Text size="sm" color="secondary">Serve previews from hosted infrastructure.</Text>
				</div>
				<div class="config__toggle">
					<Switch bind:checked={form.link_safety_enabled} label="Link safety" />
					<Text size="sm" color="secondary">Enable link safety checks.</Text>
				</div>
				<div class="config__toggle">
					<Switch bind:checked={form.renders_enabled} label="Renders" />
					<Text size="sm" color="secondary">Enable rendering pipeline.</Text>
				</div>
				<div class="config__toggle">
					<Switch bind:checked={form.translation_enabled} label="Translation" />
					<Text size="sm" color="secondary">Enable instance translation (requires AWS Translate permissions).</Text>
				</div>
			</div>

			<div class="config__grid">
				<div class="config__field">
					<Text size="sm">Render policy</Text>
					<Select
						bind:value={form.render_policy}
						options={[
							{ value: 'suspicious', label: 'Suspicious only' },
							{ value: 'always', label: 'Always' },
						]}
					/>
					{#if fieldErrors.render_policy}
						<Text size="sm" color="secondary">{fieldErrors.render_policy}</Text>
					{/if}
				</div>
				<div class="config__field">
					<Text size="sm">Overage policy</Text>
					<Select
						bind:value={form.overage_policy}
						options={[
							{ value: 'block', label: 'Block' },
							{ value: 'allow', label: 'Allow' },
						]}
					/>
					{#if fieldErrors.overage_policy}
						<Text size="sm" color="secondary">{fieldErrors.overage_policy}</Text>
					{/if}
				</div>
			</div>

			{#if requiresRenderAlwaysAck}
				<Alert variant="warning" title="Cost warning">
					<Text size="sm">
						<span class="config__mono">render_policy=always</span> may significantly increase cost.
					</Text>
					<div class="config__row">
						<Switch bind:checked={ackRenderAlways} label="I understand." />
					</div>
				</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Moderation</Heading>
			{/snippet}

			<div class="config__grid">
				<div class="config__toggle">
					<Switch bind:checked={form.moderation_enabled} label="Enabled" />
					<Text size="sm" color="secondary">Enable moderation pipeline.</Text>
				</div>
				<div class="config__field">
					<Text size="sm">Trigger</Text>
					<Select
						bind:value={form.moderation_trigger}
						options={[
							{ value: 'on_reports', label: 'On reports' },
							{ value: 'always', label: 'Always' },
							{ value: 'links_media_only', label: 'Links/media only' },
							{ value: 'virality', label: 'Virality' },
						]}
					/>
					{#if fieldErrors.moderation_trigger}
						<Text size="sm" color="secondary">{fieldErrors.moderation_trigger}</Text>
					{/if}
				</div>
				<div class="config__field">
					<TextField
						label="Virality minimum"
						bind:value={form.moderation_virality_min}
						placeholder="0"
						invalid={Boolean(fieldErrors.moderation_virality_min)}
						errorMessage={fieldErrors.moderation_virality_min}
					/>
				</div>
			</div>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">AI</Heading>
			{/snippet}

			<Text size="sm" color="secondary">
				Batching reduces cost at the expense of latency. <span class="config__mono">hybrid</span> can balance both.
			</Text>

			<div class="config__grid">
				<div class="config__toggle">
					<Switch bind:checked={form.ai_enabled} label="Enabled" />
					<Text size="sm" color="secondary">Enable AI enrichment.</Text>
				</div>
				<div class="config__field">
					<TextField
						label="Model set"
						bind:value={form.ai_model_set}
						placeholder="default"
						invalid={Boolean(fieldErrors.ai_model_set)}
						errorMessage={fieldErrors.ai_model_set}
					/>
				</div>
				<div class="config__field">
					<Text size="sm">Batching mode</Text>
					<Select
						bind:value={form.ai_batching_mode}
						options={[
							{ value: 'none', label: 'None' },
							{ value: 'in_request', label: 'In request' },
							{ value: 'worker', label: 'Worker' },
							{ value: 'hybrid', label: 'Hybrid' },
						]}
					/>
					{#if fieldErrors.ai_batching_mode}
						<Text size="sm" color="secondary">{fieldErrors.ai_batching_mode}</Text>
					{/if}
				</div>
			</div>

			<div class="config__grid">
				<div class="config__field">
					<TextField
						label="Batch max items"
						bind:value={form.ai_batch_max_items}
						placeholder="10"
						invalid={Boolean(fieldErrors.ai_batch_max_items)}
						errorMessage={fieldErrors.ai_batch_max_items}
					/>
				</div>
				<div class="config__field">
					<TextField
						label="Batch max total bytes"
						bind:value={form.ai_batch_max_total_bytes}
						placeholder="1048576"
						invalid={Boolean(fieldErrors.ai_batch_max_total_bytes)}
						errorMessage={fieldErrors.ai_batch_max_total_bytes}
					/>
				</div>
				<div class="config__field">
					<TextField
						label="Pricing multiplier (bps)"
						bind:value={form.ai_pricing_multiplier_bps}
						placeholder="10000"
						invalid={Boolean(fieldErrors.ai_pricing_multiplier_bps)}
						errorMessage={fieldErrors.ai_pricing_multiplier_bps}
					/>
				</div>
				<div class="config__field">
					<TextField
						label="Max inflight jobs"
						bind:value={form.ai_max_inflight_jobs}
						placeholder="10"
						invalid={Boolean(fieldErrors.ai_max_inflight_jobs)}
						errorMessage={fieldErrors.ai_max_inflight_jobs}
					/>
				</div>
			</div>

			{#if requiresAIAck}
				<Alert variant="warning" title="Cost warning">
					<Text size="sm">
						AI can materially increase cost. Confirm before enabling.
					</Text>
					<div class="config__row">
						<Switch bind:checked={ackAI} label="I understand." />
					</div>
				</Alert>
			{/if}
		</Card>
	{/if}
</div>

<style>
	.config {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.config__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.config__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.config__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.config__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.config__grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
		gap: var(--gr-spacing-scale-4);
		margin-top: var(--gr-spacing-scale-4);
	}

	.config__field {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.config__toggle {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		padding: var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-color-border-subtle);
		border-radius: var(--gr-radius-md);
		background: var(--gr-color-surface);
	}

	.config__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
		flex-wrap: wrap;
	}

	.config__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
