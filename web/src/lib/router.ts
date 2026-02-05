import { readable } from 'svelte/store';

function normalizePath(pathname: string): string {
	if (!pathname) {
		return '/';
	}
	if (pathname.length > 1 && pathname.endsWith('/')) {
		return pathname.slice(0, -1);
	}
	return pathname;
}

export const currentPath = readable<string>(normalizePath(window.location.pathname), (set) => {
	const onPopState = () => set(normalizePath(window.location.pathname));
	window.addEventListener('popstate', onPopState);
	return () => window.removeEventListener('popstate', onPopState);
});

export function navigate(to: string): void {
	const normalized = normalizePath(to);
	if (normalized === normalizePath(window.location.pathname)) {
		return;
	}
	history.pushState({}, '', normalized);
	window.dispatchEvent(new PopStateEvent('popstate'));
}

