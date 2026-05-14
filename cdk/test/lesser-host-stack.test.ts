import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import * as cdk from 'aws-cdk-lib';

import { LesserHostStack, shouldUseLocalWebBundling } from '../lib/lesser-host-stack';

process.env.GOTOOLCHAIN = process.env.GOTOOLCHAIN || 'auto';

type SynthesizedTemplate = {
	Resources?: Record<string, { Type?: string; Properties?: Record<string, unknown> }>;
};

let synthesizedTemplate: SynthesizedTemplate | undefined;

function synthTemplate(): SynthesizedTemplate {
	if (synthesizedTemplate) {
		return synthesizedTemplate;
	}

	const app = new cdk.App();
	const stack = new LesserHostStack(app, 'TestLesserHostStack', { stage: 'lab' });
	const assembly = app.synth();
	const artifact = assembly.getStackArtifact(stack.artifactId);
	synthesizedTemplate = JSON.parse(readFileSync(artifact.templateFullPath, 'utf8')) as SynthesizedTemplate;
	return synthesizedTemplate;
}

function synthTemplateWithContext(context: Record<string, unknown>): SynthesizedTemplate {
	const app = new cdk.App({ context });
	const stack = new LesserHostStack(app, 'TestLesserHostStackWithContext', { stage: 'lab' });
	const assembly = app.synth();
	const artifact = assembly.getStackArtifact(stack.artifactId);
	return JSON.parse(readFileSync(artifact.templateFullPath, 'utf8')) as SynthesizedTemplate;
}

function findResources(template: SynthesizedTemplate, type: string): Array<Record<string, unknown>> {
	return Object.values(template.Resources ?? {})
		.filter((resource) => resource?.Type === type)
		.map((resource) => resource?.Properties ?? {});
}

function parseAccessLogFormat(stage: Record<string, unknown>): Record<string, unknown> {
	const accessLogSettings = stage.AccessLogSettings;
	assert.ok(accessLogSettings && typeof accessLogSettings === 'object', 'expected stage access log settings');
	const format = (accessLogSettings as { Format?: unknown }).Format;
	assert.equal(typeof format, 'string', 'expected access log format string');
	return JSON.parse(format as string) as Record<string, unknown>;
}

test('state table exposes the active update recovery index', () => {
	const template = synthTemplate();
	const tables = findResources(template, 'AWS::DynamoDB::Table');
	const matchingTable = tables.find((table) => {
		const indexes = Array.isArray(table.GlobalSecondaryIndexes) ? table.GlobalSecondaryIndexes : [];
		return indexes.some((index) => {
			if (!index || typeof index !== 'object') {
				return false;
			}
			const name = (index as { IndexName?: unknown }).IndexName;
			const keySchema = Array.isArray((index as { KeySchema?: unknown }).KeySchema)
				? ((index as { KeySchema: Array<Record<string, unknown>> }).KeySchema)
				: [];
			return name === 'gsi2' &&
				keySchema.some((key) => key.AttributeName === 'gsi2PK' && key.KeyType === 'HASH') &&
				keySchema.some((key) => key.AttributeName === 'gsi2SK' && key.KeyType === 'RANGE');
		});
	});

	assert.ok(matchingTable, 'expected state table gsi2 for active update recovery');
});

test('stack schedules the managed update sweep every five minutes', () => {
	const template = synthTemplate();
	const rules = findResources(template, 'AWS::Events::Rule');
	const matchingRule = rules.find((rule) => {
		const targets = Array.isArray(rule.Targets) ? rule.Targets : [];
		return rule.Name === 'lesser-host-lab-update-sweep' &&
			rule.ScheduleExpression === 'rate(5 minutes)' &&
			targets.length > 0;
	});

	assert.ok(matchingRule, 'expected managed update sweep EventBridge rule');
});

test('control-plane HTTP API access logs do not persist raw request paths', () => {
	const template = synthTemplate();
	const stages = findResources(template, 'AWS::ApiGatewayV2::Stage');
	const controlPlaneStage = stages.find((stage) => stage.StageName === '$default');
	assert.ok(controlPlaneStage, 'expected control-plane HTTP API default stage');
	const format = parseAccessLogFormat(controlPlaneStage);

	assert.equal(format.path, '$context.routeKey');
	assert.equal(format.routeKey, '$context.routeKey');
	assert.notEqual(format.path, '$context.path');
});

test('private mint-conversation instance read route has a templated HTTP API route key', () => {
	const template = synthTemplate();
	const routes = findResources(template, 'AWS::ApiGatewayV2::Route');
	const matching = routes.find((route) => {
		return route.RouteKey === 'GET /api/v1/soul/instance/agents/{agentId}/mint-conversations/{conversationId}';
	});

	assert.ok(matching, 'expected private mint-conversation single-read route to have a non-raw route key');
});

function findLambdaByFunctionName(template: SynthesizedTemplate, namePart: string): Record<string, unknown> | undefined {
	return findResources(template, 'AWS::Lambda::Function').find((fn) => {
		const name = fn.FunctionName;
		return typeof name === 'string' && name.includes(namePart);
	});
}

function lambdaEnvironment(fn: Record<string, unknown> | undefined): Record<string, unknown> {
	const environment = fn?.Environment;
	if (!environment || typeof environment !== 'object') {
		return {};
	}
	const variables = (environment as { Variables?: unknown }).Variables;
	return variables && typeof variables === 'object' ? (variables as Record<string, unknown>) : {};
}

test('trust api receives soul registration runtime configuration', () => {
	const template = synthTemplate();
	const trustFn = findLambdaByFunctionName(template, 'trust-api');
	const env = lambdaEnvironment(trustFn);

	for (const key of [
		'SOUL_ENABLED',
		'SOUL_CHAIN_ID',
		'SOUL_RPC_URL_SSM_PARAM',
		'SOUL_REGISTRY_CONTRACT_ADDRESS',
		'SOUL_REPUTATION_ATTESTATION_CONTRACT_ADDRESS',
		'SOUL_VALIDATION_ATTESTATION_CONTRACT_ADDRESS',
		'SOUL_ADMIN_SAFE_ADDRESS',
		'SOUL_TX_MODE',
		'SOUL_SUPPORTED_CAPABILITIES',
		'SOUL_PACK_BUCKET_NAME',
	]) {
		assert.ok(key in env, `expected trust-api environment to include ${key}`);
	}
});

test('provision runner cannot assume the organization vending role', () => {
	const orgVendingRoleArn = 'arn:aws:iam::123456789012:role/lesser-host-org-vending';
	const template = synthTemplateWithContext({ managedOrgVendingRoleArn: orgVendingRoleArn });
	const resources = template.Resources ?? {};

	const codeBuildRoleIds = new Set(
		Object.entries(resources)
			.filter(([, resource]) => {
				if (resource.Type !== 'AWS::IAM::Role') {
					return false;
				}
				const policy = resource.Properties?.AssumeRolePolicyDocument;
				const statements = policy && typeof policy === 'object'
					? (policy as { Statement?: unknown }).Statement
					: undefined;
				if (!Array.isArray(statements)) {
					return false;
				}
				return statements.some((statement) => {
					const principal = statement && typeof statement === 'object'
						? (statement as { Principal?: unknown }).Principal
						: undefined;
					const service = principal && typeof principal === 'object'
						? (principal as { Service?: unknown }).Service
						: undefined;
					return service === 'codebuild.amazonaws.com';
				});
			})
			.map(([logicalId]) => logicalId),
	);
	assert.ok(codeBuildRoleIds.size > 0, 'expected a CodeBuild service role');

	for (const [logicalId, resource] of Object.entries(resources)) {
		if (resource.Type !== 'AWS::IAM::Policy') {
			continue;
		}
		const roles = resource.Properties?.Roles;
		const attachedToCodeBuild = Array.isArray(roles) && roles.some((role) => {
			return role && typeof role === 'object' &&
				'Ref' in role &&
				codeBuildRoleIds.has((role as { Ref?: string }).Ref ?? '');
		});
		if (!attachedToCodeBuild) {
			continue;
		}

		const doc = resource.Properties?.PolicyDocument;
		const statements = doc && typeof doc === 'object'
			? (doc as { Statement?: unknown }).Statement
			: undefined;
		assert.ok(Array.isArray(statements), `expected ${logicalId} to have policy statements`);
		for (const statement of statements) {
			const resourceValue = statement && typeof statement === 'object'
				? (statement as { Resource?: unknown }).Resource
				: undefined;
			const resourcesValue = Array.isArray(resourceValue) ? resourceValue : [resourceValue];
			assert.ok(
				!resourcesValue.includes(orgVendingRoleArn),
				`expected ${logicalId} to omit org vending role from CodeBuild policies`,
			);
		}
	}
});

test('provision worker receives deploy runner role arn for tenant trust repair', () => {
	const template = synthTemplate();
	const provisionWorker = findLambdaByFunctionName(template, 'provision-worker');
	const env = lambdaEnvironment(provisionWorker);

	assert.ok(
		'MANAGED_PROVISION_RUNNER_ROLE_ARN' in env,
		'expected provision worker to receive CodeBuild service role ARN',
	);
	assert.ok(
		env.MANAGED_PROVISION_RUNNER_ROLE_ARN,
		'expected provision worker CodeBuild service role ARN to be non-empty',
	);
});

test('web asset bundling does not execute npm locally in CI', () => {
	assert.equal(shouldUseLocalWebBundling({ CI: 'true' }), false);
	assert.equal(shouldUseLocalWebBundling({ GITHUB_ACTIONS: 'true' }), false);
	assert.equal(shouldUseLocalWebBundling({}), true);
});
