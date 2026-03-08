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
import * as apigw from 'aws-cdk-lib/aws-apigateway';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import * as apigwv2Integrations from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as wafv2 from 'aws-cdk-lib/aws-wafv2';

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
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		previewDLQ.applyRemovalPolicy(removalPolicy);
		const previewQueue = new sqs.Queue(this, 'PreviewQueue', {
			queueName: `${namePrefix}-preview-queue`,
			deadLetterQueue: { queue: previewDLQ, maxReceiveCount: 3 },
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		previewQueue.applyRemovalPolicy(removalPolicy);

		const safetyDLQ = new sqs.Queue(this, 'SafetyDLQ', {
			queueName: `${namePrefix}-safety-dlq`,
			retentionPeriod: cdk.Duration.days(14),
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		safetyDLQ.applyRemovalPolicy(removalPolicy);
		const safetyQueue = new sqs.Queue(this, 'SafetyQueue', {
			queueName: `${namePrefix}-safety-queue`,
			deadLetterQueue: { queue: safetyDLQ, maxReceiveCount: 3 },
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		safetyQueue.applyRemovalPolicy(removalPolicy);

		const provisionDLQ = new sqs.Queue(this, 'ProvisionDLQ', {
			queueName: `${namePrefix}-provision-dlq`,
			retentionPeriod: cdk.Duration.days(14),
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		provisionDLQ.applyRemovalPolicy(removalPolicy);
		const provisionQueue = new sqs.Queue(this, 'ProvisionQueue', {
			queueName: `${namePrefix}-provision-queue`,
			// This queue backs managed provisioning + update orchestration. A low maxReceiveCount can
			// strand long-running jobs in "running" when a transient worker failure DLQs the next poll.
			visibilityTimeout: cdk.Duration.minutes(2),
			deadLetterQueue: { queue: provisionDLQ, maxReceiveCount: 10 },
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		provisionQueue.applyRemovalPolicy(removalPolicy);

		const commDLQ = new sqs.Queue(this, 'CommDLQ', {
			queueName: `${namePrefix}-comm-dlq`,
			retentionPeriod: cdk.Duration.days(14),
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		commDLQ.applyRemovalPolicy(removalPolicy);
		const commQueue = new sqs.Queue(this, 'CommQueue', {
			queueName: `${namePrefix}-comm-queue`,
			visibilityTimeout: cdk.Duration.minutes(1),
			deadLetterQueue: { queue: commDLQ, maxReceiveCount: 3 },
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		commQueue.applyRemovalPolicy(removalPolicy);

		const dlqAlarmPeriod = cdk.Duration.minutes(1);
		const dlqAlarmThreshold = 0;
		new cloudwatch.Alarm(this, 'PreviewDLQAlarm', {
			alarmName: `${namePrefix}-preview-dlq-visible`,
			metric: previewDLQ.metricApproximateNumberOfMessagesVisible({ period: dlqAlarmPeriod }),
			threshold: dlqAlarmThreshold,
			comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_THRESHOLD,
			evaluationPeriods: 1,
			datapointsToAlarm: 1,
			treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
		});
		new cloudwatch.Alarm(this, 'SafetyDLQAlarm', {
			alarmName: `${namePrefix}-safety-dlq-visible`,
			metric: safetyDLQ.metricApproximateNumberOfMessagesVisible({ period: dlqAlarmPeriod }),
			threshold: dlqAlarmThreshold,
			comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_THRESHOLD,
			evaluationPeriods: 1,
			datapointsToAlarm: 1,
			treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
		});
		new cloudwatch.Alarm(this, 'ProvisionDLQAlarm', {
			alarmName: `${namePrefix}-provision-dlq-visible`,
			metric: provisionDLQ.metricApproximateNumberOfMessagesVisible({ period: dlqAlarmPeriod }),
			threshold: dlqAlarmThreshold,
			comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_THRESHOLD,
			evaluationPeriods: 1,
			datapointsToAlarm: 1,
			treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
		});
		new cloudwatch.Alarm(this, 'CommDLQAlarm', {
			alarmName: `${namePrefix}-comm-dlq-visible`,
			metric: commDLQ.metricApproximateNumberOfMessagesVisible({ period: dlqAlarmPeriod }),
			threshold: dlqAlarmThreshold,
			comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_THRESHOLD,
			evaluationPeriods: 1,
			datapointsToAlarm: 1,
			treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
		});

				const attestationSigningKey = new kms.Key(this, 'AttestationSigningKey', {
					description: `${namePrefix} attestation signing`,
					keySpec: kms.KeySpec.RSA_2048,
					keyUsage: kms.KeyUsage.SIGN_VERIFY,
					removalPolicy,
				});
				attestationSigningKey.addAlias(`alias/${namePrefix}-attestation-signing`);

				const ensGatewaySigningKey = new kms.Key(this, 'ENSGatewaySigningKey', {
					description: `${namePrefix} ENS gateway signing`,
					keySpec: kms.KeySpec.ECC_SECG_P256K1,
					keyUsage: kms.KeyUsage.SIGN_VERIFY,
					removalPolicy,
				});
				ensGatewaySigningKey.addAlias(`alias/${namePrefix}-ens-gateway-signing`);

				const soulPackBucket = new s3.Bucket(this, 'SoulPackBucket', {
					bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-soul-packs`,
					blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
					enforceSSL: true,
					versioned: true,
				removalPolicy,
				autoDeleteObjects: stage !== 'live',
			});

			new ssm.StringParameter(this, 'SoulPackBucketNameParam', {
				parameterName: `/soul/${stage}/packBucketName`,
				stringValue: soulPackBucket.bucketName,
				description: `lesser-soul registry artifacts bucket name (${stage})`,
				tier: ssm.ParameterTier.STANDARD,
			});

		const bootstrapWalletAddress =
			(this.node.tryGetContext('bootstrapWalletAddress') as string | undefined) ?? '';
			const webAuthnRPID = (this.node.tryGetContext('webauthnRpId') as string | undefined) ?? '';
			const webAuthnOrigins = (this.node.tryGetContext('webauthnOrigins') as string | undefined) ?? '';
			const ensGatewayResolverAddress =
				(this.node.tryGetContext('ensGatewayResolverAddress') as string | undefined) ?? '';
			const ensGatewayTTLSeconds = (this.node.tryGetContext('ensGatewayTtlSeconds') as string | undefined) ?? '';

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
					'TAG="${LESSER_VERSION:-}"',
					'resolve_latest_release_tag() {',
					'  o="$1"',
					'  r="$2"',
					'  url=$(curl -sSfL -o /dev/null -w "%{url_effective}" "https://github.com/$o/$r/releases/latest")',
					'  echo "${url##*/}"',
					'}',
					'if [ -z "$TAG" ]; then',
					'  echo "Resolving latest Lesser release..."',
					'  TAG=$(resolve_latest_release_tag "$OWNER" "$REPO")',
					'fi',
				'test -n "$TAG"',
				'test "$TAG" != "null"',
				'echo "Using Lesser release: $TAG"',
					'curl -sSfL -o lesser-src.tgz "https://github.com/$OWNER/$REPO/archive/refs/tags/$TAG.tar.gz"',
				'mkdir -p lesser-src && tar -xzf lesser-src.tgz --strip-components=1 -C lesser-src',
				'ARCH=$(uname -m)',
				'if [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "amd64" ]; then BIN_NAME="lesser-linux-amd64"; fi',
				'if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then BIN_NAME="lesser-linux-arm64"; fi',
				'test -n "${BIN_NAME:-}"',
					'curl -sSfL -o lesser-src/lesser "https://github.com/$OWNER/$REPO/releases/download/$TAG/$BIN_NAME"',
					'curl -sSfL -o checksums.txt "https://github.com/$OWNER/$REPO/releases/download/$TAG/checksums.txt"',
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
			'enable_agents() {',
			'  echo "Ensuring agents are enabled..."',
			'  STACK_NAME="$APP_SLUG-$STAGE"',
			'  API_FN="$APP_SLUG-$STAGE-api"',
			'  GRAPHQL_FN="$APP_SLUG-$STAGE-graphql"',
			'  GRAPHQL_WS_FN="$APP_SLUG-$STAGE-graphql-ws"',
			'',
			'  update_lambda_env() {',
			'    FN="$1"',
			'    echo "Setting agent env vars on Lambda: $FN"',
			'    CUR=$(aws lambda get-function-configuration --profile managed --function-name "$FN" --output json | jq -c \'.Environment.Variables // {}\')',
			'    NEXT=$(printf "%s" "$CUR" | jq -c \'. + {"ALLOW_AGENTS":"true","ALLOW_AGENT_REGISTRATION":"true"}\')',
			'    ENV=$(jq -nc --argjson vars "$NEXT" \'{Variables:$vars}\')',
			'    aws lambda update-function-configuration --profile managed --function-name "$FN" --environment "$ENV" >/dev/null',
			'  }',
			'',
			'  wait_lambda_update() {',
			'    FN="$1"',
			'    for i in $(seq 1 60); do',
			'      STATUS=$(aws lambda get-function-configuration --profile managed --function-name "$FN" --query "LastUpdateStatus" --output text)',
			'      case "$STATUS" in',
			'        Successful) return 0 ;;',
			'        Failed) fail "Lambda update failed: $FN" ;;',
			'      esac',
			'      sleep 2',
			'    done',
			'    fail "Lambda update timed out: $FN"',
			'  }',
			'',
			'  update_lambda_env "$API_FN"',
			'  update_lambda_env "$GRAPHQL_FN"',
			'  update_lambda_env "$GRAPHQL_WS_FN"',
			'',
			'  wait_lambda_update "$API_FN"',
			'  wait_lambda_update "$GRAPHQL_FN"',
			'  wait_lambda_update "$GRAPHQL_WS_FN"',
			'',
			'  TABLE_NAME=$(aws cloudformation describe-stacks --profile managed --stack-name "$STACK_NAME" --output json | jq -r \'.Stacks[0].Outputs[] | select(.OutputKey=="TableName") | .OutputValue\' | head -n 1)',
			'  test -n "$TABLE_NAME" && test "$TABLE_NAME" != "null"',
			'',
			'  NOW=$(date -u +"%Y-%m-%dT%H:%M:%S.%NZ")',
			'  aws dynamodb update-item --profile managed --region "$TARGET_REGION" --table-name "$TABLE_NAME" \\',
			'    --key \'{"PK":{"S":"INSTANCE#CONFIG"},"SK":{"S":"AGENT_CONFIG"}}\' \\',
			'    --update-expression \'SET allowAgents=:t, allowAgentRegistration=:t, defaultQuarantineDays=:dq, maxAgentsPerOwner=:mao, allowRemoteAgents=:ar, remoteQuarantineDays=:rdq, blockedAgentDomains=:empty, trustedAgentDomains=:empty, agentMaxPostsPerHour=:ampph, verifiedAgentMaxPostsPerHour=:vampph, agentMaxFollowsPerHour=:amfph, verifiedAgentMaxFollowsPerHour=:vamfph, hybridRetrievalEnabled=:hre, hybridRetrievalMaxCandidates=:hrmc, updatedAt=:now\' \\',
			'    --expression-attribute-values \'{":t":{"BOOL":true},":dq":{"N":"7"},":mao":{"N":"3"},":ar":{"BOOL":true},":rdq":{"N":"7"},":empty":{"L":[]},":ampph":{"N":"50"},":vampph":{"N":"200"},":amfph":{"N":"20"},":vamfph":{"N":"100"},":hre":{"BOOL":false},":hrmc":{"N":"200"},":now":{"S":"\'"$NOW"\'"}}\' \\',
			'    --return-values NONE >/dev/null',
			'  echo "Agents enabled."',
			'}',
			'if bool_on "${TIP_ENABLED:-}"; then',
			'  if [ -z "${TIP_CHAIN_ID:-}" ]; then fail "TIP_CHAIN_ID is required when TIP_ENABLED=true"; fi',
			'  case "$TIP_CHAIN_ID" in *[!0-9]*|"") fail "TIP_CHAIN_ID must be a positive integer when TIP_ENABLED=true";; 0) fail "TIP_CHAIN_ID must be > 0 when TIP_ENABLED=true";; esac',
			'  if [ -z "${TIP_CONTRACT_ADDRESS:-}" ]; then fail "TIP_CONTRACT_ADDRESS is required when TIP_ENABLED=true"; fi',
				'fi',
				'jq -n --arg slug "$APP_SLUG" --arg stage "$STAGE" --arg admin_wallet_address "$ADMIN_WALLET_ADDRESS" --arg admin_username "$ADMIN_USERNAME" --arg admin_wallet_chain_id "${ADMIN_WALLET_CHAIN_ID:-}" --arg consent_message "$CONSENT_MESSAGE" --arg consent_signature "${CONSENT_SIGNATURE:-}" --arg lesser_host_url "${LESSER_HOST_URL:-}" --arg lesser_host_attestations_url "${LESSER_HOST_ATTESTATIONS_URL:-}" --arg lesser_host_instance_key_arn "${LESSER_HOST_INSTANCE_KEY_ARN:-}" --arg translation_enabled "${TRANSLATION_ENABLED:-}" --arg tip_enabled "${TIP_ENABLED:-}" --arg tip_chain_id "${TIP_CHAIN_ID:-}" --arg tip_contract_address "${TIP_CONTRACT_ADDRESS:-}" --arg ai_enabled "${AI_ENABLED:-}" --arg ai_moderation_enabled "${AI_MODERATION_ENABLED:-}" --arg ai_nsfw_detection_enabled "${AI_NSFW_DETECTION_ENABLED:-}" --arg ai_spam_detection_enabled "${AI_SPAM_DETECTION_ENABLED:-}" --arg ai_pii_detection_enabled "${AI_PII_DETECTION_ENABLED:-}" --arg ai_content_detection_enabled "${AI_CONTENT_DETECTION_ENABLED:-}" \'def bool($v): ($v|ascii_downcase) as $x | ($x=="true" or $x=="1" or $x=="yes" or $x=="on"); {"schema":1,"slug":$slug,"stage":$stage,"admin_wallet_address":$admin_wallet_address,"admin_username":$admin_username} | if $admin_wallet_chain_id != "" then .admin_wallet_chain_id = ($admin_wallet_chain_id|tonumber) else . end | if $consent_message != "" then .consent_message = $consent_message else . end | if $consent_signature != "" then .consent_signature = $consent_signature else . end | if $lesser_host_url != "" then .lesser_host_url = $lesser_host_url else . end | if $lesser_host_attestations_url != "" then .lesser_host_attestations_url = $lesser_host_attestations_url elif $lesser_host_url != "" then .lesser_host_attestations_url = $lesser_host_url else . end | if $lesser_host_instance_key_arn != "" then .lesser_host_instance_key_arn = $lesser_host_instance_key_arn else . end | if $translation_enabled != "" then .translation_enabled = bool($translation_enabled) else . end | if $tip_enabled != "" then .tip_enabled = bool($tip_enabled) else . end | if $tip_chain_id != "" then .tip_chain_id = ($tip_chain_id|tonumber) else . end | if $tip_contract_address != "" then .tip_contract_address = $tip_contract_address else . end | if $ai_enabled != "" then .ai_enabled = bool($ai_enabled) else . end | if $ai_moderation_enabled != "" then .ai_moderation_enabled = bool($ai_moderation_enabled) else . end | if $ai_nsfw_detection_enabled != "" then .ai_nsfw_detection_enabled = bool($ai_nsfw_detection_enabled) else . end | if $ai_spam_detection_enabled != "" then .ai_spam_detection_enabled = bool($ai_spam_detection_enabled) else . end | if $ai_pii_detection_enabled != "" then .ai_pii_detection_enabled = bool($ai_pii_detection_enabled) else . end | if $ai_content_detection_enabled != "" then .ai_content_detection_enabled = bool($ai_content_detection_enabled) else . end\' > "$PROVISION_INPUT"',
				'STAGE_DOMAIN="$BASE_DOMAIN"',
				'if [ "$STAGE" != "live" ]; then STAGE_DOMAIN="$STAGE.$BASE_DOMAIN"; fi',
					'if [ "$RUN_MODE" = "lesser" ]; then',
				'  ./lesser up --app "$APP_SLUG" --base-domain "$BASE_DOMAIN" --aws-profile managed --provisioning-input "$PROVISION_INPUT"',
				'  if [ -n "${CONSENT_MESSAGE_B64:-}" ] && [ -n "${CONSENT_SIGNATURE:-}" ]; then ./lesser init-admin --base-domain "$BASE_DOMAIN" --aws-profile managed --provisioning-input "$PROVISION_INPUT"; else echo "Skipping init-admin (missing consent message/signature)."; fi',
				'  enable_agents',
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
							'    BODY_TAG=$(resolve_latest_release_tag "$BODY_OWNER" "$BODY_REPO")',
							'  fi',
							'  test -n "$BODY_TAG" && test "$BODY_TAG" != "null"',
							'  echo "Using lesser-body release: $BODY_TAG"',
							'  curl -sSfL -o lesser-body-src.tgz "https://github.com/$BODY_OWNER/$BODY_REPO/archive/refs/tags/$BODY_TAG.tar.gz"',
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
						'  echo "Building Lesser lambda artifacts (bin/*.zip)..."',
						'  ./lesser build lambdas',
						'  HOSTED_ZONE_NAME="${BASE_DOMAIN%.}."',
						'  HOSTED_ZONE_ID=$(aws route53 list-hosted-zones-by-name --profile managed --dns-name "$HOSTED_ZONE_NAME" --output json | jq -r --arg name "$HOSTED_ZONE_NAME" \'.HostedZones[] | select(.Name==$name and (.Config.PrivateZone|not)) | .Id\' | head -n 1)',
						'  test -n "$HOSTED_ZONE_ID" && test "$HOSTED_ZONE_ID" != "null"',
						'  HOSTED_ZONE_ID="${HOSTED_ZONE_ID#/hostedzone/}"',
						'  cd infra/cdk',
						'  AWS_PROFILE=managed cdk deploy "$APP_SLUG-$STAGE" --require-approval never -c app="$APP_SLUG" -c baseDomain="$BASE_DOMAIN" -c hostedZoneId="$HOSTED_ZONE_ID" -c stage="$STAGE" -c bodyEnabled=true -c soulEnabled=true -c lesserHostUrl="$LESSER_HOST_URL" -c lesserHostAttestationsUrl="$LESSER_HOST_ATTESTATIONS_URL" -c lesserHostInstanceKeyArn="$LESSER_HOST_INSTANCE_KEY_ARN" -c translationEnabled="$TRANSLATION_ENABLED" -c tipEnabled="$TIP_ENABLED" -c tipChainId="${TIP_CHAIN_ID:-}" -c tipContractAddress="${TIP_CONTRACT_ADDRESS:-}"',
						'  cd - >/dev/null',
						'  enable_agents',
						'  MCP_RECEIPT_PATH="$STATE_DIR/mcp-state.json"',
						'  jq -n --arg stage "$STAGE" --arg base_domain "$BASE_DOMAIN" --arg mcp_url "https://api.$STAGE_DOMAIN/mcp" \'{version:1,stage:$stage,base_domain:$base_domain,mcp_url:$mcp_url}\' > "$MCP_RECEIPT_PATH"',
						'  aws s3 cp "$MCP_RECEIPT_PATH" "s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY"',
						'else',
					'  fail "unknown RUN_MODE: $RUN_MODE"',
					'fi',
				].join('\n');

				// This buildspec is too large to inline into the CodeBuild project due to the 25,600 char limit.
				// Store it as an S3 asset and reference it by ARN to keep deployments reliable and future-proof.
				const provisionRunnerBuildSpecObject = {
					version: '0.2',
					env: {
						shell: 'bash',
					},
					phases: {
						install: {
							commands: [
								'set -euo pipefail',
								'echo "Installing runner tools..."',
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
				};
				const provisionRunnerBuildSpecPath = path.join(
					this.repoRoot(),
					'cdk',
					'.build',
					'provision-runner-buildspec.json',
				);
				fs.mkdirSync(path.dirname(provisionRunnerBuildSpecPath), { recursive: true });
				fs.writeFileSync(provisionRunnerBuildSpecPath, JSON.stringify(provisionRunnerBuildSpecObject), 'utf8');

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
					buildSpec: codebuild.BuildSpec.fromAsset(provisionRunnerBuildSpecPath),
				});

			// Note: soul registry artifacts live in the soul bucket, but the managed provisioning runner does not
			// fetch or write soul artifacts.

			const controlPlaneFn = this.goLambda('ControlPlaneApi', './cmd/control-plane-api', {
				STAGE: stage,
				STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PREVIEW_QUEUE_URL: previewQueue.queueUrl,
			SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			PROVISION_QUEUE_URL: provisionQueue.queueUrl,
			COMM_QUEUE_URL: commQueue.queueUrl,
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
		}, { timeoutSeconds: 120 });

		const trustFn = this.goLambda('TrustApi', './cmd/trust-api', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PREVIEW_QUEUE_URL: previewQueue.queueUrl,
			SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			SOUL_ENABLED: soulEnabled,
			ENS_GATEWAY_SIGNING_KEY_ID: ensGatewaySigningKey.keyId,
			ENS_GATEWAY_RESOLVER_ADDRESS: ensGatewayResolverAddress.trim(),
			ENS_GATEWAY_TTL_SECONDS: ensGatewayTTLSeconds.trim(),
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
					exclude: ['cdk/cdk.out/**', 'cdk/node_modules/**', 'cdk/.build/**', '.git/**', '**/.env'],
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
				ATTESTATION_SIGNING_KEY_ID: attestationSigningKey.keyId,
				ATTESTATION_PUBLIC_KEY_IDS: attestationSigningKey.keyId,
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

		const commWorkerFn = this.goLambda('CommWorker', './cmd/comm-worker', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			COMM_QUEUE_URL: commQueue.queueUrl,
			SOUL_ENABLED: soulEnabled,
			MANAGED_ORG_VENDING_ROLE_ARN: managedOrgVendingRoleArn,
			MANAGED_INSTANCE_ROLE_NAME: managedInstanceRoleName,
			MANAGED_DEFAULT_REGION: managedDefaultRegion,
		});

		stateTable.grantReadWriteData(controlPlaneFn);
		stateTable.grantReadWriteData(trustFn);
		stateTable.grantReadWriteData(renderWorkerFn);
		stateTable.grantReadWriteData(aiWorkerFn);
		stateTable.grantReadWriteData(soulReputationWorkerFn);
		stateTable.grantReadWriteData(provisionWorkerFn);
		stateTable.grantReadWriteData(commWorkerFn);
		artifactsBucket.grantReadWrite(controlPlaneFn);
		soulPackBucket.grantReadWrite(controlPlaneFn);
		soulPackBucket.grantReadWrite(soulReputationWorkerFn);
		artifactsBucket.grantReadWrite(trustFn);
		artifactsBucket.grantReadWrite(renderWorkerFn);
		artifactsBucket.grantRead(aiWorkerFn);
		artifactsBucket.grantRead(provisionWorkerFn);
		artifactsBucket.grantReadWrite(provisionRunnerProject);
		attestationSigningKey.grant(trustFn, 'kms:Sign', 'kms:GetPublicKey');
		ensGatewaySigningKey.grant(trustFn, 'kms:Sign', 'kms:GetPublicKey');
		attestationSigningKey.grant(aiWorkerFn, 'kms:Sign', 'kms:GetPublicKey');
		attestationSigningKey.grant(soulReputationWorkerFn, 'kms:Sign', 'kms:GetPublicKey');
		previewQueue.grantSendMessages(controlPlaneFn);
		previewQueue.grantSendMessages(trustFn);
		previewQueue.grantConsumeMessages(renderWorkerFn);
		safetyQueue.grantSendMessages(controlPlaneFn);
		safetyQueue.grantSendMessages(trustFn);
		safetyQueue.grantConsumeMessages(aiWorkerFn);
		provisionQueue.grantSendMessages(controlPlaneFn);
		provisionQueue.grantConsumeMessages(provisionWorkerFn);
		provisionQueue.grantSendMessages(provisionWorkerFn);
		commQueue.grantSendMessages(controlPlaneFn);
		commQueue.grantConsumeMessages(commWorkerFn);

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

		commWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['sts:AssumeRole'],
				resources: [`arn:aws:iam::*:role/${managedInstanceRoleName.trim() || 'OrganizationAccountAccessRole'}`],
			}),
		);
		if (managedOrgVendingRoleArn.trim()) {
			commWorkerFn.addToRolePolicy(
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
		commWorkerFn.addEventSource(new lambdaEventSources.SqsEventSource(commQueue, { batchSize: 1 }));

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
		for (const fn of [controlPlaneFn, trustFn, aiWorkerFn]) {
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
		const migaduSsmParamArns = [
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/migadu`,
		];
		const soulCommSsmParamArns = [
			cdk.Stack.of(this).formatArn({
				service: 'ssm',
				resource: 'parameter',
				resourceName: `lesser-host/soul/${stage}/*`,
			}),
		];
		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['ssm:GetParameter', 'ssm:GetParameters'],
				resources: migaduSsmParamArns,
			}),
		);
		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['ssm:GetParameter', 'ssm:GetParameters', 'ssm:PutParameter'],
				resources: soulCommSsmParamArns,
			}),
		);
		commWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['ssm:GetParameter', 'ssm:GetParameters'],
				resources: soulCommSsmParamArns,
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
		commWorkerFn.addToRolePolicy(
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

		const apiAccessLogRetention =
			stage === 'live' ? logs.RetentionDays.THREE_MONTHS : logs.RetentionDays.ONE_MONTH;
		const controlPlaneAccessLogs = new logs.LogGroup(this, 'ControlPlaneApiAccessLogs', {
			logGroupName: `/aws/apigwv2/${namePrefix}-control-plane`,
			retention: apiAccessLogRetention,
			removalPolicy,
		});
		const controlPlaneSseAccessLogs = new logs.LogGroup(this, 'ControlPlaneSseApiAccessLogs', {
			logGroupName: `/aws/apigw/${namePrefix}-control-plane-sse`,
			retention: apiAccessLogRetention,
			removalPolicy,
		});
		const trustAccessLogs = new logs.LogGroup(this, 'TrustApiAccessLogs', {
			logGroupName: `/aws/apigwv2/${namePrefix}-trust`,
			retention: apiAccessLogRetention,
			removalPolicy,
		});
		const apiThrottle = stage === 'live'
			? { throttlingRateLimit: 500, throttlingBurstLimit: 1000 }
			: { throttlingRateLimit: 100, throttlingBurstLimit: 200 };

		const controlPlaneApi = new apigwv2.HttpApi(this, 'ControlPlaneHttpApi', {
			apiName: `${namePrefix}-control-plane`,
			defaultIntegration: new apigwv2Integrations.HttpLambdaIntegration(
				'ControlPlaneIntegration', controlPlaneFn,
			),
		});
		// AppTheory can stream SSE correctly through API Gateway REST proxy responses, but not via HttpApi.
		// Keep the main control plane on HttpApi and route only the soul mint conversation subtree to REST.
		const controlPlaneSseApi = new apigw.LambdaRestApi(this, 'ControlPlaneSseRestApi', {
			restApiName: `${namePrefix}-control-plane-sse`,
			handler: controlPlaneFn,
			proxy: true,
			cloudWatchRole: true,
			endpointConfiguration: {
				types: [apigw.EndpointType.REGIONAL],
			},
			deployOptions: {
				stageName: stage,
				loggingLevel: apigw.MethodLoggingLevel.ERROR,
				metricsEnabled: true,
				throttlingRateLimit: apiThrottle.throttlingRateLimit,
				throttlingBurstLimit: apiThrottle.throttlingBurstLimit,
				accessLogDestination: new apigw.LogGroupLogDestination(controlPlaneSseAccessLogs),
				accessLogFormat: apigw.AccessLogFormat.custom(JSON.stringify({
					requestId: apigw.AccessLogField.contextRequestId(),
					ip: apigw.AccessLogField.contextIdentitySourceIp(),
					requestTime: apigw.AccessLogField.contextRequestTime(),
					method: apigw.AccessLogField.contextHttpMethod(),
					path: apigw.AccessLogField.contextResourcePath(),
					protocol: apigw.AccessLogField.contextProtocol(),
					status: apigw.AccessLogField.contextStatus(),
					responseLength: apigw.AccessLogField.contextResponseLength(),
					integrationError: apigw.AccessLogField.contextIntegrationErrorMessage(),
					userAgent: apigw.AccessLogField.contextIdentityUserAgent(),
				})),
			},
		});

		const trustApi = new apigwv2.HttpApi(this, 'TrustHttpApi', {
			apiName: `${namePrefix}-trust`,
			defaultIntegration: new apigwv2Integrations.HttpLambdaIntegration(
				'TrustIntegration', trustFn,
			),
		});

		new logs.CfnResourcePolicy(this, 'ApiGatewayAccessLogsResourcePolicy', {
			policyName: `${namePrefix}-apigw-access-logs`,
			policyDocument: JSON.stringify({
				Version: '2012-10-17',
				Statement: [
					{
						Sid: 'ApiGatewayAccessLogs',
						Effect: 'Allow',
						Principal: { Service: 'apigateway.amazonaws.com' },
						Action: ['logs:CreateLogStream', 'logs:PutLogEvents'],
						Resource: [
							`${controlPlaneAccessLogs.logGroupArn}:*`,
							`${controlPlaneSseAccessLogs.logGroupArn}:*`,
							`${trustAccessLogs.logGroupArn}:*`,
						],
					},
				],
			}),
		});

		const controlPlaneStage = controlPlaneApi.defaultStage?.node.defaultChild as apigwv2.CfnStage | undefined;
		if (controlPlaneStage) {
			controlPlaneStage.accessLogSettings = {
				destinationArn: controlPlaneAccessLogs.logGroupArn,
				format: JSON.stringify({
					requestId: '$context.requestId',
					ip: '$context.identity.sourceIp',
					requestTime: '$context.requestTime',
					method: '$context.httpMethod',
					path: '$context.path',
					protocol: '$context.protocol',
					status: '$context.status',
					responseLength: '$context.responseLength',
					routeKey: '$context.routeKey',
					integrationError: '$context.integrationErrorMessage',
					userAgent: '$context.identity.userAgent',
				}),
			};
			controlPlaneStage.defaultRouteSettings = apiThrottle;
		}

		const trustStage = trustApi.defaultStage?.node.defaultChild as apigwv2.CfnStage | undefined;
		if (trustStage) {
			trustStage.accessLogSettings = {
				destinationArn: trustAccessLogs.logGroupArn,
				format: JSON.stringify({
					requestId: '$context.requestId',
					ip: '$context.identity.sourceIp',
					requestTime: '$context.requestTime',
					method: '$context.httpMethod',
					path: '$context.path',
					protocol: '$context.protocol',
					status: '$context.status',
					responseLength: '$context.responseLength',
					routeKey: '$context.routeKey',
					integrationError: '$context.integrationErrorMessage',
					userAgent: '$context.identity.userAgent',
				}),
			};
			trustStage.defaultRouteSettings = apiThrottle;
		}

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

		const accessLogsBucket = new s3.Bucket(this, 'AccessLogsBucket', {
			bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-access-logs`,
			accessControl: s3.BucketAccessControl.LOG_DELIVERY_WRITE,
			objectOwnership: s3.ObjectOwnership.OBJECT_WRITER,
			blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
			enforceSSL: true,
			lifecycleRules: [
				{
					id: 'ExpireAccessLogs',
					expiration: cdk.Duration.days(stage === 'live' ? 180 : 30),
				},
			],
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
		const safeAppCsp = [
			"default-src 'none'",
			"base-uri 'none'",
			"object-src 'none'",
			"frame-ancestors https://safe.global https://*.safe.global",
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
		const safeAppSecurityHeaders = new cloudfront.ResponseHeadersPolicy(this, 'SafeAppSecurityHeaders', {
			responseHeadersPolicyName: `${namePrefix}-safe-app-security`,
			corsBehavior: {
				accessControlAllowCredentials: false,
				accessControlAllowHeaders: ['Authorization', 'Content-Type', 'X-Requested-With'],
				accessControlAllowMethods: ['GET', 'HEAD', 'OPTIONS'],
				accessControlAllowOrigins: ['*'],
				accessControlMaxAge: cdk.Duration.minutes(10),
				originOverride: true,
			},
			securityHeadersBehavior: {
				contentSecurityPolicy: {
					contentSecurityPolicy: safeAppCsp,
					override: true,
				},
				contentTypeOptions: { override: true },
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

		const apiSecurityHeaders = new cloudfront.ResponseHeadersPolicy(this, 'ApiSecurityHeaders', {
			responseHeadersPolicyName: `${namePrefix}-api-security`,
			securityHeadersBehavior: {
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
    uri.startsWith("/webhooks/") ||
    uri.startsWith("/resolve") ||
    uri.startsWith("/health") ||
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
		const controlPlaneSseOrigin = new origins.RestApiOrigin(controlPlaneSseApi, {
			readTimeout: cdk.Duration.seconds(120),
			responseCompletionTimeout: cdk.Duration.seconds(180),
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
			responseHeadersPolicy: apiSecurityHeaders,
		};
		const apiSseBehavior: cloudfront.BehaviorOptions = {
			origin: controlPlaneSseOrigin,
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_ALL,
			cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
			originRequestPolicy: cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
			responseHeadersPolicy: apiSecurityHeaders,
		};

		const trustApiBehavior: cloudfront.BehaviorOptions = {
			origin: trustOrigin,
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_ALL,
			cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
			originRequestPolicy: cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
			responseHeadersPolicy: apiSecurityHeaders,
		};

		const trustBehaviorNoCache: cloudfront.BehaviorOptions = {
			origin: trustOrigin,
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
			cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
			originRequestPolicy: cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
			responseHeadersPolicy: apiSecurityHeaders,
		};

		const trustBehaviorCached: cloudfront.BehaviorOptions = {
			origin: trustOrigin,
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
			cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
			originRequestPolicy: cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
			responseHeadersPolicy: apiSecurityHeaders,
		};

		const webAcl = new wafv2.CfnWebACL(this, 'WebAcl', {
			name: `${namePrefix}-web-acl`,
			description: `${namePrefix} WAF`,
			scope: 'CLOUDFRONT',
			defaultAction: { allow: {} },
			visibilityConfig: {
				cloudWatchMetricsEnabled: true,
				metricName: `${namePrefix}-web-acl`,
				sampledRequestsEnabled: true,
			},
			rules: [
				{
					name: 'AWSManagedRulesCommonRuleSet',
					priority: 0,
					overrideAction: { none: {} },
					statement: {
						managedRuleGroupStatement: {
							vendorName: 'AWS',
							name: 'AWSManagedRulesCommonRuleSet',
						},
					},
					visibilityConfig: {
						cloudWatchMetricsEnabled: true,
						metricName: `${namePrefix}-waf-common`,
						sampledRequestsEnabled: true,
					},
				},
				{
					name: 'AWSManagedRulesKnownBadInputsRuleSet',
					priority: 1,
					overrideAction: { none: {} },
					statement: {
						managedRuleGroupStatement: {
							vendorName: 'AWS',
							name: 'AWSManagedRulesKnownBadInputsRuleSet',
						},
					},
					visibilityConfig: {
						cloudWatchMetricsEnabled: true,
						metricName: `${namePrefix}-waf-bad-inputs`,
						sampledRequestsEnabled: true,
					},
				},
				{
					name: 'IpRateLimit',
					priority: 2,
					action: { block: {} },
					statement: {
						rateBasedStatement: {
							limit: stage === 'live' ? 2000 : 5000,
							aggregateKeyType: 'IP',
						},
					},
					visibilityConfig: {
						cloudWatchMetricsEnabled: true,
						metricName: `${namePrefix}-waf-ip-rate-limit`,
						sampledRequestsEnabled: true,
					},
				},
			],
		});

		const webDistribution = new cloudfront.Distribution(this, 'WebDistribution', {
			defaultRootObject: 'index.html',
			certificate: webCert,
			domainNames: webCert ? [webDomainName] : undefined,
			webAclId: webAcl.attrArn,
			enableLogging: true,
			logBucket: accessLogsBucket,
			logFilePrefix: `${namePrefix}/cloudfront/`,
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
				'safe-app*': {
					origin: new origins.S3Origin(webBucket, { originAccessIdentity: webOai }),
					viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
					allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
					cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
					responseHeadersPolicy: safeAppSecurityHeaders,
					functionAssociations: [
						{
							function: webSpaRewriteFn,
							eventType: cloudfront.FunctionEventType.VIEWER_REQUEST,
						},
					],
				},
				'resolve*': trustBehaviorNoCache,
				'health*': trustBehaviorNoCache,
				'api/v1/previews*': trustApiBehavior,
				'api/v1/renders*': trustApiBehavior,
				'api/v1/publish/jobs*': trustApiBehavior,
				'api/v1/soul/agents/*/update-registration': trustApiBehavior,
				'api/v1/ai/*': trustApiBehavior,
				'api/v1/budget/debit': trustApiBehavior,
				'api/v1/soul/agents/register/*/mint-conversation*': apiSseBehavior,
				'api/v1/soul/agents/*/mint-conversation*': apiSseBehavior,

				'api/*': apiBehavior,
				'auth/*': apiBehavior,
				'webhooks/*': apiBehavior,
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

					const commDashboard = new cloudwatch.Dashboard(this, 'CommDashboard', {
						dashboardName: `${namePrefix}-comm`,
					});

					const commWebhook5xx = new cloudwatch.MathExpression({
						expression: `SUM(SEARCH('{lesser-host,Stage,Service,Provider,Channel} MetricName="CommWebhook5xx" AND Stage="${stage}" AND Service="control-plane-api"', 'Sum', 300))`,
						period: cdk.Duration.minutes(5),
					});
					const commOutboundProviderRejected = new cloudwatch.MathExpression({
						expression: `SUM(SEARCH('{lesser-host,Stage,Service,Instance,Channel,Provider,Status} MetricName="CommOutboundRequests" AND Stage="${stage}" AND Service="control-plane-api" AND Status="provider_rejected"', 'Sum', 300))`,
						period: cdk.Duration.minutes(5),
					});
					const commWebhook5xxAlarmMetric = new cloudwatch.Metric({
						namespace: 'lesser-host',
						metricName: 'CommWebhook5xx',
						dimensionsMap: { Stage: stage, Service: 'control-plane-api' },
						statistic: 'Sum',
						period: cdk.Duration.minutes(5),
					});
					const commOutboundProviderRejectedAlarmMetric = new cloudwatch.Metric({
						namespace: 'lesser-host',
						metricName: 'CommOutboundRequests',
						dimensionsMap: { Stage: stage, Service: 'control-plane-api', Status: 'provider_rejected' },
						statistic: 'Sum',
						period: cdk.Duration.minutes(5),
					});

					commDashboard.addWidgets(
						new cloudwatch.GraphWidget({
							title: 'Comm Queue Depth',
							left: [commQueue.metricApproximateNumberOfMessagesVisible({ period: cdk.Duration.minutes(5) })],
							width: 12,
						}),
						new cloudwatch.GraphWidget({
							title: 'Comm Queue Oldest Message (s)',
							left: [commQueue.metricApproximateAgeOfOldestMessage({ period: cdk.Duration.minutes(5) })],
							width: 12,
						}),
						new cloudwatch.GraphWidget({
							title: 'Comm DLQ Visible',
							left: [commDLQ.metricApproximateNumberOfMessagesVisible({ period: cdk.Duration.minutes(5) })],
							width: 12,
						}),
						new cloudwatch.GraphWidget({
							title: 'Comm Webhook 5xx (Total)',
							left: [commWebhook5xx],
							width: 12,
						}),
						new cloudwatch.GraphWidget({
							title: 'Comm Outbound Provider Rejected (Total)',
							left: [commOutboundProviderRejected],
							width: 24,
						}),
					);

					const controlPlaneLogGroupName = `/aws/lambda/${controlPlaneFn.functionName}`;
					logs.LogGroup.fromLogGroupName(this, 'ControlPlaneApiLogGroup', controlPlaneLogGroupName);

					commDashboard.addWidgets(
						new cloudwatch.LogQueryWidget({
							title: 'Comm Webhook Failures',
							width: 24,
							height: 6,
							logGroupNames: [controlPlaneLogGroupName],
							queryString: [
								'fields @timestamp, path, status, error_code, request_id',
								'| filter path like /webhooks/comm/ and status >= 400',
								'| sort @timestamp desc',
								'| limit 50',
							].join('\n'),
						}),
					);

					new cloudwatch.Alarm(this, 'CommWebhook5xxAlarm', {
						alarmName: `${namePrefix}-comm-webhooks-5xx`,
						metric: commWebhook5xxAlarmMetric,
						threshold: 1,
						evaluationPeriods: 1,
						datapointsToAlarm: 1,
						treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
					});
					new cloudwatch.Alarm(this, 'CommQueueOldestAgeAlarm', {
						alarmName: `${namePrefix}-comm-queue-oldest-age`,
						metric: commQueue.metricApproximateAgeOfOldestMessage({ period: cdk.Duration.minutes(5) }),
						threshold: 300,
						evaluationPeriods: 1,
						datapointsToAlarm: 1,
						treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
					});
					new cloudwatch.Alarm(this, 'CommOutboundProviderRejectedAlarm', {
						alarmName: `${namePrefix}-comm-outbound-provider-rejected`,
						metric: commOutboundProviderRejectedAlarmMetric,
						threshold: 10,
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
