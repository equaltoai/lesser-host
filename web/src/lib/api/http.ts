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
