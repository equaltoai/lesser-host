<!--
@component
ProvisioningTimeline — vertical timeline of provisioning-job steps.

Project 39 M2.4 (issue #430). Renders the kind-specific step list per
the provisioning walk as a vertical timeline, marks the active step,
and exposes a "View live CodeBuild log" link on the active node so
operators can drop into the AWS console without leaving the page.

The active-step indicator follows the WAI-ARIA "progressbar within a
list" pattern: the timeline is a `<ol>`; the active step carries
`aria-current="step"`; each completed step uses `aria-checked="true"`.

The "live log" is exposed as a link (not an embedded iframe). Embedding
CodeBuild log output cross-origin would either require unauthenticated
public log access (which we never do) or relax the strict single-origin
CSP (which we never do). The link pattern preserves both.

Step lists per kind (from the provisioning walk's table):

  provision:     account.create → account.move → dns.delegate →
                  cdk.deploy → lesser.deploy → body.deploy →
                  wire.mcp → verify
  update-lesser: deploy.lesser → verify.lesser
  update-body:   deploy.body → verify.body
  wire-mcp:      wire.mcp → verify.mcp
  unknown:       (single "step" node sourced from job.step)

Posture preserved:
- Strict-CSP-safe: no inline styles / scripts, no third-party origins.
- Multi-tenant isolation: render-only; the parent owns the data fetch
  with the operator-JWT (host control-plane scope).
- Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.4
-->
<script lang="ts" module>
	import type { JobKind } from './JobKindBadge.svelte';

	const STEPS_BY_KIND: Record<JobKind, string[]> = {
		'provision': [
			'account.create',
			'account.move',
			'dns.delegate',
			'cdk.deploy',
			'lesser.deploy',
			'body.deploy',
			'wire.mcp',
			'verify',
		],
		'update-lesser': ['deploy.lesser', 'verify.lesser'],
		'update-body': ['deploy.body', 'verify.body'],
		'wire-mcp': ['wire.mcp', 'verify.mcp'],
		'unknown': [],
	};

	/**
	 * Build the step list for a given kind. For 'unknown' (or any kind with
	 * an empty list), fall back to a single node sourced from `activeStep`
	 * so the timeline always renders at least the current step.
	 */
	export function stepsForKind(kind: JobKind, activeStep: string | undefined): string[] {
		const enumerated = STEPS_BY_KIND[kind] ?? [];
		if (enumerated.length > 0) return enumerated;
		return activeStep ? [activeStep] : [];
	}

	/**
	 * Classify each step as 'completed', 'active', or 'pending' relative
	 * to the active step. The active step is matched case-insensitively
	 * to tolerate backend-side variants like "Deploy.Lesser" vs.
	 * "deploy.lesser".
	 */
	export type StepState = 'completed' | 'active' | 'pending' | 'failed';
	export function classifySteps(
		steps: string[],
		activeStep: string | undefined,
		jobStatus: string,
	): StepState[] {
		if (!activeStep) {
			// No active step → mark all as pending (or completed if status is 'ok').
			return steps.map(() => (jobStatus.toLowerCase() === 'ok' ? 'completed' : 'pending'));
		}
		const activeIndex = steps.findIndex((s) => s.toLowerCase() === activeStep.toLowerCase());
		// If the active step isn't in the enumerated list (unexpected step name),
		// classify everything as pending so the operator can still see the steps.
		if (activeIndex < 0) return steps.map(() => 'pending');
		return steps.map((_, i) => {
			if (i < activeIndex) return 'completed';
			if (i === activeIndex) {
				return jobStatus.toLowerCase() === 'error' ? 'failed' : 'active';
			}
			return 'pending';
		});
	}
</script>

<script lang="ts">
	import { Text } from 'src/lib/ui';

	let {
		kind,
		activeStep,
		status,
		runUrl,
		errorMessage,
	}: {
		kind: JobKind;
		activeStep?: string;
		status: string;
		runUrl?: string;
		errorMessage?: string;
	} = $props();

	const steps = $derived(stepsForKind(kind, activeStep));
	const states = $derived(classifySteps(steps, activeStep, status));
</script>

{#if steps.length === 0}
	<div class="timeline timeline--empty">
		<Text size="sm" color="secondary">
			No step telemetry available for this job. Step list is kind-specific; this kind has not
			emitted progress events.
		</Text>
	</div>
{:else}
	<ol class="timeline" aria-label="Provisioning timeline">
		{#each steps as step, i (step)}
			{@const state = states[i]}
			<li
				class="timeline__step timeline__step--{state}"
				aria-current={state === 'active' ? 'step' : undefined}
				data-state={state}
			>
				<span class="timeline__indicator" aria-hidden="true"></span>
				<div class="timeline__content">
					<div class="timeline__row">
						<span class="timeline__step-name">{step}</span>
						{#if state === 'active'}
							<span class="timeline__pill timeline__pill--active">Active</span>
						{:else if state === 'failed'}
							<span class="timeline__pill timeline__pill--failed">Failed</span>
						{:else if state === 'completed'}
							<span class="timeline__pill timeline__pill--completed">Done</span>
						{/if}
					</div>
					{#if (state === 'active' || state === 'failed') && runUrl}
						<a class="timeline__log" href={runUrl} target="_blank" rel="noopener noreferrer">
							View live CodeBuild log ↗
						</a>
					{/if}
					{#if state === 'failed' && errorMessage}
						<Text size="sm" color="secondary">{errorMessage}</Text>
					{/if}
				</div>
			</li>
		{/each}
	</ol>
{/if}

<style>
	.timeline {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4);
	}

	.timeline--empty {
		padding: var(--gr-spacing-scale-4) 0;
	}

	.timeline__step {
		display: grid;
		grid-template-columns: 1.25rem 1fr;
		gap: var(--gr-spacing-scale-3);
		align-items: start;
		position: relative;
		padding-left: 0;
	}

	/* Connector line between consecutive step indicators. */
	.timeline__step:not(:last-child)::before {
		content: '';
		position: absolute;
		left: 0.6rem;
		top: 1.4rem;
		bottom: -1rem;
		width: 1px;
		background: var(--gr-color-border, currentColor);
		opacity: 0.35;
	}

	.timeline__indicator {
		width: 1rem;
		height: 1rem;
		border-radius: 999px;
		border: 2px solid var(--gr-color-border, currentColor);
		background: transparent;
		margin-top: 0.2rem;
	}

	.timeline__step--completed .timeline__indicator {
		background: var(--ds-success-500);
		border-color: var(--ds-success-500);
	}

	.timeline__step--active .timeline__indicator {
		background: var(--ds-warning-500);
		border-color: var(--ds-warning-500);
		box-shadow: 0 0 0 4px color-mix(in srgb, var(--ds-warning-500) 28%, transparent);
	}

	.timeline__step--failed .timeline__indicator {
		background: var(--ds-error-500);
		border-color: var(--ds-error-500);
	}

	.timeline__content {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		min-width: 0;
	}

	.timeline__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.timeline__step-name {
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, monospace);
		font-size: 0.92em;
		color: var(--ds-fg-1, inherit);
	}

	.timeline__pill {
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		padding: 0.1rem 0.45rem;
		border-radius: 999px;
		border: 1px solid currentColor;
	}

	.timeline__pill--active {
		color: var(--ds-warning-500);
		background: color-mix(in srgb, var(--ds-warning-500) 14%, transparent);
	}

	.timeline__pill--completed {
		color: var(--ds-success-500);
		background: color-mix(in srgb, var(--ds-success-500) 14%, transparent);
	}

	.timeline__pill--failed {
		color: var(--ds-error-500);
		background: color-mix(in srgb, var(--ds-error-500) 14%, transparent);
	}

	.timeline__log {
		font-size: 0.92rem;
		color: var(--ds-action-link, var(--ds-warning-500));
		text-decoration: none;
		border-bottom: 1px solid transparent;
		align-self: flex-start;
	}
	.timeline__log:hover {
		border-bottom-color: currentColor;
	}
</style>
