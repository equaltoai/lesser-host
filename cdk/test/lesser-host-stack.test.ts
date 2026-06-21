import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import * as cdk from 'aws-cdk-lib';

import { LesserHostStack, shouldUseLocalWebBundling } from '../lib/lesser-host-stack';
import {
	LAB_ENS_GATEWAY_CHAIN_ID,
	LAB_ENS_GATEWAY_CHAIN_NAME,
	LIVE_ENS_GATEWAY_CHAIN_ID,
	LIVE_ENS_GATEWAY_CHAIN_NAME,
	ensGatewayChainConfigForStage,
	ensGatewayContextForStage,
	ensGatewayResolverAddressFromContext,
} from '../lib/ens-gateway-config';
import { INBOUND_EMAIL_RULE_SET_NAME } from '../lib/ses-inbound-rule-set-name';
import {
	LAB_SOUL_EMAIL_INBOUND_DOMAIN,
	LIVE_SOUL_EMAIL_INBOUND_DOMAIN,
	defaultSoulEmailInboundDomainForStage,
	soulEmailInboundDomainFromContext,
} from '../lib/soul-email-inbound-domain';

process.env.GOTOOLCHAIN = process.env.GOTOOLCHAIN || 'auto';

type SynthesizedTemplate = {
	Resources?: Record<string, { Type?: string; Properties?: Record<string, unknown> }>;
};

let synthesizedTemplate: SynthesizedTemplate | undefined;

function synthesizeTemplate(stage: string, context: Record<string, unknown>, stackId: string): SynthesizedTemplate {
	// Full LesserHostStack synthesis stages web assets. Keep each test synth in
	// its own short-lived outdir so repeated assertions cannot accumulate
	// copied web/CDK asset directories and exhaust hosted runner disk.
	const outdir = mkdtempSync(join(tmpdir(), 'lesser-host-cdk-test-'));
	try {
		const app = new cdk.App({ context, outdir });
		const stack = new LesserHostStack(app, stackId, { stage });
		const assembly = app.synth();
		const artifact = assembly.getStackArtifact(stack.artifactId);
		return JSON.parse(readFileSync(artifact.templateFullPath, 'utf8')) as SynthesizedTemplate;
	} finally {
		rmSync(outdir, { recursive: true, force: true });
	}
}

function synthTemplate(): SynthesizedTemplate {
	if (!synthesizedTemplate) {
		synthesizedTemplate = synthesizeTemplate('lab', {}, 'TestLesserHostStack');
	}
	return synthesizedTemplate;
}

function synthTemplateWithContext(context: Record<string, unknown>): SynthesizedTemplate {
	return synthesizeTemplate('lab', context, 'TestLesserHostStackWithContext');
}

function synthTemplateForStage(stage: string, context: Record<string, unknown> = {}): SynthesizedTemplate {
	if (Object.keys(context).length === 0 && stage.trim() !== 'live') {
		return synthTemplate();
	}
	return synthesizeTemplate(stage, context, `TestLesserHostStack-${stage}`);
}

function findResources(template: SynthesizedTemplate, type: string): Array<Record<string, unknown>> {
	return Object.values(template.Resources ?? {})
		.filter((resource) => resource?.Type === type)
		.map((resource) => resource?.Properties ?? {});
}

function findResourceEntries(
	template: SynthesizedTemplate,
	type: string,
): Array<[string, { Type?: string; Properties?: Record<string, unknown> }]> {
	return Object.entries(template.Resources ?? {}).filter(([, resource]) => resource?.Type === type);
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

test('stages add unique SES inbound rules to the shared activated rule set', () => {
	for (const { stage, recipient, ruleName } of [
		{ stage: 'lab', recipient: LAB_SOUL_EMAIL_INBOUND_DOMAIN, ruleName: 'lesser-host-lab-ingress' },
		{ stage: 'live', recipient: LIVE_SOUL_EMAIL_INBOUND_DOMAIN, ruleName: 'lesser-host-live-ingress' },
	]) {
		const template = synthTemplateForStage(stage);

		assert.equal(
			findResources(template, 'AWS::SES::ReceiptRuleSet').length,
			0,
			`${stage} stack must import the shared SES receipt rule set by name, not create its own`,
		);
		assert.equal(
			findResources(template, 'Custom::AWS').length,
			0,
			`${stage} stack must not activate or mutate the shared SES receipt rule set`,
		);

		const rules = findResourceEntries(template, 'AWS::SES::ReceiptRule');
		assert.equal(rules.length, 1, `expected exactly one SES receipt rule in ${stage}`);
		const [logicalId, resource] = rules[0]!;
		assert.ok(
			logicalId.startsWith(`InboundEmailRuleSetIngress${stage}`),
			`expected stage-unique Ingress-${stage} logical id, got ${logicalId}`,
		);

		const props = resource.Properties ?? {};
		assert.equal(props.RuleSetName, INBOUND_EMAIL_RULE_SET_NAME);
		const rule = props.Rule;
		assert.ok(rule && typeof rule === 'object', `expected ${stage} receipt rule properties`);
		const ruleProps = rule as Record<string, unknown>;
		assert.equal(ruleProps.Name, ruleName);
		assert.deepEqual(ruleProps.Recipients, [recipient]);
		assert.equal(ruleProps.Enabled, true);
		assert.equal(ruleProps.ScanEnabled, true);
		assert.equal(ruleProps.TlsPolicy, 'Require');

		const actions = ruleProps.Actions;
		assert.ok(Array.isArray(actions), `expected ${stage} receipt rule actions`);
		const inboundBucket = findResourceEntries(template, 'AWS::S3::Bucket').find(([, bucket]) => {
			const bucketName = JSON.stringify(bucket.Properties?.BucketName);
			return bucketName.includes(`lesser-host-${stage}-`) && bucketName.includes('-inbound-email');
		});
		assert.ok(inboundBucket, `expected ${stage} inbound email bucket`);
		const s3Action = (actions[0] as { S3Action?: Record<string, unknown> }).S3Action;
		assert.ok(s3Action, `expected ${stage} receipt rule S3 action`);
		assert.deepEqual(s3Action.BucketName, { Ref: inboundBucket[0] });
		assert.equal(s3Action.ObjectKeyPrefix, 'ses/inbound/');

		const emailIngressFn = findResourceEntries(template, 'AWS::Lambda::Function').find(([, fn]) =>
			fn.Properties?.FunctionName === `lesser-host-${stage}-email-ingress`
		);
		assert.ok(emailIngressFn, `expected ${stage} email-ingress function`);
		const emailIngressEnv = (
			emailIngressFn[1].Properties?.Environment as { Variables?: Record<string, unknown> } | undefined
		)?.Variables ?? {};
		assert.equal(emailIngressEnv.SOUL_EMAIL_INBOUND_DOMAIN, recipient);
		const lambdaAction = (actions[1] as { LambdaAction?: Record<string, unknown> }).LambdaAction;
		assert.ok(lambdaAction, `expected ${stage} receipt rule Lambda action`);
		assert.equal(lambdaAction.InvocationType, 'Event');
		assert.deepEqual(lambdaAction.FunctionArn, { 'Fn::GetAtt': [emailIngressFn[0], 'Arn'] });
	}
});

test('stage-specific inbound bridge domain defaults are explicit', () => {
	assert.equal(defaultSoulEmailInboundDomainForStage('lab'), LAB_SOUL_EMAIL_INBOUND_DOMAIN);
	assert.equal(defaultSoulEmailInboundDomainForStage(' live '), LIVE_SOUL_EMAIL_INBOUND_DOMAIN);
	assert.equal(defaultSoulEmailInboundDomainForStage('preview'), LAB_SOUL_EMAIL_INBOUND_DOMAIN);
});

test('stage-specific inbound bridge context overrides remain disjoint', () => {
	const scope = new cdk.App({
		context: {
			soulEmailInboundDomainLab: 'lab-override.example',
			soulEmailInboundDomainLive: 'live-override.example',
			soulEmailInboundDomain: 'generic.example',
		},
	});
	for (const { stage, want } of [
		{ stage: 'lab', want: 'lab-override.example' },
		{ stage: 'live', want: 'live-override.example' },
	]) {
		assert.equal(soulEmailInboundDomainFromContext(scope, stage), want);
	}
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

function findBucketByLogicalIdPrefix(
	template: SynthesizedTemplate,
	logicalIdPrefix: string,
): Record<string, unknown> {
	const entry = findResourceEntries(template, 'AWS::S3::Bucket').find(([logicalId]) =>
		logicalId.startsWith(logicalIdPrefix),
	);
	assert.ok(entry, `expected S3 bucket logical id starting with ${logicalIdPrefix}`);
	return entry[1].Properties ?? {};
}

function bucketLifecycleRules(bucket: Record<string, unknown>): Array<Record<string, unknown>> {
	const lifecycle = bucket.LifecycleConfiguration;
	assert.ok(lifecycle && typeof lifecycle === 'object', 'expected bucket lifecycle configuration');
	const rules = (lifecycle as { Rules?: unknown }).Rules;
	assert.ok(Array.isArray(rules), 'expected bucket lifecycle rules');
	return rules.filter((rule): rule is Record<string, unknown> => Boolean(rule) && typeof rule === 'object');
}

function lifecycleRuleId(rule: Record<string, unknown>): string {
	return typeof rule.Id === 'string' ? rule.Id : JSON.stringify(rule.Id);
}

function lifecycleRulePrefix(rule: Record<string, unknown>): string | undefined {
	const filter = rule.Filter;
	if (filter && typeof filter === 'object') {
		const prefix = (filter as { Prefix?: unknown }).Prefix;
		if (typeof prefix === 'string') {
			return prefix;
		}
	}
	return typeof rule.Prefix === 'string' ? rule.Prefix : undefined;
}

function lifecycleRuleExpiresCurrentObjects(rule: Record<string, unknown>): boolean {
	return 'ExpirationInDays' in rule || 'ExpirationDate' in rule || 'ExpiredObjectDeleteMarker' in rule;
}

function expiringLifecycleRulesMatchingKey(
	rules: Array<Record<string, unknown>>,
	key: string,
): Array<Record<string, unknown>> {
	return rules
		.filter(lifecycleRuleExpiresCurrentObjects)
		.filter((rule) => {
			const prefix = lifecycleRulePrefix(rule);
			return prefix === undefined || key.startsWith(prefix);
		});
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

test('trust api receives ENS gateway runtime configuration separate from soul registry config', () => {
	const template = synthTemplateForStage('live', {
		ensGatewayEnabledLive: 'true',
		ensGatewayResolverAddressLab: '0x0000000000000000000000000000000000001111',
		ensGatewayResolverAddressLive: '0x0000000000000000000000000000000000002222',
		ensGatewayRootName: 'lessersoul.eth',
		soulEnabledLive: 'false',
		soulChainIdLive: '11155111',
		soulRegistryContractAddressLive: '0x0000000000000000000000000000000000003333',
	});
	const trustFn = findLambdaByFunctionName(template, 'trust-api');
	const env = lambdaEnvironment(trustFn);

	assert.equal(env.ENS_GATEWAY_ENABLED, 'true');
	assert.equal(env.ENS_GATEWAY_CHAIN_ID, LIVE_ENS_GATEWAY_CHAIN_ID);
	assert.equal(env.ENS_GATEWAY_CHAIN_NAME, LIVE_ENS_GATEWAY_CHAIN_NAME);
	assert.equal(env.ENS_GATEWAY_ROOT_NAME, 'lessersoul.eth');
	assert.equal(env.ENS_GATEWAY_RESOLVER_ADDRESS, '0x0000000000000000000000000000000000002222');

	assert.equal(env.SOUL_ENABLED, 'false');
	assert.equal(env.SOUL_CHAIN_ID, '11155111');
	assert.equal(env.SOUL_REGISTRY_CONTRACT_ADDRESS, '0x0000000000000000000000000000000000003333');
});

test('ENS gateway lab and live context selection uses stage-owned resolver and chain values', () => {
	const context = {
		ensGatewayEnabledLab: 'true',
		ensGatewayEnabledLive: 'true',
		ensGatewayResolverAddressLab: '0x0000000000000000000000000000000000001111',
		ensGatewayResolverAddressLive: '0x0000000000000000000000000000000000002222',
		ensGatewayResolverAddress: '0x0000000000000000000000000000000000009999',
		soulChainIdLab: '31337',
		soulChainIdLive: '8453',
	};
	const scope = new cdk.App({ context });

	assert.equal(
		ensGatewayResolverAddressFromContext(scope, 'lab'),
		'0x0000000000000000000000000000000000001111',
	);
	assert.equal(
		ensGatewayResolverAddressFromContext(scope, 'live'),
		'0x0000000000000000000000000000000000002222',
	);
	assert.notEqual(
		ensGatewayResolverAddressFromContext(scope, 'lab'),
		ensGatewayResolverAddressFromContext(scope, 'live'),
	);

	assert.equal(ensGatewayContextForStage(scope, 'lab', 'ensGatewayEnabled'), 'true');
	assert.equal(ensGatewayContextForStage(scope, 'live', 'ensGatewayEnabled'), 'true');

	const labChain = ensGatewayChainConfigForStage('lab');
	const liveChain = ensGatewayChainConfigForStage('live');
	assert.equal(labChain.chainId, LAB_ENS_GATEWAY_CHAIN_ID);
	assert.equal(labChain.chainName, LAB_ENS_GATEWAY_CHAIN_NAME);
	assert.equal(liveChain.chainId, LIVE_ENS_GATEWAY_CHAIN_ID);
	assert.equal(liveChain.chainName, LIVE_ENS_GATEWAY_CHAIN_NAME);
});

test('legacy generic ENS resolver context is a lab-only migration fallback', () => {
	const context = {
		ensGatewayResolverAddress: '0x0000000000000000000000000000000000009999',
	};
	const scope = new cdk.App({ context });

	assert.equal(
		ensGatewayResolverAddressFromContext(scope, 'lab'),
		'0x0000000000000000000000000000000000009999',
	);
	assert.equal(ensGatewayResolverAddressFromContext(scope, 'live'), '');
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

test('hosted genesis durable async queue is wired to control plane and ai worker', () => {
	const template = synthTemplate();
	const queues = findResourceEntries(template, 'AWS::SQS::Queue');
	const queue = queues.find(([, resource]) =>
		resource.Properties?.QueueName === 'lesser-host-lab-hosted-genesis-queue'
	);
	const dlq = queues.find(([, resource]) =>
		resource.Properties?.QueueName === 'lesser-host-lab-hosted-genesis-dlq'
	);
	assert.ok(queue, 'expected hosted genesis worker queue');
	assert.ok(dlq, 'expected hosted genesis DLQ');
	assert.equal(queue[1].Properties?.VisibilityTimeout, 180, 'hosted genesis queue must cover ai-worker timeout');
	assert.deepEqual(
		queue[1].Properties?.RedrivePolicy,
		{
			deadLetterTargetArn: { 'Fn::GetAtt': [dlq[0], 'Arn'] },
			maxReceiveCount: 3,
		},
		'hosted genesis queue must fail closed to its DLQ after bounded retries',
	);

	const controlPlane = findLambdaByFunctionName(template, 'control-plane-api');
	const aiWorker = findLambdaByFunctionName(template, 'ai-worker');
	const controlPlaneEnv = lambdaEnvironment(controlPlane);
	const aiWorkerEnv = lambdaEnvironment(aiWorker);
	assert.ok('HOSTED_GENESIS_QUEUE_URL' in controlPlaneEnv, 'control plane must receive hosted genesis queue url');
	assert.ok('HOSTED_GENESIS_QUEUE_URL' in aiWorkerEnv, 'ai worker must receive hosted genesis queue url');
	assert.equal(aiWorker?.Timeout, 120, 'ai worker needs durable async LLM work timeout headroom');

	const mappings = findResources(template, 'AWS::Lambda::EventSourceMapping');
	const mapping = mappings.find((entry) => JSON.stringify(entry).includes(queue[0]) && JSON.stringify(entry).includes('AiWorker'));
	assert.ok(mapping, 'expected hosted genesis queue event source on ai-worker');
	assert.equal(mapping.BatchSize, 1, 'hosted genesis jobs must process one conversation turn at a time');
});

test('web asset bundling does not execute npm locally in CI', () => {
	assert.equal(shouldUseLocalWebBundling({ CI: 'true' }), false);
	assert.equal(shouldUseLocalWebBundling({ GITHUB_ACTIONS: 'true' }), false);
	assert.equal(shouldUseLocalWebBundling({}), true);
});

test('web SSR Lambda packages the Vite manifest beside the server bundle', () => {
	const template = synthTemplate();
	const fn = findLambdaByFunctionName(template, '-web-ssr');
	assert.equal(
		fn?.Handler,
		'server/face-app.handler',
		'WebSsrFn must package web/dist so face-app can read ../.vite/manifest.json and emit client assets',
	);
});

test('LH-27 htmlStore lifecycle does not expire static hydration sidecars', () => {
	const template = synthTemplate();
	const htmlStoreBucket = findBucketByLogicalIdPrefix(template, 'WebHtmlStoreBucket');
	const rules = bucketLifecycleRules(htmlStoreBucket);

	const expiringStaticSidecarRules = expiringLifecycleRulesMatchingKey(
		rules,
		'_facetheory/data/home.json',
	);
	assert.deepEqual(
		expiringStaticSidecarRules.map(lifecycleRuleId),
		[],
		'_facetheory/data/* static sidecars are deploy-pruned and must not match any expiring lifecycle rule',
	);

	const expiringIsrRules = expiringLifecycleRulesMatchingKey(rules, 'isr/index.html');
	assert.equal(
		expiringIsrRules.length,
		1,
		'ISR HTML objects under the AppTheory htmlStoreKeyPrefix must retain lifecycle expiration',
	);
	assert.equal(lifecycleRulePrefix(expiringIsrRules[0]!), 'isr/');

	const expiringRuntimeSidecarRules = expiringLifecycleRulesMatchingKey(
		rules,
		'isr/ssr-hydration/example-digest/1-2.json',
	);
	assert.equal(
		expiringRuntimeSidecarRules.length,
		1,
		'future /_facetheory/ssr-data signed sidecars should expire when stored under the ISR prefix',
	);
	assert.equal(lifecycleRulePrefix(expiringRuntimeSidecarRules[0]!), 'isr/');

	const webSsrFn = findLambdaByFunctionName(template, '-web-ssr');
	const env = lambdaEnvironment(webSsrFn);
	assert.equal(
		env.FACETHEORY_ISR_PREFIX,
		'isr',
		'future runtime sidecar storage should use the same ISR-prefixed object namespace',
	);
});

// ────────────────────────────────────────────────────────────────────
// M0.13 — CloudFront composition tests (SEC-8 verifier input).
//
// These tests lock the rendered distribution shape produced by
// AppTheorySsrSite v1.9.0 + the bearer-auth co-origin behaviors host
// attaches via addBehavior(). They catch:
//   - regressions to "exactly one OAC-protected default SSR origin"
//   - regressions to "S3 behavior for /_facetheory/data/* lives on
//     the dedicated htmlStoreBucket, not the SPA webBucket"
//   - regressions where OAC propagates onto bearer-auth API origins
//     (which would force CloudFront to sign every API request and break
//     the existing bearer auth contract)
// ────────────────────────────────────────────────────────────────────

type CloudFrontOrigin = {
	Id?: unknown;
	DomainName?: unknown;
	OriginAccessControlId?: unknown;
};

type CloudFrontBehavior = {
	PathPattern?: unknown;
	TargetOriginId?: unknown;
};

type DistributionConfig = {
	Origins?: CloudFrontOrigin[];
	DefaultCacheBehavior?: { TargetOriginId?: unknown };
	CacheBehaviors?: CloudFrontBehavior[];
};

type WebAclRule = {
	Name?: unknown;
	Priority?: unknown;
	Action?: unknown;
	OverrideAction?: unknown;
	Statement?: Record<string, unknown>;
	VisibilityConfig?: unknown;
};

function distributionConfig(template: SynthesizedTemplate): DistributionConfig {
	const distributions = findResources(template, 'AWS::CloudFront::Distribution');
	assert.equal(
		distributions.length,
		1,
		`expected exactly one CloudFront distribution, got ${distributions.length}`,
	);
	const config = distributions[0]?.DistributionConfig;
	assert.ok(config && typeof config === 'object', 'distribution missing DistributionConfig');
	return config as DistributionConfig;
}

function webAclRules(template: SynthesizedTemplate): WebAclRule[] {
	const webAcls = findResources(template, 'AWS::WAFv2::WebACL');
	assert.equal(webAcls.length, 1, `expected exactly one WebACL, got ${webAcls.length}`);
	const rules = webAcls[0]?.Rules;
	assert.ok(Array.isArray(rules), 'WebACL missing Rules array');
	return rules as WebAclRule[];
}

test('M0.13 distribution: preserves legacy logical id for in-place alias migration', () => {
	const distributions = findResourceEntries(synthTemplate(), 'AWS::CloudFront::Distribution');
	assert.equal(
		distributions.length,
		1,
		`expected exactly one CloudFront distribution, got ${distributions.length}`,
	);
	assert.equal(
		distributions[0]?.[0],
		'WebDistribution59C46482',
		'CloudFront distribution logical id must remain the legacy WebDistribution id so lab/live aliases update in place instead of colliding on create-before-delete',
	);
});

function originById(config: DistributionConfig, id: unknown): CloudFrontOrigin {
	const origin = (config.Origins ?? []).find((entry) => entry.Id === id);
	assert.ok(origin, `expected origin with id ${String(id)}`);
	return origin;
}

function originDomainSourceLogicalId(origin: CloudFrontOrigin): string {
	const domain = origin.DomainName;
	if (!domain) return '';
	if (typeof domain === 'string') return domain;
	// CloudFormation references can nest arbitrarily for Lambda Function URL
	// origins:
	//   { Fn::Select: [2, { Fn::Split: ["/", { Fn::GetAtt: [<id>, FunctionUrl] }] }] }
	// For HTTP API Gateway origins they nest under Fn::Join + Ref:
	//   { Fn::Join: ["", [{ Ref: <id> }, ...]] }
	// Walk the object tree and return the first Fn::GetAtt logical id, or
	// the first Ref logical id, whichever appears first in a JSON walk.
	const seen = JSON.stringify(domain);
	const getAttMatch = seen.match(/"Fn::GetAtt":\["([^"]+)"/);
	if (getAttMatch) return getAttMatch[1]!;
	const refMatch = seen.match(/"Ref":"([^"]+)"/);
	if (refMatch) return refMatch[1]!;
	return seen;
}

test('M0.13 distribution: exactly one OAC-protected default SSR Lambda origin', () => {
	const config = distributionConfig(synthTemplate());
	const defaultTargetId = config.DefaultCacheBehavior?.TargetOriginId;
	assert.ok(defaultTargetId, 'distribution missing default behavior target origin');
	const defaultOrigin = originById(config, defaultTargetId);
	const domainSourceId = originDomainSourceLogicalId(defaultOrigin);
	assert.ok(
		domainSourceId.startsWith('WebSiteSsrUrl'),
		`default origin must point at the AppTheorySsrSite Lambda Function URL (WebSiteSsrUrl*); got ${domainSourceId}`,
	);
	assert.ok(
		defaultOrigin.OriginAccessControlId,
		'default SSR Lambda origin must be OAC-protected (AppTheorySsrSite AWS_IAM fail-closed)',
	);
});

test('M0.13 distribution: /_facetheory/data/* is backed by the dedicated htmlStoreBucket', () => {
	const config = distributionConfig(synthTemplate());
	const sidecarBehavior = (config.CacheBehaviors ?? []).find(
		(b) => b.PathPattern === '_facetheory/data/*',
	);
	assert.ok(sidecarBehavior, 'expected /_facetheory/data/* cache behavior');
	const sidecarOrigin = originById(config, sidecarBehavior.TargetOriginId);
	const domainSourceId = originDomainSourceLogicalId(sidecarOrigin);
	assert.ok(
		domainSourceId.startsWith('WebHtmlStoreBucket'),
		`/_facetheory/data/* must be backed by the dedicated htmlStoreBucket (WebHtmlStoreBucket*); got ${domainSourceId}`,
	);
	assert.ok(
		sidecarOrigin.OriginAccessControlId,
		'/_facetheory/data/* S3 origin must be OAC-protected (the SSR Lambda writes signed sidecars; CloudFront signs reads)',
	);
});

test('M0.13 distribution: bearer-auth API/trust origins are NOT OAC-protected', () => {
	const config = distributionConfig(synthTemplate());
	// Bearer-auth co-origins host attaches via addBehavior(). These are
	// HTTP API Gateway v2 + REST API SSE origins, not Lambda Function URLs.
	// They MUST NOT inherit OAC from the AppTheorySsrSite-managed default
	// origin — that would force CloudFront to sign every request to these
	// origins and break the existing bearer-auth contract.
	const bearerPatterns = [
		'api/*',
		'auth/*',
		'webhooks/*',
		'setup/status',
		'setup/bootstrap/*',
		'setup/admin',
		'setup/finalize',
		'.well-known/*',
		'attestations',
		'attestations/*',
		'resolve*',
		'health*',
		'api/v1/previews*',
		'api/v1/renders*',
		'api/v1/trust/*',
		'api/v1/publish/jobs*',
		'api/v1/ai/*',
		'api/v1/budget/debit',
		'api/v1/soul/agents/*/update-registration',
		'api/v1/soul/agents/register/*/mint-conversation*',
		'api/v1/soul/agents/*/mint-conversation*',
		'api/v1/soul/instance/agents/register/*/mint-conversation*',
	];
	for (const pattern of bearerPatterns) {
		const behavior = (config.CacheBehaviors ?? []).find((b) => b.PathPattern === pattern);
		assert.ok(behavior, `expected ${pattern} cache behavior`);
		const origin = originById(config, behavior.TargetOriginId);
		assert.equal(
			origin.OriginAccessControlId,
			undefined,
			`bearer-auth origin behind ${pattern} must NOT be OAC-protected; OAC would break the existing bearer auth contract`,
		);
	}
});

test('P49 M2 distribution: Lesser instance mint-conversation route uses mint-conversation REST origin', () => {
	const config = distributionConfig(synthTemplate());
	const exactLesserPattern = 'api/v1/soul/instance/agents/register/*/mint-conversation*';
	const behavior = (config.CacheBehaviors ?? []).find((b) => b.PathPattern === exactLesserPattern);
	assert.ok(
		behavior,
		`expected exact Lesser-used instance mint-conversation pattern ${exactLesserPattern}`,
	);
	const origin = originById(config, behavior.TargetOriginId);
	const domainSourceId = originDomainSourceLogicalId(origin);
	assert.ok(
		domainSourceId.startsWith('ControlPlaneSseRestApi'),
		`exact Lesser-used instance mint-conversation route must use the mint-conversation REST origin while returning durable JSON; got ${domainSourceId}`,
	);
});

test('M0.13 WAF: allows no-User-Agent only for ENS /resolve CCIP-read', () => {
	const rules = webAclRules(synthTemplate());
	const commonRule = rules.find((rule) => rule.Name === 'AWSManagedRulesCommonRuleSet');
	assert.ok(commonRule, 'expected AWSManagedRulesCommonRuleSet rule');
	const commonStatement = commonRule.Statement?.ManagedRuleGroupStatement as
		| { RuleActionOverrides?: Array<Record<string, unknown>> }
		| undefined;
	assert.ok(commonStatement, 'CommonRuleSet missing ManagedRuleGroupStatement');
	const noUserAgentOverride = (commonStatement.RuleActionOverrides ?? []).find(
		(override) => override.Name === 'NoUserAgent_HEADER',
	);
	assert.deepEqual(
		noUserAgentOverride,
		{ Name: 'NoUserAgent_HEADER', ActionToUse: { Count: {} } },
		'managed no-User-Agent rule must be counted so host can apply a route-scoped /resolve exception',
	);

	const customRule = rules.find((rule) => rule.Name === 'BlockNoUserAgentExceptResolve');
	assert.ok(customRule, 'expected custom no-User-Agent block rule');
	assert.deepEqual(customRule.Action, { Block: {} }, 'custom no-User-Agent rule must block');
	assert.equal(customRule.Priority, 1, 'custom no-User-Agent rule must run immediately after CommonRuleSet');

	const andStatement = customRule.Statement?.AndStatement as { Statements?: Array<Record<string, unknown>> } | undefined;
	assert.ok(andStatement, 'custom no-User-Agent rule must be an AND statement');
	const statements = andStatement.Statements ?? [];
	assert.equal(statements.length, 2, 'custom no-User-Agent rule must have exactly label and path predicates');

	const labelPredicate = statements.find((statement) => statement.LabelMatchStatement);
	assert.deepEqual(
		labelPredicate,
		{
			LabelMatchStatement: {
				Scope: 'LABEL',
				Key: 'awswaf:managed:aws:core-rule-set:NoUserAgent_Header',
			},
		},
		'custom rule must reuse the managed no-User-Agent label instead of reimplementing header semantics',
	);

	const pathPredicate = statements.find((statement) => statement.NotStatement);
	assert.deepEqual(
		pathPredicate,
		{
			NotStatement: {
				Statement: {
					ByteMatchStatement: {
						FieldToMatch: { UriPath: {} },
						PositionalConstraint: 'EXACTLY',
						SearchString: '/resolve',
						TextTransformations: [{ Priority: 0, Type: 'NONE' }],
					},
				},
			},
		},
		'custom rule must exempt exactly /resolve and no broader trust/API path',
	);
});

test('M0.13 distribution: preserves all bearer-auth path patterns from the legacy distribution', () => {
	const config = distributionConfig(synthTemplate());
	const observedPatterns = new Set(
		(config.CacheBehaviors ?? []).map((b) => b.PathPattern).filter((p): p is string => typeof p === 'string'),
	);
	// Patterns that existed on the pre-AppTheorySsrSite legacy distribution
	// MUST all still exist. (AppTheorySsrSite's edge function also adds
	// pattern variants like "assets" alongside "assets/*"; we only assert
	// the host-attached behaviors here.)
	const legacyPatterns = [
		'safe-app*',
		'resolve*',
		'health*',
		'api/v1/previews*',
		'api/v1/renders*',
		'api/v1/trust/*',
		'api/v1/publish/jobs*',
		'api/v1/soul/agents/*/update-registration',
		'api/v1/ai/*',
		'api/v1/budget/debit',
		'api/v1/soul/agents/register/*/mint-conversation*',
		'api/v1/soul/agents/*/mint-conversation*',
		'api/v1/soul/instance/agents/register/*/mint-conversation*',
		'api/*',
		'auth/*',
		'webhooks/*',
		'setup/status',
		'setup/bootstrap/*',
		'setup/admin',
		'setup/finalize',
		'.well-known/*',
		'attestations',
		'attestations/*',
	];
	for (const pattern of legacyPatterns) {
		assert.ok(
			observedPatterns.has(pattern),
			`expected legacy bearer-auth path pattern ${pattern} to be preserved on the AppTheorySsrSite-managed distribution`,
		);
	}
});

// ────────────────────────────────────────────────────────────────────
// M0.14 — CSP integrity tests (SEC-5 verifier input).
//
// Locks the rendered webCsp + safeAppCsp byte-strings against drift.
// The CSP byte-strings are the host's strict single-origin contract:
//   - default-src 'none'
//   - no unsafe-inline / no unsafe-eval
//   - no nonce-* / no sha256-* / no sha384-* / no sha512-* relaxations
//   - no non-self script origin
//   - frame-ancestors locked to 'none' (webCsp) or
//     safe.global allowlist (safeAppCsp); no other variants
// ────────────────────────────────────────────────────────────────────

const EXPECTED_WEB_CSP =
	"default-src 'none'; " +
	"base-uri 'none'; " +
	"object-src 'none'; " +
	"frame-ancestors 'none'; " +
	"form-action 'self'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self'; " +
	"style-src 'self'; " +
	"script-src 'self'; " +
	"connect-src 'self'; " +
	"manifest-src 'self'";

const EXPECTED_SAFE_APP_CSP =
	"default-src 'none'; " +
	"base-uri 'none'; " +
	"object-src 'none'; " +
	"frame-ancestors https://safe.global https://*.safe.global; " +
	"form-action 'self'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self'; " +
	"style-src 'self'; " +
	"script-src 'self'; " +
	"connect-src 'self'; " +
	"manifest-src 'self'";

function findCspByPolicyName(template: SynthesizedTemplate, nameSuffix: string): string {
	const policies = findResources(template, 'AWS::CloudFront::ResponseHeadersPolicy');
	const match = policies.find((p) => {
		const config = (p as Record<string, unknown>).ResponseHeadersPolicyConfig as
			| { Name?: unknown }
			| undefined;
		const name = config?.Name;
		return typeof name === 'string' && name.endsWith(nameSuffix);
	}) as Record<string, unknown> | undefined;
	assert.ok(match, `expected a ResponseHeadersPolicy with name ending in ${nameSuffix}`);
	const config = match.ResponseHeadersPolicyConfig as {
		SecurityHeadersConfig?: {
			ContentSecurityPolicy?: { ContentSecurityPolicy?: unknown };
		};
	};
	const csp = config.SecurityHeadersConfig?.ContentSecurityPolicy?.ContentSecurityPolicy;
	assert.equal(typeof csp, 'string', `${nameSuffix} CSP must be a string`);
	return csp as string;
}

test('M0.14 CSP integrity: webCsp byte-string locked', () => {
	const template = synthTemplate();
	const csp = findCspByPolicyName(template, '-web-security');
	assert.equal(
		csp,
		EXPECTED_WEB_CSP,
		'webCsp byte-string drifted; strict single-origin posture must be preserved',
	);
});

test('M0.14 CSP integrity: safeAppCsp byte-string locked (Safe frame-ancestors exception preserved)', () => {
	const template = synthTemplate();
	const csp = findCspByPolicyName(template, '-safe-app-security');
	assert.equal(
		csp,
		EXPECTED_SAFE_APP_CSP,
		'safeAppCsp byte-string drifted; the Safe-app frame-ancestor exception must be preserved exactly',
	);
});

test('M0.14 CSP integrity: webCsp + safeAppCsp reject every CSP relaxation vector', () => {
	const template = synthTemplate();
	const policies = [EXPECTED_WEB_CSP, EXPECTED_SAFE_APP_CSP, findCspByPolicyName(template, '-web-security'), findCspByPolicyName(template, '-safe-app-security')];
	// Assert exact byte-strings (above tests) + behavioral assertions on
	// the *rendered* CSPs that catch a future hand-edit that quietly relaxes
	// posture without changing the expected constant (defense in depth).
	const renderedWeb = findCspByPolicyName(template, '-web-security');
	const renderedSafeApp = findCspByPolicyName(template, '-safe-app-security');
	for (const csp of [renderedWeb, renderedSafeApp]) {
		assert.ok(!csp.includes("'unsafe-inline'"), "CSP must not contain 'unsafe-inline'");
		assert.ok(!csp.includes("'unsafe-eval'"), "CSP must not contain 'unsafe-eval'");
		assert.ok(!csp.includes("'unsafe-hashes'"), "CSP must not contain 'unsafe-hashes'");
		assert.ok(!/nonce-/.test(csp), 'CSP must not contain nonce-* relaxations');
		assert.ok(!/sha256-/.test(csp), 'CSP must not contain sha256-* relaxations');
		assert.ok(!/sha384-/.test(csp), 'CSP must not contain sha384-* relaxations');
		assert.ok(!/sha512-/.test(csp), 'CSP must not contain sha512-* relaxations');
		// script-src must be 'self' only — no non-self origin allowed.
		const scriptSrc = csp.match(/script-src ([^;]+)/);
		assert.ok(scriptSrc, 'CSP must declare script-src');
		assert.equal(
			scriptSrc[1]!.trim(),
			"'self'",
			"script-src must be exactly 'self' (no non-self origin, no relaxation)",
		);
	}
	// Web (non-Safe) policy must keep frame-ancestors 'none'.
	assert.ok(
		/frame-ancestors 'none'/.test(renderedWeb),
		"webCsp must keep frame-ancestors 'none' (no embedding)",
	);
	// Safe-app policy is the explicit allowed variant — same.global only.
	assert.ok(
		/frame-ancestors https:\/\/safe.global https:\/\/\*\.safe\.global/.test(renderedSafeApp),
		'safeAppCsp must keep the safe.global frame-ancestors allowlist exactly',
	);
	void policies; // tsc: keep the list alive even though we only assert via renderedWeb/renderedSafeApp
});
