import SafeAppsSDK from '@safe-global/safe-apps-sdk';
import type { BaseTransaction, GatewayTransactionDetails, SafeInfo } from '@safe-global/safe-apps-sdk';

const SAFE_APP_TX_KEY_PREFIX = 'lesser-host:safe-app:tx:v1:';
const SAFE_WALLET_APPS_OPEN_URL = 'https://app.safe.global/apps/open';

export interface SafeAppContext {
	sdk: SafeAppsSDK;
	info: SafeInfo;
}

export interface SafeAppExecutionStatus {
	txStatus?: string;
	txHash?: string;
	executedAt?: number;
	confirmationsRequired?: number;
	confirmationsSubmitted?: number;
}

function safeChainSlugForChainId(chainId: number): string | null {
	switch (chainId) {
		case 1:
			return 'eth';
		case 11155111:
			return 'sep';
		default:
			return null;
	}
}

export function buildSafeWalletAppUrl(input: {
	appUrl: string;
	safeAddress: string;
	chainId: number;
}): string | null {
	const safeChainSlug = safeChainSlugForChainId(input.chainId);
	if (!safeChainSlug || !input.appUrl || !input.safeAddress) {
		return null;
	}
	const url = new URL(SAFE_WALLET_APPS_OPEN_URL);
	url.searchParams.set('safe', `${safeChainSlug}:${input.safeAddress}`);
	url.searchParams.set('appUrl', input.appUrl);
	return url.toString();
}

function withTimeout<T>(promise: Promise<T>, timeoutMs: number): Promise<T> {
	return new Promise<T>((resolve, reject) => {
		const timer = window.setTimeout(() => reject(new Error('safe app detection timed out')), timeoutMs);
		void promise.then(
			(value) => {
				window.clearTimeout(timer);
				resolve(value);
			},
			(error) => {
				window.clearTimeout(timer);
				reject(error);
			},
		);
	});
}

export async function detectSafeAppContext(timeoutMs: number = 2000): Promise<SafeAppContext | null> {
	if (typeof window === 'undefined' || window.self === window.top) {
		return null;
	}

	const sdk = new SafeAppsSDK();
	try {
		const info = await withTimeout(sdk.safe.getInfo(), timeoutMs);
		return { sdk, info };
	} catch {
		return null;
	}
}

export async function submitSafeAppTransaction(ctx: SafeAppContext, tx: BaseTransaction): Promise<string> {
	const res = await ctx.sdk.txs.send({ txs: [tx] });
	return res.safeTxHash;
}

export async function getSafeAppTransaction(ctx: SafeAppContext, safeTxHash: string): Promise<GatewayTransactionDetails> {
	return ctx.sdk.txs.getBySafeTxHash(safeTxHash);
}

export function summarizeSafeAppExecution(details: GatewayTransactionDetails | null | undefined): SafeAppExecutionStatus {
	if (!details) return {};
	const summary: SafeAppExecutionStatus = {
		txStatus: typeof details.txStatus === 'string' ? details.txStatus : undefined,
		txHash: typeof details.txHash === 'string' && details.txHash ? details.txHash : undefined,
		executedAt: typeof details.executedAt === 'number' ? details.executedAt : undefined,
	};

	const execInfo = details.detailedExecutionInfo as
		| { confirmationsRequired?: number; confirmationsSubmitted?: number }
		| undefined;
	if (execInfo) {
		if (typeof execInfo.confirmationsRequired === 'number') {
			summary.confirmationsRequired = execInfo.confirmationsRequired;
		}
		if (typeof execInfo.confirmationsSubmitted === 'number') {
			summary.confirmationsSubmitted = execInfo.confirmationsSubmitted;
		}
	}

	return summary;
}

function txStorageKey(operationId: string): string {
	return `${SAFE_APP_TX_KEY_PREFIX}${operationId}`;
}

export function loadPendingSafeAppTxHash(operationId: string): string {
	try {
		return localStorage.getItem(txStorageKey(operationId)) || '';
	} catch {
		return '';
	}
}

export function savePendingSafeAppTxHash(operationId: string, safeTxHash: string): void {
	try {
		localStorage.setItem(txStorageKey(operationId), safeTxHash);
	} catch {
		// ignore
	}
}

export function clearPendingSafeAppTxHash(operationId: string): void {
	try {
		localStorage.removeItem(txStorageKey(operationId));
	} catch {
		// ignore
	}
}
