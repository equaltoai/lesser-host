<script lang="ts">
	let {
		edition,
		size = 36,
	}: {
		edition: string;
		size?: number;
	} = $props();
</script>

<div class="brand-lockup" aria-label={`lesser.host — ${edition}`}>
	<div
		class="brand-lockup__mark"
		style={`--brand-lockup-mark-size: ${size}px;`}
		aria-hidden="true"
	>
		&lt;
	</div>
	<div class="brand-lockup__wordmark">
		<span class="brand-lockup__main">&lt;esser.host</span>
		<span class="brand-lockup__edition">{edition}</span>
	</div>
</div>

<style>
	.brand-lockup {
		display: flex;
		align-items: center;
		gap: 0.65rem;
		min-width: 0;
	}

	.brand-lockup__mark {
		width: var(--brand-lockup-mark-size, 36px);
		height: var(--brand-lockup-mark-size, 36px);
		border-radius: 9px;
		background: var(--ds-lesser-600, #9333ea);
		display: grid;
		place-items: center;
		color: #fff;
		font-family: var(--ds-font-heading, Georgia, serif);
		font-size: calc(var(--brand-lockup-mark-size, 36px) * 0.6);
		font-weight: 700;
		line-height: 1;
		box-shadow: 0 4px 12px rgba(147, 51, 234, 0.35);
		flex: 0 0 auto;
	}

	.brand-lockup__wordmark {
		display: flex;
		flex-direction: column;
		line-height: 1.1;
		min-width: 0;
	}

	.brand-lockup__main {
		font-family: var(--ds-font-heading, Georgia, serif);
		font-size: 1.18rem;
		font-weight: 700;
		color: var(--ds-fg-1, #332116);
		letter-spacing: -0.01em;
		white-space: nowrap;
	}

	.brand-lockup__edition {
		font-size: 0.66rem;
		font-weight: 700;
		letter-spacing: 0.14em;
		text-transform: uppercase;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
		margin-top: 2px;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
</style>
