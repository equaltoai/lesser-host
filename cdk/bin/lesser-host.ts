#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { LesserHostStack } from '../lib/lesser-host-stack';

const app = new cdk.App();

function findUpward(startDir: string, predicate: (dir: string) => boolean): string {
	let current = startDir;
	for (;;) {
		if (predicate(current)) return current;
		const parent = path.dirname(current);
		if (parent === current) {
			throw new Error(`Failed to locate expected CDK directory from ${startDir}`);
		}
		current = parent;
	}
}

function cdkDir(): string {
	return findUpward(__dirname, (dir) => fs.existsSync(path.join(dir, 'cdk.json')));
}

function applyLocalContextOverrides(app: cdk.App): void {
	const localContextPath = path.join(cdkDir(), 'cdk.context.local.json');
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

const stage = (app.node.tryGetContext('stage') as string | undefined) ?? 'lab';
const profile = process.env.AWS_PROFILE || process.env.AWS_DEFAULT_PROFILE || '';

function awsCliValue(args: string[]): string {
	try {
		return execFileSync('aws', args, {
			encoding: 'utf8',
			stdio: ['ignore', 'pipe', 'ignore'],
			env: {
				...process.env,
				AWS_PAGER: '',
			},
		}).trim();
	} catch {
		return '';
	}
}

const accountFromProfile =
	profile === ''
		? ''
		: awsCliValue(['sts', 'get-caller-identity', '--query', 'Account', '--output', 'text', '--profile', profile]);
const regionFromProfile =
	profile === '' ? '' : awsCliValue(['configure', 'get', 'region', '--profile', profile]);

const account = process.env.CDK_DEFAULT_ACCOUNT || process.env.CDK_DEPLOY_ACCOUNT || process.env.AWS_ACCOUNT_ID || accountFromProfile || undefined;
const region =
	process.env.CDK_DEFAULT_REGION ||
	process.env.CDK_DEPLOY_REGION ||
	process.env.AWS_REGION ||
	process.env.AWS_DEFAULT_REGION ||
	regionFromProfile ||
	undefined;
const env = account || region ? { account, region } : undefined;

new LesserHostStack(app, `lesser-host-${stage}`, { stage, env });
