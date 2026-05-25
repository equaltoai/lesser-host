/* ============================================================================
 * Host shell aggregator (web/src/lib/shell)
 *
 * Single import surface exposing the greater-components shell primitives the
 * Project 39 web UI rework adopts. Eleven shell primitives come from the
 * vendored `@equaltoai/greater-components` shell package (pinned in
 * `web/components.json` to greater-v0.10.0-rc.1); `Section` already lives in
 * `@equaltoai/greater-components` primitives and is re-exported through the
 * same surface for consumer convenience.
 *
 * Consumers import every shell primitive from a single path:
 *
 *   import {
 *     Shell, Sidebar, Topbar, Panel, StatCard, SummaryStrip,
 *     Section, PageFrame, PageTitle, Breadcrumb, Callout,
 *     CommandPalette,
 *   } from 'src/lib/shell';
 *
 * Posture invariants preserved:
 *   - Strict-CSP-safe: components ship `.svelte` + sibling `.css`; no inline
 *     styles, no event handlers at module evaluation time, no browser globals
 *     at module evaluation time (per upstream component docs).
 *   - DS bridge: components consume the stable `--gr-*` token surface, which
 *     `web/src/lib/tokens/gr-bridge.css` (M0.2) maps to host's `--ds-*` Agent
 *     Genesis palette. No per-component re-skin needed.
 *   - Greater steward authority: this is a re-export layer, not a
 *     re-implementation. Future shell-primitive additions land here by
 *     extending the re-export list, not by rewriting component code.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.6
 * Issue: equaltoai/lesser-host#386
 * ========================================================================== */

export {
	Shell,
	Sidebar,
	Topbar,
	Panel,
	StatCard,
	SummaryStrip,
	PageFrame,
	PageTitle,
	Breadcrumb,
	Callout,
	CommandPalette,
	scoreCommandPaletteItem,
	filterAndRankCommandPaletteItems,
	tokenizeCommandPaletteQuery,
} from 'src/lib/greater/shell';

export type {
	ShellSidebarPlacement,
	SidebarWidth,
	SidebarVariant,
	TopbarVariant,
	PanelVariant,
	PanelPadding,
	StatCardStatus,
	StatCardTrendDirection,
	StatCardTrend,
	SummaryStripColumns,
	SummaryStripGap,
	PageFrameWidth,
	PageFrameAsidePlacement,
	PageTitleLevel,
	BreadcrumbItem,
	CalloutTone,
	CalloutRole,
	CommandPaletteItem,
	CommandPaletteGroup,
	CommandPaletteFilter,
} from 'src/lib/greater/shell';

export { Section, type SectionProps } from '@equaltoai/greater-components-primitives';
