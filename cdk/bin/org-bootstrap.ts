#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { OrgBootstrapStack } from '../lib/org-bootstrap-stack';

const app = new cdk.App();

function applyLocalContextOverrides(app: cdk.App): void {
	const localContextPath = path.resolve(__dirname, '../cdk.context.local.json');
	if (!fs.existsSync(localContextPath)) return;

	let raw: string;
	try {
		raw = fs.readFileSync(localContextPath, 'utf8');
	} catch (err) {
		throw new Error(`Failed reading ${localContextPath}: ${String(err)}`);
	}

	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch (err) {
		throw new Error(`Invalid JSON in ${localContextPath}: ${String(err)}`);
	}

	const maybeContext =
		typeof parsed === 'object' && parsed !== null && 'context' in parsed
			? (parsed as { context: unknown }).context
			: parsed;

	if (typeof maybeContext !== 'object' || maybeContext === null) return;

	for (const [key, value] of Object.entries(maybeContext)) {
		const currentValue = app.node.tryGetContext(key);
		const currentString = typeof currentValue === 'string' ? currentValue : '';
		const isPlaceholder =
			currentValue === undefined ||
			currentString.trim() === '' ||
			currentString.includes('<YOUR_') ||
			(currentString.startsWith('<') && currentString.endsWith('>'));
		if (isPlaceholder) app.node.setContext(key, value);
	}
}

applyLocalContextOverrides(app);

const stackName = (app.node.tryGetContext('orgBootstrapStackName') as string | undefined) ??
	'lesser-host-org-bootstrap';
const controlPlaneAccountId =
	(app.node.tryGetContext('orgBootstrapControlPlaneAccountId') as string | undefined) ??
	(app.node.tryGetContext('controlPlaneAccountId') as string | undefined) ??
	'';
const roleName = (app.node.tryGetContext('managedOrgVendingRoleName') as string | undefined) ??
	'lesser-host-org-vending';

new OrgBootstrapStack(app, stackName, {
	controlPlaneAccountId,
	roleName,
});
