import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { renderProvisionRunnerBuildCommands } from '../lib/provision-runner-buildspec';

const buildCommands = renderProvisionRunnerBuildCommands();

test('rendered provision runner build script is valid bash', () => {
	const result = spawnSync('bash', ['-n'], {
		encoding: 'utf8',
		input: buildCommands,
	});

	assert.ifError(result.error);
	assert.equal(result.status, 0, result.stderr || result.stdout);
});

test('RUN_MODE=lesser uses the CLI binary with --release-dir', () => {
	assert.match(buildCommands, /prepare_lesser_release_dir "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /prepare_lesser_checkout_dir "\$LESSER_RELEASE_DIR" "\$LESSER_CHECKOUT_DIR"/);
	assert.match(buildCommands, /ensure_lesser_go_toolchain "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /ensure_lesser_host_instance_key_secret/);
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
	assert.match(buildCommands, /managed_instance_key:\$instance_key\[0\]/);
	assert.match(buildCommands, /prepare_lesser_body_auxiliary_assets/);
	assert.match(buildCommands, /upload_lesser_body_auxiliary_assets "\$BODY_RELEASE_DIR" "\$BODY_ASSET_BUCKET" "\$BODY_ASSET_PREFIX"/);
	assert.match(buildCommands, /verify_downloaded_asset_checksum "\$body_release_dir\/checksums.txt" "\$path" "\$body_release_dir\/\$path"/);
	assert.match(buildCommands, /byte-size mismatch for lesser-body auxiliary asset/);
	assert.match(buildCommands, /AWS_PROFILE=managed aws s3 cp "\$body_release_dir\/\$path" "s3:\/\/\$body_asset_bucket\/\$object_key"/);
	assert.match(buildCommands, /managed_auxiliary_assets_v1/);
	assert.match(buildCommands, /BODY_ASSET_BUCKET="cdk-hnb659fds-assets-\$TARGET_ACCOUNT_ID-\$TARGET_REGION"/);
	assert.doesNotMatch(buildCommands, /lesser-body-src/);
	assert.doesNotMatch(buildCommands, /npx cdk deploy --all/);
	assert.doesNotMatch(buildCommands, /npm ci/);
});

test('RUN_MODE=lesser-mcp uses the CLI binary with --release-dir', () => {
	assert.match(buildCommands, /prepare_lesser_checkout_dir "\$LESSER_RELEASE_DIR" "\$LESSER_CHECKOUT_DIR"/);
	assert.match(buildCommands, /ensure_lesser_go_toolchain "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /managed_instance_key:\$instance_key\[0\]/);
	assert.match(buildCommands, /cd "\$LESSER_CHECKOUT_DIR"/);
	assert.match(buildCommands, /"\$LESSER_RELEASE_DIR\/lesser" up --app "\$APP_SLUG" --base-domain "\$BASE_DOMAIN" --aws-profile managed --provisioning-input "\$PROVISION_INPUT" --release-dir "\$LESSER_RELEASE_DIR"/);
	assert.match(buildCommands, /mcp_lambda_arn/);
	assert.doesNotMatch(buildCommands, /cd infra\/cdk/);
	assert.doesNotMatch(buildCommands, /npx cdk deploy/);
	assert.doesNotMatch(buildCommands, /deploy_lesser_assembly_stack/);
	assert.doesNotMatch(buildCommands, /aws cloudformation deploy/);
});

test('Lesser phases serialize the explicit instance-plane flag into provisioning input', () => {
	assert.equal(
		buildCommands.match(/--arg instance_plane_enabled "\$\{INSTANCE_PLANE_ENABLED:-\}"/g)?.length,
		2,
	);
	assert.equal(
		buildCommands.match(/\.instance_plane_enabled = bool\(\$instance_plane_enabled\)/g)?.length,
		2,
	);
});

test('managed lesser-body default is the verified instance-plane baseline', () => {
	const cdkConfig = JSON.parse(readFileSync(join(__dirname, '..', '..', 'cdk.json'), 'utf8')) as {
		context?: { managedLesserBodyDefaultVersion?: string };
	};
	assert.equal(cdkConfig.context?.managedLesserBodyDefaultVersion, 'v1.0.8');
});

test('runner manages instance-key secret through managed profile receipt proof', () => {
	assert.match(buildCommands, /aws secretsmanager describe-secret --profile managed --secret-id "\$secret_ref"/);
	assert.match(buildCommands, /aws secretsmanager create-secret --profile managed/);
	assert.match(buildCommands, /aws secretsmanager update-secret --profile managed --secret-id "\$secret_arn"/);
	assert.match(buildCommands, /write_managed_instance_key_receipt "\$MANAGED_INSTANCE_KEY_RECEIPT_PATH"/);
	assert.match(buildCommands, /managed_instance_key:\$instance_key\[0\]/);
	assert.doesNotMatch(buildCommands, /secret:\$plaintext/);
});

test('runner manages soul-binding integration secret and passes one ARN to both children', () => {
	// Idempotent ensure through the managed profile, distinct from the instance key.
	assert.match(buildCommands, /ensure_soul_binding_integration_secret/);
	assert.match(buildCommands, /soul-binding-integration" "\$key_stage" "\$key_slug"/);
	assert.match(buildCommands, /lesser-host:soul-binding-key-id/);
	assert.match(buildCommands, /printf "lsbi_%s" "\$token"/);
	// Lesser receives the ARN via provisioning input and inherits the exported env var.
	assert.match(buildCommands, /--arg soul_binding_integration_key_arn "\$\{SOUL_BINDING_INTEGRATION_KEY_ARN:-\}"/);
	assert.match(buildCommands, /\.soul_binding_integration_key_arn = \$soul_binding_integration_key_arn/);
	assert.match(buildCommands, /"\$\{SOUL_BINDING_INTEGRATION_KEY_ARN:\?SOUL_BINDING_INTEGRATION_KEY_ARN is required\}"/);
	// lesser-body receives the exact same shell variable as its bearer secret ARN.
	assert.match(buildCommands, /LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN="\$SOUL_BINDING_INTEGRATION_KEY_ARN"/);
	// Receipts prove the ARN/key id without the bearer value.
	assert.match(buildCommands, /write_soul_binding_integration_receipt "\$SOUL_BINDING_INTEGRATION_RECEIPT_PATH"/);
	assert.match(buildCommands, /soul_binding_integration:\$soul_binding\[0\]/);
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
