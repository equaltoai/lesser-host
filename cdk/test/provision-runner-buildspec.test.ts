import assert from 'node:assert/strict';
import test from 'node:test';

import { renderProvisionRunnerBuildCommands } from '../lib/provision-runner-buildspec';

const buildCommands = renderProvisionRunnerBuildCommands();

test('RUN_MODE=lesser uses the CLI binary with --release-dir', () => {
	assert.match(buildCommands, /prepare_lesser_release_dir "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /prepare_lesser_checkout_dir "\$LESSER_RELEASE_DIR" "\$LESSER_CHECKOUT_DIR"/);
	assert.match(buildCommands, /ensure_lesser_go_toolchain "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /export GOTOOLCHAIN="\$\{GOTOOLCHAIN:-auto\}"/);
	assert.match(buildCommands, /cd "\$LESSER_CHECKOUT_DIR"/);
	assert.match(buildCommands, /"\$LESSER_RELEASE_DIR\/lesser" up --app "\$APP_SLUG" --base-domain "\$BASE_DOMAIN" --aws-profile managed --provisioning-input "\$PROVISION_INPUT" --release-dir "\$LESSER_RELEASE_DIR"/);
	assert.doesNotMatch(buildCommands, /cd infra\/cdk/);
	assert.doesNotMatch(buildCommands, /deploy_lesser_assembly_stack/);
	assert.doesNotMatch(buildCommands, /aws cloudformation deploy/);
});

test('RUN_MODE=lesser-body uses the release helper instead of a source checkout', () => {
	assert.match(buildCommands, /prepare_lesser_body_release_dir/);
	assert.match(buildCommands, /deploy-lesser-body-from-release\.sh/);
	assert.match(buildCommands, /--no-execute-changeset/);
	assert.match(buildCommands, /BODY_TEMPLATE_CERT_S3_KEY/);
	assert.match(buildCommands, /BODY_FAILURE_S3_KEY/);
	assert.match(buildCommands, /body-template-certification\.json/);
	assert.match(buildCommands, /body-failure\.json/);
	assert.match(buildCommands, /BODY_ASSET_BUCKET="cdk-hnb659fds-assets-\$TARGET_ACCOUNT_ID-\$TARGET_REGION"/);
	assert.doesNotMatch(buildCommands, /lesser-body-src/);
	assert.doesNotMatch(buildCommands, /npx cdk deploy --all/);
	assert.doesNotMatch(buildCommands, /npm ci/);
});

test('RUN_MODE=lesser-mcp uses the CLI binary with --release-dir', () => {
	assert.match(buildCommands, /prepare_lesser_checkout_dir "\$LESSER_RELEASE_DIR" "\$LESSER_CHECKOUT_DIR"/);
	assert.match(buildCommands, /ensure_lesser_go_toolchain "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /cd "\$LESSER_CHECKOUT_DIR"/);
	assert.match(buildCommands, /"\$LESSER_RELEASE_DIR\/lesser" up --app "\$APP_SLUG" --base-domain "\$BASE_DOMAIN" --aws-profile managed --provisioning-input "\$PROVISION_INPUT" --release-dir "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /mcp_lambda_arn/);
	assert.doesNotMatch(buildCommands, /cd infra\/cdk/);
	assert.doesNotMatch(buildCommands, /npx cdk deploy/);
	assert.doesNotMatch(buildCommands, /deploy_lesser_assembly_stack/);
	assert.doesNotMatch(buildCommands, /aws cloudformation deploy/);
});

test('runner emits explicit asset-contract failure messages', () => {
	assert.match(buildCommands, /lesser-body release unexpectedly requires a source checkout/);
	assert.match(buildCommands, /unexpected lesser-body deploy manifest path/);
	assert.match(buildCommands, /Lesser release manifest version mismatch/);
});

test('RUN_MODE=lesser-body handles boolean false manifest flags without jq fallback drift', () => {
	assert.doesNotMatch(buildCommands, /\.deploy\.source_checkout_required \/\/ empty/);
	assert.doesNotMatch(buildCommands, /\.deploy\.npm_install_required \/\/ empty/);
	assert.match(
		buildCommands,
		/if \.deploy\.source_checkout_required == false then "false" elif \.deploy\.source_checkout_required == true then "true" else empty end/,
	);
	assert.match(
		buildCommands,
		/if \.deploy\.npm_install_required == false then "false" elif \.deploy\.npm_install_required == true then "true" else empty end/,
	);
});
