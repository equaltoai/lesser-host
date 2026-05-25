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
