import assert from 'node:assert/strict';
import test from 'node:test';

import { renderProvisionRunnerBuildCommands } from '../lib/provision-runner-buildspec';

const buildCommands = renderProvisionRunnerBuildCommands();

test('RUN_MODE=lesser uses verified Lesser release assets', () => {
	assert.match(buildCommands, /prepare_lesser_release_dir "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /--release-dir "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /lesser-lambda-bundle\.tar\.gz/);
	assert.match(buildCommands, /lesser-lambda-bundle\.json/);
});

test('RUN_MODE=lesser-body uses the release helper instead of a source checkout', () => {
	assert.match(buildCommands, /prepare_lesser_body_release_dir/);
	assert.match(buildCommands, /deploy-lesser-body-from-release\.sh/);
	assert.match(buildCommands, /BODY_ASSET_BUCKET="cdk-hnb659fds-assets-\$TARGET_ACCOUNT_ID-\$TARGET_REGION"/);
	assert.doesNotMatch(buildCommands, /lesser-body-src/);
	assert.doesNotMatch(buildCommands, /npx cdk deploy --all/);
	assert.doesNotMatch(buildCommands, /npm ci/);
});

test('RUN_MODE=lesser-mcp reuses the release lambda bundle', () => {
	assert.match(buildCommands, /install_lesser_lambda_bundle "\$LESSER_RELEASE_DIR" "\$LESSER_MCP_ASSET_ROOT"/);
	assert.match(buildCommands, /lambdaAssetRoot="\$LESSER_MCP_ASSET_ROOT"/);
	assert.doesNotMatch(buildCommands, /\.\/lesser build lambdas/);
});

test('runner emits explicit asset-contract failure messages', () => {
	assert.match(buildCommands, /unexpected Lesser lambda bundle manifest kind/);
	assert.match(buildCommands, /lesser-body release unexpectedly requires a source checkout/);
	assert.match(buildCommands, /unexpected lesser-body deploy manifest path/);
});
