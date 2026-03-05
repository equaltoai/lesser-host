<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { OperatorProvisionJobDetail } from 'src/lib/api/operatorProvisioning';
	import {
		adoptOperatorProvisionJobAccount,
		appendOperatorProvisionJobNote,
		getOperatorProvisionJob,
		retryOperatorProvisionJob,
	} from 'src/lib/api/operatorProvisioning';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import {
		Alert,
		Badge,
		Button,
		Card,
		CopyButton,
		DefinitionItem,
		DefinitionList,
		Heading,
		Spinner,
		Text,
		TextArea,
		TextField,
	} from 'src/lib/ui';

	let { token, id } = $props<{ token: string; id: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let job = $state<OperatorProvisionJobDetail | null>(null);

	let retryLoading = $state(false);
	let retryError = $state<string | null>(null);

	let noteInput = $state('');
	let noteLoading = $state(false);
	let noteError = $state<string | null>(null);

	let adoptAccountId = $state('');
	let adoptAccountEmail = $state('');
	let adoptNote = $state('');
		let adoptLoading = $state(false);
		let adoptError = $state<string | null>(null);

		let showReceipt = $state(false);

	const statusBadge = $derived.by(() => badgeForStatus(job?.status ?? ''));
	const stalledMinutes = $derived.by(() => {
		if (!job) return null;
		const status = (job.status || '').toLowerCase();
		if (status !== 'queued' && status !== 'running') return null;
		const updatedAt = Date.parse(job.updated_at);
		if (!Number.isFinite(updatedAt)) return null;
		const ageMs = Date.now() - updatedAt;
		const thresholdMs = 30 * 60 * 1000;
		if (ageMs < thresholdMs) return null;
		return Math.floor(ageMs / 60000);
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

	function badgeForStatus(status: string): { variant: 'outlined' | 'filled'; color: 'success' | 'warning' | 'error' | 'gray' } {
		const s = (status || '').toLowerCase();
		if (s === 'ok') return { variant: 'filled', color: 'success' };
		if (s === 'running' || s === 'queued') return { variant: 'outlined', color: 'warning' };
		if (s === 'error') return { variant: 'filled', color: 'error' };
		return { variant: 'outlined', color: 'gray' };
	}

	async function load() {
		errorMessage = null;
		job = null;

		loading = true;
		try {
			job = await getOperatorProvisionJob(token, id);
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

	async function retry() {
		retryError = null;
		retryLoading = true;
		try {
			job = await retryOperatorProvisionJob(token, id);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			retryError = formatError(err);
		} finally {
			retryLoading = false;
		}
	}

	async function appendNote() {
		noteError = null;
		const trimmed = noteInput.trim();
		if (!trimmed) {
			noteError = 'Note is required.';
			return;
		}

		noteLoading = true;
		try {
			job = await appendOperatorProvisionJobNote(token, id, trimmed);
			noteInput = '';
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			noteError = formatError(err);
		} finally {
			noteLoading = false;
		}
	}

	async function adoptAccount() {
		adoptError = null;
		const accountId = adoptAccountId.trim();
		if (!/^\d{12}$/.test(accountId)) {
			adoptError = 'Account id must be a 12-digit AWS account id.';
			return;
		}

		adoptLoading = true;
		try {
			job = await adoptOperatorProvisionJobAccount(token, id, {
				account_id: accountId,
				account_email: adoptAccountEmail.trim() || undefined,
				note: adoptNote.trim() || undefined,
			});
			adoptAccountId = '';
			adoptAccountEmail = '';
			adoptNote = '';
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			adoptError = formatError(err);
		} finally {
			adoptLoading = false;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="op-job">
	<header class="op-job__header">
		<div class="op-job__title">
			<Heading level={2} size="xl">Provisioning job</Heading>
			<Text color="secondary"><span class="op-job__mono">{id}</span></Text>
		</div>
		<div class="op-job__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate('/operator/provisioning/jobs')}>Back</Button>
		</div>
	</header>

	{#if loading}
		<div class="op-job__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Provisioning job">{errorMessage}</Alert>
	{:else if job}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<div class="op-job__row">
					<div class="op-job__row-left">
						<Heading level={3} size="lg">Overview</Heading>
						<Badge variant={statusBadge.variant} color={statusBadge.color} size="sm">{job?.status ?? '—'}</Badge>
					</div>
					<div class="op-job__row-right">
						<CopyButton size="sm" text={job?.id ?? ''} />
					</div>
				</div>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Job id" monospace>{job.id}</DefinitionItem>
				<DefinitionItem label="Instance" monospace>{job.instance_slug}</DefinitionItem>
				<DefinitionItem label="Step" monospace>{job.step || '—'}</DefinitionItem>
				<DefinitionItem label="Attempts" monospace>{String(job.attempts)}/{String(job.max_attempts || 0)}</DefinitionItem>
				<DefinitionItem label="Run id" monospace>{job.run_id || '—'}</DefinitionItem>
				<DefinitionItem label="Request id" monospace>{job.request_id || '—'}</DefinitionItem>
				<DefinitionItem label="Updated" monospace>{job.updated_at}</DefinitionItem>
			</DefinitionList>

			<div class="op-job__row">
				<Button variant="ghost" onclick={() => navigate(`/operator/instances/${job?.instance_slug ?? ''}`)}>Open instance</Button>
				<Button variant="outline" onclick={() => void retry()} disabled={retryLoading || job?.status === 'ok'}>
					Retry / requeue
				</Button>
				{#if retryLoading}
					<div class="op-job__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Working…</Text>
					</div>
				{/if}
			</div>
			{#if retryError}
				<Alert variant="error" title="Retry failed">{retryError}</Alert>
			{/if}
		</Card>

		{#if job.status === 'error'}
			<Alert variant="error" title="Failure">
				<Text size="sm">
					<span class="op-job__mono">{job.error_code || 'error'}</span>
				</Text>
				{#if job.error_message}
					<Text size="sm">{job.error_message}</Text>
				{/if}
			</Alert>
		{/if}

		{#if stalledMinutes}
			<Alert variant="warning" title="Provisioning may be stalled">
				<Text size="sm">
					No updates in {stalledMinutes} minutes. If this looks stuck, retry or adopt an existing account.
				</Text>
			</Alert>
		{/if}

		{#if job.status === 'error'}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Adopt existing account</Heading>
				{/snippet}

				<Text size="sm" color="secondary">
					Use this when an AWS account was created but provisioning failed before it could be attached.
					This resets the job to <span class="op-job__mono">account.move</span> and requeues it.
				</Text>

				<div class="op-job__adopt">
					<TextField
						label="Account id"
						placeholder="123456789012"
						bind:value={adoptAccountId}
						required
					/>
					<TextField
						label="Account email (optional)"
						placeholder="ops+slug@example.com"
						bind:value={adoptAccountEmail}
					/>
					<TextField
						label="Note (optional)"
						placeholder="Why we are adopting this account"
						bind:value={adoptNote}
					/>
					<div class="op-job__row">
						<Button variant="solid" onclick={() => void adoptAccount()} disabled={adoptLoading}>Adopt account</Button>
						{#if adoptLoading}
							<div class="op-job__loading-inline">
								<Spinner size="sm" />
								<Text size="sm">Working…</Text>
							</div>
						{/if}
					</div>
					{#if adoptError}
						<Alert variant="error" title="Adopt failed">{adoptError}</Alert>
					{/if}
				</div>
			</Card>
		{/if}

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Operator note</Heading>
			{/snippet}

			{#if job.note}
				<TextArea value={job.note} readonly rows={6} />
			{:else}
				<Text size="sm" color="secondary">No note.</Text>
			{/if}

			<div class="op-job__note">
				<TextArea bind:value={noteInput} placeholder="Append a note…" rows={3} />
				<div class="op-job__row">
					<Button variant="solid" onclick={() => void appendNote()} disabled={noteLoading}>Append</Button>
					{#if noteLoading}
						<div class="op-job__loading-inline">
							<Spinner size="sm" />
							<Text size="sm">Saving…</Text>
						</div>
					{/if}
				</div>
				{#if noteError}
					<Alert variant="error" title="Note failed">{noteError}</Alert>
				{/if}
			</div>
		</Card>

			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Lesser receipt</Heading>
				{/snippet}

			<Text size="sm" color="secondary">
				Receipt JSON may be large. Only display when needed.
			</Text>

			<div class="op-job__row">
				<Button variant="outline" onclick={() => (showReceipt = !showReceipt)} disabled={!job?.has_receipt}>
					{showReceipt ? 'Hide receipt' : 'Show receipt'}
				</Button>
				{#if !job?.has_receipt}
					<Text size="sm" color="secondary">No receipt stored.</Text>
				{/if}
			</div>

				{#if showReceipt && job?.receipt_json}
					<TextArea value={job.receipt_json} readonly rows={10} />
				{/if}
			</Card>
		{:else}
			<Alert variant="warning" title="No data">
				<Text size="sm">No job response.</Text>
			</Alert>
	{/if}
</div>

<style>
	.op-job {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-job__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-job__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-job__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-job__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-job__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-job__row-left {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-job__row-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-job__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-job__note {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-job__adopt {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-job__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
