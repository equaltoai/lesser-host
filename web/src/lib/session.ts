import { derived, writable } from 'svelte/store';

export interface Session {
	tokenType: string;
	token: string;
	expiresAt: string;
	username: string;
	role: string;
	method?: string;
	walletAddress?: string;
}

const SESSION_STORAGE_KEY = 'lesser-host:session:v1';

function isValidSession(value: unknown): value is Session {
	if (!value || typeof value !== 'object') return false;
	const record = value as Record<string, unknown>;
	return (
		typeof record.tokenType === 'string' &&
		typeof record.token === 'string' &&
		typeof record.expiresAt === 'string' &&
		typeof record.username === 'string' &&
		typeof record.role === 'string'
	);
}

function isExpired(expiresAt: string): boolean {
	const parsed = Date.parse(expiresAt);
	if (!Number.isFinite(parsed)) return false;
	return parsed <= Date.now();
}

function loadInitialSession(): Session | null {
	const raw = sessionStorage.getItem(SESSION_STORAGE_KEY);
	if (!raw) return null;

	try {
		const parsed = JSON.parse(raw) as unknown;
		if (!isValidSession(parsed)) return null;
		if (isExpired(parsed.expiresAt)) return null;
		return parsed;
	} catch {
		return null;
	}
}

export const session = writable<Session | null>(loadInitialSession());

session.subscribe((value) => {
	if (!value) {
		sessionStorage.removeItem(SESSION_STORAGE_KEY);
		return;
	}

	if (isExpired(value.expiresAt)) {
		sessionStorage.removeItem(SESSION_STORAGE_KEY);
		session.set(null);
		return;
	}

	sessionStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(value));
});

export function setSession(value: Session | null): void {
	session.set(value);
}

export function clearSession(): void {
	session.set(null);
}

export const isAuthenticated = derived(session, (value) => Boolean(value?.token));

export const isOperatorSession = derived(session, (value) =>
	value ? value.role === 'admin' || value.role === 'operator' : false,
);

