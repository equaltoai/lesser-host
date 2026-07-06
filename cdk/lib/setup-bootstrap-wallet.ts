import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as cdk from 'aws-cdk-lib';
import * as customresources from 'aws-cdk-lib/custom-resources';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import { Construct } from 'constructs';

export interface SetupBootstrapWalletProvisioning {
	readonly address: string;
	readonly source: 'cdk-owned' | 'override';
	readonly parameterName?: string;
}

export interface SetupBootstrapWalletProps {
	readonly stage: string;
	readonly namePrefix: string;
	readonly removalPolicy: cdk.RemovalPolicy;
	readonly overrideAddress?: unknown;
}

const evmAddressPattern = /^0x[0-9a-fA-F]{40}$/;
const placeholderPattern = /<[^>]*YOUR[^>]*>|YOUR_[A-Z0-9_]+/i;

export function provisionSetupBootstrapWallet(
	scope: Construct,
	id: string,
	props: SetupBootstrapWalletProps,
): SetupBootstrapWalletProvisioning {
	const overrideAddress = normalizedBootstrapWalletOverride(props.overrideAddress);
	if (overrideAddress) {
		new cdk.CfnOutput(scope, 'SetupBootstrapWalletSource', {
			value: 'BOOTSTRAP_WALLET_ADDRESS override',
		});
		return {
			address: overrideAddress,
			source: 'override',
		};
	}

	const parameterName = `/lesser-host/${props.stage}/setup/bootstrap-wallet-private-key`;
	const parameterArn = ssmParamArn(scope, parameterName);
	const handler = new lambda.Function(scope, `${id}Handler`, {
		functionName: `${props.namePrefix}-setup-bootstrap-wallet-resource`,
		description: 'CDK-owned one-time setup bootstrap wallet generator',
		runtime: lambda.Runtime.PROVIDED_AL2023,
		handler: 'bootstrap',
		code: buildGoBootstrapCode(`${id}Handler`, './cmd/setup-bootstrap-wallet-resource'),
		memorySize: 256,
		timeout: cdk.Duration.seconds(30),
		environment: {
			BOOTSTRAP_WALLET_SSM_PARAM_NAME: parameterName,
		},
	});

	handler.addToRolePolicy(new iam.PolicyStatement({
		sid: 'ManageSetupBootstrapWalletSSMParameter',
		actions: [
			'ssm:PutParameter',
			'ssm:GetParameter',
			'ssm:GetParameters',
			'ssm:DeleteParameter',
		],
		resources: [parameterArn],
	}));
	handler.addToRolePolicy(new iam.PolicyStatement({
		sid: 'UseSetupBootstrapWalletSSMKMSParameter',
		actions: ['kms:Decrypt', 'kms:Encrypt', 'kms:GenerateDataKey'],
		resources: ['*'],
		conditions: {
			StringEquals: { 'kms:ViaService': `ssm.${cdk.Aws.REGION}.amazonaws.com` },
		},
	}));

	const provider = new customresources.Provider(scope, `${id}Provider`, {
		onEventHandler: handler,
	});
	const resource = new cdk.CustomResource(scope, id, {
		serviceToken: provider.serviceToken,
		removalPolicy: props.removalPolicy,
		properties: {
			BootstrapWalletSSMParamName: parameterName,
			Stage: props.stage,
		},
	});

	const address = cdk.Token.asString(resource.getAtt('BootstrapWalletAddress'));
	const stackOwnedParameterName = cdk.Token.asString(resource.getAtt('BootstrapWalletSSMParamName'));
	new cdk.CfnOutput(scope, 'SetupBootstrapWalletSource', {
		value: 'CDK custom resource',
	});
	new cdk.CfnOutput(scope, 'SetupBootstrapWalletSSMParamName', {
		value: stackOwnedParameterName,
	});

	return {
		address,
		source: 'cdk-owned',
		parameterName: stackOwnedParameterName,
	};
}

export function normalizedBootstrapWalletOverride(rawValue: unknown): string {
	const value = typeof rawValue === 'string' ? rawValue.trim() : '';
	if (value === '') {
		return '';
	}
	if (placeholderPattern.test(value)) {
		throw new Error('bootstrapWalletAddress override must be a real EVM 0x address; placeholders are not accepted');
	}
	if (!evmAddressPattern.test(value)) {
		throw new Error('bootstrapWalletAddress override must be a valid EVM 0x address');
	}
	return `0x${value.slice(2).toLowerCase()}`;
}

function ssmParamArn(_scope: Construct, paramName: string): string {
	const name = paramName.startsWith('/') ? paramName : `/${paramName}`;
	return `arn:aws:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter${name}`;
}

function buildGoBootstrapCode(id: string, entry: string): lambda.Code {
	const root = repoRoot();
	const buildDir = path.join(root, 'cdk', '.build', id);
	fs.mkdirSync(buildDir, { recursive: true });
	execFileSync('go', ['build', '-o', path.join(buildDir, 'bootstrap'), entry], {
		cwd: root,
		stdio: 'inherit',
		env: {
			...process.env,
			CGO_ENABLED: '0',
			GOOS: 'linux',
			GOARCH: 'amd64',
		},
	});
	return lambda.Code.fromAsset(buildDir);
}

function repoRoot(): string {
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
