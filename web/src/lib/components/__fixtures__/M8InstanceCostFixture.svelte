<!--
@component
M8InstanceCostFixture — isolated visual fixture for the M8 Instance Cost UI.
Renders InstanceCost with deterministic mock cost/usage data to exercise
the fully-loaded M8 design at 1440×900:

- Header metric cards: MTD spend vs budget (CostGauge + ProgressBar at
  ~47% usage), Compute GB-sec (Metric + Sparkline showing 7-day series),
  Egress GB (Metric)
- "Where the dollars go" breakdown table with 5+ categorized services
  (Lambda, DynamoDB, Egress, CloudFront, S3, API Gateway) each with
  progress bars and percent-of-total
- Budget alarms panel with warning/critical/cap threshold rows (all
  switches disabled with honest tooltip)
- Monthly credit summary with request/cache stats

Mock data is provided via the Vite alias of src/lib/api/portalUsage →
fixtures/__mocks__/portalUsage.ts — no backend contact. The component
renders the loaded state deterministically from the mock; empty/error
states are exercised by source tests and template branches.

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
