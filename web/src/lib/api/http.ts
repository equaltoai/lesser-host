export class ApiError extends Error {
	readonly status: number;
	readonly code?: string;

	constructor(message: string, status: number, code?: string) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
		this.code = code;
	}
}

type JsonObject = Record<string, unknown>;

function isJsonObject(value: unknown): value is JsonObject {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export async function fetchJson<T>(input: RequestInfo | URL, init?: RequestInit): Promise<T> {
	const res = await fetch(input, init);

	const contentType = res.headers.get('content-type') || '';
	const isJson = contentType.includes('application/json');

	if (res.ok) {
		if (!isJson) {
			throw new ApiError(`expected JSON response from ${res.url}`, res.status);
		}
		return (await res.json()) as T;
	}

	let message = `request failed (${res.status})`;
	let code: string | undefined;

	if (isJson) {
		const body = (await res.json().catch(() => null)) as unknown;
		if (isJsonObject(body)) {
			const bodyMessage = typeof body.message === 'string' ? body.message : undefined;
			const bodyCode = typeof body.code === 'string' ? body.code : undefined;
			if (bodyMessage) {
				message = bodyMessage;
			}
			code = bodyCode;
		}
	}

	throw new ApiError(message, res.status, code);
}

export function jsonRequest<T>(body: T): RequestInit {
	return {
		headers: {
			'content-type': 'application/json',
		},
		body: JSON.stringify(body),
	};
}

/**
 * Returns the URL if it uses the https: protocol, otherwise returns undefined.
 * Prevents javascript:, data:, and other dangerous URI schemes from being used in href attributes.
 */
export function safeHref(url: string | undefined | null): string | undefined {
	if (!url) return undefined;
	try {
		const parsed = new URL(url);
		if (parsed.protocol === 'https:' || parsed.protocol === 'http:') return url;
		return undefined;
	} catch {
		return undefined;
	}
}

const ALLOWED_CHECKOUT_HOSTS = ['checkout.stripe.com'];

/**
 * Validates that a URL is safe for navigation (e.g., checkout redirects).
 * Only allows HTTPS URLs on explicitly allowed hosts, or same-origin URLs.
 */
export function isSafeRedirectUrl(url: string): boolean {
	try {
		const parsed = new URL(url, window.location.origin);
		if (parsed.protocol !== 'https:') return false;
		if (parsed.origin === window.location.origin) return true;
		return ALLOWED_CHECKOUT_HOSTS.includes(parsed.hostname);
	} catch {
		return false;
	}
}
