<!--
@component
Host's ActivitySparkline — thin re-export wrapper around the
greater-components host-platform ActivitySparkline (vendored under
`src/lib/greater/host-platform` at greater-v0.10.0-rc.1, vendored in M0.8
alongside FleetCard + CostGauge).

Gives consumers a stable host import path under `src/lib/components/`
matching the M0.7/M0.8/M0.9 convention. All props, snippets, and types
pass through unchanged; the upstream component implements every M0.10
acceptance behavior:

  - Inline sparkline takes numeric series + optional baseline
  - SVG-rendered for crisp scaling at any size
  - Strict-no-inline-CSP safe (.svelte + sibling .css authoring; SVG
    elements emitted as DOM nodes, not via innerHTML)
  - Consumes `--gr-*` token surface bridged to host's `--ds-*` palette

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.10
Issue: equaltoai/lesser-host#390
-->

<script lang="ts">
	import type { ComponentProps } from 'svelte';
	import { ActivitySparkline } from 'src/lib/greater/host-platform';

	type Props = ComponentProps<typeof ActivitySparkline>;
	let props: Props = $props();
</script>

<ActivitySparkline {...props} />
