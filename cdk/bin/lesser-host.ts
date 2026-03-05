#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { LesserHostStack } from '../lib/lesser-host-stack';

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

const stage = (app.node.tryGetContext('stage') as string | undefined) ?? 'lab';

new LesserHostStack(app, `lesser-host-${stage}`, { stage });
