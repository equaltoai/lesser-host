<!--
@component
JobKindBadge — provisioning-job kind badge for the Operator Console.

Project 39 M2.3 (issue #429). Renders a colored Badge for the four
provisioning-job kinds enumerated by the provisioning walk:

  provision      — first-time per-slug provisioning (orange)
  update-lesser  — lesser-only deploy (amber)
  update-body    — lesser-body-only deploy (violet)
  wire-mcp       — host-internal MCP wire alignment (info / blue)

The kind is UI-derived from the underlying record; this component is
display-only and does not fetch or mutate. See `deriveJobKind()` in
`src/lib/components/jobKind.ts` for the discriminator helper.

Strict-CSP-safe: ships as a thin wrapper around the greater-components
Badge primitive; no inline styles, no third-party origins.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.3
-->
<script lang="ts" module>
	export type JobKind = 'provision' | 'update-lesser' | 'update-body' | 'wire-mcp' | 'unknown';

	type Tone = {
		color: 'success' | 'warning' | 'error' | 'gray';
		variant: 'outlined' | 'filled';
		label: string;
	};

	export function toneForKind(kind: JobKind): Tone {
		switch (kind) {
			case 'provision':
				// First-time provisioning is the canonical orange-on-amber tone.
				return { color: 'warning', variant: 'filled', label: 'Provision' };
			case 'update-lesser':
				// Lesser update lands in warning (amber) outlined — the most common
				// post-provisioning update path.
				return { color: 'warning', variant: 'outlined', label: 'Update · lesser' };
			case 'update-body':
				// Body updates use the success palette to distinguish them visually
				// from lesser updates on dense lists.
				return { color: 'success', variant: 'outlined', label: 'Update · body' };
			case 'wire-mcp':
				// Wire-MCP is host-internal MCP re-alignment; gray-outlined to read
				// as a system-administered remediation rather than a customer-visible
				// deploy.
				return { color: 'gray', variant: 'filled', label: 'Wire · MCP' };
			default:
				return { color: 'gray', variant: 'outlined', label: 'Unknown' };
		}
	}
</script>

<script lang="ts">
	import { Badge } from 'src/lib/ui';

	let { kind, size = 'sm' }: { kind: JobKind; size?: 'sm' | 'md' } = $props();

	const tone = $derived(toneForKind(kind));
</script>

<Badge color={tone.color} variant={tone.variant} {size}>{tone.label}</Badge>
