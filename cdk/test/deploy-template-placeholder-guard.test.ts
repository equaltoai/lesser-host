import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

function writeTemplate(template: Record<string, unknown>): { path: string; cleanup: () => void } {
	const dir = mkdtempSync(join(tmpdir(), 'lesser-host-placeholder-guard-'));
	const path = join(dir, 'template.json');
	writeFileSync(path, `${JSON.stringify(template, null, 2)}\n`);
	return { path, cleanup: () => rmSync(dir, { recursive: true, force: true }) };
}

function runPlaceholderGuard(templatePath: string): ReturnType<typeof spawnSync> {
	return spawnSync(
		'node',
		[join(process.cwd(), '..', 'scripts', 'validate-deploy-template-placeholders.mjs'), templatePath],
		{ cwd: process.cwd(), encoding: 'utf8' },
	);
}

test('deploy template placeholder guard rejects placeholder bootstrap wallet env wiring', () => {
	const fixture = writeTemplate({
		Resources: {
			ControlPlaneApi: {
				Type: 'AWS::Lambda::Function',
				Properties: {
					Environment: {
						Variables: {
							BOOTSTRAP_WALLET_ADDRESS: '<YOUR_BOOTSTRAP_WALLET_ADDRESS>',
						},
					},
				},
			},
		},
	});
	try {
		const result = runPlaceholderGuard(fixture.path);
		assert.notEqual(result.status, 0, 'expected placeholder bootstrap wallet to fail deploy template guard');
		assert.match(String(result.stderr), /BOOTSTRAP_WALLET_ADDRESS/);
	} finally {
		fixture.cleanup();
	}
});
