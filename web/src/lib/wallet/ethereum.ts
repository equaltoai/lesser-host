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

export async function requestAccounts(provider: Eip1193Provider): Promise<string[]> {
	const accounts = (await provider.request({ method: 'eth_requestAccounts' })) as unknown;
	if (!Array.isArray(accounts)) {
		throw new Error('wallet returned invalid accounts response');
	}
	return accounts.map((a) => String(a));
}

export async function getChainId(provider: Eip1193Provider): Promise<number> {
	const chainIdHex = (await provider.request({ method: 'eth_chainId' })) as unknown;
	const hex = typeof chainIdHex === 'string' ? chainIdHex : '';
	const parsed = parseInt(hex, 16);
	return Number.isFinite(parsed) && parsed > 0 ? parsed : 1;
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
