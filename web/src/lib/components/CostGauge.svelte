<!--
@component
Host's CostGauge — thin re-export wrapper around the greater-components
host-platform CostGauge (vendored under `src/lib/greater/host-platform` at
greater-v0.10.0-rc.1, vendored in M0.8 alongside FleetCard).

Gives consumers a stable host import path under `src/lib/components/`
matching the M0.7/M0.8 convention. All props, snippets, and types pass
through unchanged; the upstream component implements every M0.9
acceptance behavior:

  - Radial gauge with current/limit/currency props
  - Colored arc indicates consumption
  - ARIA labels for screen readers (Arch PR #670 a11y review gated the
    ARIA meter contract enforcement + accessible name composition via
    `labelledby`)
  - Strict-no-inline-CSP safe (.svelte + sibling .css authoring)
  - Consumes `--gr-*` token surface bridged to host's `--ds-*` palette

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.9
Issue: equaltoai/lesser-host#389
-->

<script lang="ts">
	import type { ComponentProps } from 'svelte';
	import { CostGauge } from 'src/lib/greater/host-platform';

	type Props = ComponentProps<typeof CostGauge>;
	let props: Props = $props();
</script>

<CostGauge {...props} />
