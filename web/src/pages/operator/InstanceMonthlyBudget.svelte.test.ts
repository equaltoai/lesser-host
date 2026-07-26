import { mount, unmount } from 'svelte';
import { tick } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import InstanceMonthlyBudget from './InstanceMonthlyBudget.svelte';

const month = new Date().toISOString().slice(0, 7);
const token = 'operator-session-token';
const slug = 'trenchcoat';

const mounted: Array<{ instance: Record<string, never>; target: HTMLElement }> = [];

afterEach(() => {
	for (const { instance, target } of mounted.splice(0)) {
		unmount(instance);
		target.remove();
	}
	vi.restoreAllMocks();
	document.body.innerHTML = '';
});

describe('InstanceMonthlyBudget', () => {
	it('shows the current UTC month included, used, and remaining credits', async () => {
		installBudgetFetchMock();
		const target = mountBudget(false);

		await waitForText(target, 'Budget exhausted');

		expect(normalizedText(target)).toContain(month);
		expect(definitionValue(target, 'Included credits')).toBe('1000');
		expect(definitionValue(target, 'Used credits')).toBe('1000');
		expect(definitionValue(target, 'Remaining')).toBe('0');
		expect(buttonByText(target, 'Adjust included credits')).toBeNull();
		expect(normalizedText(target)).toContain('Admin role required to adjust included credits.');
	});

	it('requires an explicit confirmation before the admin budget PUT', async () => {
		const fetchMock = installBudgetFetchMock();
		const target = mountBudget(true);

		await waitForText(target, 'Budget exhausted');
		buttonByText(target, 'Adjust included credits')?.click();
		await flushAsync();

		await typeInto(inputByLabel(target, 'Included credits'), '2000');
		buttonByText(target, 'Review change')?.click();
		await flushAsync();

		expect(fetchMock).toHaveBeenCalledTimes(1);
		expect(normalizedText(target)).toContain(`Change ${month} included credits from 1000 to 2000`);
		expect(normalizedText(target)).toContain('Used credits remain unchanged.');

		buttonByText(target, 'Confirm budget change')?.click();
		await waitForText(target, 'Monthly budget updated.');

		expect(fetchMock).toHaveBeenCalledTimes(2);
		const [input, init] = fetchMock.mock.calls[1] ?? [];
		expect(input).toBe(`/api/v1/instances/${slug}/budgets/${month}`);
		expect(init?.method).toBe('PUT');
		expect(new Headers(init?.headers).get('authorization')).toBe(`Bearer ${token}`);
		expect(init?.body).toBe(JSON.stringify({ included_credits: 2000 }));
		expect(definitionValue(target, 'Included credits')).toBe('2000');
		expect(definitionValue(target, 'Used credits')).toBe('1000');
		expect(definitionValue(target, 'Remaining')).toBe('1000');
	});
});

function installBudgetFetchMock() {
	return vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
		const path = String(input);
		if (path === `/api/v1/portal/instances/${slug}/budgets/${month}` && !init?.method) {
			return Promise.resolve(
				jsonResponse({
					instance_slug: slug,
					month,
					included_credits: 1000,
					used_credits: 1000,
					remaining_credits: 0,
				}),
			);
		}
		if (path === `/api/v1/instances/${slug}/budgets/${month}` && init?.method === 'PUT') {
			return Promise.resolve(
				jsonResponse({
					instance_slug: slug,
					month,
					included_credits: 2000,
					used_credits: 1000,
					remaining_credits: 1000,
				}),
			);
		}
		return Promise.resolve(jsonResponse({ error: { message: `unexpected request: ${path}` } }, 500));
	});
}

function mountBudget(canEdit: boolean): HTMLElement {
	const target = document.createElement('div');
	document.body.appendChild(target);
	const instance = mount(InstanceMonthlyBudget, {
		target,
		props: { token, slug, canEdit },
	}) as unknown as Record<string, never>;
	mounted.push({ instance, target });
	return target;
}

function jsonResponse(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json' },
	});
}

async function flushAsync() {
	for (let i = 0; i < 5; i += 1) {
		await tick();
		await new Promise((resolve) => setTimeout(resolve, 0));
	}
}

async function waitForText(target: HTMLElement, expected: string) {
	for (let i = 0; i < 30; i += 1) {
		await flushAsync();
		if (target.textContent?.includes(expected)) return;
	}
	expect(target.textContent).toContain(expected);
}

function normalizedText(root: ParentNode): string {
	return (root.textContent || '').replace(/\s+/g, ' ').trim();
}

function buttonByText(root: ParentNode, text: string): HTMLButtonElement | null {
	return (
		Array.from(root.querySelectorAll('button')).find((button) =>
			normalizedText(button).includes(text),
		) ?? null
	);
}

function inputByLabel(root: ParentNode, label: string): HTMLInputElement {
	const labelElement = Array.from(root.querySelectorAll('label')).find((candidate) =>
		normalizedText(candidate).includes(label),
	);
	const input = labelElement?.closest('.gr-textfield')?.querySelector('input');
	if (!(input instanceof HTMLInputElement)) {
		throw new Error(`input not found for label ${label}`);
	}
	return input;
}

async function typeInto(input: HTMLInputElement, value: string) {
	input.value = value;
	input.dispatchEvent(new Event('input', { bubbles: true }));
	await flushAsync();
}

function definitionValue(root: ParentNode, label: string): string {
	const term = Array.from(root.querySelectorAll('dt')).find((candidate) =>
		normalizedText(candidate).includes(label),
	);
	const value = term?.nextElementSibling;
	if (!(value instanceof HTMLElement)) {
		throw new Error(`definition value not found for ${label}`);
	}
	return normalizedText(value);
}
