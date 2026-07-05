// Shared CDK test helpers for the lesser-host stack test suite.
//
// P52 H1 MAI-1 split: the single test file cdk/test/lesser-host-stack.test.ts
// exceeded the gov-infra TS/JS 2000-line file budget. The hosted-genesis MicroVM
// tests were extracted into cdk/test/lesser-host-stack-microvm.test.ts, and the
// synth + lookup helpers both files depend on were factored into this module so
// neither test file duplicates them. Everything here is pure helper code shared
// across the two test files; it holds no test cases of its own.

import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import * as cdk from 'aws-cdk-lib';

import { LesserHostStack, type LesserHostStackProps } from '../lib/lesser-host-stack';

process.env.GOTOOLCHAIN = process.env.GOTOOLCHAIN || 'auto';

const webLookupAccount = '123456789012';
const webLookupRegion = 'us-east-1';
export const testWebRootDomain = 'lesser.host';
export const testWebHostedZoneId = 'ZTESTLOCALDOMAIN0001';
export const webLookupContext = {};
export const webStackEnv = { account: webLookupAccount, region: webLookupRegion };

export function writeTestDeployLocalConfig(
	configPath: string,
	overrides: Partial<{ rootDomain: string; hostedZoneId: string; hostedZoneName: string }> = {},
): void {
	const rootDomain = overrides.rootDomain ?? testWebRootDomain;
	const hostedZoneId = overrides.hostedZoneId ?? testWebHostedZoneId;
	const hostedZoneName = overrides.hostedZoneName ?? rootDomain;
	writeFileSync(configPath, `${JSON.stringify({
		domain: {
			lab: { rootDomain, hostedZoneId, hostedZoneName },
			live: { rootDomain, hostedZoneId, hostedZoneName },
		},
	}, null, 2)}\n`);
}

export function hostedGenesisMicrovmContext(_stage = 'lab'): Record<string, string> {
	// P52 H1 step 2 (F1): the hosted-genesis MicroVM path takes NO caller-supplied
	// context for deployed stages — the base-image ARN is the pinned AWS-managed
	// literal HOSTED_GENESIS_MICROVM_BASE_IMAGE_ARN, and the auth token + SSM param
	// name are CDK-owned via a custom resource. Returns {} so synth asserts the
	// pinned literal flows through verbatim. Kept (not deleted) to preserve the
	// call sites that spread it into every test's context.
	return {};
}

export const hostedGenesisMicrovmRequiredContext = hostedGenesisMicrovmContext('lab');

export type SynthesizedTemplate = {
	Resources?: Record<string, { Type?: string; Properties?: Record<string, unknown> }>;
	Outputs?: Record<string, unknown>;
};

let synthesizedTemplate: SynthesizedTemplate | undefined;

export function synthesizeTemplate(
	stage: string,
	context: Record<string, unknown>,
	stackId: string,
	props: Omit<LesserHostStackProps, 'stage'> = {},
): SynthesizedTemplate {
	// Full LesserHostStack synthesis stages web assets. Keep each test synth in
	// its own short-lived outdir so repeated assertions cannot accumulate
	// copied web/CDK asset directories and exhaust hosted runner disk.
	const outdir = mkdtempSync(join(tmpdir(), 'lesser-host-cdk-test-'));
	try {
		const domainConfigPath = props.domainConfigPath ?? join(outdir, 'deploy.local.json');
		if (!props.domainConfigPath) {
			writeTestDeployLocalConfig(domainConfigPath);
		}
		const app = new cdk.App({ context, outdir });
		const stack = new LesserHostStack(app, stackId, { stage, ...props, domainConfigPath });
		const assembly = app.synth();
		const artifact = assembly.getStackArtifact(stack.artifactId);
		return JSON.parse(readFileSync(artifact.templateFullPath, 'utf8')) as SynthesizedTemplate;
	} finally {
		rmSync(outdir, { recursive: true, force: true });
	}
}

export function synthTemplate(): SynthesizedTemplate {
	if (!synthesizedTemplate) {
		synthesizedTemplate = synthesizeTemplate('lab', { ...webLookupContext, ...hostedGenesisMicrovmContext('lab') }, 'TestLesserHostStack', { env: webStackEnv });
	}
	return synthesizedTemplate;
}

export function synthTemplateWithContext(context: Record<string, unknown>): SynthesizedTemplate {
	return synthesizeTemplate('lab', { ...webLookupContext, ...hostedGenesisMicrovmContext('lab'), ...context }, 'TestLesserHostStackWithContext', { env: webStackEnv });
}

export function synthTemplateForStage(stage: string, context: Record<string, unknown> = {}): SynthesizedTemplate {
	if (Object.keys(context).length === 0 && stage.trim() === 'lab') {
		return synthTemplate();
	}
	return synthesizeTemplate(stage, { ...webLookupContext, ...hostedGenesisMicrovmContext(stage), ...context }, `TestLesserHostStack-${stage}`, { env: webStackEnv });
}

export function cdkJsonContext(): Record<string, unknown> {
	const cdkJson = JSON.parse(readFileSync(join(process.cwd(), 'cdk.json'), 'utf8')) as { context?: unknown };
	assert.ok(cdkJson.context && typeof cdkJson.context === 'object', 'expected cdk.json context object');
	return cdkJson.context as Record<string, unknown>;
}

export function synthTemplateForStageWithCdkJsonContext(stage: string, context: Record<string, unknown> = {}): SynthesizedTemplate {
	return synthesizeTemplate(
		stage,
		{ ...cdkJsonContext(), ...webLookupContext, ...hostedGenesisMicrovmContext(stage), ...context },
		`TestLesserHostStack-cdk-json-${stage}`,
		{ env: webStackEnv },
	);
}

export function runDependencyCycleValidator(template: Record<string, unknown>) {
	const tempDir = mkdtempSync(join(tmpdir(), 'lesser-host-cfn-deps-'));
	try {
		const templatePath = join(tempDir, 'template.json');
		writeFileSync(templatePath, JSON.stringify(template, null, 2));
		return spawnSync(
			process.execPath,
			[join(process.cwd(), '..', 'scripts', 'validate-cfn-dependency-cycles.mjs'), templatePath],
			{ encoding: 'utf8' },
		);
	} finally {
		rmSync(tempDir, { recursive: true, force: true });
	}
}

export function findResources(template: SynthesizedTemplate, type: string): Array<Record<string, unknown>> {
	return Object.values(template.Resources ?? {})
		.filter((resource) => resource?.Type === type)
		.map((resource) => resource?.Properties ?? {});
}

export function findResourceEntries(
	template: SynthesizedTemplate,
	type: string,
): Array<[string, { Type?: string; Properties?: Record<string, unknown> }]> {
	return Object.entries(template.Resources ?? {}).filter(([, resource]) => resource?.Type === type);
}

// Principal.Service values from an IAM role's AssumeRolePolicyDocument as an exact
// array (string or string[] normalized). Callers assert EXACT membership via
// Array.prototype.includes (element equality, not a substring match — the latter is
// a spoofable-host smell flagged by CodeQL on string-valued principals).
export function roleServicePrincipals(role: { Properties?: Record<string, unknown> }): string[] {
	const statements = (role.Properties?.AssumeRolePolicyDocument as { Statement?: unknown[] } | undefined)?.Statement;
	if (!Array.isArray(statements)) {
		return [];
	}
	return statements.flatMap((statement) => {
		const service = (statement as { Principal?: { Service?: unknown } } | undefined)?.Principal?.Service;
		if (typeof service === 'string') {
			return [service];
		}
		return Array.isArray(service) ? service.filter((s): s is string => typeof s === 'string') : [];
	});
}

export function findLambdaEntryByFunctionName(
	template: SynthesizedTemplate,
	namePart: string,
): [string, { Type?: string; Properties?: Record<string, unknown> }] | undefined {
	return findResourceEntries(template, 'AWS::Lambda::Function').find(([, resource]) => {
		const name = resource.Properties?.FunctionName;
		return typeof name === 'string' && name.includes(namePart);
	});
}

export function lambdaEnvironment(fn: Record<string, unknown> | undefined): Record<string, unknown> {
	const environment = fn?.Environment;
	if (!environment || typeof environment !== 'object') {
		return {};
	}
	const variables = (environment as { Variables?: unknown }).Variables;
	return variables && typeof variables === 'object' ? (variables as Record<string, unknown>) : {};
}
