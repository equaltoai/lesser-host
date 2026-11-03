import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { renderProvisionRunnerBuildCommands } from '../lib/provision-runner-buildspec';

const buildCommands = renderProvisionRunnerBuildCommands();
const HELPERS_PATH = join(__dirname, '..', '..', 'lib', 'provision-runner', 'helpers.sh');

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

test('runner reuses the one canonical soul-binding secret without rotation', () => {
	const start = buildCommands.indexOf('ensure_soul_binding_integration_secret()');
	const end = buildCommands.indexOf('vapid_secret_name()', start);
	assert.notEqual(start, -1);
	assert.notEqual(end, -1);
	const ensureBody = buildCommands.slice(start, end);

	assert.match(ensureBody, /secret_ref="\$\{SOUL_BINDING_INTEGRATION_KEY_ARN:-\}"/);
	assert.match(ensureBody, /if \[ -z "\$secret_ref" \]; then secret_ref=\$\(soul_binding_integration_secret_name\); fi/);
	assert.match(ensureBody, /describe-secret --profile managed --secret-id "\$secret_ref"/);
	assert.match(ensureBody, /validate_soul_binding_integration_secret_tags "\$desc_path"/);
	assert.match(ensureBody, /read_managed_instance_key_plaintext "\$secret_arn"/);
	assert.doesNotMatch(ensureBody, /update-secret|put-secret-value|SOUL_BINDING_INTEGRATION_ROTATE/);
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

test('runner provisions VAPID key per stage in the instance account and exports the lesser contract env', () => {
	// Shared setup runs the ensure for both lesser and lesser-mcp phases.
	assert.match(buildCommands, /ensure_lesser_host_instance_key_secret\nensure_soul_binding_integration_secret\nensure_vapid_key_secret/);
	// Secret identity mirrors lesser's scripts/ensure_vapid_credentials.sh (lesser/vapid-key-<stage>).
	assert.match(buildCommands, /printf "lesser\/vapid-key-%s" "\$key_stage"/);
	// Subject mirrors the script: mailto:push@<base-domain> with VAPID_SUBJECT_OVERRIDE respected.
	assert.match(buildCommands, /\$\{VAPID_SUBJECT_OVERRIDE:-mailto:push@\$\{BASE_DOMAIN\}\}/);
	// P-256 generation matches the script's DER trailing-65-byte urlsafe base64 encoding.
	assert.match(buildCommands, /openssl ecparam -name prime256v1 -genkey -noout -out "\$priv_path"/);
	assert.match(buildCommands, /tail -c 65 "\$pub_der_path" \| base64 -w0 \| tr '\+\/' '-_' \| tr -d '=\\n\\r'/);
	// Exported env vars are exactly what lesser up consumes (up.go/cdk.go): ARN, public key, subject.
	assert.match(buildCommands, /export VAPID_SECRET_ARN="\$secret_arn"/);
	assert.match(buildCommands, /export VAPID_PUBLIC_KEY="\$public_key"/);
	assert.match(buildCommands, /export VAPID_SUBJECT="\$resolved_subject"/);
	assert.match(buildCommands, /export VAPID_RECEIPT_PATH/);
	// The ensure writes the receipt after export.
	assert.match(buildCommands, /write_vapid_key_receipt "\$VAPID_RECEIPT_PATH" "\$secret_arn" "\$public_key" "\$resolved_subject" "\$reused" "\$regenerated"/);
});

test('runner reuses an existing VAPID secret without rotation', () => {
	const start = buildCommands.indexOf('ensure_vapid_key_secret() {');
	const end = buildCommands.indexOf('validate_https_custom_domain()', start);
	assert.notEqual(start, -1);
	assert.notEqual(end, -1);
	const ensureBody = buildCommands.slice(start, end);

	assert.match(ensureBody, /secret_ref="\$\{VAPID_SECRET_ARN:-\}"/);
	assert.match(ensureBody, /if \[ -z "\$secret_ref" \]; then secret_ref=\$\(vapid_secret_name\); fi/);
	assert.match(ensureBody, /describe-secret --profile managed --secret-id "\$secret_ref"/);
	// The read is fail-closed: stderr is captured and any read error aborts the
	// deploy; the old `2>/dev/null || echo ""` suppression is gone.
	assert.match(ensureBody, /if existing_secret=\$\(aws secretsmanager get-secret-value/);
	assert.match(ensureBody, /2>"\$secret_value_err"/);
	assert.doesNotMatch(ensureBody, /get-secret-value .*2>\/dev\/null/);
	assert.match(ensureBody, /fail "failed to read VAPID secret value: \$secret_arn"/);
	// A reused payload is validated, not just checked non-empty: 87-char
	// urlsafe public key + EC PEM private key, else fail closed naming the secret.
	assert.match(ensureBody, /grep -Eq '\^\[A-Za-z0-9_-\]\{87\}\$'/);
	assert.match(ensureBody, /grep -q '\^-----BEGIN EC PRIVATE KEY-----'/);
	assert.match(ensureBody, /fail "VAPID secret \$secret_arn has a malformed public key/);
	assert.match(ensureBody, /fail "VAPID secret \$secret_arn has a malformed private key/);
	// A pre-existing healthy secret is reused as-is: subject preserved, reused=true.
	assert.match(ensureBody, /resolved_subject="\$subject"/);
	assert.match(ensureBody, /reused="true"/);
	// The only in-place rewrite is a genuinely key-material-less payload, and it
	// is recorded: regenerated=true. No rotation flag exists for VAPID.
	assert.match(ensureBody, /regenerated="true"/);
	assert.doesNotMatch(ensureBody, /VAPID_SECRET_ARN_ROTATE|ROTATE_VAPID/);
});

test('runner creates a missing VAPID secret with the exact lesser payload schema', () => {
	const start = buildCommands.indexOf('ensure_vapid_key_secret() {');
	const end = buildCommands.indexOf('validate_https_custom_domain()', start);
	assert.notEqual(start, -1);
	assert.notEqual(end, -1);
	const ensureBody = buildCommands.slice(start, end);

	assert.match(ensureBody, /grep -q "ResourceNotFoundException" "\$desc_err"/);
	assert.match(ensureBody, /create-secret --profile managed \\/);
	assert.match(ensureBody, /--name "\$\(vapid_secret_name\)"/);
	// Payload schema matches ensure_vapid_credentials.sh: public_key, private_key, subject, created_at, updated_at.
	assert.match(ensureBody, /\{public_key:\$pub, private_key:\$priv, subject:\$sub, created_at:\$created, updated_at:\$now\}/);
	assert.match(ensureBody, /\{public_key:\$pub, private_key:\$priv, subject:\$sub, created_at:\$now, updated_at:\$now\}/);
	assert.match(ensureBody, /reused="false"/);
});

test('VAPID receipt records ARN/subject/state without ever touching the private key', () => {
	const start = buildCommands.indexOf('write_vapid_key_receipt() {');
	const end = buildCommands.indexOf('ensure_vapid_key_secret() {', start);
	assert.notEqual(start, -1);
	assert.notEqual(end, -1);
	const receiptBody = buildCommands.slice(start, end);

	assert.match(receiptBody, /\{version:1,source:\$source,secret_arn:\$secret_arn,public_key:\$public_key,subject:\$subject,instance_slug:\$instance_slug,stage:\$stage,reused:\(\$reused=="true"\),regenerated:\(\$regenerated=="true"\),verified_at:\$verified_at\}/);
	assert.doesNotMatch(receiptBody, /private_key/);
});

test('both lesser phases require VAPID_SECRET_ARN and embed the VAPID receipt into the managed receipt', () => {
	assert.equal(
		buildCommands.match(/VAPID_SECRET_ARN:\?VAPID_SECRET_ARN is required/g)?.length,
		2,
	);
	assert.equal(
		buildCommands.match(/case "\$VAPID_SECRET_ARN" in arn:\*\)/g)?.length,
		2,
	);
	assert.equal(
		buildCommands.match(/--slurpfile vapid "\$VAPID_RECEIPT_PATH"/g)?.length,
		2,
	);
	assert.equal(
		buildCommands.match(/vapid:\$vapid\[0\]/g)?.length,
		2,
	);
});

type VapidFixtureOpts = {
	existing: boolean;
	/** Existing-secret payload variant; 'valid' is the default. */
	variant?: 'valid' | 'corrupt' | 'malformed-pub' | 'unhealthy' | 'read-error';
	subjectOverride?: string;
	stage?: string;
};

function vapidFixtureHarness(): {
	run: (opts: VapidFixtureOpts) => {
		exports: Record<string, string>;
		receipt: Record<string, unknown>;
		calls: string[];
		payload?: Record<string, string>;
		putPayload?: Record<string, string>;
		payloadMode?: string;
		stderr: string;
	};
	runExpectFail: (opts: VapidFixtureOpts) => { status: number; stderr: string; calls: string[] };
} {
	const helpers = readFileSync(HELPERS_PATH, 'utf8');
	const secretValueFixtures: Record<string, string> = {
		valid: JSON.stringify({
			public_key: 'BL9E-mliZkOGWnOKLVZXPK177IKNiIyq3C1z4u-havKgNce467Ni3lzK1ogCQGRafdvCzATt6Bi_-O_puQPldh4',
			private_key: '-----BEGIN EC PRIVATE KEY-----\nFAKE\n-----END EC PRIVATE KEY-----',
			subject: 'mailto:push@lesser.host',
			created_at: '2026-08-01T00:00:00Z',
			updated_at: '2026-08-01T00:00:00Z',
		}),
		corrupt: 'this is not json {{{',
		'malformed-pub': JSON.stringify({
			public_key: 'AQID',
			private_key: '-----BEGIN EC PRIVATE KEY-----\nFAKE\n-----END EC PRIVATE KEY-----',
			subject: 'mailto:push@lesser.host',
			created_at: '2026-08-01T00:00:00Z',
			updated_at: '2026-08-01T00:00:00Z',
		}),
		unhealthy: JSON.stringify({
			public_key: '',
			private_key: '',
			subject: 'mailto:push@lesser.host',
			created_at: '2026-08-01T00:00:00Z',
			updated_at: '2026-08-01T00:00:00Z',
		}),
	};

	function prepare(opts: VapidFixtureOpts) {
		const work = mkdtempSync(join(tmpdir(), 'vapid-fixture-'));
		try {
			const binDir = join(work, 'bin');
			const stateDir = join(work, 'state');
			const callsPath = join(work, 'calls.log');
			mkdirSync(binDir);
			mkdirSync(stateDir);
			const fixtureDir = join(work, 'fixture');
			mkdirSync(fixtureDir);
			const stage = opts.stage ?? 'live';
			if (opts.existing) {
				writeFileSync(
					join(fixtureDir, 'describe.json'),
					JSON.stringify({
						ARN: `arn:aws:secretsmanager:us-east-1:111122223333:secret:lesser/vapid-key-${stage}-EXISTING`,
						Name: `lesser/vapid-key-${stage}`,
						Tags: [],
					}),
				);
				const variant = opts.variant ?? 'valid';
				if (variant !== 'read-error') {
					writeFileSync(join(fixtureDir, 'secret-value.txt'), secretValueFixtures[variant]);
				}
			}
			const stub = [
				'#!/bin/bash',
				'log() { printf "%s\\n" "$*" >> "${AWS_CALL_LOG:-/dev/null}"; }',
				'cmd="$1"; shift',
				'if [ "$cmd" = "secretsmanager" ]; then',
				'  sub="$1"; shift',
				'  case "$sub" in',
				'    describe-secret)',
				'      log "describe-secret $*"',
				'      if [ -f "$STUB_FIXTURE_DIR/describe.json" ]; then cat "$STUB_FIXTURE_DIR/describe.json"; exit 0; fi',
				'      echo "ResourceNotFoundException: Secret not found" >&2; exit 254',
				'      ;;',
				'    get-secret-value)',
				'      log "get-secret-value $*"',
				'      if [ "${STUB_GET_SECRET_VALUE_ERROR:-}" = "1" ]; then echo "An error occurred (ThrottlingException) when calling the GetSecretValue operation: Rate exceeded" >&2; exit 254; fi',
				'      if [ -f "$STUB_FIXTURE_DIR/secret-value.txt" ]; then cat "$STUB_FIXTURE_DIR/secret-value.txt"; exit 0; fi',
				'      echo "" >&2; exit 254',
				'      ;;',
				'    create-secret)',
				'      created_path=$(printf "%s" "$*" | grep -o "file://[^ ]*" | head -n 1 | sed "s|file://||")',
				'      if [ -n "$created_path" ] && [ -f "$created_path" ]; then cp "$created_path" "$STUB_FIXTURE_DIR/created-payload.json"; stat -c "%a" "$created_path" > "$STUB_FIXTURE_DIR/created-payload.mode"; fi',
				'      name=$(printf "%s" "$*" | grep -o -- "--name [^ ]*" | sed "s/--name //")',
				'      log "create-secret $*"',
				'      printf "%s\\n" "arn:aws:secretsmanager:us-east-1:111122223333:secret:${name}-ABC123"',
				'      exit 0',
				'      ;;',
				'    put-secret-value)',
				'      put_path=$(printf "%s" "$*" | grep -o "file://[^ ]*" | head -n 1 | sed "s|file://||")',
				'      if [ -n "$put_path" ] && [ -f "$put_path" ]; then cp "$put_path" "$STUB_FIXTURE_DIR/put-payload.json"; stat -c "%a" "$put_path" > "$STUB_FIXTURE_DIR/put-payload.mode"; fi',
				'      log "put-secret-value $*"',
				'      exit 0',
				'      ;;',
				'    *)',
				'      echo "unhandled: $sub" >&2; exit 1',
				'      ;;',
				'  esac',
				'fi',
				'echo "unhandled cmd: $cmd" >&2; exit 1',
			].join('\n');
			writeFileSync(join(binDir, 'aws'), stub, { mode: 0o755 });

			const env: Record<string, string> = {
				PATH: `${binDir}:${process.env.PATH ?? ''}`,
				STUB_FIXTURE_DIR: fixtureDir,
				STUB_GET_SECRET_VALUE_ERROR: opts.variant === 'read-error' ? '1' : '',
				AWS_CALL_LOG: callsPath,
				STATE_DIR: stateDir,
				STAGE: stage,
				APP_SLUG: 'acme',
				BASE_DOMAIN: 'lesser.host',
			};
			if (opts.subjectOverride) env.VAPID_SUBJECT_OVERRIDE = opts.subjectOverride;

			const script = [
				'set -euo pipefail',
				`source ${JSON.stringify(HELPERS_PATH)}`,
				'ensure_vapid_key_secret',
				'printf "VAPID_SECRET_ARN=%s\\n" "$VAPID_SECRET_ARN"',
				'printf "VAPID_PUBLIC_KEY=%s\\n" "$VAPID_PUBLIC_KEY"',
				'printf "VAPID_SUBJECT=%s\\n" "$VAPID_SUBJECT"',
				'printf "VAPID_RECEIPT_PATH=%s\\n" "$VAPID_RECEIPT_PATH"',
			].join('\n');
			return { work, binDir, stateDir, callsPath, fixtureDir, env, script };
		} catch (err) {
			rmSync(work, { recursive: true, force: true });
			throw err;
		}
	}

	function parseExports(stdout: string): Record<string, string> {
		const exports: Record<string, string> = {};
		for (const line of stdout.split('\n')) {
			const m = /^([A-Z_]+)=(.*)$/.exec(line);
			if (m) exports[m[1]] = m[2];
		}
		return exports;
	}

	function readCalls(callsPath: string): string[] {
		return readFileSync(callsPath, 'utf8').trim().split('\n').filter(Boolean);
	}

	return {
		run(opts) {
			const { work, callsPath, fixtureDir, env, script } = prepare(opts);
			try {
				const result = spawnSync('bash', ['-c', script], { encoding: 'utf8', env });
				assert.ifError(result.error);
				assert.equal(result.status, 0, `ensure_vapid_key_secret failed:\n${result.stderr}\n${result.stdout}`);

				const exports = parseExports(result.stdout);
				const calls = readCalls(callsPath);
				const createdPayloadPath = join(fixtureDir, 'created-payload.json');
				const putPayloadPath = join(fixtureDir, 'put-payload.json');
				const payload = existsSync(createdPayloadPath)
					? (JSON.parse(readFileSync(createdPayloadPath, 'utf8')) as Record<string, string>)
					: undefined;
				const putPayload = existsSync(putPayloadPath)
					? (JSON.parse(readFileSync(putPayloadPath, 'utf8')) as Record<string, string>)
					: undefined;
				const payloadMode = existsSync(join(fixtureDir, 'created-payload.mode'))
					? readFileSync(join(fixtureDir, 'created-payload.mode'), 'utf8').trim()
					: undefined;
				const receipt = JSON.parse(readFileSync(exports['VAPID_RECEIPT_PATH'] ?? '', 'utf8'));
				return { exports, receipt, calls, payload, putPayload, payloadMode, stderr: result.stderr ?? '' };
			} finally {
				rmSync(work, { recursive: true, force: true });
			}
		},
		runExpectFail(opts) {
			const { work, callsPath, env, script } = prepare(opts);
			try {
				const result = spawnSync('bash', ['-c', script], { encoding: 'utf8', env });
				assert.ifError(result.error);
				return { status: result.status ?? -1, stderr: result.stderr ?? '', calls: readCalls(callsPath) };
			} finally {
				rmSync(work, { recursive: true, force: true });
			}
		},
	};
}

test('VAPID fixture: reuses an existing secret (no create call, subject preserved)', () => {
	const { exports: env, receipt, calls } = vapidFixtureHarness().run({ existing: true });
	assert.equal(env.VAPID_SECRET_ARN, 'arn:aws:secretsmanager:us-east-1:111122223333:secret:lesser/vapid-key-live-EXISTING');
	assert.equal(env.VAPID_PUBLIC_KEY, 'BL9E-mliZkOGWnOKLVZXPK177IKNiIyq3C1z4u-havKgNce467Ni3lzK1ogCQGRafdvCzATt6Bi_-O_puQPldh4');
	assert.equal(env.VAPID_SUBJECT, 'mailto:push@lesser.host');
	assert.equal(receipt.reused, true);
	assert.equal(receipt.regenerated, false);
	assert.equal(receipt.secret_arn, env.VAPID_SECRET_ARN);
	assert.equal(receipt.stage, 'live');
	assert.equal(receipt.instance_slug, 'acme');
	assert.ok(calls.some((c) => c.startsWith('describe-secret')), 'describe-secret must be called');
	assert.ok(calls.some((c) => c.startsWith('get-secret-value')), 'get-secret-value must be called');
	assert.ok(!calls.some((c) => c.startsWith('create-secret')), 'reuse path must not create');
	assert.ok(!calls.some((c) => c.startsWith('put-secret-value')), 'reuse path must not rewrite');
});

test('VAPID fixture: creates a missing secret with the exact payload schema', () => {
	const { exports: env, receipt, calls, payload, payloadMode } = vapidFixtureHarness().run({ existing: false });
	assert.equal(env.VAPID_SECRET_ARN, 'arn:aws:secretsmanager:us-east-1:111122223333:secret:lesser/vapid-key-live-ABC123');
	assert.equal(env.VAPID_SUBJECT, 'mailto:push@lesser.host');
	assert.match(env.VAPID_PUBLIC_KEY, /^[A-Za-z0-9_-]{87}$/);
	assert.equal(receipt.reused, false);
	assert.equal(receipt.regenerated, false);
	assert.ok(calls.some((c) => c.startsWith('create-secret')), 'create path must call create-secret');
	// The payload written to Secrets Manager matches ensure_vapid_credentials.sh's schema.
	assert.ok(payload, 'create-secret payload must be captured');
	assert.equal(Object.keys(payload ?? {}).sort().join(','), 'created_at,private_key,public_key,subject,updated_at');
	assert.match(payload?.public_key ?? '', /^[A-Za-z0-9_-]{87}$/);
	assert.match(payload?.private_key ?? '', /^-----BEGIN EC PRIVATE KEY-----/);
	assert.equal(payload?.subject, 'mailto:push@lesser.host');
	assert.match(payload?.created_at ?? '', /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);
	assert.match(payload?.updated_at ?? '', /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);
	// Private-key-bearing payload file is written under umask 077 (mode 600).
	assert.equal(payloadMode, '600');
	// The receipt never carries the private key.
	assert.equal('private_key' in receipt, false);
});

test('VAPID fixture: honors VAPID_SUBJECT_OVERRIDE on create', () => {
	const { exports: env, payload } = vapidFixtureHarness().run({ existing: false, subjectOverride: 'mailto:ops@example.com' });
	assert.equal(env.VAPID_SUBJECT, 'mailto:ops@example.com');
	assert.equal(payload?.subject, 'mailto:ops@example.com');
});

test('VAPID fixture: fails closed when the secret value read fails (never regenerates in place)', () => {
	const { status, stderr, calls } = vapidFixtureHarness().runExpectFail({ existing: true, variant: 'read-error' });
	assert.notEqual(status, 0);
	assert.match(stderr, /failed to read VAPID secret value/);
	assert.match(stderr, /vapid-key-live-EXISTING/);
	assert.ok(!calls.some((c) => c.startsWith('put-secret-value')), 'a read failure must not regenerate/rewrite the secret');
	assert.ok(!calls.some((c) => c.startsWith('create-secret')), 'a read failure must not create a new secret');
});

test('VAPID fixture: fails closed on a corrupt (non-JSON) payload', () => {
	const { status, stderr } = vapidFixtureHarness().runExpectFail({ existing: true, variant: 'corrupt' });
	assert.notEqual(status, 0);
	assert.match(stderr, /VAPID secret .* has a corrupt \(non-JSON\) payload/);
	assert.match(stderr, /vapid-key-live-EXISTING/);
});

test('VAPID fixture: fails closed on a malformed public key', () => {
	const { status, stderr, calls } = vapidFixtureHarness().runExpectFail({ existing: true, variant: 'malformed-pub' });
	assert.notEqual(status, 0);
	assert.match(stderr, /malformed public key/);
	assert.match(stderr, /vapid-key-live-EXISTING/);
	assert.ok(!calls.some((c) => c.startsWith('put-secret-value')), 'a malformed payload must not be rewritten');
});

test('VAPID fixture: regenerates only a genuinely key-material-less secret and records regenerated=true', () => {
	const { exports: env, receipt, calls, putPayload, stderr } = vapidFixtureHarness().run({ existing: true, variant: 'unhealthy' });
	// The rewrite is loud: the log line names the secret.
	assert.match(stderr, /WARN: VAPID secret .* lacks key material; regenerating key pair in place/);
	assert.match(stderr, /vapid-key-live-EXISTING/);
	// Same secret object, keypair replaced in place: reused=true + regenerated=true.
	assert.equal(receipt.reused, true);
	assert.equal(receipt.regenerated, true);
	assert.equal(receipt.secret_arn, env.VAPID_SECRET_ARN);
	assert.match(env.VAPID_PUBLIC_KEY, /^[A-Za-z0-9_-]{87}$/);
	assert.ok(calls.some((c) => c.startsWith('put-secret-value')), 'regeneration must rewrite the existing secret in place');
	assert.ok(!calls.some((c) => c.startsWith('create-secret')), 'regeneration must not create a new secret');
	assert.ok(putPayload, 'put-secret-value payload must be captured');
	assert.match(putPayload?.public_key ?? '', /^[A-Za-z0-9_-]{87}$/);
	assert.match(putPayload?.private_key ?? '', /^-----BEGIN EC PRIVATE KEY-----/);
	// Subject and created_at are preserved across the regeneration.
	assert.equal(putPayload?.subject, 'mailto:push@lesser.host');
	assert.equal(putPayload?.created_at, '2026-08-01T00:00:00Z');
	assert.match(putPayload?.updated_at ?? '', /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);
	// The receipt never carries the private key.
	assert.equal('private_key' in receipt, false);
});

test('VAPID fixture: names the secret by normalized stage (STAGE=dev -> lesser/vapid-key-dev)', () => {
	const { exports: env, receipt, calls } = vapidFixtureHarness().run({ existing: false, stage: 'dev' });
	// Secret lookup and creation both target lesser/vapid-key-dev.
	assert.ok(calls.some((c) => c.startsWith('describe-secret') && c.includes('--secret-id lesser/vapid-key-dev')), 'describe-secret must target lesser/vapid-key-dev');
	assert.ok(calls.some((c) => c.startsWith('create-secret') && c.includes('--name lesser/vapid-key-dev')), 'create-secret must target lesser/vapid-key-dev');
	assert.match(env.VAPID_SECRET_ARN, /lesser\/vapid-key-dev/);
	assert.equal(receipt.stage, 'dev');
});
