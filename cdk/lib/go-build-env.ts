import * as fs from 'node:fs';
import * as path from 'node:path';

const repoGoToolchainCache = new Map<string, string>();
const goDirectivePattern = /^go\s+([0-9]+(?:\.[0-9]+){1,2})\s*$/m;

function repoGoToolchain(repoRoot: string): string {
	const cached = repoGoToolchainCache.get(repoRoot);
	if (cached) return cached;

	const goModPath = path.join(repoRoot, 'go.mod');
	const goMod = fs.readFileSync(goModPath, 'utf8');
	const match = goMod.match(goDirectivePattern);
	if (!match) {
		throw new Error(`failed to resolve Go toolchain from ${goModPath}`);
	}

	const toolchain = `go${match[1]}`;
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
