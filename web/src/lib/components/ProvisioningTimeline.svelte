<!--
@component
ProvisioningTimeline — vertical timeline of provisioning-job steps.

Project 39 M2.4 (issue #430) shipped the base timeline; M2.13
(issue #439) refines it per the four job kinds and adds an explicit
auto-queued "Wire MCP in Lesser" gated step at the end of every
update-body job timeline. The gated step is a UI affordance — it
informs the operator that a body update intentionally leaves MCP in
a wire-stale posture until either the fleet wire-all CTA or the
per-instance wire-mcp flow remediates it. It is NOT a backend step
inside the update-body job's state machine (BodyOnly jobs run
body → verify → done; the MCP re-wire is a follow-up MCP-only
UpdateJob), so it never carries an `active` or `failed` state — it
only ever renders as `auto-queued`.

Step vocabulary is sourced from the real provision-worker / update-job
constants (NOT from a parallel UI-side enumeration). Aligned to source
after PR #512 arch review 4363557132 Blocker 3:

  provision steps      — from `internal/provisionworker/server.go`
                          `provisionStep*` constants:
                          queued → account.create → account.create.poll →
                          account.move → account.assumeRole →
                          dns.childZone → dns.parentDelegation →
                          instance.config → deploy.start → deploy.wait →
                          receipt.ingest → body.deploy.start →
                          body.deploy.wait → deploy.mcp.start →
                          deploy.mcp.wait → done
  update-* phases      — from `internal/provisionworker/update_jobs.go`
                          `updatePhase*` constants. Kind discriminates:
                          update-lesser: deploy → verify
                          update-body  : body → verify (+ auto-queued
                                         wire-mcp gated step, UI-only)
                          wire-mcp     : mcp → verify

A `failed` step is rendered if the job's status is 'error' and the
active step matches a known step name; otherwise an unknown / new step
is rendered as a single fallback node.

The "View live CodeBuild log" link is only rendered when a real
`runUrl` is supplied. The earlier draft synthesised a URL from
`run_id`, but the ProvisionJob operator response carries `run_id` as a
build identifier — not a console URL — and host's region is not
exposed in the response either. Synthesising an AWS console URL would
mislead operators (and break on stage/region drift), so the link is
honest: only renders when the backend gives us a real URL. The
update-job response already carries `run_url` / `deploy_run_url` /
`body_run_url` / `mcp_run_url`; provision-job will carry the same once
backend M2.4-companion work lands.

The active-step indicator follows the WAI-ARIA "progressbar within a
list" pattern: the timeline is a `<ol>`; the active step carries
`aria-current="step"`; per-step state is exposed via `data-state` for
analytics + gov-rubric checks. The auto-queued gated step uses
`data-state="auto-queued"` and never receives `aria-current` since it
is a follow-up consequence rather than a current/completed work item.

Posture preserved:
- Strict-CSP-safe: no inline styles / scripts, no third-party origins.
- Multi-tenant isolation: render-only; the parent owns the data fetch
  with the operator-JWT (host control-plane scope).
- Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.4, M2.13
-->
<script lang="ts" module>
	import type { JobKind } from './JobKindBadge.svelte';

	/**
	 * Sentinel step name for the auto-queued "Wire MCP in Lesser" gated
	 * step appended to every update-body timeline. The colon-separated
	 * form cannot collide with any real backend step constant (which use
	 * dot-separated forms like `body.deploy.start`), so `findIndex` matches
	 * against the active step never accidentally select it.
	 */
	export const AUTO_QUEUED_WIRE_MCP_STEP = 'wire-mcp-in-lesser:auto-queued';

	/**
	 * Human-readable label rendered for the gated step. Kept separate from
	 * the sentinel so the sentinel can change without retranslating UI text.
	 */
	export const AUTO_QUEUED_WIRE_MCP_LABEL = 'Wire MCP in Lesser';

	/**
	 * Step lists per kind. Sourced verbatim from the real
	 * provisionworker / update_jobs state machine constants so the
	 * timeline cannot drift from production behaviour.
	 *
	 * The `provision` list omits the `failed` terminal (rendered via
	 * status='error' state on the matching active step) and includes
	 * `done` as the canonical success terminal.
	 */
	const STEPS_BY_KIND: Record<JobKind, string[]> = {
		'provision': [
			'queued',
			'account.create',
			'account.create.poll',
			'account.move',
			'account.assumeRole',
			'dns.childZone',
			'dns.parentDelegation',
			'instance.config',
			'deploy.start',
			'deploy.wait',
			'receipt.ingest',
			'body.deploy.start',
			'body.deploy.wait',
			'deploy.mcp.start',
			'deploy.mcp.wait',
			'done',
		],
		// Update-job phases. `updatePhaseNone` (empty string) and the
		// per-phase status (`pending`/`running`/`succeeded`/`failed`/`skipped`)
		// are not part of the visible step list; we render the major-phase
		// sequence and let the per-step state reflect status.
		'update-lesser': ['deploy', 'verify'],
		// `body` and `verify` are the real BodyOnly state machine. The
		// trailing AUTO_QUEUED_WIRE_MCP_STEP is a UI-only gated affordance:
		// it cues the operator that a body update intentionally leaves the
		// MCP component wire-stale until a follow-up MCP-only UpdateJob
		// remediates. classifySteps() always marks it `auto-queued`.
		'update-body': ['body', 'verify', AUTO_QUEUED_WIRE_MCP_STEP],
		'wire-mcp': ['mcp', 'verify'],
		'unknown': [],
	};

	/** Identify the auto-queued gated sentinel without leaking constants. */
	export function isAutoQueuedStep(step: string): boolean {
		return step === AUTO_QUEUED_WIRE_MCP_STEP;
	}

	/** Display label for a step — folds the auto-queued sentinel to its label. */
	export function stepLabel(step: string): string {
		return step === AUTO_QUEUED_WIRE_MCP_STEP ? AUTO_QUEUED_WIRE_MCP_LABEL : step;
	}

	/**
	 * Build the step list for a given kind. For 'unknown' (or any kind
	 * with an empty list), fall back to a single node sourced from
	 * `activeStep` so the timeline always renders at least the current
	 * step. If the active step is not in the enumerated list (an unexpected
	 * server-side step name — most likely a new phase added in
	 * provision-worker before this component was updated), the active step
	 * is appended at the end so it still surfaces somewhere. The unknown
	 * step is inserted BEFORE any trailing auto-queued sentinel so the
	 * gated step remains visually terminal.
	 */
	export function stepsForKind(kind: JobKind, activeStep: string | undefined): string[] {
		const enumerated = STEPS_BY_KIND[kind] ?? [];
		if (enumerated.length === 0) return activeStep ? [activeStep] : [];
		if (!activeStep) return enumerated;
		const normalized = activeStep.toLowerCase();
		const hit = enumerated.some(
			(s) => !isAutoQueuedStep(s) && s.toLowerCase() === normalized,
		);
		if (hit) return enumerated;
		// Unknown step name — surface it so the operator at least sees it,
		// but keep the auto-queued gated sentinel (if any) at the tail end.
		const trailingAutoQueued = enumerated.filter(isAutoQueuedStep);
		const realSteps = enumerated.filter((s) => !isAutoQueuedStep(s));
		return [...realSteps, activeStep, ...trailingAutoQueued];
	}

	/**
	 * Classify each step as 'completed', 'active', 'failed', 'pending',
	 * or 'auto-queued' relative to the active step. The active step is
	 * matched case-insensitively to tolerate backend-side variants. When
	 * the active step is unknown (not in the enumerated list and not
	 * appended by `stepsForKind`), all real steps are marked pending
	 * (auto-queued sentinels keep their gated state). The auto-queued
	 * sentinel is ALWAYS classified `auto-queued` regardless of job
	 * status — it represents a downstream consequence (a follow-up
	 * MCP-only UpdateJob) rather than progress within this job.
	 */
	export type StepState = 'completed' | 'active' | 'pending' | 'failed' | 'auto-queued';
	export function classifySteps(
		steps: string[],
		activeStep: string | undefined,
		jobStatus: string,
	): StepState[] {
		const status = (jobStatus || '').toLowerCase();
		if (!activeStep) {
			// No active step → mark all real steps pending (or completed if
			// status is 'ok'); gated steps stay auto-queued.
			return steps.map((s) =>
				isAutoQueuedStep(s) ? 'auto-queued' : status === 'ok' ? 'completed' : 'pending',
			);
		}
		const normalized = activeStep.toLowerCase();
		// findIndex against real steps only — auto-queued sentinels are not
		// candidates for the active position.
		const activeIndex = steps.findIndex(
			(s) => !isAutoQueuedStep(s) && s.toLowerCase() === normalized,
		);
		if (activeIndex < 0) {
			return steps.map((s) => (isAutoQueuedStep(s) ? 'auto-queued' : 'pending'));
		}
		return steps.map((s, i) => {
			if (isAutoQueuedStep(s)) return 'auto-queued';
			if (i < activeIndex) return 'completed';
			if (i === activeIndex) {
				return status === 'error' || status === 'failed' ? 'failed' : 'active';
			}
			// If the whole job is done, every real step is completed.
			if (status === 'ok' || status === 'done') return 'completed';
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
		/**
		 * Real CodeBuild run URL surfaced by the backend. The earlier
		 * draft synthesised this from `run_id`; that was wrong because
		 * `run_id` is the build identifier, not a console URL, and the
		 * region is not exposed in the response. Only render the log
		 * link when the backend provides a real URL.
		 */
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
			{@const autoQueued = isAutoQueuedStep(step)}
			<li
				class="timeline__step timeline__step--{state}"
				aria-current={state === 'active' ? 'step' : undefined}
				data-state={state}
			>
				<span class="timeline__indicator" aria-hidden="true"></span>
				<div class="timeline__content">
					<div class="timeline__row">
						<span class="timeline__step-name">{stepLabel(step)}</span>
						{#if state === 'active'}
							<span class="timeline__pill timeline__pill--active">Active</span>
						{:else if state === 'failed'}
							<span class="timeline__pill timeline__pill--failed">Failed</span>
						{:else if state === 'completed'}
							<span class="timeline__pill timeline__pill--completed">Done</span>
						{:else if state === 'auto-queued'}
							<span class="timeline__pill timeline__pill--auto-queued">Auto-queued</span>
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
					{#if autoQueued}
						<!--
							Explain the gated step's downstream contract so the
							operator knows what to look for once this job lands. The
							copy intentionally references the fleet wire-all CTA + the
							per-instance manual remediation path; both are operator-
							owned next steps.
						-->
						<Text size="sm" color="secondary">
							Body updates leave MCP wire-stale until a follow-up MCP-only UpdateJob
							re-wires it. Use the "Wire all" CTA on /operator/provisioning or
							/operator/releases, or open the per-instance detail page for manual
							control.
						</Text>
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
		background: #22c55e;
		border-color: #22c55e;
	}

	.timeline__step--active .timeline__indicator {
		background: #f59e0b;
		border-color: #f59e0b;
		box-shadow: 0 0 0 4px rgba(245, 158, 11, 0.28);
	}

	.timeline__step--failed .timeline__indicator {
		background: #ef4444;
		border-color: #ef4444;
	}

	/*
	 * M2.13 auto-queued gated step visuals — distinct from active /
	 * completed / failed so the operator reads it as a downstream
	 * consequence rather than a real step in the current job. Dashed
	 * border + transparent fill cue "queued elsewhere, not happening
	 * here."
	 */
	.timeline__step--auto-queued .timeline__indicator {
		background: transparent;
		border-color: #6b7280;
		border-style: dashed;
	}

	.timeline__step--auto-queued .timeline__step-name {
		color: var(--ds-fg-2, inherit);
		opacity: 0.85;
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
		color: #f59e0b;
		background: rgba(245, 158, 11, 0.14);
	}

	.timeline__pill--completed {
		color: #22c55e;
		background: rgba(34, 197, 94, 0.14);
	}

	.timeline__pill--failed {
		color: #ef4444;
		background: rgba(239, 68, 68, 0.14);
	}

	.timeline__pill--auto-queued {
		color: #6b7280;
		background: rgba(107, 114, 128, 0.16);
		border-style: dashed;
	}

	.timeline__log {
		font-size: 0.92rem;
		color: #f59e0b;
		text-decoration: none;
		border-bottom: 1px solid transparent;
		align-self: flex-start;
	}
	.timeline__log:hover {
		border-bottom-color: currentColor;
	}
</style>
