import { readable } from 'svelte/store';

export const SAFE_APP_BASE_PATH = '/safe-app';
const SAFE_APP_TARGET_KEY = 'lesser-host:safe-app:target:v1';

function normalizePath(pathname: string): string {
	if (!pathname) {
		return '/';
	}
	if (pathname.length > 1 && pathname.endsWith('/')) {
		return pathname.slice(0, -1);
	}
	return pathname;
}

function appBasePath(pathname: string): string {
	return pathname === SAFE_APP_BASE_PATH || pathname.startsWith(`${SAFE_APP_BASE_PATH}/`) ? SAFE_APP_BASE_PATH : '';
}

const CURRENT_BASE_PATH = appBasePath(window.location.pathname);

function stripBasePath(pathname: string, basePath: string): string {
	if (!basePath) return pathname;
	if (pathname === basePath) return '/';
	if (pathname.startsWith(`${basePath}/`)) {
		return pathname.slice(basePath.length) || '/';
	}
	return pathname;
}

function normalizeAppPath(pathname: string): string {
	return normalizePath(stripBasePath(pathname, CURRENT_BASE_PATH));
}

function withBasePath(pathname: string): string {
	const normalized = normalizePath(pathname);
	if (!CURRENT_BASE_PATH) return normalized;
	if (normalized === '/') return CURRENT_BASE_PATH;
	return `${CURRENT_BASE_PATH}${normalized}`;
}

export function isSafeAppPath(pathname: string = window.location.pathname): boolean {
	return appBasePath(pathname) === SAFE_APP_BASE_PATH;
}

export function safeAppRootUrl(origin: string = window.location.origin): string {
	return `${origin}${SAFE_APP_BASE_PATH}`;
}

export function stageSafeAppTarget(pathname: string): void {
	try {
		localStorage.setItem(SAFE_APP_TARGET_KEY, normalizePath(pathname));
	} catch {
		// ignore
	}
}

export function consumeSafeAppTarget(): string {
	try {
		const value = localStorage.getItem(SAFE_APP_TARGET_KEY);
		localStorage.removeItem(SAFE_APP_TARGET_KEY);
		return value ? normalizePath(value) : '/';
	} catch {
		return '/';
	}
}

export const currentPath = readable<string>(normalizeAppPath(window.location.pathname), (set) => {
	const onPopState = () => set(normalizeAppPath(window.location.pathname));
	window.addEventListener('popstate', onPopState);
	return () => window.removeEventListener('popstate', onPopState);
});

export function navigate(to: string): void {
	const normalized = normalizePath(to);
	const target = withBasePath(normalized);
	if (target === normalizePath(window.location.pathname)) {
		return;
	}
	history.pushState({}, '', target);
	window.dispatchEvent(new PopStateEvent('popstate'));
}
