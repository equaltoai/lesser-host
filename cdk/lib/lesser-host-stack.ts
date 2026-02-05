import * as path from 'node:path';
import { execSync } from 'node:child_process';
import * as fs from 'node:fs';

import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
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
	import * as s3 from 'aws-cdk-lib/aws-s3';
	import * as sqs from 'aws-cdk-lib/aws-sqs';

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

		const previewQueue = new sqs.Queue(this, 'PreviewQueue', {
			queueName: `${namePrefix}-preview-queue`,
		});
		previewQueue.applyRemovalPolicy(removalPolicy);

		const safetyQueue = new sqs.Queue(this, 'SafetyQueue', {
			queueName: `${namePrefix}-safety-queue`,
		});
		safetyQueue.applyRemovalPolicy(removalPolicy);

		const provisionQueue = new sqs.Queue(this, 'ProvisionQueue', {
			queueName: `${namePrefix}-provision-queue`,
		});
		provisionQueue.applyRemovalPolicy(removalPolicy);

		const attestationSigningKey = new kms.Key(this, 'AttestationSigningKey', {
			description: `${namePrefix} attestation signing`,
			keySpec: kms.KeySpec.RSA_2048,
			keyUsage: kms.KeyUsage.SIGN_VERIFY,
			removalPolicy,
		});
		attestationSigningKey.addAlias(`alias/${namePrefix}-attestation-signing`);

		const bootstrapWalletAddress =
			(this.node.tryGetContext('bootstrapWalletAddress') as string | undefined) ?? '';
		const webAuthnRPID = (this.node.tryGetContext('webauthnRpId') as string | undefined) ?? '';
		const webAuthnOrigins = (this.node.tryGetContext('webauthnOrigins') as string | undefined) ?? '';

		const managedProvisioningEnabled =
			(this.node.tryGetContext('managedProvisioningEnabled') as string | undefined) ?? '';
		const managedParentDomain = (this.node.tryGetContext('managedParentDomain') as string | undefined) ?? '';
		const managedParentHostedZoneId =
			(this.node.tryGetContext('managedParentHostedZoneId') as string | undefined) ?? '';
		const managedInstanceRoleName =
			(this.node.tryGetContext('managedInstanceRoleName') as string | undefined) ?? '';
		const managedTargetOuId = (this.node.tryGetContext('managedTargetOuId') as string | undefined) ?? '';
		const managedAccountEmailTemplate =
			(this.node.tryGetContext('managedAccountEmailTemplate') as string | undefined) ?? '';
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

		const tipEnabled = (this.node.tryGetContext('tipEnabled') as string | undefined) ?? '';
		const tipChainId = (this.node.tryGetContext('tipChainId') as string | undefined) ?? '';
		const tipRpcUrl = (this.node.tryGetContext('tipRpcUrl') as string | undefined) ?? '';
		const tipContractAddress = (this.node.tryGetContext('tipContractAddress') as string | undefined) ?? '';
		const tipAdminSafeAddress = (this.node.tryGetContext('tipAdminSafeAddress') as string | undefined) ?? '';
		const tipDefaultHostWalletAddress =
			(this.node.tryGetContext('tipDefaultHostWalletAddress') as string | undefined) ?? '';
		const tipDefaultHostFeeBps = (this.node.tryGetContext('tipDefaultHostFeeBps') as string | undefined) ?? '';
		const tipTxMode = (this.node.tryGetContext('tipTxMode') as string | undefined) ?? '';

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
				phases: {
					install: {
						commands: [
							'set -euo pipefail',
							'echo \"Installing runner tools...\"',
							'if command -v yum >/dev/null 2>&1; then yum install -y jq tar gzip unzip; fi',
							'if command -v apt-get >/dev/null 2>&1; then apt-get update -y && apt-get install -y jq tar gzip unzip; fi',
							'node -v || true',
							'npm -v || true',
							'npm install -g n',
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
						commands: [
							'set -euo pipefail',
							'echo \"Assuming role into target account...\"',
							'CREDS=$(aws sts assume-role --role-arn \"arn:aws:iam::$TARGET_ACCOUNT_ID:role/$TARGET_ROLE_NAME\" --role-session-name \"lesser-host-$APP_SLUG\" --duration-seconds 3600 --query \"Credentials.[AccessKeyId,SecretAccessKey,SessionToken]\" --output text)',
							'read MANAGED_AK MANAGED_SK MANAGED_TOKEN <<< \"$CREDS\"',
							'mkdir -p ~/.aws',
							'printf \"[managed]\\\\naws_access_key_id=%s\\\\naws_secret_access_key=%s\\\\naws_session_token=%s\\\\n\" \"$MANAGED_AK\" \"$MANAGED_SK\" \"$MANAGED_TOKEN\" > ~/.aws/credentials',
							'printf \"[profile managed]\\\\nregion=%s\\\\noutput=json\\\\n\" \"$TARGET_REGION\" > ~/.aws/config',
							'aws sts get-caller-identity --profile managed',
						],
					},
					build: {
						commands: [
							'set -euo pipefail',
							'OWNER=\"${GITHUB_OWNER:-equaltoai}\"',
							'REPO=\"${GITHUB_REPO:-lesser}\"',
							'TOKEN=\"${GITHUB_TOKEN:-}\"',
							'if [ -z \"${LESSER_VERSION:-}\" ]; then',
							'  if [ -n \"$TOKEN\" ]; then',
							'    TAG=$(curl -sSfL -H \"Accept: application/vnd.github+json\" -H \"Authorization: Bearer $TOKEN\" \"https://api.github.com/repos/$OWNER/$REPO/releases/latest\" | jq -r .tag_name)',
							'  else',
							'    TAG=$(curl -sSfL -H \"Accept: application/vnd.github+json\" \"https://api.github.com/repos/$OWNER/$REPO/releases/latest\" | jq -r .tag_name)',
							'  fi',
							'else',
							'  TAG=\"$LESSER_VERSION\"',
							'fi',
							'echo \"Using Lesser tag: $TAG\"',
							'if [ -n \"$TOKEN\" ]; then',
							'  curl -sSfL -H \"Accept: application/vnd.github+json\" -H \"Authorization: Bearer $TOKEN\" -o lesser-src.tgz \"https://api.github.com/repos/$OWNER/$REPO/tarball/$TAG\"',
							'else',
							'  curl -sSfL -H \"Accept: application/vnd.github+json\" -o lesser-src.tgz \"https://api.github.com/repos/$OWNER/$REPO/tarball/$TAG\"',
							'fi',
							'mkdir -p lesser-src && tar -xzf lesser-src.tgz --strip-components=1 -C lesser-src',
							'if [ -n \"$TOKEN\" ]; then',
							'  RELEASE_JSON=$(curl -sSfL -H \"Accept: application/vnd.github+json\" -H \"Authorization: Bearer $TOKEN\" \"https://api.github.com/repos/$OWNER/$REPO/releases/tags/$TAG\")',
							'else',
							'  RELEASE_JSON=$(curl -sSfL -H \"Accept: application/vnd.github+json\" \"https://api.github.com/repos/$OWNER/$REPO/releases/tags/$TAG\")',
							'fi',
							'BIN_ID=$(echo \"$RELEASE_JSON\" | jq -r \'.assets[] | select(.name==\"lesser-linux-amd64\") | .id\' | head -n 1)',
							'CHK_ID=$(echo \"$RELEASE_JSON\" | jq -r \'.assets[] | select(.name==\"checksums.txt\") | .id\' | head -n 1)',
							'test -n \"$BIN_ID\"',
							'test -n \"$CHK_ID\"',
							'if [ -n \"$TOKEN\" ]; then',
							'  curl -sSfL -H \"Authorization: Bearer $TOKEN\" -H \"Accept: application/octet-stream\" -o lesser-linux-amd64 \"https://api.github.com/repos/$OWNER/$REPO/releases/assets/$BIN_ID\"',
							'  curl -sSfL -H \"Authorization: Bearer $TOKEN\" -H \"Accept: application/octet-stream\" -o checksums.txt \"https://api.github.com/repos/$OWNER/$REPO/releases/assets/$CHK_ID\"',
							'else',
							'  curl -sSfL -H \"Accept: application/octet-stream\" -o lesser-linux-amd64 \"https://api.github.com/repos/$OWNER/$REPO/releases/assets/$BIN_ID\"',
							'  curl -sSfL -H \"Accept: application/octet-stream\" -o checksums.txt \"https://api.github.com/repos/$OWNER/$REPO/releases/assets/$CHK_ID\"',
							'fi',
							'EXPECTED=$(grep \"lesser-linux-amd64\" checksums.txt | awk \'{print $1}\')',
							'ACTUAL=$(sha256sum lesser-linux-amd64 | awk \'{print $1}\')',
							'test \"$EXPECTED\" = \"$ACTUAL\"',
							'chmod +x lesser-linux-amd64',
							'mv lesser-linux-amd64 lesser-src/lesser',
							'cd lesser-src',
							'GO_TOOLCHAIN=$(grep \"^toolchain \" go.mod | awk \'{print $2}\')',
							'GO_VERSION=\"${GO_TOOLCHAIN#go}\"',
							'echo \"Installing Go toolchain: $GO_VERSION\"',
							'curl -sSfL -o go.tgz \"https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz\"',
							'rm -rf /usr/local/go && tar -C /usr/local -xzf go.tgz',
							'export PATH=\"/usr/local/go/bin:$PATH\"',
							'go version',
							'set +e',
							'./lesser up --app \"$APP_SLUG\" --base-domain \"$BASE_DOMAIN\" --aws-profile managed 2>&1 | tee /tmp/lesser-up.log',
							'STATUS=$?',
							'set -e',
							'if [ \"$STATUS\" -ne 0 ]; then',
							'  if grep -q \"--out is required\" /tmp/lesser-up.log; then',
							'    ./lesser up --app \"$APP_SLUG\" --base-domain \"$BASE_DOMAIN\" --aws-profile managed --out /tmp/bootstrap.json',
							'  else',
							'    exit \"$STATUS\"',
							'  fi',
							'fi',
							'RECEIPT_PATH=\"$HOME/.lesser/$APP_SLUG/$BASE_DOMAIN/state.json\"',
							'test -f \"$RECEIPT_PATH\"',
							'aws s3 cp \"$RECEIPT_PATH\" \"s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY\"',
							'if [ -f /tmp/bootstrap.json ]; then aws s3 cp /tmp/bootstrap.json \"s3://$ARTIFACT_BUCKET/$BOOTSTRAP_S3_KEY\"; fi',
						],
					},
				},
			}),
		});

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
			TIP_ENABLED: tipEnabled,
			TIP_CHAIN_ID: tipChainId,
			TIP_RPC_URL: tipRpcUrl,
			TIP_CONTRACT_ADDRESS: tipContractAddress,
			TIP_ADMIN_SAFE_ADDRESS: tipAdminSafeAddress,
			TIP_DEFAULT_HOST_WALLET_ADDRESS: tipDefaultHostWalletAddress,
			TIP_DEFAULT_HOST_FEE_BPS: tipDefaultHostFeeBps,
			TIP_TX_MODE: tipTxMode,
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

		const provisionWorkerFn = this.goLambda('ProvisionWorker', './cmd/provision-worker', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PROVISION_QUEUE_URL: provisionQueue.queueUrl,
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
		});

		stateTable.grantReadWriteData(controlPlaneFn);
		stateTable.grantReadWriteData(trustFn);
		stateTable.grantReadWriteData(renderWorkerFn);
		stateTable.grantReadWriteData(aiWorkerFn);
		stateTable.grantReadWriteData(provisionWorkerFn);
		artifactsBucket.grantReadWrite(controlPlaneFn);
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

		provisionWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: [
					'organizations:CreateAccount',
					'organizations:DescribeCreateAccountStatus',
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

		provisionWorkerFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['route53:ChangeResourceRecordSets'],
				resources: [
					managedParentHostedZoneId.trim()
						? `arn:aws:route53:::hostedzone/${managedParentHostedZoneId.trim()}`
						: 'arn:aws:route53:::hostedzone/*',
				],
			}),
		);

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
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/api/stripe/secret`,
			`arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/lesser-host/api/stripe/webhook`,
		];
		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['ssm:GetParameter', 'ssm:GetParameters'],
				resources: paymentsSsmParamArns,
			}),
		);
		controlPlaneFn.addToRolePolicy(
			new iam.PolicyStatement({
				actions: ['kms:Decrypt'],
				resources: ['*'],
				conditions: {
					StringEquals: { 'kms:ViaService': `ssm.${cdk.Aws.REGION}.amazonaws.com` },
				},
			}),
		);

		const retentionSweepRule = new events.Rule(this, 'RetentionSweepRule', {
			ruleName: `${namePrefix}-retention-sweep`,
			schedule: events.Schedule.rate(cdk.Duration.days(1)),
		});
		retentionSweepRule.addTarget(new targets.LambdaFunction(renderWorkerFn));

		const controlPlaneUrl = controlPlaneFn.addFunctionUrl({
			authType: lambda.FunctionUrlAuthType.NONE,
		});
		const trustUrl = trustFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.NONE });

		new cdk.CfnOutput(this, 'ControlPlaneUrl', { value: controlPlaneUrl.url });
		new cdk.CfnOutput(this, 'TrustUrl', { value: trustUrl.url });
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
		}

	private goLambda(id: string, entry: string, environment: Record<string, string>): lambda.Function {
		const repoRoot = this.repoRoot();
		const buildDir = path.join(repoRoot, 'cdk', '.build', id);
		fs.mkdirSync(buildDir, { recursive: true });
		execSync('go build -o ' + path.join(buildDir, 'main') + ' ' + entry, {
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
			handler: 'main',
			runtime: lambda.Runtime.GO_1_X,
			memorySize: 256,
			timeout: cdk.Duration.seconds(10),
			environment,
		});
	}

	private repoRoot(): string {
		// This file lives at cdk/lib/*.ts; repo root is two levels up.
		return path.resolve(__dirname, '../..');
	}
}
