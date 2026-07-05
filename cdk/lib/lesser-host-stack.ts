import * as path from 'node:path';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import { AppTheorySsrSite, AppTheorySsrSiteMode } from '@theory-cloud/apptheory-cdk';
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
import * as ses from 'aws-cdk-lib/aws-ses';
import * as sesActions from 'aws-cdk-lib/aws-ses-actions';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import * as sqs from 'aws-cdk-lib/aws-sqs';
import * as apigw from 'aws-cdk-lib/aws-apigateway';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import * as apigwv2Integrations from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as wafv2 from 'aws-cdk-lib/aws-wafv2';
import { renderProvisionRunnerBuildCommands, renderProvisionRunnerPreBuildCommands } from './provision-runner-buildspec';
import { CostTelemetryWorker } from './cost-telemetry-worker-construct';
import { configureHostedGenesisMicrovm } from './hosted-genesis-microvm';
import { INBOUND_EMAIL_RULE_SET_NAME } from './ses-inbound-rule-set-name';
import { soulEmailInboundDomainFromContext } from './soul-email-inbound-domain';
import {
	ensGatewayChainConfigForStage,
	ensGatewayContextForStage,
	ensGatewayResolverAddressFromContext,
	ensGatewayRootNameFromContext,
} from './ens-gateway-config';
import { hostWebAclRules } from './web-acl-rules';
import { readWebDomainConfig } from './deploy-local-domain-config';
export { readWebDomainConfig, type WebDomainConfig } from './deploy-local-domain-config';
export interface LesserHostStackProps extends cdk.StackProps {
	stage: string;
	/** Path to the gitignored deployer-local domain config; tests pass a fixture. */
	domainConfigPath?: string;
}

const defaultManagedInstanceRoleName = 'OrganizationAccountAccessRole';
const managedOrgVendingRoleArnPattern = /^arn:(aws|aws-us-gov|aws-cn):iam::\d{12}:role\/[\w+=,.@/-]+$/;
const examplePlaceholderPattern = /<[^>]*YOUR[^>]*>|YOUR_[A-Z0-9_]+/i;

export function shouldUseLocalWebBundling(env: NodeJS.ProcessEnv = process.env): boolean {
	return env.CI !== 'true' && env.GITHUB_ACTIONS !== 'true';
}

function sanitizedManagedOrgVendingRoleArn(rawValue: unknown): string {
	const value = sanitizedOptionalContextString(rawValue);
	if (value === '' || examplePlaceholderPattern.test(value)) {
		return '';
	}
	if (!managedOrgVendingRoleArnPattern.test(value)) {
		throw new Error('managedOrgVendingRoleArn must be a valid IAM role ARN or blank');
	}
	return value;
}

function sanitizedOptionalContextString(rawValue: unknown): string {
	const value = typeof rawValue === 'string' ? rawValue.trim() : '';
	return examplePlaceholderPattern.test(value) ? '' : value;
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
		stateTable.addGlobalSecondaryIndex({
			indexName: 'gsi2',
			partitionKey: { name: 'gsi2PK', type: dynamodb.AttributeType.STRING },
			sortKey: { name: 'gsi2SK', type: dynamodb.AttributeType.STRING },
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

		const inboundEmailBucket = new s3.Bucket(this, 'InboundEmailBucket', {
			bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-inbound-email`,
			blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
			enforceSSL: true,
			lifecycleRules: [
				{
					id: 'ExpireInboundEmail',
					prefix: 'ses/inbound/',
					expiration: cdk.Duration.days(14),
				},
			],
			removalPolicy,
			autoDeleteObjects: stage !== 'live',
		});

		const soulCommMailboxRetentionDays = 90;
		const soulCommMailboxBucket = new s3.Bucket(this, 'SoulCommMailboxBucket', {
			bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-soul-comm-mailbox`,
			blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
			enforceSSL: true,
			encryption: s3.BucketEncryption.KMS_MANAGED,
			lifecycleRules: [
				{
					id: 'ExpireSoulCommMailboxContent',
					prefix: 'mailbox/v1/',
					expiration: cdk.Duration.days(soulCommMailboxRetentionDays),
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
			visibilityTimeout: cdk.Duration.minutes(3),
			deadLetterQueue: { queue: safetyDLQ, maxReceiveCount: 3 },
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		safetyQueue.applyRemovalPolicy(removalPolicy);

		const hostedGenesisDLQ = new sqs.Queue(this, 'HostedGenesisDLQ', {
			queueName: `${namePrefix}-hosted-genesis-dlq`,
			retentionPeriod: cdk.Duration.days(14),
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		hostedGenesisDLQ.applyRemovalPolicy(removalPolicy);
		const hostedGenesisQueue = new sqs.Queue(this, 'HostedGenesisQueue', {
			queueName: `${namePrefix}-hosted-genesis-queue`,
			visibilityTimeout: cdk.Duration.minutes(3),
			deadLetterQueue: { queue: hostedGenesisDLQ, maxReceiveCount: 3 },
			encryption: sqs.QueueEncryption.SQS_MANAGED,
		});
		hostedGenesisQueue.applyRemovalPolicy(removalPolicy);

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
		new cloudwatch.Alarm(this, 'HostedGenesisDLQAlarm', {
			alarmName: `${namePrefix}-hosted-genesis-dlq-visible`,
			metric: hostedGenesisDLQ.metricApproximateNumberOfMessagesVisible({ period: dlqAlarmPeriod }),
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
			const ensGatewayChain = ensGatewayChainConfigForStage(stage);
			const ensGatewayEnabled = ensGatewayContextForStage(this, stage, 'ensGatewayEnabled');
			const ensGatewayRootName = ensGatewayRootNameFromContext(this);
			const ensGatewayResolverAddress = ensGatewayResolverAddressFromContext(this, stage);
			const ensGatewayTTLSeconds = ensGatewayContextForStage(this, stage, 'ensGatewayTtlSeconds');

		const managedProvisioningEnabled =
			(this.node.tryGetContext('managedProvisioningEnabled') as string | undefined) ?? '';
		const managedOrgVendingRoleArn = sanitizedManagedOrgVendingRoleArn(
			this.node.tryGetContext('managedOrgVendingRoleArn'),
		);
		const managedOrgVendingRoleEnv: Record<string, string> = managedOrgVendingRoleArn
			? { MANAGED_ORG_VENDING_ROLE_ARN: managedOrgVendingRoleArn }
			: {};
		const managedParentDomain = (this.node.tryGetContext('managedParentDomain') as string | undefined) ?? '';
		const managedParentHostedZoneId =
			sanitizedOptionalContextString(this.node.tryGetContext('managedParentHostedZoneId'));
		const managedInstanceRoleName =
			(this.node.tryGetContext('managedInstanceRoleName') as string | undefined) ?? defaultManagedInstanceRoleName;
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
			const managedProvisionConsentEncryptionKeyHex =
				(this.node.tryGetContext('managedProvisionConsentEncryptionKeyHex') as string | undefined) ?? '';
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
			const managedEnableAgentRegistration =
				(this.node.tryGetContext('managedEnableAgentRegistration') as string | undefined) ?? 'true';

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
		const soulEmailInboundDomain = soulEmailInboundDomainFromContext(this, stage);
		const inboundEmailS3Prefix = 'ses/inbound/';

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

			const provisionRunnerPreBuild = renderProvisionRunnerPreBuildCommands();
			const provisionRunnerBuild = renderProvisionRunnerBuildCommands();

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
									'RUN_MODE="${RUN_MODE:-lesser}"',
									[
										'if [ "$RUN_MODE" = "lesser-body" ]; then',
										'  echo "Skipping Node/CDK/pnpm install for RUN_MODE=lesser-body"',
										'else',
										'  node -v || true',
										'  npm -v || true',
										'  if ! command -v n >/dev/null 2>&1; then npm install -g n; fi',
										'  n 24',
										'  hash -r',
										'  node -v',
										'  npm install -g aws-cdk@2',
										'  npm install -g pnpm@9',
										'  cdk --version',
										'  pnpm --version',
										'fi',
									].join('\n'),
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
						// Newer Lesser releases can exhaust SMALL during api lambda compilation in lesser-mcp/body-only flows.
						computeType: codebuild.ComputeType.LARGE,
					},
					environmentVariables: {
					GITHUB_OWNER: { value: lesserGitHubOwner },
					GITHUB_REPO: { value: lesserGitHubRepo },
					LESSER_BODY_GITHUB_OWNER: { value: lesserBodyGitHubOwner },
					LESSER_BODY_GITHUB_REPO: { value: lesserBodyGitHubRepo },
					LESSER_BODY_VERSION: { value: managedLesserBodyDefaultVersion.trim() },
					MANAGED_ENABLE_AGENT_REGISTRATION: { value: managedEnableAgentRegistration.trim() },
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
			SOUL_COMM_MAILBOX_BUCKET_NAME: soulCommMailboxBucket.bucketName,
			SOUL_COMM_MAILBOX_RETENTION_DAYS: String(soulCommMailboxRetentionDays),
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
				MANAGED_PROVISION_CONSENT_ENCRYPTION_KEY_HEX: managedProvisionConsentEncryptionKeyHex.trim(),
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
			SOUL_PACK_BUCKET_NAME: soulPackBucket.bucketName,
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
			SOUL_CHAIN_ID: soulChainId,
			SOUL_RPC_URL_SSM_PARAM: soulRpcUrlSsmParam,
			SOUL_REGISTRY_CONTRACT_ADDRESS: soulRegistryContractAddress,
			SOUL_REPUTATION_ATTESTATION_CONTRACT_ADDRESS: soulReputationAttestationContractAddress,
			SOUL_VALIDATION_ATTESTATION_CONTRACT_ADDRESS: soulValidationAttestationContractAddress,
			SOUL_ADMIN_SAFE_ADDRESS: soulAdminSafeAddress,
			SOUL_TX_MODE: soulTxMode,
			SOUL_SUPPORTED_CAPABILITIES: soulSupportedCapabilities,
			SOUL_PACK_BUCKET_NAME: soulPackBucket.bucketName,
			ENS_GATEWAY_ENABLED: ensGatewayEnabled.trim(),
			ENS_GATEWAY_CHAIN_ID: ensGatewayChain.chainId,
			ENS_GATEWAY_CHAIN_NAME: ensGatewayChain.chainName,
			ENS_GATEWAY_ROOT_NAME: ensGatewayRootName,
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
			HOSTED_GENESIS_QUEUE_URL: hostedGenesisQueue.queueUrl,
			ATTESTATION_SIGNING_KEY_ID: attestationSigningKey.keyId,
			ATTESTATION_PUBLIC_KEY_IDS: attestationSigningKey.keyId,
		}, { timeoutSeconds: 120 });

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
			...managedOrgVendingRoleEnv,
			MANAGED_PARENT_DOMAIN: managedParentDomain,
			MANAGED_PARENT_HOSTED_ZONE_ID: managedParentHostedZoneId,
			MANAGED_INSTANCE_ROLE_NAME: managedInstanceRoleName,
			MANAGED_TARGET_OU_ID: managedTargetOuId,
			MANAGED_ACCOUNT_EMAIL_TEMPLATE: managedAccountEmailTemplate,
			MANAGED_ACCOUNT_NAME_PREFIX: managedAccountNamePrefix,
				MANAGED_DEFAULT_REGION: managedDefaultRegion,
				MANAGED_LESSER_DEFAULT_VERSION: managedLesserDefaultVersion,
				MANAGED_PROVISION_RUNNER_PROJECT_NAME: provisionRunnerProjectName,
				MANAGED_PROVISION_RUNNER_ROLE_ARN: provisionRunnerProject.role?.roleArn ?? '',
				MANAGED_LESSER_GITHUB_OWNER: lesserGitHubOwner,
				MANAGED_LESSER_GITHUB_REPO: lesserGitHubRepo,
				MANAGED_LESSER_GITHUB_TOKEN_SSM_PARAM: managedLesserGitHubTokenSsmParam.trim(),
				MANAGED_LESSER_BODY_DEFAULT_VERSION: managedLesserBodyDefaultVersion,
				MANAGED_LESSER_BODY_GITHUB_OWNER: lesserBodyGitHubOwner,
				MANAGED_LESSER_BODY_GITHUB_REPO: lesserBodyGitHubRepo,
				MANAGED_PROVISION_CONSENT_ENCRYPTION_KEY_HEX: managedProvisionConsentEncryptionKeyHex.trim(),
			});

		const commWorkerFn = this.goLambda('CommWorker', './cmd/comm-worker', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			COMM_QUEUE_URL: commQueue.queueUrl,
			SOUL_COMM_MAILBOX_BUCKET_NAME: soulCommMailboxBucket.bucketName,
			SOUL_COMM_MAILBOX_RETENTION_DAYS: String(soulCommMailboxRetentionDays),
			SOUL_ENABLED: soulEnabled,
			...managedOrgVendingRoleEnv,
			MANAGED_INSTANCE_ROLE_NAME: managedInstanceRoleName,
			MANAGED_DEFAULT_REGION: managedDefaultRegion,
		});
		const emailIngressFn = this.goLambda(
			'EmailIngress',
			'./cmd/email-ingress',
			{
				STAGE: stage,
				COMM_QUEUE_URL: commQueue.queueUrl,
				SOUL_EMAIL_INBOUND_DOMAIN: soulEmailInboundDomain,
				INBOUND_EMAIL_BUCKET_NAME: inboundEmailBucket.bucketName,
				INBOUND_EMAIL_S3_PREFIX: inboundEmailS3Prefix,
			},
			{ memorySize: 512, timeoutSeconds: 30 },
		);
		const costTelemetry = new CostTelemetryWorker(this, 'CostTelemetry', { namePrefix, repoRoot, stage });
		void configureHostedGenesisMicrovm(this, { stage, namePrefix, repoRoot, removalPolicy, stateTable, controlPlaneFunction: controlPlaneFn });
		stateTable.grantReadWriteData(controlPlaneFn);
		stateTable.grantReadWriteData(trustFn);
		stateTable.grantReadWriteData(renderWorkerFn);
		stateTable.grantReadWriteData(aiWorkerFn);
		stateTable.grantReadWriteData(soulReputationWorkerFn);
		stateTable.grantReadWriteData(provisionWorkerFn);
		stateTable.grantReadWriteData(commWorkerFn);
		artifactsBucket.grantReadWrite(controlPlaneFn);
		soulCommMailboxBucket.grantReadWrite(controlPlaneFn);
		soulCommMailboxBucket.grantReadWrite(commWorkerFn);
		soulPackBucket.grantReadWrite(controlPlaneFn);
		soulPackBucket.grantReadWrite(trustFn);
		soulPackBucket.grantReadWrite(soulReputationWorkerFn);
		artifactsBucket.grantReadWrite(trustFn);
		artifactsBucket.grantReadWrite(renderWorkerFn);
		artifactsBucket.grantRead(aiWorkerFn);
		artifactsBucket.grantRead(provisionWorkerFn);
		artifactsBucket.grantReadWrite(provisionRunnerProject);
		inboundEmailBucket.grantRead(emailIngressFn);
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
		hostedGenesisQueue.grantConsumeMessages(aiWorkerFn);
		provisionQueue.grantSendMessages(controlPlaneFn);
		provisionQueue.grantConsumeMessages(provisionWorkerFn);
		provisionQueue.grantSendMessages(provisionWorkerFn);
		commQueue.grantSendMessages(controlPlaneFn);
		commQueue.grantConsumeMessages(commWorkerFn);
		commQueue.grantSendMessages(emailIngressFn);

		const inboundEmailIdentity = new ses.CfnEmailIdentity(this, 'InboundEmailIdentity', {
			emailIdentity: soulEmailInboundDomain,
			dkimSigningAttributes: {
				nextSigningKeyLength: 'RSA_2048_BIT',
			},
		});
		const inboundEmailRuleSet = ses.ReceiptRuleSet.fromReceiptRuleSetName(
			this,
			'InboundEmailRuleSet',
			INBOUND_EMAIL_RULE_SET_NAME,
		);
		const inboundEmailRule = inboundEmailRuleSet.addRule(`Ingress-${stage}`, {
			receiptRuleName: `${namePrefix}-ingress`,
			enabled: true,
			recipients: [soulEmailInboundDomain],
			scanEnabled: true,
			actions: [
				new sesActions.S3({
					bucket: inboundEmailBucket,
					objectKeyPrefix: inboundEmailS3Prefix,
				}),
				new sesActions.Lambda({
					function: emailIngressFn,
					invocationType: sesActions.LambdaInvocationType.EVENT,
				}),
			],
			tlsPolicy: ses.TlsPolicy.REQUIRE,
		});
		inboundEmailRule.node.addDependency(inboundEmailIdentity);

		const managedInstanceRoleArn = `arn:aws:iam::*:role/${managedInstanceRoleName.trim() || defaultManagedInstanceRoleName}`;
		for (const principal of [provisionRunnerProject, provisionWorkerFn, controlPlaneFn, commWorkerFn]) {
			principal.addToRolePolicy(
				new iam.PolicyStatement({
					actions: ['sts:AssumeRole'],
					resources: [managedInstanceRoleArn],
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

		if (managedOrgVendingRoleArn.trim()) {
			for (const fn of [provisionWorkerFn, controlPlaneFn, commWorkerFn]) {
				fn.addToRolePolicy(
					new iam.PolicyStatement({
						actions: ['sts:AssumeRole'],
						resources: [managedOrgVendingRoleArn.trim()],
					}),
				);
			}
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
		aiWorkerFn.addEventSource(new lambdaEventSources.SqsEventSource(hostedGenesisQueue, { batchSize: 1 }));
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
		for (const fn of [controlPlaneFn, trustFn]) {
			fn.addToRolePolicy(
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
		}
		const migaduSsmParamArns = [
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/migadu`,
		];
		const telnyxSsmParamArns = [
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/telnyx`,
		];
		const soulCommSsmParamArns = [
			cdk.Stack.of(this).formatArn({
				service: 'ssm',
				resource: 'parameter',
				resourceName: `lesser-host/soul/${stage}/*`,
			}),
		];
		const commWebhookSsmParamArns = [
			cdk.Stack.of(this).formatArn({
				service: 'ssm',
				resource: 'parameter',
				resourceName: `lesser-host/comm/${stage}/webhook`,
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
				actions: ['ssm:GetParameter', 'ssm:GetParameters'],
				resources: telnyxSsmParamArns,
			}),
		);
		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['ssm:GetParameter', 'ssm:GetParameters'],
				resources: commWebhookSsmParamArns,
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
			for (const fn of [controlPlaneFn, trustFn]) {
				fn.addToRolePolicy(
					new iam.PolicyStatement({
						actions: ['ssm:GetParameter'],
						resources: [
							`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/${soulRpcUrlSsmParam.replace(/^\//, '')}`,
						],
					}),
				);
			}
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

		const updateSweepRule = new events.Rule(this, 'UpdateSweepRule', {
			ruleName: `${namePrefix}-update-sweep`,
			schedule: events.Schedule.rate(cdk.Duration.minutes(5)),
		});
		updateSweepRule.addTarget(new targets.LambdaFunction(provisionWorkerFn));

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

		const controlPlaneIntegration = new apigwv2Integrations.HttpLambdaIntegration(
			'ControlPlaneIntegration', controlPlaneFn,
		);
		const controlPlaneApi = new apigwv2.HttpApi(this, 'ControlPlaneHttpApi', {
			apiName: `${namePrefix}-control-plane`,
			defaultIntegration: controlPlaneIntegration,
		});
		controlPlaneApi.addRoutes({
			path: '/api/v1/soul/instance/agents/{agentId}/mint-conversations/{conversationId}',
			methods: [apigwv2.HttpMethod.GET],
			integration: controlPlaneIntegration,
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
					// HTTP API access logs cannot transform path parameters. Persist the
					// route key instead of the raw path so private mint-conversation
					// identifiers never land in API Gateway access logs; Lambda request
					// logs retain sanitized path correlation.
					path: '$context.routeKey',
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

		const webDomain = readWebDomainConfig(stage, props.domainConfigPath);
		const webDomainName = stage === 'live' ? webDomain.rootDomain : `${stage}.${webDomain.rootDomain}`;

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

		const webZone = route53.HostedZone.fromHostedZoneAttributes(this, 'WebHostedZone', {
			hostedZoneId: webDomain.hostedZoneId,
			zoneName: webDomain.hostedZoneName,
		});
		const webCert = new acm.DnsValidatedCertificate(this, 'WebCertificate', {
			domainName: webDomainName,
			hostedZone: webZone,
			region: 'us-east-1',
		});

		const webOai = new cloudfront.OriginAccessIdentity(this, 'WebOAI');
		webBucket.grantRead(webOai);

		const controlPlaneDomain = `${controlPlaneApi.httpApiId}.execute-api.${cdk.Aws.REGION}.amazonaws.com`;
		const trustDomain = `${trustApi.httpApiId}.execute-api.${cdk.Aws.REGION}.amazonaws.com`;

		const controlPlaneOrigin = new origins.HttpOrigin(controlPlaneDomain, {
			protocolPolicy: cloudfront.OriginProtocolPolicy.HTTPS_ONLY,
		});
		// Avoid RestApiOrigin: its generated DeploymentStage edge cycles through PUBLIC_BASE_URL.
		const controlPlaneSseOrigin = new origins.HttpOrigin(cdk.Fn.join('', [controlPlaneSseApi.restApiId, `.execute-api.${cdk.Aws.REGION}.`, cdk.Aws.URL_SUFFIX]), {
			originPath: `/${stage}`, protocolPolicy: cloudfront.OriginProtocolPolicy.HTTPS_ONLY,
			readTimeout: cdk.Duration.seconds(120), responseCompletionTimeout: cdk.Duration.seconds(180),
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
			rules: hostWebAclRules(namePrefix, stage),
		});

		// Pre-build web/dist so SSR assets exist before fromAsset; AppTheorySsrSite
		// wraps the SSR Lambda with an OAC-protected Function URL (AWS_IAM fail-closed).
		this.ensureWebBuild();
		const webSsrFn = new lambda.Function(this, 'WebSsrFn', {
			functionName: `${namePrefix}-web-ssr`,
			code: lambda.Code.fromAsset(path.join(this.repoRoot(), 'web', 'dist')),
			handler: 'server/face-app.handler',
			runtime: lambda.Runtime.NODEJS_22_X,
			memorySize: 512,
			timeout: cdk.Duration.seconds(15),
			environment: { NODE_OPTIONS: '--enable-source-maps' },
			logRetention: logs.RetentionDays.ONE_WEEK,
		});

		// FaceTheory ISR HTML store: currently holds build-time static hydration
		// sidecars under `_facetheory/data/` (one `<routeId>.json` per shell,
		// generated by web/scripts/build-sidecars.mjs and uploaded by
		// WebSidecarsDeployment below; served at /_facetheory/data/* via the
		// addBehavior below). Future ISR-rendered HTML lives under AppTheory's
		// `isr/` htmlStoreKeyPrefix, and future per-request signed sidecars
		// (createSsrHydrationSidecarStore at /_facetheory/ssr-data/<token>)
		// should use that same store prefix, e.g. `isr/ssr-hydration/...`. The
		// lifecycle rule is therefore scoped only to the ISR prefix; deploy-pruned
		// static sidecars under `_facetheory/data/` intentionally have no TTL.
		const htmlStoreKeyPrefix = 'isr';
		const htmlStoreObjectTtl = cdk.Duration.days(stage === 'live' ? 7 : 1);
		const htmlStoreBucket = new s3.Bucket(this, 'WebHtmlStoreBucket', {
			bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-html-store`,
			blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
			enforceSSL: true,
			encryption: s3.BucketEncryption.S3_MANAGED,
			versioned: stage === 'live',
			lifecycleRules: [
				{
					id: 'ExpireIsrHtmlAndRuntimeSidecars',
					prefix: `${htmlStoreKeyPrefix}/`,
					expiration: htmlStoreObjectTtl,
					noncurrentVersionExpiration: htmlStoreObjectTtl,
				},
			],
			removalPolicy,
			autoDeleteObjects: stage !== 'live',
		});

		// AppTheorySsrSite (M0.12) — canonical AppTheory v1.11.0 composition.
		// Owns the single CloudFront distribution + OAC-signed SSR Lambda
		// origin and the direct S3 Vite asset behaviors.
		const webSite = new AppTheorySsrSite(this, 'WebSite', {
			ssrFunction: webSsrFn,
			mode: AppTheorySsrSiteMode.SSR_ONLY,
			ssrUrlAuthType: lambda.FunctionUrlAuthType.AWS_IAM,
			invokeMode: lambda.InvokeMode.RESPONSE_STREAM,
			assetsBucket: webBucket,
			htmlStoreBucket,
			htmlStoreKeyPrefix,
			responseHeadersPolicy: webSecurityHeaders,
			certificateArn: webCert?.certificateArn,
			domainName: webCert ? webDomainName : undefined,
			// hostedZone intentionally omitted: AppTheorySsrSite would only
			// create an A record; host preserves the existing A + AAAA pair
			// in the `if (webZone && webCert)` block below.
			webAclId: webAcl.attrArn,
			enableLogging: true,
			logsBucket: accessLogsBucket,
			removalPolicy,
			autoDeleteObjects: stage !== 'live',
		});
		// Preserve the legacy logical id so the existing CNAME owner updates in place.
		(webSite.distribution.node.defaultChild as cloudfront.CfnDistribution).overrideLogicalId('WebDistribution59C46482');

		// /_facetheory/data/* hand-wired to htmlStoreBucket with OAC reads.
		const sidecarOrigin = origins.S3BucketOrigin.withOriginAccessControl(htmlStoreBucket);
		webSite.distribution.addBehavior('_facetheory/data/*', sidecarOrigin, {
			viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
			allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
			cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
			responseHeadersPolicy: webSecurityHeaders,
		});
		// Bearer-auth co-origins (existing HTTP API + SSE REST; not Function
		// URLs, so AppTheorySsrSite.bearerFunctionUrlOrigins[] doesn't apply).
		// Attached via addBehavior(); OAC does NOT propagate (M0.13 test).
		webSite.distribution.addBehavior(
			'safe-app*',
			new origins.S3Origin(webBucket, { originAccessIdentity: webOai }),
			{
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
		);

		// Helper for attaching the existing HTTP API behaviors. Pulls the
		// origin out of the constructed BehaviorOptions object so we can
		// pass the rest of the options through to addBehavior's third arg.
		const attachBearerBehavior = (pattern: string, behavior: cloudfront.BehaviorOptions) => {
			webSite.distribution.addBehavior(pattern, behavior.origin, {
				viewerProtocolPolicy: behavior.viewerProtocolPolicy,
				allowedMethods: behavior.allowedMethods,
				cachePolicy: behavior.cachePolicy,
				originRequestPolicy: behavior.originRequestPolicy,
				responseHeadersPolicy: behavior.responseHeadersPolicy,
			});
		};

		attachBearerBehavior('resolve*', trustBehaviorNoCache);
		attachBearerBehavior('health*', trustBehaviorNoCache);
		attachBearerBehavior('api/v1/previews*', trustApiBehavior);
		attachBearerBehavior('api/v1/renders*', trustApiBehavior);
		attachBearerBehavior('api/v1/trust/*', trustApiBehavior);
		attachBearerBehavior('api/v1/publish/jobs*', trustApiBehavior);
		attachBearerBehavior('api/v1/soul/agents/*/update-registration', trustApiBehavior);
		attachBearerBehavior('api/v1/ai/*', trustApiBehavior);
		attachBearerBehavior('api/v1/budget/debit', trustApiBehavior);
		attachBearerBehavior('api/v1/soul/agents/register/*/mint-conversation*', apiSseBehavior);
		attachBearerBehavior('api/v1/soul/agents/*/mint-conversation*', apiSseBehavior);
		// Lesser instance-key hosted-genesis uses JSON HostedGenesisSession authority, not legacy SSE/queue authority.
		attachBearerBehavior('api/v1/soul/instance/agents/register/*/mint-conversation*', apiBehavior);

		attachBearerBehavior('api/*', apiBehavior);
		attachBearerBehavior('auth/*', apiBehavior);
		attachBearerBehavior('webhooks/*', apiBehavior);
		attachBearerBehavior('setup/status', apiBehavior);
		attachBearerBehavior('setup/bootstrap/*', apiBehavior);
		attachBearerBehavior('setup/admin', apiBehavior);
		attachBearerBehavior('setup/finalize', apiBehavior);

		attachBearerBehavior('.well-known/*', trustBehaviorCached);
		attachBearerBehavior('attestations', trustBehaviorNoCache);
		attachBearerBehavior('attestations/*', trustBehaviorCached);

		// Back-compat alias so downstream env-wiring / route53 / outputs /
		// bucket-deployment blocks below keep referencing `webDistribution`
		// without diff churn.
		const webDistribution = webSite.distribution;

		const publicBaseURL = webCert
			? `https://${webDomainName}`
			: `https://${webDistribution.distributionDomainName}`;
		controlPlaneFn.addEnvironment('PUBLIC_BASE_URL', publicBaseURL);
		controlPlaneFn.addEnvironment('SOUL_EMAIL_INBOUND_DOMAIN', soulEmailInboundDomain);
		trustFn.addEnvironment('PUBLIC_BASE_URL', publicBaseURL);

		new s3deploy.BucketDeployment(this, 'WebDeployment', {
			destinationBucket: webBucket,
			sources: [
				s3deploy.Source.asset(path.join(this.repoRoot(), 'web'), {
					bundling: {
						image: cdk.DockerImage.fromRegistry('node:24-bookworm'),
						local: {
							tryBundle(outputDir: string) {
								if (!shouldUseLocalWebBundling()) {
									return false;
								}

								const webDir = path.join(repoRoot, 'web');
								const tempDir = fs.mkdtempSync(path.join(path.join(repoRoot, 'cdk', '.build'), 'web-bundle-'));
								try {
									fs.cpSync(webDir, tempDir, {
										recursive: true,
										filter: (src) => {
											const rel = path.relative(webDir, src);
											if (rel === '') return true;
											return !rel.startsWith('node_modules') && !rel.startsWith('dist');
										},
									});
									execFileSync('npm', ['ci'], { cwd: tempDir, stdio: 'inherit' });
									execFileSync('npm', ['run', 'build'], { cwd: tempDir, stdio: 'inherit' });
									fs.cpSync(path.join(tempDir, 'dist'), outputDir, { recursive: true });
									return true;
								} catch {
									return false;
								} finally {
									fs.rmSync(tempDir, { recursive: true, force: true });
								}
							},
						},
						command: [
							'bash',
							'-c',
							'export HOME=/tmp npm_config_cache=/tmp/.npm NPM_CONFIG_CACHE=/tmp/.npm && rm -rf /tmp/webbuild && mkdir -p /tmp/webbuild /tmp/.npm && cp -R /asset-input/. /tmp/webbuild && cd /tmp/webbuild && rm -rf node_modules dist && npm ci --cache /tmp/.npm && npm run build && cp -r dist/* /asset-output/',
						],
					},
				}),
			],
			distribution: webDistribution,
			distributionPaths: ['/*'],
		});

		// Deploy the static FaceTheory hydration sidecars to htmlStoreBucket
		// at key prefix `_facetheory/data/` so CloudFront's /_facetheory/data/*
		// behavior (which forwards the full URL path to S3 without OriginPath
		// rewrite) finds each `<routeId>.json` at the expected key. Sources
		// are produced by `web/scripts/build-sidecars.mjs` and bundled into
		// `dist/sidecars/` during `npm run build` (see WebDeployment above).
		new s3deploy.BucketDeployment(this, 'WebSidecarsDeployment', {
			destinationBucket: htmlStoreBucket,
			destinationKeyPrefix: '_facetheory/data',
			sources: [
				s3deploy.Source.asset(path.join(this.repoRoot(), 'web', 'dist', 'sidecars')),
			],
			distribution: webDistribution,
			distributionPaths: ['/_facetheory/data/*'],
			prune: true,
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
			new cdk.CfnOutput(this, 'InboundEmailBucketName', { value: inboundEmailBucket.bucketName });
			new cdk.CfnOutput(this, 'SoulCommMailboxBucketName', { value: soulCommMailboxBucket.bucketName });
			new cdk.CfnOutput(this, 'SoulEmailInboundDomain', { value: soulEmailInboundDomain });
			new cdk.CfnOutput(this, 'InboundEmailMXRecord', {
				value: `10 inbound-smtp.${cdk.Aws.REGION}.amazonaws.com`,
			});
			new cdk.CfnOutput(this, 'InboundEmailDkimRecordName1', { value: inboundEmailIdentity.attrDkimDnsTokenName1 });
			new cdk.CfnOutput(this, 'InboundEmailDkimRecordValue1', { value: inboundEmailIdentity.attrDkimDnsTokenValue1 });
			new cdk.CfnOutput(this, 'InboundEmailDkimRecordName2', { value: inboundEmailIdentity.attrDkimDnsTokenName2 });
			new cdk.CfnOutput(this, 'InboundEmailDkimRecordValue2', { value: inboundEmailIdentity.attrDkimDnsTokenValue2 });
			new cdk.CfnOutput(this, 'InboundEmailDkimRecordName3', { value: inboundEmailIdentity.attrDkimDnsTokenName3 });
			new cdk.CfnOutput(this, 'InboundEmailDkimRecordValue3', { value: inboundEmailIdentity.attrDkimDnsTokenValue3 });
			new cdk.CfnOutput(this, 'AttestationSigningKeyId', { value: attestationSigningKey.keyId });
			new cdk.CfnOutput(this, 'PreviewQueueUrl', { value: previewQueue.queueUrl });
			new cdk.CfnOutput(this, 'SafetyQueueUrl', { value: safetyQueue.queueUrl });
			new cdk.CfnOutput(this, 'HostedGenesisQueueUrl', { value: hostedGenesisQueue.queueUrl });
			new cdk.CfnOutput(this, 'ProvisionQueueUrl', { value: provisionQueue.queueUrl });
			new cdk.CfnOutput(this, 'RenderWorkerFunctionName', { value: renderWorkerFn.functionName });
			new cdk.CfnOutput(this, 'AiWorkerFunctionName', { value: aiWorkerFn.functionName });
			new cdk.CfnOutput(this, 'ProvisionWorkerFunctionName', { value: provisionWorkerFn.functionName });
			new cdk.CfnOutput(this, 'EmailIngressFunctionName', { value: emailIngressFn.functionName });
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
			execFileSync('go', ['build', '-o', path.join(buildDir, 'bootstrap'), entry], {
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

	// Deterministic per CDK process: FIRST call in a Node process runs
	// the web build; subsequent stack constructions in the SAME process
	// skip via the static guard. Avoids the stale-artifact hazard where
	// editing web/src/** leaves ignored web/dist files in place and
	// `cdk synth` packages outdated SSR Lambda + sidecars while
	// WebDeployment's separate bundling builds fresh (split deploy).
	private ensureWebBuild(): void {
		if (LesserHostStack.webBuildPreparedForProcess) return;
		const webDir = path.join(this.repoRoot(), 'web');
		console.log('[lesser-host-stack] building web/ for WebSsrFn + WebSidecarsDeployment');
		execFileSync('npm', ['ci', '--no-audit', '--no-fund'], { cwd: webDir, stdio: 'inherit' });
		execFileSync('npm', ['run', 'build'], { cwd: webDir, stdio: 'inherit' });
		LesserHostStack.webBuildPreparedForProcess = true;
	}
	private static webBuildPreparedForProcess = false;

	private repoRoot(): string {
		let current = __dirname;
		for (;;) {
			const candidate = path.resolve(current, '..');
			if (
				fs.existsSync(path.join(candidate, 'cdk')) &&
				fs.existsSync(path.join(candidate, 'cmd')) &&
				fs.existsSync(path.join(candidate, 'web'))
			) {
				return candidate;
			}
			const parent = path.dirname(candidate);
			if (parent === candidate) {
				throw new Error(`Failed to locate lesser-host repo root from ${__dirname}`);
			}
			current = candidate;
		}
	}
}
