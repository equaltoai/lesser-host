import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, fetchJson, jsonRequest } from './http';

const originalFetch = globalThis.fetch;

afterEach(() => {
	globalThis.fetch = originalFetch;
});

describe('fetchJson', () => {
	it('returns JSON for ok responses', async () => {
		globalThis.fetch = vi.fn(async () => {
			return new Response(JSON.stringify({ ok: true }), {
				status: 200,
				headers: { 'content-type': 'application/json; charset=utf-8' },
			});
		}) as unknown as typeof fetch;

		await expect(fetchJson<{ ok: boolean }>('/test')).resolves.toEqual({ ok: true });
	});

	it('throws when ok response is not JSON', async () => {
		globalThis.fetch = vi.fn(async () => {
			return new Response('ok', { status: 200, headers: { 'content-type': 'text/plain' } });
		}) as unknown as typeof fetch;

		await expect(fetchJson('/test')).rejects.toBeInstanceOf(ApiError);
	});

	it('parses JSON error body message + code', async () => {
		globalThis.fetch = vi.fn(async () => {
			return new Response(JSON.stringify({ message: 'bad request', code: 'app.bad_request' }), {
				status: 400,
				headers: { 'content-type': 'application/json' },
			});
		}) as unknown as typeof fetch;

		await expect(fetchJson('/test')).rejects.toMatchObject({
			name: 'ApiError',
			message: 'bad request',
			status: 400,
			code: 'app.bad_request',
		});
	});
});

describe('jsonRequest', () => {
	it('produces application/json body', () => {
		const req = jsonRequest({ hello: 'world' });
		expect(req.headers).toEqual({ 'content-type': 'application/json' });
		expect(req.body).toBe(JSON.stringify({ hello: 'world' }));
	});
});

