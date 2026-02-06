import type { InstanceResponse, UpdateInstanceConfigRequest } from 'src/lib/api/portalInstances';

export type RenderPolicy = 'always' | 'suspicious';
export type OveragePolicy = 'block' | 'allow';
export type ModerationTrigger = 'on_reports' | 'always' | 'links_media_only' | 'virality';
export type AIBatchingMode = 'none' | 'in_request' | 'worker' | 'hybrid';

export interface InstanceConfigForm {
	hosted_previews_enabled: boolean;
	link_safety_enabled: boolean;
	renders_enabled: boolean;
	render_policy: RenderPolicy;
	overage_policy: OveragePolicy;
	moderation_enabled: boolean;
	moderation_trigger: ModerationTrigger;
	moderation_virality_min: string;
	ai_enabled: boolean;
	ai_model_set: string;
	ai_batching_mode: AIBatchingMode;
	ai_batch_max_items: string;
	ai_batch_max_total_bytes: string;
	ai_pricing_multiplier_bps: string;
	ai_max_inflight_jobs: string;
}

export function configFormFromInstance(instance: InstanceResponse): InstanceConfigForm {
	return {
		hosted_previews_enabled: Boolean(instance.hosted_previews_enabled),
		link_safety_enabled: Boolean(instance.link_safety_enabled),
		renders_enabled: Boolean(instance.renders_enabled),
		render_policy: (instance.render_policy as RenderPolicy) || 'suspicious',
		overage_policy: (instance.overage_policy as OveragePolicy) || 'block',
		moderation_enabled: Boolean(instance.moderation_enabled),
		moderation_trigger: (instance.moderation_trigger as ModerationTrigger) || 'on_reports',
		moderation_virality_min: String(instance.moderation_virality_min ?? 0),
		ai_enabled: Boolean(instance.ai_enabled),
		ai_model_set: String(instance.ai_model_set ?? ''),
		ai_batching_mode: (instance.ai_batching_mode as AIBatchingMode) || 'none',
		ai_batch_max_items: String(instance.ai_batch_max_items ?? 1),
		ai_batch_max_total_bytes: String(instance.ai_batch_max_total_bytes ?? 1),
		ai_pricing_multiplier_bps: String(instance.ai_pricing_multiplier_bps ?? 10_000),
		ai_max_inflight_jobs: String(instance.ai_max_inflight_jobs ?? 1),
	};
}

export function validateInstanceConfigForm(form: InstanceConfigForm): Record<string, string> {
	const errors: Record<string, string> = {};

	if (form.render_policy !== 'always' && form.render_policy !== 'suspicious') {
		errors.render_policy = 'Render policy must be always or suspicious.';
	}
	if (form.overage_policy !== 'block' && form.overage_policy !== 'allow') {
		errors.overage_policy = 'Overage policy must be block or allow.';
	}

	if (!['on_reports', 'always', 'links_media_only', 'virality'].includes(form.moderation_trigger)) {
		errors.moderation_trigger = 'Moderation trigger must be on_reports, always, links_media_only, or virality.';
	}
	const viralityMin = parseIntStrict(form.moderation_virality_min);
	if (!Number.isFinite(viralityMin) || viralityMin < 0) {
		errors.moderation_virality_min = 'Virality minimum must be an integer >= 0.';
	}

	if (form.ai_model_set.trim() === '') {
		errors.ai_model_set = 'AI model set cannot be empty.';
	}
	if (!['none', 'in_request', 'worker', 'hybrid'].includes(form.ai_batching_mode)) {
		errors.ai_batching_mode = 'Batching mode must be none, in_request, worker, or hybrid.';
	}

	const batchMaxItems = parsePositiveIntStrict(form.ai_batch_max_items);
	if (!Number.isFinite(batchMaxItems)) {
		errors.ai_batch_max_items = 'Batch max items must be an integer > 0.';
	}
	const batchMaxBytes = parsePositiveIntStrict(form.ai_batch_max_total_bytes);
	if (!Number.isFinite(batchMaxBytes)) {
		errors.ai_batch_max_total_bytes = 'Batch max total bytes must be an integer > 0.';
	}
	const pricingBps = parsePositiveIntStrict(form.ai_pricing_multiplier_bps);
	if (!Number.isFinite(pricingBps) || pricingBps > 1_000_000) {
		errors.ai_pricing_multiplier_bps = 'Pricing multiplier (bps) must be an integer > 0 and <= 1000000.';
	}
	const maxInflight = parsePositiveIntStrict(form.ai_max_inflight_jobs);
	if (!Number.isFinite(maxInflight) || maxInflight > 10_000) {
		errors.ai_max_inflight_jobs = 'Max inflight jobs must be an integer > 0 and <= 10000.';
	}

	return errors;
}

export function buildUpdateInstanceConfigRequest(form: InstanceConfigForm): UpdateInstanceConfigRequest {
	return {
		hosted_previews_enabled: form.hosted_previews_enabled,
		link_safety_enabled: form.link_safety_enabled,
		renders_enabled: form.renders_enabled,
		render_policy: form.render_policy,
		overage_policy: form.overage_policy,
		moderation_enabled: form.moderation_enabled,
		moderation_trigger: form.moderation_trigger,
		moderation_virality_min: parseIntStrict(form.moderation_virality_min),
		ai_enabled: form.ai_enabled,
		ai_model_set: form.ai_model_set.trim(),
		ai_batching_mode: form.ai_batching_mode,
		ai_batch_max_items: parseIntStrict(form.ai_batch_max_items),
		ai_batch_max_total_bytes: parseIntStrict(form.ai_batch_max_total_bytes),
		ai_pricing_multiplier_bps: parseIntStrict(form.ai_pricing_multiplier_bps),
		ai_max_inflight_jobs: parseIntStrict(form.ai_max_inflight_jobs),
	};
}

function parseIntStrict(raw: string): number {
	const trimmed = raw.trim();
	if (!/^-?[0-9]+$/.test(trimmed)) return Number.NaN;
	const parsed = Number.parseInt(trimmed, 10);
	return Number.isFinite(parsed) ? parsed : Number.NaN;
}

function parsePositiveIntStrict(raw: string): number {
	const parsed = parseIntStrict(raw);
	if (!Number.isFinite(parsed) || parsed <= 0) return Number.NaN;
	return parsed;
}

