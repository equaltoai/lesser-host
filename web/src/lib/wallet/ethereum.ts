export interface Eip1193Provider {
	request(args: { method: string; params?: unknown[] | Record<string, unknown> }): Promise<unknown>;
	on?: (event: string, handler: (...args: unknown[]) => void) => void;
	removeListener?: (event: string, handler: (...args: unknown[]) => void) => void;
}

declare global {
	interface Window {
		ethereum?: Eip1193Provider;
	}
}

export function getEthereumProvider(): Eip1193Provider | null {
	return window.ethereum ?? null;
}

export async function getAccounts(provider: Eip1193Provider): Promise<string[]> {
	const accounts = (await provider.request({ method: 'eth_accounts' })) as unknown;
	if (!Array.isArray(accounts)) {
		throw new Error('wallet returned invalid accounts response');
	}
	return accounts.map((a) => String(a));
}

export async function requestAccounts(provider: Eip1193Provider): Promise<string[]> {
	const accounts = (await provider.request({ method: 'eth_requestAccounts' })) as unknown;
	if (!Array.isArray(accounts)) {
		throw new Error('wallet returned invalid accounts response');
	}
	return accounts.map((a) => String(a));
}

export async function ensureAccounts(provider: Eip1193Provider): Promise<string[]> {
	const existing = await getAccounts(provider);
	if (existing.length > 0) return existing;
	return requestAccounts(provider);
}

export async function getChainId(provider: Eip1193Provider): Promise<number> {
	const chainIdHex = (await provider.request({ method: 'eth_chainId' })) as unknown;
	const hex = typeof chainIdHex === 'string' ? chainIdHex : '';
	const parsed = parseInt(hex, 16);
	return Number.isFinite(parsed) && parsed > 0 ? parsed : 1;
}

export async function switchEthereumChain(provider: Eip1193Provider, chainId: number): Promise<void> {
	const chainIdHex = `0x${chainId.toString(16)}`;
	await provider.request({
		method: 'wallet_switchEthereumChain',
		params: [{ chainId: chainIdHex }],
	});
}

function normalizeTransactionValue(value?: string): string {
	const trimmed = String(value || '').trim();
	if (!trimmed) return '0x0';
	if (trimmed.startsWith('0x') || trimmed.startsWith('0X')) {
		const parsed = BigInt(trimmed);
		return `0x${parsed.toString(16)}`;
	}
	const parsed = BigInt(trimmed);
	return `0x${parsed.toString(16)}`;
}

export async function sendEthereumTransaction(
	provider: Eip1193Provider,
	input: { from: string; to: string; value?: string; data?: string },
): Promise<string> {
	const txHash = (await provider.request({
		method: 'eth_sendTransaction',
		params: [
			{
				from: input.from,
				to: input.to,
				value: normalizeTransactionValue(input.value),
				data: input.data || '0x',
			},
		],
	})) as unknown;
	if (typeof txHash !== 'string' || !txHash.trim()) {
		throw new Error('wallet returned invalid transaction hash');
	}
	return txHash;
}

export async function getEthereumTransactionReceipt(
	provider: Eip1193Provider,
	txHash: string,
): Promise<Record<string, unknown> | null> {
	const receipt = (await provider.request({
		method: 'eth_getTransactionReceipt',
		params: [txHash],
	})) as unknown;
	if (receipt == null) return null;
	if (typeof receipt !== 'object' || Array.isArray(receipt)) {
		throw new Error('wallet returned invalid transaction receipt');
	}
	return receipt as Record<string, unknown>;
}

export async function waitForEthereumTransactionReceipt(
	provider: Eip1193Provider,
	txHash: string,
	timeoutMs: number = 5 * 60 * 1000,
	pollMs: number = 3 * 1000,
): Promise<Record<string, unknown>> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const receipt = await getEthereumTransactionReceipt(provider, txHash);
		if (receipt) return receipt;
		if (Date.now() >= deadline) {
			throw new Error('transaction was not mined before timeout');
		}
		await new Promise((resolve) => window.setTimeout(resolve, pollMs));
	}
}

export async function personalSign(
	provider: Eip1193Provider,
	message: string,
	address: string,
): Promise<string> {
	const paramsA = [message, address];
	try {
		return (await provider.request({ method: 'personal_sign', params: paramsA })) as string;
	} catch {
		// Some wallets use the reversed param order.
		const paramsB = [address, message];
		return (await provider.request({ method: 'personal_sign', params: paramsB })) as string;
	}
}

export async function signTypedDataV4(
	provider: Eip1193Provider,
	address: string,
	typedData: unknown,
): Promise<string> {
	const payload = typeof typedData === 'string' ? typedData : JSON.stringify(typedData);
	const params = [address, payload];
	try {
		return (await provider.request({ method: 'eth_signTypedData_v4', params })) as string;
	} catch {
		// Wallets may only support older variants.
		try {
			return (await provider.request({ method: 'eth_signTypedData_v3', params })) as string;
		} catch {
			return (await provider.request({ method: 'eth_signTypedData', params })) as string;
		}
	}
}
