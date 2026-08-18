import * as fs from 'node:fs';
import * as path from 'node:path';

const repoGoToolchainCache = new Map<string, string>();
const goDirectivePattern = /^go\s+([0-9]+(?:\.[0-9]+){1,2})(?:\s*\/\/.*)?$/;
const toolchainDirectivePattern = /^toolchain\s+go([0-9]+(?:\.[0-9]+){1,2})(?:\s*\/\/.*)?$/;

function compareGoVersions(a: string, b: string): number {
	const aParts = a.split('.').map(Number);
	const bParts = b.split('.').map(Number);
	const maxLength = Math.max(aParts.length, bParts.length);

	for (let i = 0; i < maxLength; i += 1) {
		const diff = (aParts[i] ?? 0) - (bParts[i] ?? 0);
		if (diff !== 0) {
			return diff;
		}
	}

	return 0;
}

function resolvedGoToolchain(goModPath: string, goMod: string): string {
	let goVersion: string | undefined;
	let toolchainVersion: string | undefined;

	for (const rawLine of goMod.split(/\r?\n/)) {
		const line = rawLine.trim();
		const goMatch = line.match(goDirectivePattern);
		if (goMatch) {
			goVersion = goMatch[1];
			continue;
		}

		const toolchainMatch = line.match(toolchainDirectivePattern);
		if (toolchainMatch) {
			toolchainVersion = toolchainMatch[1];
		}
	}

	if (!goVersion) {
		throw new Error(`failed to resolve Go toolchain from ${goModPath}`);
	}

	if (!toolchainVersion || compareGoVersions(goVersion, toolchainVersion) >= 0) {
		return `go${goVersion}`;
	}

	return `go${toolchainVersion}`;
}

function repoGoToolchain(repoRoot: string): string {
	const cached = repoGoToolchainCache.get(repoRoot);
	if (cached) return cached;

	const goModPath = path.join(repoRoot, 'go.mod');
	const goMod = fs.readFileSync(goModPath, 'utf8');
	const toolchain = resolvedGoToolchain(goModPath, goMod);
	repoGoToolchainCache.set(repoRoot, toolchain);
	return toolchain;
}

export function synthGoBuildEnv(
	repoRoot: string,
	overrides?: { CGO_ENABLED?: string; GOOS?: string; GOARCH?: string },
): NodeJS.ProcessEnv {
	const { CGO_ENABLED = '0', GOOS = 'linux', GOARCH = 'amd64' } = overrides ?? {};
	return {
		...process.env,
		CGO_ENABLED,
		GOOS,
		GOARCH,
		GOTOOLCHAIN: repoGoToolchain(repoRoot),
	};
}
