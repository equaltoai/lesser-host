<!--
@component
M8InstanceCostFixture — isolated visual fixture for the M8 Instance Cost UI.
Renders InstanceCost with realistic mock cost/usage data to exercise all
UI states: header metric cards (MTD spend vs budget with CostGauge,
Compute GB-sec with sparkline, Egress GB), "Where the dollars go"
breakdown table with progress bars, and budget alarms panel (all disabled).

Not mounted by any customer portal route — exists solely for local
visual review and headless PNG capture at 1440×900.

@license AGPL-3.0-only
@public
-->
<script lang="ts">
	import InstanceCost from 'src/pages/portal/InstanceCost.svelte';

	const fixtureToken = 'fixture-token-m8';
	const fixtureSlug = 'simulacrum';
</script>

<div class="fixture-wrapper">
	<InstanceCost token={fixtureToken} slug={fixtureSlug} />
</div>

<style>
	.fixture-wrapper {
		max-width: 1152px;
		margin: 0 auto;
		padding: var(--ds-space-6, 1.5rem);
	}
</style>
