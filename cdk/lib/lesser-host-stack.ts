import * as path from 'node:path';
import { execSync } from 'node:child_process';
import * as fs from 'node:fs';

import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as acm from 'aws-cdk-lib/aws-certificatemanager';
import * as cloudfront from 'aws-cdk-lib/aws-cloudfront';
import * as origins from 'aws-cdk-lib/aws-cloudfront-origins';
import * as codebuild from 'aws-cdk-lib/aws-codebuild';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
	import * as events from 'aws-cdk-lib/aws-events';
	import * as targets from 'aws-cdk-lib/aws-events-targets';
	import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
	import * as iam from 'aws-cdk-lib/aws-iam';
	import * as kms from 'aws-cdk-lib/aws-kms';
	import * as lambda from 'aws-cdk-lib/aws-lambda';
	import * as lambdaEventSources from 'aws-cdk-lib/aws-lambda-event-sources';
	import * as logs from 'aws-cdk-lib/aws-logs';
	import * as route53 from 'aws-cdk-lib/aws-route53';
		import * as route53Targets from 'aws-cdk-lib/aws-route53-targets';
		import * as s3 from 'aws-cdk-lib/aws-s3';
		import * as s3deploy from 'aws-cdk-lib/aws-s3-deployment';
		import * as ssm from 'aws-cdk-lib/aws-ssm';
		import * as sqs from 'aws-cdk-lib/aws-sqs';
		import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
		import * as apigwv2Integrations from 'aws-cdk-lib/aws-apigatewayv2-integrations';

export interface LesserHostStackProps extends cdk.StackProps {
	stage: string;
}

export class LesserHostStack extends cdk.Stack {
	private readonly namePrefix: string;

	constructor(scope: Construct, id: string, props: LesserHostStackProps) {
		super(scope, id, props);

		const appName = 'lesser-host';
		const stage = props.stage;
		this.namePrefix = `${appName}-${stage}`;
		const namePrefix = this.namePrefix;
		const removalPolicy = stage === 'live' ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY;

		const stateTable = new dynamodb.Table(this, 'StateTable', {
			tableName: `${namePrefix}-state`,
			partitionKey: { name: 'PK', type: dynamodb.AttributeType.STRING },
			sortKey: { name: 'SK', type: dynamodb.AttributeType.STRING },
			billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
			timeToLiveAttribute: 'ttl',
			pointInTimeRecoverySpecification: { pointInTimeRecoveryEnabled: true },
			removalPolicy,
		});

		stateTable.addGlobalSecondaryIndex({
			indexName: 'gsi1',
			partitionKey: { name: 'gsi1PK', type: dynamodb.AttributeType.STRING },
			sortKey: { name: 'gsi1SK', type: dynamodb.AttributeType.STRING },
			projectionType: dynamodb.ProjectionType.ALL,
		});

		const artifactsBucket = new s3.Bucket(this, 'ArtifactsBucket', {
			bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-artifacts`,
			blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
			enforceSSL: true,
			lifecycleRules: [
				{
					id: 'ExpireModerationInputs',
					prefix: 'moderation/',
					expiration: cdk.Duration.days(30),
				},
			],
			removalPolicy,
			autoDeleteObjects: stage !== 'live',
		});

		const previewDLQ = new sqs.Queue(this, 'PreviewDLQ', {
			queueName: `${namePrefix}-preview-dlq`,
			retentionPeriod: cdk.Duration.days(14),
		});
		previewDLQ.applyRemovalPolicy(removalPolicy);
		const previewQueue = new sqs.Queue(this, 'PreviewQueue', {
			queueName: `${namePrefix}-preview-queue`,
			deadLetterQueue: { queue: previewDLQ, maxReceiveCount: 3 },
		});
		previewQueue.applyRemovalPolicy(removalPolicy);

		const safetyDLQ = new sqs.Queue(this, 'SafetyDLQ', {
			queueName: `${namePrefix}-safety-dlq`,
			retentionPeriod: cdk.Duration.days(14),
		});
		safetyDLQ.applyRemovalPolicy(removalPolicy);
		const safetyQueue = new sqs.Queue(this, 'SafetyQueue', {
			queueName: `${namePrefix}-safety-queue`,
			deadLetterQueue: { queue: safetyDLQ, maxReceiveCount: 3 },
		});
		safetyQueue.applyRemovalPolicy(removalPolicy);

		const provisionDLQ = new sqs.Queue(this, 'ProvisionDLQ', {
			queueName: `${namePrefix}-provision-dlq`,
			retentionPeriod: cdk.Duration.days(14),
		});
		provisionDLQ.applyRemovalPolicy(removalPolicy);
		const provisionQueue = new sqs.Queue(this, 'ProvisionQueue', {
			queueName: `${namePrefix}-provision-queue`,
			deadLetterQueue: { queue: provisionDLQ, maxReceiveCount: 3 },
		});
		provisionQueue.applyRemovalPolicy(removalPolicy);

			const attestationSigningKey = new kms.Key(this, 'AttestationSigningKey', {
				description: `${namePrefix} attestation signing`,
				keySpec: kms.KeySpec.RSA_2048,
				keyUsage: kms.KeyUsage.SIGN_VERIFY,
				removalPolicy,
			});
			attestationSigningKey.addAlias(`alias/${namePrefix}-attestation-signing`);

			const soulPackBucket = new s3.Bucket(this, 'SoulPackBucket', {
				bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-soul-packs`,
				blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
				enforceSSL: true,
				versioned: true,
				removalPolicy,
				autoDeleteObjects: stage !== 'live',
			});

			const soulPackSigningKey = new kms.Key(this, 'SoulPackSigningKey', {
				description: `${namePrefix} lesser-soul pack signing`,
				keySpec: kms.KeySpec.RSA_2048,
				keyUsage: kms.KeyUsage.SIGN_VERIFY,
				removalPolicy,
			});
			soulPackSigningKey.addAlias(`alias/${namePrefix}-soul-pack-signing`);

			new ssm.StringParameter(this, 'SoulPackBucketNameParam', {
				parameterName: `/soul/${stage}/packBucketName`,
				stringValue: soulPackBucket.bucketName,
				description: `lesser-soul pack bucket name (${stage})`,
				tier: ssm.ParameterTier.STANDARD,
			});
			new ssm.StringParameter(this, 'SoulPackSigningKeyArnParam', {
				parameterName: `/soul/${stage}/signingKeyArn`,
				stringValue: soulPackSigningKey.keyArn,
				description: `lesser-soul pack signing key ARN (${stage})`,
				tier: ssm.ParameterTier.STANDARD,
			});
			new ssm.StringParameter(this, 'SoulPackVersionParam', {
				parameterName: `/soul/${stage}/packVersion`,
				stringValue: 'unset',
				description: `lesser-soul stage pack version pointer (${stage})`,
				tier: ssm.ParameterTier.STANDARD,
			});

		const bootstrapWalletAddress =
			(this.node.tryGetContext('bootstrapWalletAddress') as string | undefined) ?? '';
		const webAuthnRPID = (this.node.tryGetContext('webauthnRpId') as string | undefined) ?? '';
		const webAuthnOrigins = (this.node.tryGetContext('webauthnOrigins') as string | undefined) ?? '';

		const managedProvisioningEnabled =
			(this.node.tryGetContext('managedProvisioningEnabled') as string | undefined) ?? '';
		const managedOrgVendingRoleArn =
			(this.node.tryGetContext('managedOrgVendingRoleArn') as string | undefined) ?? '';
		const managedParentDomain = (this.node.tryGetContext('managedParentDomain') as string | undefined) ?? '';
		const managedParentHostedZoneId =
			(this.node.tryGetContext('managedParentHostedZoneId') as string | undefined) ?? '';
		const managedInstanceRoleName =
			(this.node.tryGetContext('managedInstanceRoleName') as string | undefined) ?? '';
		const managedTargetOuId = (this.node.tryGetContext('managedTargetOuId') as string | undefined) ?? '';
		const managedAccountEmailTemplateLab =
			(this.node.tryGetContext('managedAccountEmailTemplateLab') as string | undefined) ?? '';
		const managedAccountEmailTemplateLive =
			(this.node.tryGetContext('managedAccountEmailTemplateLive') as string | undefined) ?? '';
		const managedAccountEmailTemplateLegacy =
			(this.node.tryGetContext('managedAccountEmailTemplate') as string | undefined) ?? '';
		const managedAccountEmailTemplate =
			stage === 'live'
				? managedAccountEmailTemplateLive.trim() || managedAccountEmailTemplateLegacy.trim()
				: managedAccountEmailTemplateLab.trim() || managedAccountEmailTemplateLegacy.trim();
		const managedAccountNamePrefix =
			(this.node.tryGetContext('managedAccountNamePrefix') as string | undefined) ?? '';
		const managedDefaultRegion = (this.node.tryGetContext('managedDefaultRegion') as string | undefined) ?? '';
		const managedLesserDefaultVersion =
			(this.node.tryGetContext('managedLesserDefaultVersion') as string | undefined) ?? '';
			const managedProvisionRunnerProjectName =
				(this.node.tryGetContext('managedProvisionRunnerProjectName') as string | undefined) ?? '';
			const managedLesserGitHubOwner = (this.node.tryGetContext('managedLesserGitHubOwner') as string | undefined) ?? '';
			const managedLesserGitHubRepo = (this.node.tryGetContext('managedLesserGitHubRepo') as string | undefined) ?? '';
			const managedLesserGitHubTokenSsmParam =
				(this.node.tryGetContext('managedLesserGitHubTokenSsmParam') as string | undefined) ?? '';
			const managedLesserBodyDefaultVersion =
				(this.node.tryGetContext('managedLesserBodyDefaultVersion') as string | undefined) ?? '';
			const managedLesserBodyGitHubOwner =
				(this.node.tryGetContext('managedLesserBodyGitHubOwner') as string | undefined) ?? '';
			const managedLesserBodyGitHubRepo =
				(this.node.tryGetContext('managedLesserBodyGitHubRepo') as string | undefined) ?? '';

		const tipStageSuffix = stage === 'live' ? 'Live' : 'Lab';
		const tipContext = (key: string): string =>
			(this.node.tryGetContext(`${key}${tipStageSuffix}`) as string | undefined) ??
			(this.node.tryGetContext(key) as string | undefined) ??
			'';

		const tipEnabled = tipContext('tipEnabled');
		const tipChainId = tipContext('tipChainId');
		const tipContractAddress = tipContext('tipContractAddress');
		const tipAdminSafeAddress = tipContext('tipAdminSafeAddress');
		const tipDefaultHostWalletAddress = tipContext('tipDefaultHostWalletAddress');
		const tipDefaultHostFeeBps = tipContext('tipDefaultHostFeeBps');
		const tipTxMode = tipContext('tipTxMode');

		const tipRpcUrlSsmParam = tipContext('tipRpcUrlSsmParam').trim();

		const soulStageSuffix = stage === 'live' ? 'Live' : 'Lab';
		const soulContext = (key: string): string =>
			(this.node.tryGetContext(`${key}${soulStageSuffix}`) as string | undefined) ??
			(this.node.tryGetContext(key) as string | undefined) ??
			'';

		const soulEnabled = soulContext('soulEnabled');
		const soulChainId = soulContext('soulChainId');
		const soulRegistryContractAddress = soulContext('soulRegistryContractAddress');
		const soulReputationAttestationContractAddress = soulContext('soulReputationAttestationContractAddress');
		const soulValidationAttestationContractAddress = soulContext('soulValidationAttestationContractAddress');
		const soulRpcUrlSsmParam = soulContext('soulRpcUrlSsmParam').trim();
		const soulMintSignerKeySsmParam = soulContext('soulMintSignerKeySsmParam').trim();
		const soulAdminSafeAddress = soulContext('soulAdminSafeAddress');
		const soulTxMode = soulContext('soulTxMode');
		const soulSupportedCapabilities = soulContext('soulSupportedCapabilities');
		const soulReputationTipStartBlock = soulContext('soulReputationTipStartBlock');
		const soulReputationTipBlockChunkSize = soulContext('soulReputationTipBlockChunkSize');
		const soulReputationTipScale = soulContext('soulReputationTipScale');
		const soulReputationWeightEconomic = soulContext('soulReputationWeightEconomic');
		const soulReputationWeightSocial = soulContext('soulReputationWeightSocial');
		const soulReputationWeightValidation = soulContext('soulReputationWeightValidation');
		const soulReputationWeightTrust = soulContext('soulReputationWeightTrust');
		const soulValidationDecayEpochHours = soulContext('soulValidationDecayEpochHours');
		const soulValidationDecayRate = soulContext('soulValidationDecayRate');

		const paymentsProvider = (this.node.tryGetContext('paymentsProvider') as string | undefined) ?? '';
		const paymentsCentsPer1000Credits =
			(this.node.tryGetContext('paymentsCentsPer1000Credits') as string | undefined) ?? '';
		const paymentsCheckoutSuccessUrl =
			(this.node.tryGetContext('paymentsCheckoutSuccessUrl') as string | undefined) ?? '';
		const paymentsCheckoutCancelUrl = (this.node.tryGetContext('paymentsCheckoutCancelUrl') as string | undefined) ?? '';

			const provisionRunnerProjectName =
				managedProvisionRunnerProjectName.trim() || `${namePrefix}-provision-runner`;
			const lesserGitHubOwner = managedLesserGitHubOwner.trim() || 'equaltoai';
			const lesserGitHubRepo = managedLesserGitHubRepo.trim() || 'lesser';
			const lesserBodyGitHubOwner = managedLesserBodyGitHubOwner.trim() || 'equaltoai';
			const lesserBodyGitHubRepo = managedLesserBodyGitHubRepo.trim() || 'lesser-body';

			const provisionRunnerPreBuild = [
			'set -euo pipefail',
			'echo "Assuming role into target account..."',
			'if [ -n "${MANAGED_ORG_VENDING_ROLE_ARN:-}" ]; then',
			'  echo "Assuming org vending role..."',
			'  ORG_CREDS=$(aws sts assume-role --role-arn "$MANAGED_ORG_VENDING_ROLE_ARN" --role-session-name "lesser-host-org-$APP_SLUG" --duration-seconds 3600 --query "Credentials.[AccessKeyId,SecretAccessKey,SessionToken]" --output text)',
			'  read ORG_AK ORG_SK ORG_TOKEN <<< "$ORG_CREDS"',
			'  CREDS=$(AWS_ACCESS_KEY_ID=$ORG_AK AWS_SECRET_ACCESS_KEY=$ORG_SK AWS_SESSION_TOKEN=$ORG_TOKEN aws sts assume-role --role-arn "arn:aws:iam::$TARGET_ACCOUNT_ID:role/$TARGET_ROLE_NAME" --role-session-name "lesser-host-$APP_SLUG" --duration-seconds 3600 --query "Credentials.[AccessKeyId,SecretAccessKey,SessionToken]" --output text)',
			'else',
			'  CREDS=$(aws sts assume-role --role-arn "arn:aws:iam::$TARGET_ACCOUNT_ID:role/$TARGET_ROLE_NAME" --role-session-name "lesser-host-$APP_SLUG" --duration-seconds 3600 --query "Credentials.[AccessKeyId,SecretAccessKey,SessionToken]" --output text)',
			'fi',
			'read MANAGED_AK MANAGED_SK MANAGED_TOKEN <<< "$CREDS"',
			'mkdir -p ~/.aws',
			'printf "[managed]\\naws_access_key_id=%s\\naws_secret_access_key=%s\\naws_session_token=%s\\n" "$MANAGED_AK" "$MANAGED_SK" "$MANAGED_TOKEN" > ~/.aws/credentials',
			'printf "[profile managed]\\nregion=%s\\noutput=json\\n" "$TARGET_REGION" > ~/.aws/config',
			'aws sts get-caller-identity --profile managed',
		].join('\n');

			const provisionRunnerBuild = [
				'set -euo pipefail',
				'RUN_MODE="${RUN_MODE:-lesser}"',
				'OWNER="${GITHUB_OWNER:-equaltoai}"',
				'REPO="${GITHUB_REPO:-lesser}"',
				'TOKEN="${GITHUB_TOKEN:-}"',
			'TAG="${LESSER_VERSION:-}"',
			'if [ -z "$TAG" ]; then',
			'  echo "Resolving latest Lesser release..."',
			'  if [ -n "$TOKEN" ]; then',
			'    TAG=$(curl -sSfL -H "Accept: application/vnd.github+json" -H "Authorization: Bearer $TOKEN" "https://api.github.com/repos/$OWNER/$REPO/releases/latest" | jq -r .tag_name)',
			'  else',
			'    TAG=$(curl -sSfL -H "Accept: application/vnd.github+json" "https://api.github.com/repos/$OWNER/$REPO/releases/latest" | jq -r .tag_name)',
			'  fi',
			'fi',
			'test -n "$TAG"',
			'test "$TAG" != "null"',
			'echo "Using Lesser release: $TAG"',
			'if [ -n "$TOKEN" ]; then',
			'  curl -sSfL -H "Accept: application/vnd.github+json" -H "Authorization: Bearer $TOKEN" -o lesser-src.tgz "https://api.github.com/repos/$OWNER/$REPO/tarball/$TAG"',
			'  RELEASE_JSON=$(curl -sSfL -H "Accept: application/vnd.github+json" -H "Authorization: Bearer $TOKEN" "https://api.github.com/repos/$OWNER/$REPO/releases/tags/$TAG")',
			'else',
			'  curl -sSfL -H "Accept: application/vnd.github+json" -o lesser-src.tgz "https://api.github.com/repos/$OWNER/$REPO/tarball/$TAG"',
			'  RELEASE_JSON=$(curl -sSfL -H "Accept: application/vnd.github+json" "https://api.github.com/repos/$OWNER/$REPO/releases/tags/$TAG")',
			'fi',
			'mkdir -p lesser-src && tar -xzf lesser-src.tgz --strip-components=1 -C lesser-src',
			'ARCH=$(uname -m)',
			'if [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "amd64" ]; then BIN_NAME="lesser-linux-amd64"; fi',
			'if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then BIN_NAME="lesser-linux-arm64"; fi',
			'test -n "${BIN_NAME:-}"',
			'BIN_ID=$(echo "$RELEASE_JSON" | jq -r --arg name "$BIN_NAME" \'.assets[] | select(.name==$name) | .id\' | head -n 1)',
			'CHK_ID=$(echo "$RELEASE_JSON" | jq -r \'.assets[] | select(.name=="checksums.txt") | .id\' | head -n 1)',
			'test -n "$BIN_ID" && test "$BIN_ID" != "null"',
			'test -n "$CHK_ID" && test "$CHK_ID" != "null"',
			'if [ -n "$TOKEN" ]; then',
			'  curl -sSfL -H "Authorization: Bearer $TOKEN" -H "Accept: application/octet-stream" -o lesser-src/lesser "https://api.github.com/repos/$OWNER/$REPO/releases/assets/$BIN_ID"',
			'  curl -sSfL -H "Authorization: Bearer $TOKEN" -H "Accept: application/octet-stream" -o checksums.txt "https://api.github.com/repos/$OWNER/$REPO/releases/assets/$CHK_ID"',
			'else',
			'  curl -sSfL -H "Accept: application/octet-stream" -o lesser-src/lesser "https://api.github.com/repos/$OWNER/$REPO/releases/assets/$BIN_ID"',
			'  curl -sSfL -H "Accept: application/octet-stream" -o checksums.txt "https://api.github.com/repos/$OWNER/$REPO/releases/assets/$CHK_ID"',
			'fi',
			'EXPECTED=$(grep -E "(\\\\s|\\\\*)$BIN_NAME$" checksums.txt | awk \'{print $1}\' | head -n 1)',
			'test -n "$EXPECTED"',
			'ACTUAL=$(sha256sum lesser-src/lesser | awk \'{print $1}\')',
			'test "$EXPECTED" = "$ACTUAL"',
			'chmod +x lesser-src/lesser',
			'cd lesser-src',
			'GO_TOOLCHAIN=$(grep "^toolchain " go.mod | awk \'{print $2}\' || true)',
			'GO_VERSION="${GO_TOOLCHAIN#go}"',
			'if [ -z "$GO_VERSION" ]; then GO_VERSION=$(grep "^go " go.mod | awk \'{print $2}\'); fi',
			'test -n "$GO_VERSION"',
			'GO_ARCH="amd64"',
			'if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then GO_ARCH="arm64"; fi',
			'echo "Installing Go toolchain: $GO_VERSION (linux-$GO_ARCH)"',
			'curl -sSfL -o go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"',
			'rm -rf /usr/local/go && tar -C /usr/local -xzf go.tgz',
			'export PATH="/usr/local/go/bin:$PATH"',
			'go version',
			'STATE_DIR="$HOME/.lesser/$APP_SLUG/$BASE_DOMAIN"',
			'mkdir -p "$STATE_DIR"',
			'aws s3 cp "s3://$ARTIFACT_BUCKET/$BOOTSTRAP_S3_KEY" "$STATE_DIR/bootstrap.json" 2>/dev/null || true',
			'CONSENT_MESSAGE=""',
			'if [ -n "${CONSENT_MESSAGE_B64:-}" ]; then CONSENT_MESSAGE=$(printf "%s" "$CONSENT_MESSAGE_B64" | base64 --decode); fi',
			'PROVISION_INPUT="$STATE_DIR/provision.json"',
			'fail() { echo "ERROR: $1" >&2; exit 1; }',
			': "${LESSER_HOST_URL:?LESSER_HOST_URL is required}"',
			'LESSER_HOST_ATTESTATIONS_URL="${LESSER_HOST_ATTESTATIONS_URL:-$LESSER_HOST_URL}"',
			': "${LESSER_HOST_INSTANCE_KEY_ARN:?LESSER_HOST_INSTANCE_KEY_ARN is required}"',
			'validate_https_custom_domain() {',
			'  NAME="$1"',
			'  VALUE="$2"',
			'  if [ -z "$VALUE" ]; then fail "$NAME is empty"; fi',
			'  case "$VALUE" in https://*) ;; *) fail "$NAME must start with https:// (got: $VALUE)";; esac',
			'  case "$VALUE" in *.lambda-url.*|*amazonaws.com*|*.on.aws*|*cloudfront.net*) fail "$NAME must be a custom domain URL, not an AWS-generated hostname (got: $VALUE)";; esac',
			'}',
			'validate_https_custom_domain "LESSER_HOST_URL" "$LESSER_HOST_URL"',
			'validate_https_custom_domain "LESSER_HOST_ATTESTATIONS_URL" "$LESSER_HOST_ATTESTATIONS_URL"',
			'case "$LESSER_HOST_INSTANCE_KEY_ARN" in arn:*) ;; *) fail "LESSER_HOST_INSTANCE_KEY_ARN must start with arn:";; esac',
			'bool_on() {',
			'  v=$(printf "%s" "$1" | tr "[:upper:]" "[:lower:]")',
			'  case "$v" in true|1|yes|on) return 0 ;; *) return 1 ;; esac',
			'}',
			'if bool_on "${TIP_ENABLED:-}"; then',
			'  if [ -z "${TIP_CHAIN_ID:-}" ]; then fail "TIP_CHAIN_ID is required when TIP_ENABLED=true"; fi',
			'  case "$TIP_CHAIN_ID" in *[!0-9]*|"") fail "TIP_CHAIN_ID must be a positive integer when TIP_ENABLED=true";; 0) fail "TIP_CHAIN_ID must be > 0 when TIP_ENABLED=true";; esac',
			'  if [ -z "${TIP_CONTRACT_ADDRESS:-}" ]; then fail "TIP_CONTRACT_ADDRESS is required when TIP_ENABLED=true"; fi',
				'fi',
				'jq -n --arg slug "$APP_SLUG" --arg stage "$STAGE" --arg admin_wallet_address "$ADMIN_WALLET_ADDRESS" --arg admin_username "$ADMIN_USERNAME" --arg admin_wallet_chain_id "${ADMIN_WALLET_CHAIN_ID:-}" --arg consent_message "$CONSENT_MESSAGE" --arg consent_signature "${CONSENT_SIGNATURE:-}" --arg lesser_host_url "${LESSER_HOST_URL:-}" --arg lesser_host_attestations_url "${LESSER_HOST_ATTESTATIONS_URL:-}" --arg lesser_host_instance_key_arn "${LESSER_HOST_INSTANCE_KEY_ARN:-}" --arg translation_enabled "${TRANSLATION_ENABLED:-}" --arg tip_enabled "${TIP_ENABLED:-}" --arg tip_chain_id "${TIP_CHAIN_ID:-}" --arg tip_contract_address "${TIP_CONTRACT_ADDRESS:-}" --arg ai_enabled "${AI_ENABLED:-}" --arg ai_moderation_enabled "${AI_MODERATION_ENABLED:-}" --arg ai_nsfw_detection_enabled "${AI_NSFW_DETECTION_ENABLED:-}" --arg ai_spam_detection_enabled "${AI_SPAM_DETECTION_ENABLED:-}" --arg ai_pii_detection_enabled "${AI_PII_DETECTION_ENABLED:-}" --arg ai_content_detection_enabled "${AI_CONTENT_DETECTION_ENABLED:-}" \'def bool($v): ($v|ascii_downcase) as $x | ($x=="true" or $x=="1" or $x=="yes" or $x=="on"); {"schema":1,"slug":$slug,"stage":$stage,"admin_wallet_address":$admin_wallet_address,"admin_username":$admin_username} | if $admin_wallet_chain_id != "" then .admin_wallet_chain_id = ($admin_wallet_chain_id|tonumber) else . end | if $consent_message != "" then .consent_message = $consent_message else . end | if $consent_signature != "" then .consent_signature = $consent_signature else . end | if $lesser_host_url != "" then .lesser_host_url = $lesser_host_url else . end | if $lesser_host_attestations_url != "" then .lesser_host_attestations_url = $lesser_host_attestations_url elif $lesser_host_url != "" then .lesser_host_attestations_url = $lesser_host_url else . end | if $lesser_host_instance_key_arn != "" then .lesser_host_instance_key_arn = $lesser_host_instance_key_arn else . end | if $translation_enabled != "" then .translation_enabled = bool($translation_enabled) else . end | if $tip_enabled != "" then .tip_enabled = bool($tip_enabled) else . end | if $tip_chain_id != "" then .tip_chain_id = ($tip_chain_id|tonumber) else . end | if $tip_contract_address != "" then .tip_contract_address = $tip_contract_address else . end | if $ai_enabled != "" then .ai_enabled = bool($ai_enabled) else . end | if $ai_moderation_enabled != "" then .ai_moderation_enabled = bool($ai_moderation_enabled) else . end | if $ai_nsfw_detection_enabled != "" then .ai_nsfw_detection_enabled = bool($ai_nsfw_detection_enabled) else . end | if $ai_spam_detection_enabled != "" then .ai_spam_detection_enabled = bool($ai_spam_detection_enabled) else . end | if $ai_pii_detection_enabled != "" then .ai_pii_detection_enabled = bool($ai_pii_detection_enabled) else . end | if $ai_content_detection_enabled != "" then .ai_content_detection_enabled = bool($ai_content_detection_enabled) else . end\' > "$PROVISION_INPUT"',
				'STAGE_DOMAIN="$BASE_DOMAIN"',
				'if [ "$STAGE" != "live" ]; then STAGE_DOMAIN="$STAGE.$BASE_DOMAIN"; fi',
					'SOUL_STAGE="lab"',
					'if [ "$STAGE" = "live" ]; then SOUL_STAGE="live"; fi',
					'SOUL_INSTANCE_DOMAIN="$STAGE_DOMAIN"',
					'fetch_and_verify_soul_pack() {',
					'  VERSION="${SOUL_VERSION:-}"',
					'  if [ -z "$VERSION" ]; then',
					'    VERSION=$(aws ssm get-parameter --name "/soul/$SOUL_STAGE/packVersion" --query "Parameter.Value" --output text)',
					'  fi',
					'  test -n "$VERSION" && test "$VERSION" != "null" && test "$VERSION" != "unset"',
					'  PACK_BUCKET=$(aws ssm get-parameter --name "/soul/$SOUL_STAGE/packBucketName" --query "Parameter.Value" --output text)',
					'  SIGNING_KEY_ARN=$(aws ssm get-parameter --name "/soul/$SOUL_STAGE/signingKeyArn" --query "Parameter.Value" --output text)',
					'  test -n "$PACK_BUCKET" && test "$PACK_BUCKET" != "null"',
					'  test -n "$SIGNING_KEY_ARN" && test "$SIGNING_KEY_ARN" != "null"',
					'  PACK_TGZ="soul-pack-${VERSION}.tgz"',
					'  MANIFEST="soul-pack-${VERSION}.manifest.json"',
					'  SIG="soul-pack-${VERSION}.manifest.sig"',
					'  aws s3 cp "s3://$PACK_BUCKET/$PACK_TGZ" "/tmp/$PACK_TGZ"',
					'  aws s3 cp "s3://$PACK_BUCKET/$MANIFEST" "/tmp/$MANIFEST"',
					'  aws s3 cp "s3://$PACK_BUCKET/$SIG" "/tmp/$SIG"',
					'  openssl dgst -sha256 -binary "/tmp/$MANIFEST" > "/tmp/$MANIFEST.sha256"',
					'  VERIFY_JSON=$(aws kms verify --key-id "$SIGNING_KEY_ARN" --message-type DIGEST --message "fileb:///tmp/$MANIFEST.sha256" --signature "fileb:///tmp/$SIG" --signing-algorithm RSASSA_PSS_SHA_256)',
					'  if [ "$(echo "$VERIFY_JSON" | jq -r ".SignatureValid")" != "true" ]; then fail "soul pack manifest signature invalid: $VERSION"; fi',
					'  if tar -tzf "/tmp/$PACK_TGZ" | grep -E "(^/|\\.\\./|/\\.\\./)" >/dev/null; then fail "soul pack contains unsafe paths"; fi',
					'  rm -rf /tmp/soul-pack && mkdir -p /tmp/soul-pack',
					'  tar -xzf "/tmp/$PACK_TGZ" -C /tmp/soul-pack',
					'  jq -r \'.files[] | "\\(.sha256)  \\(.path)"\' "/tmp/$MANIFEST" > /tmp/soul-pack.checksums',
					'  (cd /tmp/soul-pack && sha256sum -c /tmp/soul-pack.checksums)',
					'  jq -r \'.files[].path\' "/tmp/$MANIFEST" | sort > /tmp/soul-pack.expected',
					'  (cd /tmp/soul-pack && find . -type f -print | sed "s|^./||" | sort > /tmp/soul-pack.actual)',
					'  if ! diff -u /tmp/soul-pack.expected /tmp/soul-pack.actual >/dev/null; then',
					'    echo "ERROR: soul pack file set mismatch ($VERSION)" >&2',
					'    diff -u /tmp/soul-pack.expected /tmp/soul-pack.actual || true',
					'    exit 1',
					'  fi',
					'  SOUL_PACK_DIR="/tmp/soul-pack"',
					'  SOUL_PACK_VERSION="$VERSION"',
					'}',
					'if [ "$RUN_MODE" = "lesser" ]; then',
				'  ./lesser up --app "$APP_SLUG" --base-domain "$BASE_DOMAIN" --aws-profile managed --provisioning-input "$PROVISION_INPUT"',
				'  if [ -n "${CONSENT_MESSAGE_B64:-}" ] && [ -n "${CONSENT_SIGNATURE:-}" ]; then ./lesser init-admin --base-domain "$BASE_DOMAIN" --aws-profile managed --provisioning-input "$PROVISION_INPUT"; else echo "Skipping init-admin (missing consent message/signature)."; fi',
						'  RECEIPT_PATH="$STATE_DIR/state.json"',
						'  test -f "$RECEIPT_PATH"',
						'  aws s3 cp "$RECEIPT_PATH" "s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY"',
						'  if [ -f /tmp/bootstrap.json ]; then aws s3 cp /tmp/bootstrap.json "s3://$ARTIFACT_BUCKET/$BOOTSTRAP_S3_KEY"; fi',
						'elif [ "$RUN_MODE" = "lesser-body" ]; then',
						'  echo "Deploying lesser-body (AgentCore MCP)..."',
						'  BODY_OWNER="${LESSER_BODY_GITHUB_OWNER:-equaltoai}"',
						'  BODY_REPO="${LESSER_BODY_GITHUB_REPO:-lesser-body}"',
						'  BODY_TAG="${LESSER_BODY_VERSION:-}"',
						'  if [ -z "$BODY_TAG" ]; then',
						'    echo "Resolving latest lesser-body release..."',
						'    if [ -n "$TOKEN" ]; then',
						'      BODY_TAG=$(curl -sSfL -H "Accept: application/vnd.github+json" -H "Authorization: Bearer $TOKEN" "https://api.github.com/repos/$BODY_OWNER/$BODY_REPO/releases/latest" | jq -r .tag_name)',
						'    else',
						'      BODY_TAG=$(curl -sSfL -H "Accept: application/vnd.github+json" "https://api.github.com/repos/$BODY_OWNER/$BODY_REPO/releases/latest" | jq -r .tag_name)',
						'    fi',
						'  fi',
						'  test -n "$BODY_TAG" && test "$BODY_TAG" != "null"',
						'  echo "Using lesser-body release: $BODY_TAG"',
						'  if [ -n "$TOKEN" ]; then',
						'    curl -sSfL -H "Accept: application/vnd.github+json" -H "Authorization: Bearer $TOKEN" -o lesser-body-src.tgz "https://api.github.com/repos/$BODY_OWNER/$BODY_REPO/tarball/$BODY_TAG"',
						'  else',
						'    curl -sSfL -H "Accept: application/vnd.github+json" -o lesser-body-src.tgz "https://api.github.com/repos/$BODY_OWNER/$BODY_REPO/tarball/$BODY_TAG"',
						'  fi',
						'  rm -rf lesser-body-src && mkdir -p lesser-body-src && tar -xzf lesser-body-src.tgz --strip-components=1 -C lesser-body-src',
						'  cd lesser-body-src/cdk',
						'  npm ci',
						'  AWS_PROFILE=managed npx cdk deploy --all --require-approval never -c app="$APP_SLUG" -c stage="$STAGE" -c baseDomain="$BASE_DOMAIN"',
						'  cd - >/dev/null',
						'  BODY_PARAM="/$APP_SLUG/$STAGE/lesser-body/exports/v1/mcp_lambda_arn"',
						'  BODY_LAMBDA_ARN=$(aws ssm get-parameter --profile managed --name "$BODY_PARAM" --query "Parameter.Value" --output text)',
						'  test -n "$BODY_LAMBDA_ARN" && test "$BODY_LAMBDA_ARN" != "null"',
						'  BODY_RECEIPT_PATH="$STATE_DIR/body-state.json"',
						'  jq -n --arg stage "$STAGE" --arg base_domain "$BASE_DOMAIN" --arg mcp_url "https://api.$STAGE_DOMAIN/mcp" --arg version "$BODY_TAG" --arg mcp_lambda_arn "$BODY_LAMBDA_ARN" \'{version:1,stage:$stage,base_domain:$base_domain,mcp_url:$mcp_url,lesser_body_version:$version,mcp_lambda_arn:$mcp_lambda_arn}\' > "$BODY_RECEIPT_PATH"',
						'  aws s3 cp "$BODY_RECEIPT_PATH" "s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY"',
						'elif [ "$RUN_MODE" = "lesser-mcp" ]; then',
						'  echo "Wiring POST /mcp on existing Lesser API domain..."',
						'  HOSTED_ZONE_NAME="${BASE_DOMAIN%.}."',
						'  HOSTED_ZONE_ID=$(aws route53 list-hosted-zones-by-name --profile managed --dns-name "$HOSTED_ZONE_NAME" --output json | jq -r --arg name "$HOSTED_ZONE_NAME" \'.HostedZones[] | select(.Name==$name and (.Config.PrivateZone|not)) | .Id\' | head -n 1)',
						'  test -n "$HOSTED_ZONE_ID" && test "$HOSTED_ZONE_ID" != "null"',
						'  HOSTED_ZONE_ID="${HOSTED_ZONE_ID#/hostedzone/}"',
						'  cd infra/cdk',
						'  AWS_PROFILE=managed cdk deploy "$APP_SLUG-$STAGE" --require-approval never -c app="$APP_SLUG" -c baseDomain="$BASE_DOMAIN" -c hostedZoneId="$HOSTED_ZONE_ID" -c stage="$STAGE" -c soulEnabled=true -c lesserHostUrl="$LESSER_HOST_URL" -c lesserHostAttestationsUrl="$LESSER_HOST_ATTESTATIONS_URL" -c lesserHostInstanceKeyArn="$LESSER_HOST_INSTANCE_KEY_ARN" -c translationEnabled="$TRANSLATION_ENABLED" -c tipEnabled="$TIP_ENABLED" -c tipChainId="${TIP_CHAIN_ID:-}" -c tipContractAddress="${TIP_CONTRACT_ADDRESS:-}"',
						'  cd - >/dev/null',
						'  MCP_RECEIPT_PATH="$STATE_DIR/mcp-state.json"',
						'  jq -n --arg stage "$STAGE" --arg base_domain "$BASE_DOMAIN" --arg mcp_url "https://api.$STAGE_DOMAIN/mcp" \'{version:1,stage:$stage,base_domain:$base_domain,mcp_url:$mcp_url}\' > "$MCP_RECEIPT_PATH"',
						'  aws s3 cp "$MCP_RECEIPT_PATH" "s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY"',
						'elif [ "$RUN_MODE" = "soul-deploy" ]; then',
						'  echo "Deploying lesser-soul pack..."',
						'  fetch_and_verify_soul_pack',
						'  echo "Deploying lesser-soul version: $SOUL_PACK_VERSION"',
					'  cd "$SOUL_PACK_DIR/infra/cdk"',
					'  npm ci',
					'  AWS_PROFILE=managed npx cdk deploy --all --require-approval never -c stage="$SOUL_STAGE" -c instanceDomain="$SOUL_INSTANCE_DOMAIN"',
					'  cd - >/dev/null',
					'elif [ "$RUN_MODE" = "soul-init" ]; then',
					'  echo "Bootstrapping lesser-soul from signed pack..."',
					'  fetch_and_verify_soul_pack',
					'  echo "Bootstrapping lesser-soul version: $SOUL_PACK_VERSION"',
					'  if [ ! -f "$STATE_DIR/bootstrap.json" ]; then fail "bootstrap key material missing: $STATE_DIR/bootstrap.json"; fi',
				'  BOOTSTRAP_ADDR=$(jq -r ".wallet.address" "$STATE_DIR/bootstrap.json")',
				'  BOOTSTRAP_MNEMONIC=$(jq -r ".wallet.mnemonic" "$STATE_DIR/bootstrap.json")',
				'  BOOTSTRAP_PATH=$(jq -r ".wallet.derivation_path" "$STATE_DIR/bootstrap.json")',
				'  BOOTSTRAP_CHAIN_ID=$(jq -r ".wallet.chain_id" "$STATE_DIR/bootstrap.json")',
				'  test -n "$BOOTSTRAP_ADDR" && test "$BOOTSTRAP_ADDR" != "null"',
				'  test -n "$BOOTSTRAP_MNEMONIC" && test "$BOOTSTRAP_MNEMONIC" != "null"',
				'  test -n "$BOOTSTRAP_PATH" && test "$BOOTSTRAP_PATH" != "null"',
				'  test -n "$BOOTSTRAP_CHAIN_ID" && test "$BOOTSTRAP_CHAIN_ID" != "null"',
				'  CHALLENGE_BODY=$(jq -nc --arg username "bootstrap" --arg address "$BOOTSTRAP_ADDR" --argjson chainId "$BOOTSTRAP_CHAIN_ID" "{username:$username,address:$address,chainId:$chainId}")',
				'  CHALLENGE_JSON=$(curl -sSfL -X POST "https://$STAGE_DOMAIN/auth/wallet/challenge" -H "Content-Type: application/json" -d "$CHALLENGE_BODY")',
				'  CHALLENGE_ID=$(echo "$CHALLENGE_JSON" | jq -r ".id")',
				'  CHALLENGE_MESSAGE=$(echo "$CHALLENGE_JSON" | jq -r ".message")',
				'  test -n "$CHALLENGE_ID" && test "$CHALLENGE_ID" != "null"',
				'  test -n "$CHALLENGE_MESSAGE" && test "$CHALLENGE_MESSAGE" != "null"',
				'  SIGNER_PATH="sign_wallet_message.go"',
				'  cat > "$SIGNER_PATH" <<EOF',
				'package main',
				'',
				'import (',
				'  "encoding/hex"',
				'  "encoding/json"',
				'  "fmt"',
				'  "io"',
				'  "os"',
				'  "strings"',
				'',
				'  "github.com/ethereum/go-ethereum/accounts"',
				'  "github.com/ethereum/go-ethereum/crypto"',
				'  "github.com/tyler-smith/go-bip32"',
				'  "github.com/tyler-smith/go-bip39"',
				')',
				'',
				'type bootstrapWallet struct {',
				'  Address string `json:"address"`',
				'  Mnemonic string `json:"mnemonic"`',
				'  DerivationPath string `json:"derivation_path"`',
				'}',
				'',
				'type payload struct { Wallet bootstrapWallet `json:"wallet"` }',
				'',
				'func main() {',
				'  if len(os.Args) != 2 {',
				'    fmt.Fprintln(os.Stderr, "usage: sign_wallet_message <bootstrap.json> (message via stdin)")',
				'    os.Exit(2)',
				'  }',
				'  raw, err := os.ReadFile(strings.TrimSpace(os.Args[1]))',
				'  if err != nil {',
				'    fmt.Fprintln(os.Stderr, err)',
				'    os.Exit(1)',
				'  }',
				'  var p payload',
				'  if err := json.Unmarshal(raw, &p); err != nil {',
				'    fmt.Fprintln(os.Stderr, err)',
				'    os.Exit(1)',
				'  }',
				'  msgBytes, err := io.ReadAll(os.Stdin)',
				'  if err != nil {',
				'    fmt.Fprintln(os.Stderr, err)',
				'    os.Exit(1)',
				'  }',
				'  msg := string(msgBytes)',
				'',
				'  mnemonic := strings.TrimSpace(p.Wallet.Mnemonic)',
				'  if mnemonic == "" {',
				'    fmt.Fprintln(os.Stderr, "missing mnemonic")',
				'    os.Exit(1)',
				'  }',
				'  path := strings.TrimSpace(p.Wallet.DerivationPath)',
				'  if path == "" {',
				'    path = "m/44\\x27/60\\x27/0\\x27/0/0"',
				'  }',
				'  seed := bip39.NewSeed(mnemonic, "")',
				'  derivation, err := accounts.ParseDerivationPath(path)',
				'  if err != nil {',
				'    fmt.Fprintln(os.Stderr, err)',
				'    os.Exit(1)',
				'  }',
				'',
				'  masterKey, err := bip32.NewMasterKey(seed)',
				'  if err != nil {',
				'    fmt.Fprintln(os.Stderr, err)',
				'    os.Exit(1)',
				'  }',
				'  key := masterKey',
				'  for _, c := range derivation {',
				'    key, err = key.NewChildKey(c)',
				'    if err != nil {',
				'      fmt.Fprintln(os.Stderr, err)',
				'      os.Exit(1)',
				'    }',
				'  }',
				'',
				'  privBytes := key.Key',
				'  if len(privBytes) == 33 && privBytes[0] == 0x00 {',
				'    privBytes = privBytes[1:]',
				'  }',
				'  if len(privBytes) != 32 {',
				'    fmt.Fprintln(os.Stderr, "unexpected derived private key length")',
				'    os.Exit(1)',
				'  }',
				'  priv, err := crypto.ToECDSA(privBytes)',
				'  if err != nil {',
				'    fmt.Fprintln(os.Stderr, err)',
				'    os.Exit(1)',
				'  }',
				'',
				'  hash := accounts.TextHash([]byte(msg))',
				'  sig, err := crypto.Sign(hash, priv)',
				'  if err != nil {',
				'    fmt.Fprintln(os.Stderr, err)',
				'    os.Exit(1)',
				'  }',
				'  fmt.Printf("0x%s\\n", hex.EncodeToString(sig))',
				'}',
				'EOF',
				'  SIG=$(printf "%s" "$CHALLENGE_MESSAGE" | go run "$SIGNER_PATH" "$STATE_DIR/bootstrap.json")',
				'  test -n "$SIG"',
				'  LOGIN_BODY=$(jq -nc --arg address "$BOOTSTRAP_ADDR" --arg challengeId "$CHALLENGE_ID" --arg message "$CHALLENGE_MESSAGE" --arg signature "$SIG" "{address:$address,challengeId:$challengeId,message:$message,signature:$signature}")',
					'  AUTH_JSON=$(curl -sSfL -X POST "https://$STAGE_DOMAIN/auth/wallet/login" -H "Content-Type: application/json" -d "$LOGIN_BODY")',
					'  ACCESS_TOKEN=$(echo "$AUTH_JSON" | jq -r ".access_token")',
					'  test -n "$ACCESS_TOKEN" && test "$ACCESS_TOKEN" != "null"',
					'  export AWS_PROFILE=managed',
					'  export SOUL_STAGE="$SOUL_STAGE"',
					'  export SOUL_INSTANCE_DOMAIN="$SOUL_INSTANCE_DOMAIN"',
					'  export LESSER_TOKEN="$ACCESS_TOKEN"',
					'  export LESSER_ADMIN_TOKEN="$ACCESS_TOKEN"',
					'  (cd "$SOUL_PACK_DIR" && GOTOOLCHAIN=auto go run ./cmd/soul-cli bootstrap)',
					'  SOUL_RECEIPT_PATH="$STATE_DIR/soul-state.json"',
					'  SOUL_TABLE_NAME="soul-$SOUL_STAGE"',
					'  QUEUE_BASE="https://sqs.$TARGET_REGION.amazonaws.com/$TARGET_ACCOUNT_ID"',
					'  RESEARCHER_QUEUE_URL="$QUEUE_BASE/soul-researcher"',
					'  ASSISTANT_QUEUE_URL="$QUEUE_BASE/soul-assistant"',
					'  CURATOR_QUEUE_URL="$QUEUE_BASE/soul-curator"',
					'  BRIDGE_QUEUE_URL="$QUEUE_BASE/soul-bridge"',
					'  CUSTOM_CODER_QUEUE_URL="$QUEUE_BASE/soul-custom-coder"',
					'  CUSTOM_SUMMARIZER_QUEUE_URL="$QUEUE_BASE/soul-custom-summarizer"',
					'  RESULTS_QUEUE_URL="$QUEUE_BASE/soul-results"',
					'  RESEARCHER_TOKEN_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/researcher/token"',
					'  RESEARCHER_REFRESH_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/researcher/refresh"',
					'  ASSISTANT_TOKEN_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/assistant/token"',
						'  ASSISTANT_REFRESH_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/assistant/refresh"',
						'  CURATOR_TOKEN_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/curator/token"',
						'  CURATOR_REFRESH_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/curator/refresh"',
						'  CUSTOM_CODER_TOKEN_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/custom-coder/token"',
						'  CUSTOM_CODER_REFRESH_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/custom-coder/refresh"',
						'  CUSTOM_SUMMARIZER_TOKEN_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/custom-summarizer/token"',
						'  CUSTOM_SUMMARIZER_REFRESH_PATH="/soul/$SOUL_INSTANCE_DOMAIN/agents/custom-summarizer/refresh"',
						'  jq -n --arg app "lesser-soul" --arg stage "$SOUL_STAGE" --arg instance_domain "$SOUL_INSTANCE_DOMAIN" --arg soul_version "$SOUL_PACK_VERSION" --arg soul_table_name "$SOUL_TABLE_NAME" --arg researcher_queue_url "$RESEARCHER_QUEUE_URL" --arg assistant_queue_url "$ASSISTANT_QUEUE_URL" --arg curator_queue_url "$CURATOR_QUEUE_URL" --arg bridge_queue_url "$BRIDGE_QUEUE_URL" --arg custom_coder_queue_url "$CUSTOM_CODER_QUEUE_URL" --arg custom_summarizer_queue_url "$CUSTOM_SUMMARIZER_QUEUE_URL" --arg results_queue_url "$RESULTS_QUEUE_URL" --arg researcher_token_path "$RESEARCHER_TOKEN_PATH" --arg researcher_refresh_path "$RESEARCHER_REFRESH_PATH" --arg assistant_token_path "$ASSISTANT_TOKEN_PATH" --arg assistant_refresh_path "$ASSISTANT_REFRESH_PATH" --arg curator_token_path "$CURATOR_TOKEN_PATH" --arg curator_refresh_path "$CURATOR_REFRESH_PATH" --arg custom_coder_token_path "$CUSTOM_CODER_TOKEN_PATH" --arg custom_coder_refresh_path "$CUSTOM_CODER_REFRESH_PATH" --arg custom_summarizer_token_path "$CUSTOM_SUMMARIZER_TOKEN_PATH" --arg custom_summarizer_refresh_path "$CUSTOM_SUMMARIZER_REFRESH_PATH" \'{version:2,app:$app,stage:$stage,instance_domain:$instance_domain,soul_version:$soul_version,soul_table_name:$soul_table_name,queue_urls:{researcher:$researcher_queue_url,assistant:$assistant_queue_url,curator:$curator_queue_url,bridge:$bridge_queue_url,custom_coder:$custom_coder_queue_url,custom_summarizer:$custom_summarizer_queue_url,results:$results_queue_url},agents:{researcher:{username:"soul-researcher",token_ssm_path:$researcher_token_path,refresh_ssm_path:$researcher_refresh_path},assistant:{username:"soul-assistant",token_ssm_path:$assistant_token_path,refresh_ssm_path:$assistant_refresh_path},curator:{username:"soul-curator",token_ssm_path:$curator_token_path,refresh_ssm_path:$curator_refresh_path},custom_coder:{username:"soul-coder",token_ssm_path:$custom_coder_token_path,refresh_ssm_path:$custom_coder_refresh_path},custom_summarizer:{username:"soul-summarizer",token_ssm_path:$custom_summarizer_token_path,refresh_ssm_path:$custom_summarizer_refresh_path}}}\' > "$SOUL_RECEIPT_PATH"',
					'  aws s3 cp "$SOUL_RECEIPT_PATH" "s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY"',
					'else',
					'  fail "unknown RUN_MODE: $RUN_MODE"',
					'fi',
			].join('\n');

			const provisionRunnerProject = new codebuild.Project(this, 'ProvisionRunnerProject', {
				projectName: provisionRunnerProjectName,
				timeout: cdk.Duration.hours(3),
				environment: {
					buildImage: codebuild.LinuxBuildImage.STANDARD_7_0,
				computeType: codebuild.ComputeType.SMALL,
			},
				environmentVariables: {
					GITHUB_OWNER: { value: lesserGitHubOwner },
					GITHUB_REPO: { value: lesserGitHubRepo },
					LESSER_BODY_GITHUB_OWNER: { value: lesserBodyGitHubOwner },
					LESSER_BODY_GITHUB_REPO: { value: lesserBodyGitHubRepo },
					LESSER_BODY_VERSION: { value: managedLesserBodyDefaultVersion.trim() },
					...(managedLesserGitHubTokenSsmParam.trim()
						? {
								GITHUB_TOKEN: {
									value: managedLesserGitHubTokenSsmParam.trim(),
								type: codebuild.BuildEnvironmentVariableType.PARAMETER_STORE,
							},
						}
					: {}),
			},
			buildSpec: codebuild.BuildSpec.fromObject({
				version: '0.2',
				env: {
					shell: 'bash',
				},
				phases: {
						install: {
							commands: [
								'set -euo pipefail',
								'echo \"Installing runner tools...\"',
								'if command -v yum >/dev/null 2>&1; then yum install -y jq tar gzip unzip openssl; fi',
								'if command -v apt-get >/dev/null 2>&1; then apt-get update -y && apt-get install -y jq tar gzip unzip openssl; fi',
								'node -v || true',
								'npm -v || true',
								'if ! command -v n >/dev/null 2>&1; then npm install -g n; fi',
								'n 24',
							'hash -r',
							'node -v',
							'npm install -g aws-cdk@2',
							'npm install -g pnpm@9',
							'cdk --version',
							'pnpm --version',
						],
					},
					pre_build: {
						commands: [provisionRunnerPreBuild],
					},
					build: {
						commands: [provisionRunnerBuild],
					},
				},
				}),
			});

			soulPackBucket.grantRead(provisionRunnerProject);
			soulPackSigningKey.grant(provisionRunnerProject, 'kms:Verify');
			provisionRunnerProject.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['ssm:GetParameter', 'ssm:GetParameters'],
					resources: [
						cdk.Stack.of(this).formatArn({
							service: 'ssm',
							resource: 'parameter',
							resourceName: `soul/${stage}/*`,
						}),
					],
				}),
			);

			const controlPlaneFn = this.goLambda('ControlPlaneApi', './cmd/control-plane-api', {
				STAGE: stage,
				STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PREVIEW_QUEUE_URL: previewQueue.queueUrl,
			SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			PROVISION_QUEUE_URL: provisionQueue.queueUrl,
			BOOTSTRAP_WALLET_ADDRESS: bootstrapWalletAddress,
			WEBAUTHN_RP_ID: webAuthnRPID,
			WEBAUTHN_ORIGINS: webAuthnOrigins,
			MANAGED_PROVISIONING_ENABLED: managedProvisioningEnabled,
			MANAGED_PARENT_DOMAIN: managedParentDomain,
			MANAGED_PARENT_HOSTED_ZONE_ID: managedParentHostedZoneId,
			MANAGED_INSTANCE_ROLE_NAME: managedInstanceRoleName,
			MANAGED_TARGET_OU_ID: managedTargetOuId,
			MANAGED_ACCOUNT_EMAIL_TEMPLATE: managedAccountEmailTemplate,
			MANAGED_ACCOUNT_NAME_PREFIX: managedAccountNamePrefix,
				MANAGED_DEFAULT_REGION: managedDefaultRegion,
				MANAGED_LESSER_DEFAULT_VERSION: managedLesserDefaultVersion,
				MANAGED_PROVISION_RUNNER_PROJECT_NAME: provisionRunnerProjectName,
				MANAGED_LESSER_GITHUB_OWNER: lesserGitHubOwner,
				MANAGED_LESSER_GITHUB_REPO: lesserGitHubRepo,
				MANAGED_LESSER_GITHUB_TOKEN_SSM_PARAM: managedLesserGitHubTokenSsmParam.trim(),
				MANAGED_LESSER_BODY_DEFAULT_VERSION: managedLesserBodyDefaultVersion,
				MANAGED_LESSER_BODY_GITHUB_OWNER: lesserBodyGitHubOwner,
				MANAGED_LESSER_BODY_GITHUB_REPO: lesserBodyGitHubRepo,
				TIP_ENABLED: tipEnabled,
				TIP_CHAIN_ID: tipChainId,
				TIP_RPC_URL_SSM_PARAM: tipRpcUrlSsmParam,
				TIP_CONTRACT_ADDRESS: tipContractAddress,
			TIP_ADMIN_SAFE_ADDRESS: tipAdminSafeAddress,
			TIP_DEFAULT_HOST_WALLET_ADDRESS: tipDefaultHostWalletAddress,
			TIP_DEFAULT_HOST_FEE_BPS: tipDefaultHostFeeBps,
			TIP_TX_MODE: tipTxMode,
			SOUL_ENABLED: soulEnabled,
			SOUL_CHAIN_ID: soulChainId,
			SOUL_RPC_URL_SSM_PARAM: soulRpcUrlSsmParam,
			SOUL_REGISTRY_CONTRACT_ADDRESS: soulRegistryContractAddress,
			SOUL_REPUTATION_ATTESTATION_CONTRACT_ADDRESS: soulReputationAttestationContractAddress,
			SOUL_VALIDATION_ATTESTATION_CONTRACT_ADDRESS: soulValidationAttestationContractAddress,
			SOUL_ADMIN_SAFE_ADDRESS: soulAdminSafeAddress,
			SOUL_TX_MODE: soulTxMode,
			SOUL_SUPPORTED_CAPABILITIES: soulSupportedCapabilities,
			SOUL_MINT_SIGNER_KEY_SSM_PARAM: soulMintSignerKeySsmParam,
			PAYMENTS_PROVIDER: paymentsProvider,
			PAYMENTS_CENTS_PER_1000_CREDITS: paymentsCentsPer1000Credits,
			PAYMENTS_CHECKOUT_SUCCESS_URL: paymentsCheckoutSuccessUrl,
			PAYMENTS_CHECKOUT_CANCEL_URL: paymentsCheckoutCancelUrl,
		});

		const trustFn = this.goLambda('TrustApi', './cmd/trust-api', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PREVIEW_QUEUE_URL: previewQueue.queueUrl,
			SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			ATTESTATION_SIGNING_KEY_ID: attestationSigningKey.keyId,
			ATTESTATION_PUBLIC_KEY_IDS: attestationSigningKey.keyId,
			WEBAUTHN_RP_ID: webAuthnRPID,
			WEBAUTHN_ORIGINS: webAuthnOrigins,
		});

		const repoRoot = this.repoRoot();
		const renderWorkerFn = new lambda.DockerImageFunction(this, 'RenderWorker', {
			functionName: `${namePrefix}-render-worker`,
			code: lambda.DockerImageCode.fromImageAsset(repoRoot, {
				file: 'cmd/render-worker/Dockerfile',
				exclude: ['cdk/cdk.out/**', 'cdk/node_modules/**', 'cdk/.build/**', '.git/**'],
			}),
			memorySize: 1536,
			timeout: cdk.Duration.seconds(30),
			environment: {
				STAGE: stage,
				STATE_TABLE_NAME: stateTable.tableName,
				ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
				PREVIEW_QUEUE_URL: previewQueue.queueUrl,
				SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			},
		});

		const aiWorkerFn = this.goLambda('AiWorker', './cmd/ai-worker', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PREVIEW_QUEUE_URL: previewQueue.queueUrl,
			SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			ATTESTATION_SIGNING_KEY_ID: attestationSigningKey.keyId,
			ATTESTATION_PUBLIC_KEY_IDS: attestationSigningKey.keyId,
		});

		const soulReputationWorkerFn = this.goLambda(
			'SoulReputationWorker',
			'./cmd/soul-reputation-worker',
			{
				STAGE: stage,
				STATE_TABLE_NAME: stateTable.tableName,
				TIP_ENABLED: tipEnabled,
				TIP_CHAIN_ID: tipChainId,
				TIP_RPC_URL_SSM_PARAM: tipRpcUrlSsmParam,
				TIP_CONTRACT_ADDRESS: tipContractAddress,
				SOUL_ENABLED: soulEnabled,
				SOUL_REPUTATION_TIP_START_BLOCK: soulReputationTipStartBlock,
				SOUL_REPUTATION_TIP_BLOCK_CHUNK_SIZE: soulReputationTipBlockChunkSize,
				SOUL_REPUTATION_TIP_SCALE: soulReputationTipScale,
				SOUL_REPUTATION_WEIGHT_ECONOMIC: soulReputationWeightEconomic,
				SOUL_REPUTATION_WEIGHT_SOCIAL: soulReputationWeightSocial,
				SOUL_REPUTATION_WEIGHT_VALIDATION: soulReputationWeightValidation,
				SOUL_REPUTATION_WEIGHT_TRUST: soulReputationWeightTrust,
				SOUL_VALIDATION_DECAY_EPOCH_HOURS: soulValidationDecayEpochHours,
				SOUL_VALIDATION_DECAY_RATE: soulValidationDecayRate,
			},
			{ memorySize: 512, timeoutSeconds: 120 },
		);

		const provisionWorkerFn = this.goLambda('ProvisionWorker', './cmd/provision-worker', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PROVISION_QUEUE_URL: provisionQueue.queueUrl,
			MANAGED_PROVISIONING_ENABLED: managedProvisioningEnabled,
			MANAGED_ORG_VENDING_ROLE_ARN: managedOrgVendingRoleArn,
			MANAGED_PARENT_DOMAIN: managedParentDomain,
			MANAGED_PARENT_HOSTED_ZONE_ID: managedParentHostedZoneId,
			MANAGED_INSTANCE_ROLE_NAME: managedInstanceRoleName,
			MANAGED_TARGET_OU_ID: managedTargetOuId,
			MANAGED_ACCOUNT_EMAIL_TEMPLATE: managedAccountEmailTemplate,
			MANAGED_ACCOUNT_NAME_PREFIX: managedAccountNamePrefix,
				MANAGED_DEFAULT_REGION: managedDefaultRegion,
				MANAGED_LESSER_DEFAULT_VERSION: managedLesserDefaultVersion,
				MANAGED_PROVISION_RUNNER_PROJECT_NAME: provisionRunnerProjectName,
				MANAGED_LESSER_GITHUB_OWNER: lesserGitHubOwner,
				MANAGED_LESSER_GITHUB_REPO: lesserGitHubRepo,
				MANAGED_LESSER_GITHUB_TOKEN_SSM_PARAM: managedLesserGitHubTokenSsmParam.trim(),
				MANAGED_LESSER_BODY_DEFAULT_VERSION: managedLesserBodyDefaultVersion,
				MANAGED_LESSER_BODY_GITHUB_OWNER: lesserBodyGitHubOwner,
				MANAGED_LESSER_BODY_GITHUB_REPO: lesserBodyGitHubRepo,
			});

		stateTable.grantReadWriteData(controlPlaneFn);
		stateTable.grantReadWriteData(trustFn);
		stateTable.grantReadWriteData(renderWorkerFn);
		stateTable.grantReadWriteData(aiWorkerFn);
		stateTable.grantReadWriteData(soulReputationWorkerFn);
		stateTable.grantReadWriteData(provisionWorkerFn);
		artifactsBucket.grantReadWrite(controlPlaneFn);
		soulPackBucket.grantReadWrite(controlPlaneFn);
		soulPackBucket.grantReadWrite(soulReputationWorkerFn);
		artifactsBucket.grantReadWrite(trustFn);
		artifactsBucket.grantReadWrite(renderWorkerFn);
		artifactsBucket.grantRead(aiWorkerFn);
		artifactsBucket.grantRead(provisionWorkerFn);
		artifactsBucket.grantReadWrite(provisionRunnerProject);
		attestationSigningKey.grant(trustFn, 'kms:Sign', 'kms:GetPublicKey');
		attestationSigningKey.grant(aiWorkerFn, 'kms:Sign', 'kms:GetPublicKey');
		previewQueue.grantSendMessages(controlPlaneFn);
		previewQueue.grantSendMessages(trustFn);
		previewQueue.grantConsumeMessages(renderWorkerFn);
		safetyQueue.grantSendMessages(controlPlaneFn);
		safetyQueue.grantSendMessages(trustFn);
		safetyQueue.grantConsumeMessages(aiWorkerFn);
		provisionQueue.grantSendMessages(controlPlaneFn);
		provisionQueue.grantConsumeMessages(provisionWorkerFn);
		provisionQueue.grantSendMessages(provisionWorkerFn);

		provisionRunnerProject.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['sts:AssumeRole'],
				resources: [`arn:aws:iam::*:role/${managedInstanceRoleName.trim() || 'OrganizationAccountAccessRole'}`],
			}),
		);
		if (managedOrgVendingRoleArn.trim()) {
			provisionRunnerProject.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['sts:AssumeRole'],
					resources: [managedOrgVendingRoleArn.trim()],
				}),
			);
		}

		provisionWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: [
					'organizations:CreateAccount',
					'organizations:DescribeCreateAccountStatus',
					'organizations:ListAccounts',
					'organizations:ListParents',
					'organizations:MoveAccount',
				],
				resources: ['*'],
			}),
		);

		provisionWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['sts:AssumeRole'],
				resources: [`arn:aws:iam::*:role/${managedInstanceRoleName.trim() || 'OrganizationAccountAccessRole'}`],
			}),
		);
		if (managedOrgVendingRoleArn.trim()) {
			provisionWorkerFn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['sts:AssumeRole'],
					resources: [managedOrgVendingRoleArn.trim()],
				}),
			);
		}

		if (managedParentHostedZoneId.trim()) {
			provisionWorkerFn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['route53:ChangeResourceRecordSets'],
					resources: [
						`arn:aws:route53:::hostedzone/${managedParentHostedZoneId.trim()}`,
					],
				}),
			);
		}

		provisionWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['codebuild:StartBuild'],
				resources: [provisionRunnerProject.projectArn],
			}),
		);
		provisionWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['codebuild:BatchGetBuilds'],
				resources: ['*'],
			}),
		);

		if (managedLesserGitHubTokenSsmParam.trim()) {
			const paramName = managedLesserGitHubTokenSsmParam.trim().replace(/^\/+/, '');
			const paramArn = `arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/${paramName}`;
			provisionRunnerProject.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['ssm:GetParameter', 'ssm:GetParameters'],
					resources: [paramArn],
				}),
			);
			provisionRunnerProject.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['kms:Decrypt'],
					resources: ['*'],
					conditions: {
						StringEquals: { 'kms:ViaService': `ssm.${cdk.Aws.REGION}.amazonaws.com` },
					},
				}),
			);
		}

		renderWorkerFn.addEventSource(new lambdaEventSources.SqsEventSource(previewQueue, { batchSize: 1 }));
		aiWorkerFn.addEventSource(new lambdaEventSources.SqsEventSource(safetyQueue, { batchSize: 5 }));
		provisionWorkerFn.addEventSource(new lambdaEventSources.SqsEventSource(provisionQueue, { batchSize: 1 }));

		aiWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['comprehend:DetectDominantLanguage', 'comprehend:DetectEntities', 'comprehend:DetectPiiEntities'],
				resources: ['*'],
			}),
		);
		aiWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['rekognition:DetectModerationLabels', 'rekognition:DetectText', 'rekognition:DetectFaces'],
				resources: ['*'],
			}),
		);

		const ssmParamArns = [
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/api/openai/service`,
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/api/claude`,
		];
		for (const fn of [trustFn, aiWorkerFn]) {
			fn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['ssm:GetParameter', 'ssm:GetParameters'],
					resources: ssmParamArns,
				}),
			);
			fn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['kms:Decrypt'],
					resources: ['*'],
					conditions: {
						StringEquals: { 'kms:ViaService': `ssm.${cdk.Aws.REGION}.amazonaws.com` },
					},
				}),
			);
		}

		const paymentsSsmParamArns = [
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/stripe/${stage}/secret`,
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/stripe/${stage}/webhook`,
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/api/stripe/secret`,
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/api/stripe/webhook`,
		];
		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['ssm:GetParameter', 'ssm:GetParameters'],
				resources: paymentsSsmParamArns,
			}),
		);
		if (tipRpcUrlSsmParam) {
			controlPlaneFn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['ssm:GetParameter'],
					resources: [`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/${tipRpcUrlSsmParam.replace(/^\//, '')}`],
				}),
			);
		}
		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['ssm:GetParameter', 'ssm:GetParameters'],
				resources: [
					cdk.Stack.of(this).formatArn({
						service: 'ssm',
						resource: 'parameter',
						resourceName: `soul/${stage}/*`,
					}),
				],
			}),
		);
		if (soulRpcUrlSsmParam) {
			controlPlaneFn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['ssm:GetParameter'],
					resources: [
						`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/${soulRpcUrlSsmParam.replace(/^\//, '')}`,
					],
				}),
			);
		}
		if (soulMintSignerKeySsmParam) {
			controlPlaneFn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['ssm:GetParameter'],
					resources: [
						`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/${soulMintSignerKeySsmParam.replace(/^\//, '')}`,
					],
				}),
			);
		}
		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['kms:Decrypt'],
				resources: ['*'],
				conditions: {
					StringEquals: { 'kms:ViaService': `ssm.${cdk.Aws.REGION}.amazonaws.com` },
				},
			}),
		);

		if (tipRpcUrlSsmParam) {
			soulReputationWorkerFn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['ssm:GetParameter'],
					resources: [
						`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/${tipRpcUrlSsmParam.replace(/^\//, '')}`,
					],
				}),
			);
		}
		soulReputationWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['ssm:GetParameter', 'ssm:GetParameters'],
				resources: [
					cdk.Stack.of(this).formatArn({
						service: 'ssm',
						resource: 'parameter',
						resourceName: `soul/${stage}/*`,
					}),
				],
			}),
		);
		soulReputationWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['kms:Decrypt'],
				resources: ['*'],
				conditions: {
					StringEquals: { 'kms:ViaService': `ssm.${cdk.Aws.REGION}.amazonaws.com` },
				},
			}),
		);

		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['route53:ListHostedZonesByName'],
				resources: ['*'],
			}),
		);
		if (managedParentHostedZoneId.trim()) {
			controlPlaneFn.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['route53:ChangeResourceRecordSets'],
					resources: [
						`arn:aws:route53:::hostedzone/${managedParentHostedZoneId.trim()}`,
					],
				}),
			);
		}

		const retentionSweepRule = new events.Rule(this, 'RetentionSweepRule', {
			ruleName: `${namePrefix}-retention-sweep`,
			schedule: events.Schedule.rate(cdk.Duration.days(1)),
		});
		retentionSweepRule.addTarget(new targets.LambdaFunction(renderWorkerFn));

		const soulReputationRecomputeRule = new events.Rule(this, 'SoulReputationRecomputeRule', {
			ruleName: `${namePrefix}-soul-reputation-recompute`,
			schedule: events.Schedule.rate(cdk.Duration.hours(1)),
		});
		soulReputationRecomputeRule.addTarget(new targets.LambdaFunction(soulReputationWorkerFn));

		const controlPlaneApi = new apigwv2.HttpApi(this, 'ControlPlaneHttpApi', {
			apiName: `${namePrefix}-control-plane`,
			defaultIntegration: new apigwv2Integrations.HttpLambdaIntegration(
				'ControlPlaneIntegration', controlPlaneFn,
			),
		});

		const trustApi = new apigwv2.HttpApi(this, 'TrustHttpApi', {
			apiName: `${namePrefix}-trust`,
			defaultIntegration: new apigwv2Integrations.HttpLambdaIntegration(
				'TrustIntegration', trustFn,
			),
		});

		const webRootDomain = (this.node.tryGetContext('webRootDomain') as string | undefined) ?? 'lesser.host';
		const webHostedZoneId = (this.node.tryGetContext('webHostedZoneId') as string | undefined) ?? '';
		const webHostedZoneName =
			(this.node.tryGetContext('webHostedZoneName') as string | undefined) ?? webRootDomain;
		const webDomainName = stage === 'live' ? webRootDomain : `${stage}.${webRootDomain}`;

		const webBucket = new s3.Bucket(this, 'WebBucket', {
			bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-web`,
			blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
			enforceSSL: true,
			removalPolicy,
			autoDeleteObjects: stage !== 'live',
		});

		const webCsp = [
			"default-src 'none'",
			"base-uri 'none'",
			"object-src 'none'",
			"frame-ancestors 'none'",
			"form-action 'self'",
			"img-src 'self' data: blob:",
			"font-src 'self'",
			"style-src 'self'",
			"script-src 'self'",
			"connect-src 'self'",
			"manifest-src 'self'",
		].join('; ');

		const webSecurityHeaders = new cloudfront.ResponseHeadersPolicy(this, 'WebSecurityHeaders', {
			responseHeadersPolicyName: `${namePrefix}-web-security`,
			securityHeadersBehavior: {
				contentSecurityPolicy: {
					contentSecurityPolicy: webCsp,
					override: true,
				},
				contentTypeOptions: { override: true },
				frameOptions: { frameOption: cloudfront.HeadersFrameOption.DENY, override: true },
				referrerPolicy: {
					referrerPolicy: cloudfront.HeadersReferrerPolicy.SAME_ORIGIN,
					override: true,
				},
				strictTransportSecurity: {
					accessControlMaxAge: cdk.Duration.days(365),
					includeSubdomains: true,
					preload: true,
					override: true,
				},
				xssProtection: { protection: true, modeBlock: true, override: true },
			},
			customHeadersBehavior: {
				customHeaders: [
					{
						header: 'Permissions-Policy',
						value: 'camera=(), microphone=(), geolocation=(), interest-cohort=()',
						override: true,
					},
				],
			},
		});

		const webSpaRewriteFn = new cloudfront.Function(this, 'WebSpaRewriteFn', {
			functionName: `${namePrefix}-web-spa-rewrite`,
			code: cloudfront.FunctionCode.fromInline(`function handler(event) {
  var request = event.request;
  var uri = request.uri || "/";

  // Never rewrite API routes.
  var isSetupApi =
    uri === "/setup/status" ||
    uri === "/setup/admin" ||
    uri === "/setup/finalize" ||
    uri.startsWith("/setup/bootstrap/");

  if (
    uri.startsWith("/api/") ||
    uri.startsWith("/auth/") ||
    isSetupApi ||
    uri.startsWith("/.well-known/") ||
    uri.startsWith("/attestations")
  ) {
    return request;
  }

  // If the path looks like a file, do not rewrite.
  if (uri.indexOf(".") !== -1) {
    return request;
  }

  request.uri = "/index.html";
  return request;
}`),
		});

		let webZone: route53.IHostedZone | undefined;
		let webCert: acm.ICertificate | undefined;
		if (webHostedZoneId.trim()) {
			webZone = route53.HostedZone.fromHostedZoneAttributes(this, 'WebHostedZone', {
				hostedZoneId: webHostedZoneId.trim(),
				zoneName: webHostedZoneName.trim() || webRootDomain,
			});

			webCert = new acm.DnsValidatedCertificate(this, 'WebCertificate', {
				domainName: webDomainName,
				hostedZone: webZone,
				region: 'us-east-1',
			});
		}

		const webOai = new cloudfront.OriginAccessIdentity(this, 'WebOAI');
		webBucket.grantRead(webOai);

		const controlPlaneDomain = `${controlPlaneApi.httpApiId}.execute-api.${cdk.Aws.REGION}.amazonaws.com`;
		const trustDomain = `${trustApi.httpApiId}.execute-api.${cdk.Aws.REGION}.amazonaws.com`;

		const controlPlaneOrigin = new origins.HttpOrigin(controlPlaneDomain, {
			protocolPolicy: cloudfront.OriginProtocolPolicy.HTTPS_ONLY,
		});
		const trustOrigin = new origins.HttpOrigin(trustDomain, {
			protocolPolicy: cloudfront.OriginProtocolPolicy.HTTPS_ONLY,
		});

		const apiBehavior: cloudfront.BehaviorOptions = {
			origin: controlPlaneOrigin,
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_ALL,
			cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
			originRequestPolicy: cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
		};

		const trustApiBehavior: cloudfront.BehaviorOptions = {
			origin: trustOrigin,
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_ALL,
			cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
			originRequestPolicy: cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
		};

		const trustBehaviorNoCache: cloudfront.BehaviorOptions = {
			origin: trustOrigin,
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
			cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
			originRequestPolicy: cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
		};

		const trustBehaviorCached: cloudfront.BehaviorOptions = {
			origin: trustOrigin,
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
			cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
			originRequestPolicy: cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
		};

		const webDistribution = new cloudfront.Distribution(this, 'WebDistribution', {
			defaultRootObject: 'index.html',
			certificate: webCert,
			domainNames: webCert ? [webDomainName] : undefined,
			defaultBehavior: {
				origin: new origins.S3Origin(webBucket, { originAccessIdentity: webOai }),
				viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
				allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
				cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
				responseHeadersPolicy: webSecurityHeaders,
				functionAssociations: [
					{
						function: webSpaRewriteFn,
						eventType: cloudfront.FunctionEventType.VIEWER_REQUEST,
					},
				],
			},
			additionalBehaviors: {
				'api/v1/previews*': trustApiBehavior,
				'api/v1/renders*': trustApiBehavior,
				'api/v1/publish/jobs*': trustApiBehavior,
				'api/v1/ai/*': trustApiBehavior,
				'api/v1/budget/debit': trustApiBehavior,

				'api/*': apiBehavior,
				'auth/*': apiBehavior,
				'setup/status': apiBehavior,
				'setup/bootstrap/*': apiBehavior,
				'setup/admin': apiBehavior,
				'setup/finalize': apiBehavior,

				'.well-known/*': trustBehaviorCached,
				'attestations': trustBehaviorNoCache,
				'attestations/*': trustBehaviorCached,
			},
		});

		const publicBaseURL = webCert
			? `https://${webDomainName}`
			: `https://${webDistribution.distributionDomainName}`;
		trustFn.addEnvironment('PUBLIC_BASE_URL', publicBaseURL);

		new s3deploy.BucketDeployment(this, 'WebDeployment', {
			destinationBucket: webBucket,
			sources: [
				s3deploy.Source.asset(path.join(this.repoRoot(), 'web'), {
					bundling: {
						image: cdk.DockerImage.fromRegistry('node:24-bookworm'),
						local: {
							tryBundle(outputDir: string) {
								if (process.env.CI !== 'true') {
									return false;
								}

								const webDir = path.join(repoRoot, 'web');
								try {
									execSync('npm ci', { cwd: webDir, stdio: 'inherit' });
									execSync('npm run build', { cwd: webDir, stdio: 'inherit' });
									fs.cpSync(path.join(webDir, 'dist'), outputDir, { recursive: true });
									return true;
								} catch (err) {
									return false;
								}
							},
						},
						command: [
							'bash',
							'-c',
							'npm ci && npm run build && cp -r dist/* /asset-output/',
						],
					},
				}),
			],
			distribution: webDistribution,
			distributionPaths: ['/*'],
		});

		if (webZone && webCert) {
			new route53.ARecord(this, 'WebAliasA', {
				zone: webZone,
				recordName: stage === 'live' ? undefined : stage,
				target: route53.RecordTarget.fromAlias(new route53Targets.CloudFrontTarget(webDistribution)),
			});
			new route53.AaaaRecord(this, 'WebAliasAAAA', {
				zone: webZone,
				recordName: stage === 'live' ? undefined : stage,
				target: route53.RecordTarget.fromAlias(new route53Targets.CloudFrontTarget(webDistribution)),
			});
		}

		new cdk.CfnOutput(this, 'ControlPlaneUrl', { value: controlPlaneApi.url! });
		new cdk.CfnOutput(this, 'TrustUrl', { value: trustApi.url! });
		new cdk.CfnOutput(this, 'WebDistributionDomain', { value: webDistribution.distributionDomainName });
		new cdk.CfnOutput(this, 'PublicBaseUrl', { value: `https://${webDomainName}` });
		new cdk.CfnOutput(this, 'WebUrl', {
			value: webCert ? `https://${webDomainName}` : `https://${webDistribution.distributionDomainName}`,
		});
		new cdk.CfnOutput(this, 'StateTableName', { value: stateTable.tableName });
			new cdk.CfnOutput(this, 'ArtifactsBucketName', { value: artifactsBucket.bucketName });
			new cdk.CfnOutput(this, 'AttestationSigningKeyId', { value: attestationSigningKey.keyId });
			new cdk.CfnOutput(this, 'PreviewQueueUrl', { value: previewQueue.queueUrl });
			new cdk.CfnOutput(this, 'SafetyQueueUrl', { value: safetyQueue.queueUrl });
			new cdk.CfnOutput(this, 'ProvisionQueueUrl', { value: provisionQueue.queueUrl });
			new cdk.CfnOutput(this, 'RenderWorkerFunctionName', { value: renderWorkerFn.functionName });
			new cdk.CfnOutput(this, 'AiWorkerFunctionName', { value: aiWorkerFn.functionName });
			new cdk.CfnOutput(this, 'ProvisionWorkerFunctionName', { value: provisionWorkerFn.functionName });
			new cdk.CfnOutput(this, 'RetentionSweepRuleName', { value: retentionSweepRule.ruleName });

			const aiDashboard = new cloudwatch.Dashboard(this, 'AiDashboard', {
				dashboardName: `${namePrefix}-ai`,
			});

			const trustCredits = new cloudwatch.MathExpression({
				expression: `SUM(SEARCH('{lesser-host,Stage,Service,Instance,Module,Status} MetricName="AICreditsDebited" AND Stage="${stage}" AND Service="trust-api"', 'Sum', 300))`,
				period: cdk.Duration.minutes(5),
			});
			const trustErrors = new cloudwatch.MathExpression({
				expression: `SUM(SEARCH('{lesser-host,Stage,Service,Instance,Module,Status} MetricName="AIErrors" AND Stage="${stage}" AND Service="trust-api"', 'Sum', 300))`,
				period: cdk.Duration.minutes(5),
			});
			const workerErrors = new cloudwatch.MathExpression({
				expression: `SUM(SEARCH('{lesser-host,Stage,Service,Instance,Module,Status,Provider} MetricName="AIJobErrors" AND Stage="${stage}" AND Service="ai-worker"', 'Sum', 300))`,
				period: cdk.Duration.minutes(5),
			});
			const workerFallback = new cloudwatch.MathExpression({
				expression: `SUM(SEARCH('{lesser-host,Stage,Service,Instance,Module,Status,Provider} MetricName="AILLMFallback" AND Stage="${stage}" AND Service="ai-worker"', 'Sum', 300))`,
				period: cdk.Duration.minutes(5),
			});

			aiDashboard.addWidgets(
				new cloudwatch.GraphWidget({
					title: 'AI Credits Debited (Total)',
					left: [trustCredits],
					width: 12,
				}),
				new cloudwatch.GraphWidget({
					title: 'AI Errors (Total)',
					left: [trustErrors, workerErrors],
					width: 12,
				}),
				new cloudwatch.GraphWidget({
					title: 'LLM Fallback (Total)',
					left: [workerFallback],
					width: 12,
				}),
			);

			const trustLogGroupName = `/aws/lambda/${trustFn.functionName}`;
			const workerLogGroupName = `/aws/lambda/${aiWorkerFn.functionName}`;
			logs.LogGroup.fromLogGroupName(this, 'TrustLogGroup', trustLogGroupName);
			logs.LogGroup.fromLogGroupName(this, 'AiWorkerLogGroup', workerLogGroupName);

				aiDashboard.addWidgets(
					new cloudwatch.LogQueryWidget({
						title: 'Top AI Spend (Credits Debited)',
						width: 24,
						height: 6,
					logGroupNames: [trustLogGroupName, workerLogGroupName],
					queryString: [
						'filter ispresent(AICreditsDebited) and AICreditsDebited > 0',
						'| stats sum(AICreditsDebited) as credits by Stage, Service, Instance, Module',
						'| sort credits desc',
						'| limit 20',
					].join('\n'),
				}),
				new cloudwatch.LogQueryWidget({
					title: 'Top AI Failures',
					width: 24,
					height: 6,
					logGroupNames: [trustLogGroupName, workerLogGroupName],
					queryString: [
						'filter ispresent(AIErrors) or ispresent(AIJobErrors) or ispresent(AIInternalErrors) or ispresent(AIJobInternalErrors)',
						'| stats sum(AIErrors) as req_errors, sum(AIJobErrors) as job_errors, sum(AIInternalErrors) as req_internal, sum(AIJobInternalErrors) as job_internal by Stage, Service, Instance, Module',
						'| sort (req_errors + job_errors + req_internal + job_internal) desc',
						'| limit 20',
					].join('\n'),
					}),
				);

				new cloudwatch.Alarm(this, 'TrustProxy503Alarm', {
					alarmName: `${namePrefix}-trust-proxy-503`,
					metric: new cloudwatch.Metric({
						namespace: 'lesser-host',
						metricName: 'TrustProxy503',
						dimensionsMap: { Stage: stage, Service: 'trust-api' },
						statistic: 'Sum',
						period: cdk.Duration.minutes(5),
					}),
					threshold: 1,
					evaluationPeriods: 1,
					datapointsToAlarm: 1,
					treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
				});
			}

		private goLambda(
			id: string,
			entry: string,
			environment: Record<string, string>,
			opts?: { memorySize?: number; timeoutSeconds?: number },
		): lambda.Function {
			const repoRoot = this.repoRoot();
			const buildDir = path.join(repoRoot, 'cdk', '.build', id);
			fs.mkdirSync(buildDir, { recursive: true });
			// AWS Lambda's legacy `go1.x` runtime has been deprecated; use the AL2023 custom runtime with a `bootstrap` binary.
			execSync('go build -o ' + path.join(buildDir, 'bootstrap') + ' ' + entry, {
				cwd: repoRoot,
				stdio: 'inherit',
				env: {
					...process.env,
					CGO_ENABLED: '0',
					GOOS: 'linux',
					GOARCH: 'amd64',
				},
			});
			const code = lambda.Code.fromAsset(buildDir);

			return new lambda.Function(this, id, {
				functionName: `${this.namePrefix}-${id.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase()).replace(/^-/, '')}`,
				code,
				handler: 'bootstrap',
				runtime: lambda.Runtime.PROVIDED_AL2023,
				memorySize: opts?.memorySize ?? 256,
				timeout: cdk.Duration.seconds(opts?.timeoutSeconds ?? 10),
				environment,
			});
		}

	private repoRoot(): string {
		// This file lives at cdk/lib/*.ts; repo root is two levels up.
		return path.resolve(__dirname, '../..');
	}
}
