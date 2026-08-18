import { readFileSync } from 'node:fs';

import { mount, unmount } from 'svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Eip1193Provider } from 'src/lib/wallet/ethereum';
import type { SetupStatusResponse, WalletChallengeResponse } from 'src/lib/api/controlPlane';

vi.mock('src/lib/api/controlPlane', () => ({
	getSetupStatus: vi.fn(),
	setupPasskeyRegisterBegin: vi.fn(),
	setupBootstrapChallenge: vi.fn(),
	setupBootstrapVerify: vi.fn(),
	setupCreateAdmin: vi.fn(),
	setupFinalize: vi.fn(),
	walletChallenge: vi.fn(),
	walletLogin: vi.fn(),
}));

vi.mock('src/lib/api/webauthn', () => ({
	webAuthnCredentials: vi.fn(),
	webAuthnLoginBegin: vi.fn(),
	webAuthnLoginFinish: vi.fn(),
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
	setupPasskeyRegisterBegin,
	setupBootstrapChallenge,
	setupBootstrapVerify,
	setupCreateAdmin,
	setupFinalize,
	walletChallenge,
	walletLogin,
} from 'src/lib/api/controlPlane';
import {
	webAuthnCredentials,
	webAuthnLoginBegin,
	webAuthnLoginFinish,
	webAuthnRegisterBegin,
	webAuthnRegisterFinish,
} from 'src/lib/api/webauthn';
import { getChainId, getEthereumProvider, personalSign, requestAccounts } from 'src/lib/wallet/ethereum';

const source = readFileSync('src/pages/Setup.svelte', 'utf8');

const bootstrapAddress = '0xb000000000000000000000000000000000000001';
const primaryAdminAddress = '0xa000000000000000000000000000000000000001';
const secondAdminAddress = '0xa000000000000000000000000000000000000002';
const expiresAt = '2026-07-06T18:00:00Z';
const encodedChallenge = 'Y2hhbGxlbmdl';
const encodedUserID = 'dXNlcg=='.replace(/=/g, '');

const mockGetSetupStatus = vi.mocked(getSetupStatus);
const mockSetupPasskeyRegisterBegin = vi.mocked(setupPasskeyRegisterBegin);
const mockSetupBootstrapChallenge = vi.mocked(setupBootstrapChallenge);
const mockSetupBootstrapVerify = vi.mocked(setupBootstrapVerify);
const mockSetupCreateAdmin = vi.mocked(setupCreateAdmin);
const mockSetupFinalize = vi.mocked(setupFinalize);
const mockWalletChallenge = vi.mocked(walletChallenge);
const mockWalletLogin = vi.mocked(walletLogin);
const mockWebAuthnCredentials = vi.mocked(webAuthnCredentials);
const mockWebAuthnLoginBegin = vi.mocked(webAuthnLoginBegin);
const mockWebAuthnLoginFinish = vi.mocked(webAuthnLoginFinish);
const mockWebAuthnRegisterBegin = vi.mocked(webAuthnRegisterBegin);
const mockWebAuthnRegisterFinish = vi.mocked(webAuthnRegisterFinish);
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
const mockCredentialCreate = vi.fn();
const mockCredentialGet = vi.fn();

class MockPublicKeyCredential {
	id: string;
	rawId: ArrayBuffer;
	response: unknown;
	type = 'public-key';
	authenticatorAttachment = 'platform';

	constructor(id: string, response: unknown) {
		this.id = id;
		this.rawId = new TextEncoder().encode(`${id}-raw`).buffer;
		this.response = response;
	}

	getClientExtensionResults() {
		return {};
	}
}

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

function activeStatus(overrides: Partial<SetupStatusResponse> = {}): SetupStatusResponse {
	return {
		control_plane_state: 'active',
		locked: false,
		finalize_allowed: false,
		bootstrapped_at: '2026-07-06T17:10:00Z',
		bootstrap_wallet_address_set: true,
		bootstrap_wallet_address: undefined,
		primary_admin_set: true,
		primary_admin_username: 'primary-admin',
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

function makeCreationBegin(challenge: string) {
	return {
		challenge,
		publicKey: {
			challenge: encodedChallenge,
			user: {
				id: encodedUserID,
				name: 'primary-admin',
				displayName: 'Primary Admin',
			},
			rp: {
				name: 'lesser-host',
			},
			pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
		},
	};
}

function makeLoginBegin(challenge: string) {
	return {
		challenge,
		publicKey: {
			challenge: encodedChallenge,
			allowCredentials: [],
		},
	};
}

function makeCreationCredential(id = 'setup-passkey') {
	return new MockPublicKeyCredential(id, {
		attestationObject: new TextEncoder().encode('attestation-object').buffer,
		clientDataJSON: new TextEncoder().encode('client-data-json').buffer,
		getTransports: () => ['internal'],
	});
}

function makeAssertionCredential(id = 'setup-passkey') {
	return new MockPublicKeyCredential(id, {
		authenticatorData: new TextEncoder().encode('authenticator-data').buffer,
		clientDataJSON: new TextEncoder().encode('client-data-json').buffer,
		signature: new TextEncoder().encode('signature').buffer,
		userHandle: null,
	});
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
	mockSetupPasskeyRegisterBegin.mockResolvedValue(makeCreationBegin('setup-passkey-register-begin'));
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
		if (input.passkey) {
			return {
				username: input.username,
				token_type: 'Bearer',
				token: 'admin-session-passkey-create',
				expires_at: expiresAt,
				role: 'admin',
				method: 'webauthn',
			};
		}
		return { username: input.username };
	});
	mockSetupFinalize.mockImplementation(async () => {
		currentStatus = activeStatus({ primary_admin_username: currentStatus.primary_admin_username });
		return {
			locked: false,
			bootstrapped_at: '2026-07-06T17:10:00Z',
		};
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
	mockWebAuthnLoginBegin.mockResolvedValue(makeLoginBegin('passkey-login-begin'));
	mockWebAuthnLoginFinish.mockImplementation(async ({ username, challenge }) => ({
		token_type: 'Bearer',
		token: `passkey-session-${challenge}`,
		expires_at: expiresAt,
		username,
		role: 'admin',
		method: 'webauthn',
	}));
	mockWebAuthnRegisterBegin.mockResolvedValue(makeCreationBegin('account-passkey-register-begin'));
	mockWebAuthnRegisterFinish.mockResolvedValue({ ok: true });
	mockGetEthereumProvider.mockReturnValue(mockProvider);
	mockRequestAccounts.mockImplementation(async () => [nextWalletAddress]);
	mockGetChainId.mockResolvedValue(11155111);
	mockPersonalSign.mockImplementation(async (_provider, message, address) => `sig:${address}:${message}`);

	mockCredentialCreate.mockReset();
	mockCredentialGet.mockReset();
	mockCredentialCreate.mockResolvedValue(makeCreationCredential());
	mockCredentialGet.mockResolvedValue(makeAssertionCredential());

	Object.defineProperty(window, 'PublicKeyCredential', {
		value: MockPublicKeyCredential,
		configurable: true,
	});
	Object.defineProperty(globalThis, 'PublicKeyCredential', {
		value: MockPublicKeyCredential,
		configurable: true,
	});
	Object.defineProperty(navigator, 'credentials', {
		value: {
			create: mockCredentialCreate,
			get: mockCredentialGet,
		},
		configurable: true,
	});
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
	await waitForText(target, 'Step 2 — Create primary admin');
}

function checkboxByText(root: ParentNode, text: string): HTMLInputElement {
	const label = Array.from(root.querySelectorAll('label')).find((candidate) =>
		normalizedText(candidate).includes(text),
	);
	const input = label?.querySelector('input[type="checkbox"]');
	if (!(input instanceof HTMLInputElement)) {
		throw new Error(`checkbox not found: ${text}`);
	}
	return input;
}

async function checkByLabel(root: ParentNode, text: string) {
	const input = checkboxByText(root, text);
	input.checked = true;
	input.dispatchEvent(new Event('change', { bubbles: true }));
	await flushAsync();
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
		expect(step2.textContent).toContain('wallet-first mode is waiting');
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

	it('creates a passkey-only primary admin and finalizes without linking any wallet credential', async () => {
		const target = mountSetup();

		await completeStep1(target);

		const step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		await clickEnabledButton(step2, 'Passkey-only');
		await typeInto(inputByLabel(step2, 'Username'), 'passkey-admin');
		await typeInto(inputByLabel(step2, 'Initial passkey name'), 'Setup admin passkey');
		await clickEnabledButton(step2, 'Create admin with passkey');

		expect(mockSetupPasskeyRegisterBegin).toHaveBeenCalledWith('setup-session-token', {
			username: 'passkey-admin',
			displayName: undefined,
		});
		expect(mockSetupCreateAdmin).toHaveBeenCalledWith(
			'setup-session-token',
			expect.objectContaining({
				username: 'passkey-admin',
				passkey: expect.objectContaining({
					challenge: 'setup-passkey-register-begin',
					credential_name: 'Setup admin passkey',
				}),
			}),
		);
		expect(mockWalletChallenge).not.toHaveBeenCalled();
		expect(mockWalletLogin).not.toHaveBeenCalled();
		expect(mockCredentialCreate).toHaveBeenCalledTimes(1);
		await waitForText(target, 'Primary admin passkey ready');

		const step4 = cardByHeading(target, 'Step 4 — Finalize');
		await checkByLabel(step4, 'I understand finalize is irreversible for this stage.');
		await checkByLabel(step4, 'I have access to the primary admin credential and can authenticate again later.');
		await typeInto(inputByLabel(step4, 'Type FINALIZE to confirm'), 'FINALIZE');
		await clickEnabledButton(step4, 'Finalize control plane');

		expect(mockSetupFinalize).toHaveBeenCalledWith('admin-session-passkey-create');
		expect(mockWalletLogin).not.toHaveBeenCalled();
		await waitForText(target, 'Setup complete');
	});

	it('recovers the primary admin session with a passkey after refresh and finalizes without a wallet', async () => {
		currentStatus = lockedStatus({
			bootstrapped_at: '2026-07-06T17:05:00Z',
			primary_admin_set: true,
			primary_admin_username: 'passkey-admin',
		});
		mockWebAuthnCredentials.mockResolvedValueOnce({
			credentials: [
				{
					id: 'cred-1',
					name: 'Setup admin passkey',
					created_at: '2026-07-06T17:06:00Z',
					last_used_at: '2026-07-06T17:06:00Z',
				},
			],
		});

		const target = mountSetup();
		await waitForText(target, 'Primary admin already configured');

		const step3 = cardByHeading(target, 'Step 3 — Register primary admin passkey');
		await clickEnabledButton(step3, 'Check passkeys');

		expect(mockWebAuthnLoginBegin).toHaveBeenCalledWith('passkey-admin');
		expect(mockWebAuthnLoginFinish).toHaveBeenCalledTimes(1);
		expect(mockWalletLogin).not.toHaveBeenCalled();
		expect(mockCredentialGet).toHaveBeenCalledTimes(1);
		await waitForText(target, 'Primary admin passkey ready');

		const step4 = cardByHeading(target, 'Step 4 — Finalize');
		await checkByLabel(step4, 'I understand finalize is irreversible for this stage.');
		await checkByLabel(step4, 'I have access to the primary admin credential and can authenticate again later.');
		await typeInto(inputByLabel(step4, 'Type FINALIZE to confirm'), 'FINALIZE');
		await clickEnabledButton(step4, 'Finalize control plane');

		expect(mockSetupFinalize).toHaveBeenCalledWith('passkey-session-passkey-login-begin');
		expect(mockWalletLogin).not.toHaveBeenCalled();
		await waitForText(target, 'Setup complete');
	});

	it('surfaces a passkey ceremony failure before the admin is created', async () => {
		mockCredentialCreate.mockRejectedValueOnce(new Error('Platform authenticator unavailable'));

		const target = mountSetup();

		await completeStep1(target);

		const step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		await clickEnabledButton(step2, 'Passkey-only');
		await typeInto(inputByLabel(step2, 'Username'), 'passkey-admin');
		await clickEnabledButton(step2, 'Create admin with passkey');

		await waitForText(target, 'Platform authenticator unavailable');
		expect(mockSetupCreateAdmin).not.toHaveBeenCalled();
		expect(target.textContent).not.toContain('Primary admin passkey ready');
		expect(enabledButtonByText(cardByHeading(target, 'Step 2 — Create primary admin'), 'Create admin with passkey')).toBeTruthy();
	});

	it('treats an invalid passkey-admin response as actionable and lets the operator retry', async () => {
		mockSetupCreateAdmin.mockImplementationOnce(async (_setupToken, input) => {
			return {
				username: input.username,
				token_type: 'Bearer',
				token: '',
				expires_at: expiresAt,
				role: 'customer',
				method: 'wallet',
			};
		});

		const target = mountSetup();

		await completeStep1(target);

		const step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		await clickEnabledButton(step2, 'Passkey-only');
		await typeInto(inputByLabel(step2, 'Username'), 'passkey-admin');
		await clickEnabledButton(step2, 'Create admin with passkey');

		await waitForText(target, 'Primary admin passkey setup did not return an admin session.');
		expect(target.textContent).not.toContain('Primary admin passkey ready');

		await clickEnabledButton(cardByHeading(target, 'Step 2 — Create primary admin'), 'Create admin with passkey');
		await waitForText(target, 'Primary admin passkey ready');
		expect(mockSetupCreateAdmin).toHaveBeenCalledTimes(2);
	});

	it('treats a cancelled passkey ceremony as recoverable and resets state for retry', async () => {
		mockCredentialCreate.mockResolvedValueOnce(null);

		const target = mountSetup();

		await completeStep1(target);

		const step2 = cardByHeading(target, 'Step 2 — Create primary admin');
		await clickEnabledButton(step2, 'Passkey-only');
		await typeInto(inputByLabel(step2, 'Username'), 'passkey-admin');
		await clickEnabledButton(step2, 'Create admin with passkey');

		await waitForText(target, 'No credential returned.');
		expect(mockSetupCreateAdmin).not.toHaveBeenCalled();
		expect(target.textContent).not.toContain('Primary admin passkey ready');

		await clickEnabledButton(cardByHeading(target, 'Step 2 — Create primary admin'), 'Create admin with passkey');
		await waitForText(target, 'Primary admin passkey ready');
		expect(mockSetupCreateAdmin).toHaveBeenCalledTimes(1);
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
