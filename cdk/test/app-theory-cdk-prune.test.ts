import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

// GLM-5.3 F5 (issue #1052): the hosted-genesis microvm prune wiring inside the
// AppTheory deploy wrapper must sit after the dry-run escape (dry-run makes no
// AWS mutations, so it must skip pruning) and before `cdk deploy`, and it must
// pass the stage and the synthesized template path so the pruner can fail
// closed on image-name drift. This mirrors the setup-bootstrap-wallet wrapper
// guard precedent (cdk/test/setup-bootstrap-wallet.test.ts).
test('AppTheory wrapper prunes microvm images after dry-run and before cdk deploy', () => {
	const wrapper = readFileSync(join(process.cwd(), '..', 'scripts', 'app-theory-cdk.sh'), 'utf8');

	const dryRunIndex = wrapper.indexOf('LESSER_HOST_CDK_DRY_RUN');
	const pruneIndex = wrapper.indexOf('hosted-genesis-microvm-prune');
	const deployIndex = wrapper.indexOf('cdk deploy');

	assert.ok(dryRunIndex !== -1, 'wrapper must keep the dry-run escape');
	assert.ok(pruneIndex !== -1, 'wrapper must invoke hosted-genesis-microvm-prune');
	assert.ok(deployIndex !== -1, 'wrapper must invoke cdk deploy');
	assert.ok(
		pruneIndex > dryRunIndex,
		'prune must run after the dry-run block so dry-run makes no AWS mutations',
	);
	assert.ok(
		pruneIndex < deployIndex,
		'prune must run before cdk deploy',
	);
	assert.match(
		wrapper,
		/go run \.\/scripts\/hosted-genesis-microvm-prune prune "\$stage" "\$template_path"/,
		'prune must receive the stage and the synthesized template path',
	);
});
