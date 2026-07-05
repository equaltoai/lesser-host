#!/usr/bin/env node
import { readFileSync } from 'node:fs';

const canonicalDomain = 'lesser.host';
const canonicalUrl = `https://${canonicalDomain}`;

function usage() {
	console.error('usage: validate-live-domain-template.mjs <stage> <cloudformation-template.json>');
}

function fail(details) {
	const detailText = details.length > 0 ? details.join('; ') : 'unknown validation failure';
	console.error(
		`live custom-domain guard: synthesized live template would omit or remove the canonical first-party Host domain ${canonicalDomain}; refusing before cdk deploy. Live CDK domain resolution must synthesize ${canonicalDomain} from app-theory/app.json; this failure means CDK domain resolution is broken or AppTheory app.json web-domain config is invalid or unavailable. Details: ${detailText}`,
	);
	process.exit(1);
}

function asRecord(value) {
	return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
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

function normalizedDnsName(value) {
	return typeof value === 'string' ? value.trim().replace(/\.$/, '').toLowerCase() : '';
}

function arrayIncludesString(value, expected) {
	return Array.isArray(value) && value.some((entry) => typeof entry === 'string' && entry === expected);
}

const stage = process.argv[2]?.trim().toLowerCase();
const templatePath = process.argv[3];
if (!stage || !templatePath || process.argv.length !== 4) {
	usage();
	process.exit(2);
}

if (stage !== 'live') {
	console.error(
		`live custom-domain guard: skipped stage ${stage}; only live is required to preserve ${canonicalDomain}`,
	);
	process.exit(0);
}

let template;
try {
	template = JSON.parse(readFileSync(templatePath, 'utf8'));
} catch (err) {
	fail([`failed to read template ${templatePath}: ${err instanceof Error ? err.message : String(err)}`]);
}

const resources = asRecord(template.Resources);
const outputs = asRecord(template.Outputs);
const resourceEntries = Object.entries(resources).map(([logicalId, resource]) => [logicalId, asRecord(resource)]);
const problems = [];

const distributions = resourceEntries.filter(([, resource]) => resource.Type === 'AWS::CloudFront::Distribution');
if (distributions.length !== 1) {
	problems.push(`expected exactly one CloudFront distribution, found ${distributions.length}`);
} else {
	const distributionConfig = asRecord(asRecord(distributions[0][1].Properties).DistributionConfig);
	if (!arrayIncludesString(distributionConfig.Aliases, canonicalDomain)) {
		problems.push(
			`CloudFront distribution is missing Aliases entry ${canonicalDomain} (got ${valueSummary(distributionConfig.Aliases)})`,
		);
	}

	const viewerCertificate = asRecord(distributionConfig.ViewerCertificate);
	if (viewerCertificate.CloudFrontDefaultCertificate === true) {
		problems.push('CloudFront distribution uses CloudFrontDefaultCertificate=true');
	}
	if (!Object.prototype.hasOwnProperty.call(viewerCertificate, 'AcmCertificateArn')) {
		problems.push(
			`CloudFront distribution viewer certificate is not ACM-backed (ViewerCertificate ${valueSummary(distributionConfig.ViewerCertificate)})`,
		);
	}
}

const recordSets = resourceEntries.filter(([, resource]) => resource.Type === 'AWS::Route53::RecordSet');
const hasApexRecord = (type) =>
	recordSets.some(([, resource]) => {
		const properties = asRecord(resource.Properties);
		return properties.Type === type && normalizedDnsName(properties.Name) === canonicalDomain;
	});
if (!hasApexRecord('A')) {
	problems.push(`missing Route53 apex A record for ${canonicalDomain}`);
}
if (!hasApexRecord('AAAA')) {
	problems.push(`missing Route53 apex AAAA record for ${canonicalDomain}`);
}

const lambdaPublicBaseUrls = resourceEntries
	.filter(([, resource]) => resource.Type === 'AWS::Lambda::Function')
	.map(([logicalId, resource]) => {
		const variables = asRecord(asRecord(asRecord(resource.Properties).Environment).Variables);
		return [logicalId, variables.PUBLIC_BASE_URL];
	})
	.filter(([, value]) => value !== undefined);
if (lambdaPublicBaseUrls.length === 0) {
	problems.push('no Lambda PUBLIC_BASE_URL runtime environment values found');
}
for (const [logicalId, value] of lambdaPublicBaseUrls) {
	if (value !== canonicalUrl) {
		problems.push(`Lambda ${logicalId} PUBLIC_BASE_URL is ${valueSummary(value)}, expected ${canonicalUrl}`);
	}
}

const webUrlOutput = asRecord(outputs.WebUrl).Value;
if (webUrlOutput !== canonicalUrl) {
	problems.push(`WebUrl output is ${valueSummary(webUrlOutput)}, expected ${canonicalUrl}`);
}

if (problems.length > 0) {
	fail(problems);
}

console.error(
	`live custom-domain guard: OK synthesized live template preserves ${canonicalDomain} CloudFront alias, ACM viewer certificate, Route53 A/AAAA records, PUBLIC_BASE_URL, and WebUrl`,
);
