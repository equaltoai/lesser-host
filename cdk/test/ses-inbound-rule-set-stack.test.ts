import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import * as cdk from 'aws-cdk-lib';

import { INBOUND_EMAIL_RULE_SET_NAME } from '../lib/ses-inbound-rule-set-name';
import { SesInboundRuleSetStack } from '../lib/ses-inbound-rule-set-stack';

type SynthesizedTemplate = {
	Outputs?: Record<string, { Value?: unknown }>;
	Resources?: Record<string, { Type?: string; Properties?: Record<string, unknown>; DependsOn?: string[] }>;
};

function synthTemplate(): SynthesizedTemplate {
	const app = new cdk.App();
	const stack = new SesInboundRuleSetStack(app, 'TestSesInboundRuleSetStack');
	const assembly = app.synth();
	const artifact = assembly.getStackArtifact(stack.artifactId);
	return JSON.parse(readFileSync(artifact.templateFullPath, 'utf8')) as SynthesizedTemplate;
}

function findResourceEntries(
	template: SynthesizedTemplate,
	type: string,
): Array<[string, { Type?: string; Properties?: Record<string, unknown>; DependsOn?: string[] }]> {
	return Object.entries(template.Resources ?? {}).filter(([, resource]) => resource?.Type === type);
}

function parseSdkCall(value: unknown): Record<string, unknown> {
	assert.equal(typeof value, 'string', 'expected serialized AwsCustomResource SDK call');
	return JSON.parse(value as string) as Record<string, unknown>;
}

test('shared SES inbound owner stack creates and activates the stable receipt rule set', () => {
	const template = synthTemplate();

	const ruleSets = findResourceEntries(template, 'AWS::SES::ReceiptRuleSet');
	assert.equal(ruleSets.length, 1, 'expected exactly one shared SES receipt rule set');
	const [ruleSetLogicalId, ruleSet] = ruleSets[0]!;
	assert.equal(ruleSet.Properties?.RuleSetName, INBOUND_EMAIL_RULE_SET_NAME);

	const customResources = findResourceEntries(template, 'Custom::AWS');
	const activateRuleSet = customResources.find(([logicalId]) => logicalId.startsWith('ActivateInboundRuleSet'));
	assert.ok(activateRuleSet, 'expected AwsCustomResource that activates the shared rule set');
	const [, activateResource] = activateRuleSet;
	assert.ok(
		activateResource.DependsOn?.includes(ruleSetLogicalId),
		'activation custom resource must depend on the receipt rule set',
	);

	const createCall = parseSdkCall(activateResource.Properties?.Create);
	const updateCall = parseSdkCall(activateResource.Properties?.Update);
	const deleteCall = parseSdkCall(activateResource.Properties?.Delete);
	for (const call of [createCall, updateCall]) {
		assert.equal(call.service, 'SES');
		assert.equal(call.action, 'setActiveReceiptRuleSet');
		assert.deepEqual(call.parameters, { RuleSetName: INBOUND_EMAIL_RULE_SET_NAME });
		assert.deepEqual(call.physicalResourceId, { id: 'active-inbound-rule-set' });
	}
	assert.equal(deleteCall.service, 'SES');
	assert.equal(deleteCall.action, 'setActiveReceiptRuleSet');
	assert.deepEqual(deleteCall.parameters, {});

	const policy = findResourceEntries(template, 'AWS::IAM::Policy').find(([logicalId]) =>
		logicalId.startsWith('ActivateInboundRuleSetCustomResourcePolicy')
	);
	assert.ok(policy, 'expected activation custom resource IAM policy');
	const statements = (
		(policy[1].Properties?.PolicyDocument as { Statement?: Array<Record<string, unknown>> } | undefined)
	)?.Statement ?? [];
	assert.ok(
		statements.some((statement) =>
			statement.Action === 'ses:SetActiveReceiptRuleSet' &&
			statement.Effect === 'Allow' &&
			statement.Resource === '*'
		),
		'activation custom resource must have ses:SetActiveReceiptRuleSet on *',
	);

	assert.equal(template.Outputs?.InboundRuleSetName?.Value, INBOUND_EMAIL_RULE_SET_NAME);
});
