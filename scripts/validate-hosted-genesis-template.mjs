#!/usr/bin/env node
import { readFileSync } from 'node:fs';

function usage() {
	console.error('usage: validate-hosted-genesis-template.mjs <cloudformation-template.json>');
}

function fail(message) {
	console.error(`hosted genesis template guard: ${message}`);
	process.exit(1);
}

function asRecord(value) {
	return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function hasRefTo(value, logicalId) {
	if (value === logicalId) return true;
	if (Array.isArray(value)) return value.some((entry) => hasRefTo(entry, logicalId));
	if (!value || typeof value !== 'object') return false;
	return Object.values(value).some((entry) => hasRefTo(entry, logicalId));
}

function valueSummary(value) {
	if (typeof value === 'string') return value;
	if (value === undefined) return '<missing>';
	try {
		return JSON.stringify(value);
	} catch {
		return String(value);
	}
}

function logicalRefs(value, out = new Set()) {
	if (typeof value === 'string') {
		out.add(value);
		return out;
	}
	if (Array.isArray(value)) {
		for (const entry of value) logicalRefs(entry, out);
		return out;
	}
	if (!value || typeof value !== 'object') return out;
	if (typeof value.Ref === 'string') out.add(value.Ref);
	if (Array.isArray(value['Fn::GetAtt']) && typeof value['Fn::GetAtt'][0] === 'string') out.add(value['Fn::GetAtt'][0]);
	for (const entry of Object.values(value)) logicalRefs(entry, out);
	return out;
}

const templatePath = process.argv[2];
if (!templatePath || process.argv.length !== 3) {
	usage();
	process.exit(2);
}

let template;
try {
	template = JSON.parse(readFileSync(templatePath, 'utf8'));
} catch (err) {
	fail(`failed to read template ${templatePath}: ${err instanceof Error ? err.message : String(err)}`);
}

const resources = asRecord(template.Resources);
const outputs = asRecord(template.Outputs);
const resourceEntries = Object.entries(resources).map(([logicalId, resource]) => [logicalId, asRecord(resource)]);

const hostedGenesisQueue = resourceEntries.find(([logicalId, resource]) => {
	const properties = asRecord(resource.Properties);
	return resource.Type === 'AWS::SQS::Queue' &&
		logicalId.includes('HostedGenesisQueue') &&
		JSON.stringify(properties.QueueName ?? '').includes('hosted-genesis-queue');
});
if (!hostedGenesisQueue) {
	fail('missing HostedGenesisQueue SQS resource');
}
const hostedGenesisQueueLogicalId = hostedGenesisQueue[0];

const lambdaEntries = resourceEntries.filter(([, resource]) => resource.Type === 'AWS::Lambda::Function');
function findLambda(namePart, logicalIdPart) {
	return lambdaEntries.find(([logicalId, resource]) => {
		const properties = asRecord(resource.Properties);
		return logicalId.includes(logicalIdPart) || JSON.stringify(properties.FunctionName ?? '').includes(namePart);
	});
}

function lambdaEnv(entry) {
	const resource = asRecord(entry?.[1]);
	return asRecord(asRecord(asRecord(resource.Properties).Environment).Variables);
}

const controlPlane = findLambda('control-plane-api', 'ControlPlaneApi');
if (!controlPlane) {
	fail('missing control-plane Lambda function');
}
const controlPlaneEnv = lambdaEnv(controlPlane);
if (Object.prototype.hasOwnProperty.call(controlPlaneEnv, 'HOSTED_GENESIS_QUEUE_URL')) {
	fail('control-plane Lambda must not receive HOSTED_GENESIS_QUEUE_URL; HostedGenesisSession is user-visible authority');
}
const stage = typeof controlPlaneEnv.STAGE === 'string' ? controlPlaneEnv.STAGE.trim().toLowerCase() : '';

const aiWorker = findLambda('ai-worker', 'AiWorker');
if (!aiWorker) {
	fail('missing AI worker Lambda function');
}
if (!Object.prototype.hasOwnProperty.call(lambdaEnv(aiWorker), 'HOSTED_GENESIS_QUEUE_URL')) {
	fail('AI worker Lambda lacks HOSTED_GENESIS_QUEUE_URL');
}

if (!Object.prototype.hasOwnProperty.call(outputs, 'HostedGenesisQueueUrl')) {
	fail('missing HostedGenesisQueueUrl stack output');
}

const controlPlaneRoleRefs = logicalRefs(asRecord(asRecord(controlPlane[1]).Properties).Role);
const iamPolicies = resourceEntries.filter(([, resource]) => resource.Type === 'AWS::IAM::Policy');
for (const [logicalId, policy] of iamPolicies) {
	const properties = asRecord(policy.Properties);
	const roleBindings = JSON.stringify(properties.Roles ?? {});
	const appliesToControlPlane = [...controlPlaneRoleRefs].some((roleRef) => roleBindings.includes(roleRef));
	if (!appliesToControlPlane) continue;
	const policyText = JSON.stringify(properties.PolicyDocument ?? {});
	if (policyText.includes(hostedGenesisQueueLogicalId) && policyText.includes('sqs:SendMessage')) {
		fail(`control-plane IAM policy ${logicalId} grants SendMessage to HostedGenesisQueue; queue must remain operator/backfill only`);
	}
}

const eventSourceMappings = resourceEntries.filter(([, resource]) => resource.Type === 'AWS::Lambda::EventSourceMapping');
const aiWorkerLogicalId = aiWorker[0];
const hostedGenesisMapping = eventSourceMappings.find(([, resource]) => {
	const properties = asRecord(resource.Properties);
	return hasRefTo(properties.EventSourceArn, hostedGenesisQueueLogicalId) &&
		hasRefTo(properties.FunctionName, aiWorkerLogicalId);
});
if (!hostedGenesisMapping) {
	fail('missing AI worker EventSourceMapping for hosted genesis queue');
}
if (asRecord(hostedGenesisMapping[1].Properties).BatchSize !== 1) {
	fail('AI worker hosted genesis EventSourceMapping must use BatchSize 1');
}

const microvmController = lambdaEntries.find(([logicalId, resource]) =>
	logicalId.includes('HostedGenesisMicrovmController') ||
	JSON.stringify(asRecord(resource.Properties).FunctionName ?? '').includes('hosted-genesis-microvm-controller')
);
if (microvmController) {
	const controllerEnv = lambdaEnv(microvmController);
	if (controllerEnv.APPTHEORY_MICROVM_CONTROLLER_AUTH_REQUIRED !== 'true') {
		fail('hosted genesis MicroVM controller must require AppTheory controller auth');
	}
	if (controllerEnv.APPTHEORY_MICROVM_CONTROLLER_AUTH_DEFAULT !== 'deny') {
		fail('hosted genesis MicroVM controller must fail closed with default auth deny');
	}
	if (controllerEnv.APPTHEORY_MICROVM_CONTRACT_VERSION !== 'm16.microvm/v1') {
		fail('hosted genesis MicroVM controller must pin the AppTheory v1.15 M16 contract');
	}
	if (!Object.prototype.hasOwnProperty.call(controllerEnv, 'STATE_TABLE_NAME')) {
		fail('hosted genesis MicroVM controller requires STATE_TABLE_NAME for HostedGenesisSession reconstruction');
	}
	for (const key of Object.keys(controllerEnv)) {
		const normalized = key.toLowerCase();
		if (normalized.includes('token') && !normalized.endsWith('sha256')) {
			fail(`hosted genesis MicroVM controller env ${key} looks token-bearing; only digests/non-secret refs are allowed`);
		}
	}
}

const microvmAuthorizer = lambdaEntries.find(([logicalId, resource]) =>
	logicalId.includes('HostedGenesisMicrovmAuthorizer') ||
	JSON.stringify(asRecord(resource.Properties).FunctionName ?? '').includes('hosted-genesis-microvm-authorizer')
);
if (microvmAuthorizer) {
	const authorizerEnv = lambdaEnv(microvmAuthorizer);
	const digest = authorizerEnv.APPTHEORY_MICROVM_AUTHORIZER_TOKEN_SHA256;
	if (typeof digest !== 'string' || !/^sha256:[0-9a-f]{64}$/i.test(digest)) {
		fail('hosted genesis MicroVM authorizer must receive only a sha256 token digest');
	}
}

if (stage === 'live') {
	const soulRuntimeChecks = [
		[controlPlane[0], controlPlaneEnv],
		...lambdaEntries
			.filter(([logicalId]) => logicalId !== controlPlane[0])
			.map(([logicalId, resource]) => [logicalId, lambdaEnv([logicalId, resource])])
			.filter(([, env]) => Object.prototype.hasOwnProperty.call(env, 'SOUL_ENABLED')),
	];
	const soulDisabled = soulRuntimeChecks
		.filter(([, env]) => env.SOUL_ENABLED !== 'true')
		.map(([logicalId, env]) => `${logicalId} SOUL_ENABLED=${valueSummary(env.SOUL_ENABLED)}`);
	if (soulDisabled.length > 0) {
		fail(
			`live hosted genesis and soul search require SOUL_ENABLED=true on soul-aware Lambdas; invalid runtime config: ${soulDisabled.join(', ')}`,
		);
	}
}

console.error(
	`hosted genesis template guard: OK ${templatePath} keeps HostedGenesisSession as user-visible authority, HostedGenesisQueue as non-authoritative operator/backfill transport, AI worker EventSourceMapping, and AppTheory MicroVM fail-closed/no-token invariants`,
);
