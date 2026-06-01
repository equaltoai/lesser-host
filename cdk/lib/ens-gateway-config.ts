import { Construct } from 'constructs';

export const LAB_ENS_GATEWAY_CHAIN_ID = '11155111';
export const LAB_ENS_GATEWAY_CHAIN_NAME = 'sepolia';
export const LIVE_ENS_GATEWAY_CHAIN_ID = '1';
export const LIVE_ENS_GATEWAY_CHAIN_NAME = 'mainnet';
export const DEFAULT_ENS_GATEWAY_ROOT_NAME = 'lessersoul.eth';

export interface EnsGatewayChainConfig {
	readonly chainId: string;
	readonly chainName: string;
}

function contextString(scope: Construct, key: string): string | undefined {
	const value = scope.node.tryGetContext(key);
	if (value === undefined || value === null) {
		return undefined;
	}
	return String(value);
}

function stageSuffix(stage: string): 'Lab' | 'Live' {
	return stage.trim() === 'live' ? 'Live' : 'Lab';
}

export function ensGatewayChainConfigForStage(stage: string): EnsGatewayChainConfig {
	return stageSuffix(stage) === 'Live'
		? { chainId: LIVE_ENS_GATEWAY_CHAIN_ID, chainName: LIVE_ENS_GATEWAY_CHAIN_NAME }
		: { chainId: LAB_ENS_GATEWAY_CHAIN_ID, chainName: LAB_ENS_GATEWAY_CHAIN_NAME };
}

export function ensGatewayContextForStage(
	scope: Construct,
	stage: string,
	key: string,
	fallback = '',
): string {
	const suffix = stageSuffix(stage);
	return contextString(scope, `${key}${suffix}`) ?? contextString(scope, key) ?? fallback;
}

export function ensGatewayResolverAddressFromContext(scope: Construct, stage: string): string {
	const suffix = stageSuffix(stage);
	const stageSpecific = contextString(scope, `ensGatewayResolverAddress${suffix}`);
	if (stageSpecific !== undefined) {
		return stageSpecific;
	}

	// Migration aid for existing lab deployments only. Live must be configured
	// with ensGatewayResolverAddressLive so lab and live cannot silently share
	// one legacy resolver sender.
	if (suffix === 'Lab') {
		return contextString(scope, 'ensGatewayResolverAddress') ?? '';
	}
	return '';
}

export function ensGatewayRootNameFromContext(scope: Construct): string {
	const value = (contextString(scope, 'ensGatewayRootName') ?? DEFAULT_ENS_GATEWAY_ROOT_NAME).trim().toLowerCase();
	return value || DEFAULT_ENS_GATEWAY_ROOT_NAME;
}
