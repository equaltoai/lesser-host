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
if (!Object.prototype.hasOwnProperty.call(lambdaEnv(controlPlane), 'HOSTED_GENESIS_QUEUE_URL')) {
	fail('control-plane Lambda lacks HOSTED_GENESIS_QUEUE_URL');
}

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

console.error(
	`hosted genesis template guard: OK ${templatePath} contains HostedGenesisQueue, HOSTED_GENESIS_QUEUE_URL, HostedGenesisQueueUrl, and AI worker EventSourceMapping`,
);
