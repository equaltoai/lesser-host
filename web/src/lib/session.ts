import { derived, writable } from 'svelte/store';
import { isSafeAppPath } from './router';

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
const SESSION_EXPIRED_AT_KEY = 'lesser-host:session:v1:expiredAt';
const SAFE_APP_HANDOFF_KEY = 'lesser-host:session:v1:safeAppHandoff';

interface SafeAppSessionHandoff {
	session: Session;
	expiresAt: string;
	stagedAt: string;
}

export function consumeSessionExpiredAt(): string | null {
	try {
		const value = sessionStorage.getItem(SESSION_EXPIRED_AT_KEY);
		if (!value) return null;
		sessionStorage.removeItem(SESSION_EXPIRED_AT_KEY);
		return value;
	} catch {
		return null;
	}
}

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
	if (!Number.isFinite(parsed)) return true;
	return parsed <= Date.now();
}

function isValidSafeAppSessionHandoff(value: unknown): value is SafeAppSessionHandoff {
	if (!value || typeof value !== 'object') return false;
	const record = value as Record<string, unknown>;
	return isValidSession(record.session) && typeof record.expiresAt === 'string' && typeof record.stagedAt === 'string';
}

function removeSafeAppSessionHandoff(): void {
	try {
		localStorage.removeItem(SAFE_APP_HANDOFF_KEY);
	} catch {
		// ignore
	}
}

function loadSafeAppSessionHandoff(): Session | null {
	if (!isSafeAppPath()) return null;

	try {
		const raw = localStorage.getItem(SAFE_APP_HANDOFF_KEY);
		if (!raw) return null;
		const parsed = JSON.parse(raw) as unknown;
		if (!isValidSafeAppSessionHandoff(parsed)) {
			removeSafeAppSessionHandoff();
			return null;
		}
		if (isExpired(parsed.expiresAt) || isExpired(parsed.session.expiresAt)) {
			removeSafeAppSessionHandoff();
			return null;
		}
		return parsed.session;
	} catch {
		removeSafeAppSessionHandoff();
		return null;
	}
}

function loadInitialSession(): Session | null {
	const raw = sessionStorage.getItem(SESSION_STORAGE_KEY);
	if (raw) {
		try {
			const parsed = JSON.parse(raw) as unknown;
			if (isValidSession(parsed) && !isExpired(parsed.expiresAt)) {
				return parsed;
			}
			if (isValidSession(parsed) && isExpired(parsed.expiresAt)) {
				try {
					sessionStorage.setItem(SESSION_EXPIRED_AT_KEY, parsed.expiresAt);
				} catch {
					// ignore
				}
			}
		} catch {
			// fall through to cleanup + handoff fallback
		}
	}
	try {
		sessionStorage.removeItem(SESSION_STORAGE_KEY);
	} catch {
		// ignore
	}
	return loadSafeAppSessionHandoff();
}

export const session = writable<Session | null>(loadInitialSession());

session.subscribe((value) => {
	if (!value) {
		sessionStorage.removeItem(SESSION_STORAGE_KEY);
		removeSafeAppSessionHandoff();
		return;
	}

	if (isExpired(value.expiresAt)) {
		try {
			sessionStorage.setItem(SESSION_EXPIRED_AT_KEY, value.expiresAt);
		} catch {
			// ignore
		}
		sessionStorage.removeItem(SESSION_STORAGE_KEY);
		removeSafeAppSessionHandoff();
		session.set(null);
		return;
	}

	sessionStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(value));
});

export function stageSafeAppSessionHandoff(value: Session | null, maxAgeMs: number = 10 * 60 * 1000): boolean {
	if (!value || isExpired(value.expiresAt)) {
		removeSafeAppSessionHandoff();
		return false;
	}

	const sessionExpiry = Date.parse(value.expiresAt);
	if (!Number.isFinite(sessionExpiry)) {
		removeSafeAppSessionHandoff();
		return false;
	}

	const handoffExpiry = new Date(Math.min(sessionExpiry, Date.now() + maxAgeMs)).toISOString();
	const payload: SafeAppSessionHandoff = {
		session: value,
		expiresAt: handoffExpiry,
		stagedAt: new Date().toISOString(),
	};

	try {
		localStorage.setItem(SAFE_APP_HANDOFF_KEY, JSON.stringify(payload));
		return true;
	} catch {
		return false;
	}
}

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
