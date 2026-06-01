import { Construct } from 'constructs';

export const LAB_SOUL_EMAIL_INBOUND_DOMAIN = 'lab.lessersoul.ai';
export const LIVE_SOUL_EMAIL_INBOUND_DOMAIN = 'inbound.lessersoul.ai';

export function defaultSoulEmailInboundDomainForStage(stage: string): string {
	return stage.trim().toLowerCase() === 'live'
		? LIVE_SOUL_EMAIL_INBOUND_DOMAIN
		: LAB_SOUL_EMAIL_INBOUND_DOMAIN;
}

export function soulEmailInboundDomainFromContext(scope: Construct, stage: string): string {
	const normalizedStage = stage.trim().toLowerCase();
	const stageSuffix = normalizedStage === 'live' ? 'Live' : 'Lab';
	const stageSpecific = scope.node.tryGetContext(`soulEmailInboundDomain${stageSuffix}`);
	const generic = scope.node.tryGetContext('soulEmailInboundDomain');
	const configured = stringContext(stageSpecific) || stringContext(generic);
	return configured || defaultSoulEmailInboundDomainForStage(normalizedStage);
}

function stringContext(value: unknown): string {
	if (typeof value !== 'string') return '';
	return value.trim();
}
