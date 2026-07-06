#!/usr/bin/env node
import { readFileSync } from 'node:fs';

function usage() {
	console.error('usage: validate-deploy-template-placeholders.mjs <cloudformation-template.json>');
}

function fail(message) {
	console.error(`deploy template placeholder guard: ${message}`);
	process.exit(1);
}

function asRecord(value) {
	return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function hasPlaceholder(value) {
	return typeof value === 'string' && (/<[^>]*YOUR[^>]*>/i.test(value) || /YOUR_[A-Z0-9_]+/i.test(value));
}

function visit(value, path, hits) {
	if (hasPlaceholder(value)) {
		hits.push(path);
		return;
	}
	if (Array.isArray(value)) {
		value.forEach((entry, index) => visit(entry, `${path}[${index}]`, hits));
		return;
	}
	if (!value || typeof value !== 'object') {
		return;
	}
	for (const [key, entry] of Object.entries(value)) {
		visit(entry, `${path}.${key}`, hits);
	}
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
const hits = [];
const deployCriticalLambdaEnvKeys = new Set([
	'BOOTSTRAP_WALLET_ADDRESS',
	'MANAGED_ORG_VENDING_ROLE_ARN',
]);

for (const [logicalId, resource] of Object.entries(resources)) {
	const typedResource = asRecord(resource);
	const type = typedResource.Type;
	const properties = asRecord(typedResource.Properties);
	if (typeof type === 'string' && type.startsWith('AWS::IAM::')) {
		visit(properties, `Resources.${logicalId}.Properties`, hits);
	}
	if (type === 'AWS::Lambda::Function') {
		const variables = asRecord(asRecord(properties.Environment).Variables);
		for (const key of deployCriticalLambdaEnvKeys) {
			if (Object.prototype.hasOwnProperty.call(variables, key)) {
				visit(
					variables[key],
					`Resources.${logicalId}.Properties.Environment.Variables.${key}`,
					hits,
				);
			}
		}
	}
}

if (hits.length > 0) {
	fail(`placeholder token found in deploy-critical template fields: ${hits.join(', ')}`);
}

console.error(
	`deploy template placeholder guard: OK ${templatePath} has no placeholder tokens in IAM resources or managed org vending env wiring (including bootstrap-wallet deploy-critical Lambda env wiring)`,
);
