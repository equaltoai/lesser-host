import { readFileSync } from 'node:fs';

import { mount, unmount } from 'svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Eip1193Provider } from 'src/lib/wallet/ethereum';
import type { SetupStatusResponse, WalletChallengeResponse } from 'src/lib/api/controlPlane';

vi.mock('src/lib/api/controlPlane', () => ({
	getSetupStatus: vi.fn(),
	setupBootstrapChallenge: vi.fn(),
	setupBootstrapVerify: vi.fn(),
	setupCreateAdmin: vi.fn(),
	setupFinalize: vi.fn(),
	walletChallenge: vi.fn(),
	walletLogin: vi.fn(),
}));

vi.mock('src/lib/api/webauthn', () => ({
	webAuthnCredentials: vi.fn(),
	webAuthnRegisterBegin: vi.fn(),
	webAuthnRegisterFinish: vi.fn(),
}));

vi.mock('src/lib/router', () => ({
	linkProps: (to: string) => ({ href: to, onnavigate: vi.fn() }),
	navigate: vi.fn(),
}));

vi.mock('src/lib/wallet/ethereum', () => ({
	getEthereumProvider: vi.fn(),
	requestAccounts: vi.fn(),
	getChainId: vi.fn(),
	personalSign: vi.fn(),
}));

import Setup from './Setup.svelte';
import {
	getSetupStatus,
	setupBootstrapChallenge,
	setupBootstrapVerify,
	setupCreateAdmin,
	walletChallenge,
	walletLogin,
} from 'src/lib/api/controlPlane';
import { webAuthnCredentials } from 'src/lib/api/webauthn';
import { getChainId, getEthereumProvider, personalSign, requestAccounts } from 'src/lib/wallet/ethereum';

const source = readFileSync('src/pages/Setup.svelte', 'utf8');

const bootstrapAddress = '0xb000000000000000000000000000000000000001';
const primaryAdminAddress = '0xa000000000000000000000000000000000000001';
const secondAdminAddress = '0xa000000000000000000000000000000000000002';
const expiresAt = '2026-07-06T18:00:00Z';

const mockGetSetupStatus = vi.mocked(getSetupStatus);
const mockSetupBootstrapChallenge = vi.mocked(setupBootstrapChallenge);
const mockSetupBootstrapVerify = vi.mocked(setupBootstrapVerify);
const mockSetupCreateAdmin = vi.mocked(setupCreateAdmin);
const mockWalletChallenge = vi.mocked(walletChallenge);
const mockWalletLogin = vi.mocked(walletLogin);
const mockWebAuthnCredentials = vi.mocked(webAuthnCredentials);
const mockGetEthereumProvider = vi.mocked(getEthereumProvider);
const mockRequestAccounts = vi.mocked(requestAccounts);
const mockGetChainId = vi.mocked(getChainId);
const mockPersonalSign = vi.mocked(personalSign);

let currentStatus: SetupStatusResponse;
let nextWalletAddress = bootstrapAddress;
let walletChallengeSeq = 0;
const challengeUsernames = new Map<string, string>();
const mockProvider = { request: vi.fn() } satisfies Eip1193Provider;
const mounted: Array<{ instance: Record<string, never>; target: HTMLElement }> = [];

function lockedStatus(overrides: Partial<SetupStatusResponse> = {}): SetupStatusResponse {
	return {
		control_plane_state: 'locked',
		locked: true,
		finalize_allowed: false,
		bootstrapped_at: undefined,
		bootstrap_wallet_address_set: true,
		bootstrap_wallet_address: bootstrapAddress,
		primary_admin_set: false,
		primary_admin_username: undefined,
		stage: 'lab',
		...overrides,
	};
}

function makeChallenge(
	id: string,
	address: string,
	chainId: number,
	message: string,
	username?: string,
): WalletChallengeResponse {
	return {
		id,
		username,
		address,
		chainId,
		nonce: `nonce-${id}`,
		message,
		issuedAt: '2026-07-06T17:00:00Z',
		expiresAt,
	};
}

beforeEach(() => {
	currentStatus = lockedStatus();
	nextWalletAddress = bootstrapAddress;
	walletChallengeSeq = 0;
	challengeUsernames.clear();
	mockProvider.request.mockReset();
	sessionStorage.clear();
	document.body.innerHTML = '';

	mockGetSetupStatus.mockImplementation(async () => ({ ...currentStatus }));
	mockSetupBootstrapChallenge.mockImplementation(async (address, chainId) =>
		makeChallenge('bootstrap-challenge', address, chainId, 'bootstrap signature message'),
	);
	mockSetupBootstrapVerify.mockResolvedValue({
		token_type: 'Bearer',
		token: 'setup-session-token',
		expires_at: expiresAt,
	});
	mockSetupCreateAdmin.mockImplementation(async (_setupToken, input) => {
		currentStatus = lockedStatus({
			bootstrapped_at: '2026-07-06T17:05:00Z',
			primary_admin_set: true,
			primary_admin_username: input.username,
		});
		return { username: input.username };
	});
	mockWalletChallenge.mockImplementation(async ({ username, address, chainId }) => {
		walletChallengeSeq += 1;
		const id = `admin-challenge-${walletChallengeSeq}`;
		challengeUsernames.set(id, username);
		return makeChallenge(id, address, chainId, `admin challenge for ${address}`, username);
	});
	mockWalletLogin.mockImplementation(async ({ challengeId }) => ({
		token_type: 'Bearer',
		token: `admin-session-${challengeId}`,
		expires_at: expiresAt,
		username: challengeUsernames.get(challengeId) || currentStatus.primary_admin_username || 'admin',
		role: 'admin',
		method: 'wallet',
	}));
	mockWebAuthnCredentials.mockResolvedValue({ credentials: [] });
	mockGetEthereumProvider.mockReturnValue(mockProvider);
	mockRequestAccounts.mockImplementation(async () => [nextWalletAddress]);
	mockGetChainId.mockResolvedValue(11155111);
	mockPersonalSign.mockImplementation(async (_provider, message, address) => `sig:${address}:${message}`);
});

afterEach(() => {
	for (const { instance, target } of mounted.splice(0)) {
		unmount(instance);
		target.remove();
	}
	vi.clearAllMocks();
	sessionStorage.clear();
	document.body.innerHTML = '';
});

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

function mountSetup() {
	const target = document.createElement('div');
	document.body.appendChild(target);
	const instance = mount(Setup, { target }) as unknown as Record<string, never>;
	mounted.push({ instance, target });
	return target;
}

function cardByHeading(target: HTMLElement, heading: string): HTMLElement {
	const headingEl = Array.from(target.querySelectorAll('h1,h2,h3')).find((el) =>
		normalizedText(el).includes(heading),
	);
	const card = headingEl?.closest('.gr-card');
	if (!(card instanceof HTMLElement)) {
		throw new Error(`card not found for heading ${heading}`);
	}
	return card;
}

function normalizedText(el: Element): string {
	return (el.textContent || '').replace(/\s+/g, ' ').trim();
}

function buttonsByText(root: ParentNode, text: string): HTMLButtonElement[] {
	return Array.from(root.querySelectorAll('button')).filter((button) =>
		normalizedText(button).includes(text),
	);
}

function enabledButtonByText(root: ParentNode, text: string): HTMLButtonElement {
	const button = buttonsByText(root, text).find((candidate) => !candidate.disabled);
	if (!button) throw new Error(`enabled button not found: ${text}`);
	return button;
}

async function clickEnabledButton(root: ParentNode, text: string) {
	enabledButtonByText(root, text).click();
	await flushAsync();
}

async function connectWallet(target: HTMLElement, label: string, address: string) {
	nextWalletAddress = address;
	await clickEnabledButton(target, label);
	await waitForText(target, address);
}

function inputByLabel(root: ParentNode, label: string): HTMLInputElement {
	const labelEl = Array.from(root.querySelectorAll('label')).find((candidate) =>
		normalizedText(candidate).includes(label),
	);
	const input = labelEl?.closest('.gr-textfield')?.querySelector('input');
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

async function completeStep1(target: HTMLElement) {
	await waitForText(target, 'Step 1 — Bootstrap session');
	await connectWallet(target, 'Connect bootstrap wallet', bootstrapAddress);
	await clickEnabledButton(cardByHeading(target, 'Step 1 — Bootstrap session'), 'Create challenge');
	await waitForText(target, 'bootstrap signature message');
	await clickEnabledButton(cardByHeading(target, 'Step 1 — Bootstrap session'), 'Sign & verify');
	await waitForText(target, 'Step 2 is waiting for an explicit primary admin wallet connection');
}

describe('Setup bootstrap session state', () => {
	it('does not initialize or complete Step 1 from stale sessionStorage', () => {
		expect(source).toContain("const SETUP_SESSION_KEY = 'lesser-host:setupSessionToken';");
		expect(source).toContain("let setupSessionToken = $state<string>('');");
		expect(source).not.toContain('sessionStorage.getItem(SETUP_SESSION_KEY)');
		expect(source).not.toContain('sessionStorage.setItem(SETUP_SESSION_KEY');
		expect(source).toContain('sessionStorage.removeItem(SETUP_SESSION_KEY)');
	});
});

describe('Setup two-wallet role state machine', () => {
	it('transitions Step 1 success to an explicit primary-admin-wallet connection requirement', async () => {
		const target = mountSetup();

		await completeStep1(target);

		const step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		expect(step2.textContent).toContain('Connect primary admin wallet');
		expect(step2.textContent).toContain('admin challenge controls stay unavailable');
		expect(step2.textContent).not.toContain('Username*');
		expect(buttonsByText(step2, 'Create challenge')).toHaveLength(0);
		expect(mockWalletChallenge).not.toHaveBeenCalled();
	});

	it('cannot create an admin challenge with no primary admin wallet connected', async () => {
		const target = mountSetup();

		await completeStep1(target);

		const step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		const createChallengeButtons = buttonsByText(step2, 'Create challenge');
		expect(createChallengeButtons).toHaveLength(0);
		expect(mockWalletChallenge).not.toHaveBeenCalled();
	});

	it('blocks the bootstrap wallet in Step 2 and never calls admin challenge creation', async () => {
		const target = mountSetup();

		await completeStep1(target);
		await connectWallet(target, 'Connect primary admin wallet', bootstrapAddress);

		const step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		expect(step2.textContent).toContain('Switch wallet account');
		expect(step2.textContent).toContain('The connected primary admin wallet is the bootstrap wallet');
		expect(buttonsByText(step2, 'Create challenge')).toHaveLength(0);
		expect(mockWalletChallenge).not.toHaveBeenCalled();
	});

	it('creates and signs the admin only after a distinct primary admin wallet is connected', async () => {
		const target = mountSetup();

		await completeStep1(target);
		await connectWallet(target, 'Connect primary admin wallet', primaryAdminAddress);

		const step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		await typeInto(inputByLabel(step2, 'Username'), 'primary-admin');
		await clickEnabledButton(step2, 'Create challenge');
		await waitForText(target, `admin challenge for ${primaryAdminAddress}`);

		expect(mockWalletChallenge).toHaveBeenCalledTimes(1);
		expect(mockWalletChallenge).toHaveBeenLastCalledWith({
			username: 'primary-admin',
			address: primaryAdminAddress,
			chainId: 11155111,
		});

		await clickEnabledButton(step2, 'Sign & create admin');

		expect(mockSetupCreateAdmin).toHaveBeenCalledTimes(1);
		expect(mockSetupCreateAdmin).toHaveBeenLastCalledWith(
			'setup-session-token',
			expect.objectContaining({
				username: 'primary-admin',
				wallet: expect.objectContaining({
					address: primaryAdminAddress,
					signature: `sig:${primaryAdminAddress}:admin challenge for ${primaryAdminAddress}`,
				}),
			}),
		);
	});

	it('clears stale admin challenge and cached admin session state when the admin account changes', async () => {
		const target = mountSetup();

		await completeStep1(target);
		await connectWallet(target, 'Connect primary admin wallet', primaryAdminAddress);

		let step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		await typeInto(inputByLabel(step2, 'Username'), 'primary-admin');
		await clickEnabledButton(step2, 'Create challenge');
		await waitForText(target, `admin challenge for ${primaryAdminAddress}`);

		await connectWallet(target, 'Reconnect primary admin wallet', secondAdminAddress);
		step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		expect(step2.textContent).not.toContain(`admin challenge for ${primaryAdminAddress}`);
		expect(buttonsByText(step2, 'Sign & create admin').every((button) => button.disabled)).toBe(true);

		await clickEnabledButton(step2, 'Create challenge');
		await waitForText(target, `admin challenge for ${secondAdminAddress}`);
		await clickEnabledButton(step2, 'Sign & create admin');
		await waitForText(target, 'Step 3 — Register primary admin passkey');

		const step3 = cardByHeading(target, 'Step 3 — Register primary admin passkey');
		await clickEnabledButton(step3, 'Check passkeys');
		expect(mockWalletLogin).toHaveBeenCalledTimes(1);

		await connectWallet(target, 'Reconnect primary admin wallet', primaryAdminAddress);
		await clickEnabledButton(cardByHeading(target, 'Step 3 — Register primary admin passkey'), 'Check passkeys');

		expect(mockWalletLogin).toHaveBeenCalledTimes(2);
		expect(mockWalletChallenge).toHaveBeenLastCalledWith({
			username: 'primary-admin',
			address: primaryAdminAddress,
			chainId: 11155111,
		});
	});
});
