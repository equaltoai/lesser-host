export interface PollBackoffOptions {
	initialDelayMs?: number;
	maxDelayMs?: number;
	factor?: number;
}

function abortError(): Error {
	const err = new Error('aborted');
	err.name = 'AbortError';
	return err;
}

async function sleep(ms: number, signal?: AbortSignal): Promise<void> {
	if (signal?.aborted) {
		throw abortError();
	}

	await new Promise<void>((resolve, reject) => {
		const timer = window.setTimeout(resolve, ms);
		if (!signal) return;

		signal.addEventListener(
			'abort',
			() => {
				window.clearTimeout(timer);
				reject(abortError());
			},
			{ once: true },
		);
	});
}

export async function pollUntil<T>(
	fn: () => Promise<T>,
	isDone: (value: T) => boolean,
	opts?: {
		signal?: AbortSignal;
		backoff?: PollBackoffOptions;
		onUpdate?: (value: T) => void;
	},
): Promise<T> {
	const backoff = opts?.backoff ?? {};
	const initialDelayMs = backoff.initialDelayMs ?? 1000;
	const maxDelayMs = backoff.maxDelayMs ?? 15_000;
	const factor = backoff.factor ?? 1.6;

	let delayMs = initialDelayMs;

	for (;;) {
		if (opts?.signal?.aborted) {
			throw abortError();
		}

		const value = await fn();
		opts?.onUpdate?.(value);
		if (isDone(value)) {
			return value;
		}

		await sleep(delayMs, opts?.signal);
		delayMs = Math.min(maxDelayMs, Math.max(initialDelayMs, Math.floor(delayMs * factor)));
	}
}

