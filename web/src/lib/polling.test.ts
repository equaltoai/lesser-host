import { describe, expect, it, vi } from 'vitest';

import { pollUntil } from './polling';

describe('pollUntil', () => {
	it('polls with exponential backoff until done', async () => {
		vi.useFakeTimers();

		const results = [{ done: false }, { done: false }, { done: true }];
		const fn = vi.fn(async () => {
			const next = results.shift();
			if (!next) throw new Error('unexpected call');
			return next;
		});

		const promise = pollUntil(fn, (value) => value.done, {
			backoff: { initialDelayMs: 1000, maxDelayMs: 10_000, factor: 2 },
		});

		expect(fn).toHaveBeenCalledTimes(1);

		await vi.advanceTimersByTimeAsync(1000);
		expect(fn).toHaveBeenCalledTimes(2);

		await vi.advanceTimersByTimeAsync(2000);
		await expect(promise).resolves.toEqual({ done: true });
		expect(fn).toHaveBeenCalledTimes(3);

		vi.useRealTimers();
	});

	it('throws AbortError when aborted', async () => {
		vi.useFakeTimers();

		const controller = new AbortController();
		const fn = vi.fn(async () => ({ done: false }));

		const promise = pollUntil(fn, (value) => value.done, { signal: controller.signal });
		expect(fn).toHaveBeenCalledTimes(1);

		controller.abort();
		await expect(promise).rejects.toMatchObject({ name: 'AbortError' });

		vi.useRealTimers();
	});
});
