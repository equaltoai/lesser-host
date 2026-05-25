<!--
@component
Host's FleetCard — thin re-export wrapper around the greater-components
host-platform FleetCard (vendored under `src/lib/greater/host-platform` at
greater-v0.10.0-rc.1).

Gives consumers a stable host import path under `src/lib/components/`
alongside CommandPalette.svelte, PortalLayout.svelte, OperatorLayout.svelte.
All props, snippets, and types pass through unchanged; the upstream
component implements every M0.8 acceptance behavior:

  - Card renders with slug, status pulse dot, cost gauge slot,
    sparkline slot, metadata line
  - Strict-no-inline-CSP safe (.svelte + sibling .css authoring)
  - Consumes `--gr-*` token surface bridged to host's `--ds-*` palette

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.8
Issue: equaltoai/lesser-host#388
-->

<script lang="ts">
	import type { ComponentProps } from 'svelte';
	import { FleetCard } from 'src/lib/greater/host-platform';

	type Props = ComponentProps<typeof FleetCard>;
	let props: Props = $props();
</script>

<FleetCard {...props} />
