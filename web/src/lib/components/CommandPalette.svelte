<!--
@component
Host's CommandPalette — thin re-export wrapper around the greater-components
shell CommandPalette (vendored under `src/lib/greater/shell` at
greater-v0.10.0-rc.1, re-exported through `src/lib/shell`).

This file gives consumers a stable host import path that matches host's
existing `src/lib/components/` convention (PortalLayout.svelte,
OperatorLayout.svelte, etc.) without introducing host-bespoke
implementation. All props, snippets, and types pass through unchanged;
behavior (⌘K open, fuzzy search via greater's fuzzy-filter, scoped
result groups, keyboard navigation, focus-trap on open, ESC dismiss)
is whatever the upstream component implements.

Replacing this wrapper with a direct import from `src/lib/shell` is
a future scope item if the indirection ever stops earning its keep —
today it earns it by giving host's components/ directory a uniform
import surface regardless of vendored vs host-bespoke origin.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.7
Issue: equaltoai/lesser-host#387
-->

<script lang="ts">
	import type { ComponentProps } from 'svelte';
	import { CommandPalette } from 'src/lib/shell';

	type Props = ComponentProps<typeof CommandPalette>;
	let props: Props = $props();
</script>

<CommandPalette {...props} />
