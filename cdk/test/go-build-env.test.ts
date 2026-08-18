import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { synthGoBuildEnv } from '../lib/go-build-env';

function withTempRepo(goMod: string | undefined, run: (repoRoot: string) => void) {
	const repoRoot = mkdtempSync(join(tmpdir(), 'lesser-host-go-build-env-'));
	try {
		if (goMod !== undefined) {
			writeFileSync(join(repoRoot, 'go.mod'), goMod);
		}
		run(repoRoot);
	} finally {
		rmSync(repoRoot, { recursive: true, force: true });
	}
}

test('synthGoBuildEnv uses the go directive when no toolchain override is present', () => {
	withTempRepo('module example.com/test\n\ngo 1.26.6\n', (repoRoot) => {
		assert.equal(synthGoBuildEnv(repoRoot).GOTOOLCHAIN, 'go1.26.6');
	});
});

test('synthGoBuildEnv uses the higher toolchain directive when present', () => {
	withTempRepo('module example.com/test\n\ngo 1.26.6\ntoolchain go1.26.7\n', (repoRoot) => {
		assert.equal(synthGoBuildEnv(repoRoot).GOTOOLCHAIN, 'go1.26.7');
	});
});

test('synthGoBuildEnv keeps the higher go directive when toolchain is lower or equal', () => {
	withTempRepo('module example.com/test\n\ngo 1.26.7\ntoolchain go1.26.6\n', (repoRoot) => {
		assert.equal(synthGoBuildEnv(repoRoot).GOTOOLCHAIN, 'go1.26.7');
	});
});

test('synthGoBuildEnv tolerates trailing comments on go and toolchain directives', () => {
	withTempRepo(
		'module example.com/test\n\ngo 1.26.6 // synth baseline\ntoolchain go1.26.7 // sec bump\n',
		(repoRoot) => {
			assert.equal(synthGoBuildEnv(repoRoot).GOTOOLCHAIN, 'go1.26.7');
		},
	);
});

test('synthGoBuildEnv throws when go.mod is missing', () => {
	withTempRepo(undefined, (repoRoot) => {
		assert.throws(() => synthGoBuildEnv(repoRoot), /go\.mod/);
	});
});

test('synthGoBuildEnv throws when go.mod has no usable go or toolchain directives', () => {
	withTempRepo('module example.com/test\n\nrequire example.com/dep v1.2.3\n', (repoRoot) => {
		assert.throws(() => synthGoBuildEnv(repoRoot), /failed to resolve Go toolchain/);
	});
});
