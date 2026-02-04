import * as path from 'node:path';
import { execSync } from 'node:child_process';

import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as lambda from 'aws-cdk-lib/aws-lambda';
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

		const bootstrapWalletAddress =
			(this.node.tryGetContext('bootstrapWalletAddress') as string | undefined) ?? '';
		const webAuthnRPID = (this.node.tryGetContext('webauthnRpId') as string | undefined) ?? '';
		const webAuthnOrigins = (this.node.tryGetContext('webauthnOrigins') as string | undefined) ?? '';

		const controlPlaneFn = this.goLambda('ControlPlaneApi', './cmd/control-plane-api', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PREVIEW_QUEUE_URL: previewQueue.queueUrl,
			SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			BOOTSTRAP_WALLET_ADDRESS: bootstrapWalletAddress,
			WEBAUTHN_RP_ID: webAuthnRPID,
			WEBAUTHN_ORIGINS: webAuthnOrigins,
		});

		const trustFn = this.goLambda('TrustApi', './cmd/trust-api', {
			STAGE: stage,
			STATE_TABLE_NAME: stateTable.tableName,
			ARTIFACT_BUCKET_NAME: artifactsBucket.bucketName,
			PREVIEW_QUEUE_URL: previewQueue.queueUrl,
			SAFETY_QUEUE_URL: safetyQueue.queueUrl,
			WEBAUTHN_RP_ID: webAuthnRPID,
			WEBAUTHN_ORIGINS: webAuthnOrigins,
		});

		stateTable.grantReadWriteData(controlPlaneFn);
		stateTable.grantReadWriteData(trustFn);
		artifactsBucket.grantReadWrite(controlPlaneFn);
		artifactsBucket.grantReadWrite(trustFn);
		previewQueue.grantSendMessages(controlPlaneFn);
		previewQueue.grantSendMessages(trustFn);
		safetyQueue.grantSendMessages(controlPlaneFn);
		safetyQueue.grantSendMessages(trustFn);

		const controlPlaneUrl = controlPlaneFn.addFunctionUrl({
			authType: lambda.FunctionUrlAuthType.NONE,
		});
		const trustUrl = trustFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.NONE });

		new cdk.CfnOutput(this, 'ControlPlaneUrl', { value: controlPlaneUrl.url });
		new cdk.CfnOutput(this, 'TrustUrl', { value: trustUrl.url });
	}

	private goLambda(id: string, entry: string, environment: Record<string, string>): lambda.Function {
		const repoRoot = this.repoRoot();
		const code = lambda.Code.fromAsset(repoRoot, {
			bundling: {
				local: {
					tryBundle(outputDir: string) {
						execSync('go build -o ' + path.join(outputDir, 'main') + ' ' + entry, {
							cwd: repoRoot,
							stdio: 'inherit',
							env: {
								...process.env,
								CGO_ENABLED: '0',
								GOOS: 'linux',
								GOARCH: 'amd64',
							},
						});
						return true;
					},
				},
				image: lambda.Runtime.GO_1_X.bundlingImage,
				command: [
					'bash',
					'-lc',
					[
						'cd /asset-input',
						'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /asset-output/main ' + entry,
					].join(' && '),
				],
			},
		});

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
