import * as path from 'node:path';
import { execSync } from 'node:child_process';
import * as fs from 'node:fs';

import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
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

		const tipEnabled = (this.node.tryGetContext('tipEnabled') as string | undefined) ?? '';
		const tipChainId = (this.node.tryGetContext('tipChainId') as string | undefined) ?? '';
		const tipRpcUrl = (this.node.tryGetContext('tipRpcUrl') as string | undefined) ?? '';
		const tipContractAddress = (this.node.tryGetContext('tipContractAddress') as string | undefined) ?? '';
		const tipAdminSafeAddress = (this.node.tryGetContext('tipAdminSafeAddress') as string | undefined) ?? '';
		const tipDefaultHostWalletAddress =
			(this.node.tryGetContext('tipDefaultHostWalletAddress') as string | undefined) ?? '';
		const tipDefaultHostFeeBps = (this.node.tryGetContext('tipDefaultHostFeeBps') as string | undefined) ?? '';
		const tipTxMode = (this.node.tryGetContext('tipTxMode') as string | undefined) ?? '';

		const controlPlaneFn = this.goLambda('ControlPlaneApi', './cmd/control-plane-api', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PREVIEW_QUEUE_URL: previewQueue.queueUrl,
			SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			BOOTSTRAP_WALLET_ADDRESS: bootstrapWalletAddress,
			WEBAUTHN_RP_ID: webAuthnRPID,
			WEBAUTHN_ORIGINS: webAuthnOrigins,
			TIP_ENABLED: tipEnabled,
			TIP_CHAIN_ID: tipChainId,
			TIP_RPC_URL: tipRpcUrl,
			TIP_CONTRACT_ADDRESS: tipContractAddress,
			TIP_ADMIN_SAFE_ADDRESS: tipAdminSafeAddress,
			TIP_DEFAULT_HOST_WALLET_ADDRESS: tipDefaultHostWalletAddress,
			TIP_DEFAULT_HOST_FEE_BPS: tipDefaultHostFeeBps,
			TIP_TX_MODE: tipTxMode,
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

		stateTable.grantReadWriteData(controlPlaneFn);
		stateTable.grantReadWriteData(trustFn);
		stateTable.grantReadWriteData(renderWorkerFn);
		stateTable.grantReadWriteData(aiWorkerFn);
		artifactsBucket.grantReadWrite(controlPlaneFn);
		artifactsBucket.grantReadWrite(trustFn);
		artifactsBucket.grantReadWrite(renderWorkerFn);
		artifactsBucket.grantRead(aiWorkerFn);
		attestationSigningKey.grant(trustFn, 'kms:Sign', 'kms:GetPublicKey');
		attestationSigningKey.grant(aiWorkerFn, 'kms:Sign', 'kms:GetPublicKey');
		previewQueue.grantSendMessages(controlPlaneFn);
		previewQueue.grantSendMessages(trustFn);
		previewQueue.grantConsumeMessages(renderWorkerFn);
		safetyQueue.grantSendMessages(controlPlaneFn);
		safetyQueue.grantSendMessages(trustFn);
		safetyQueue.grantConsumeMessages(aiWorkerFn);

		renderWorkerFn.addEventSource(new lambdaEventSources.SqsEventSource(previewQueue, { batchSize: 1 }));
		aiWorkerFn.addEventSource(new lambdaEventSources.SqsEventSource(safetyQueue, { batchSize: 5 }));

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
			new cdk.CfnOutput(this, 'RenderWorkerFunctionName', { value: renderWorkerFn.functionName });
			new cdk.CfnOutput(this, 'AiWorkerFunctionName', { value: aiWorkerFn.functionName });
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
