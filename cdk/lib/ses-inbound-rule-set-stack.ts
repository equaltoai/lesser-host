import * as cdk from 'aws-cdk-lib';
import * as cr from 'aws-cdk-lib/custom-resources';
import * as ses from 'aws-cdk-lib/aws-ses';

import { INBOUND_EMAIL_RULE_SET_NAME } from './ses-inbound-rule-set-name';

export class SesInboundRuleSetStack extends cdk.Stack {
	constructor(scope: cdk.App, id: string, props?: cdk.StackProps) {
		super(scope, id, props);

		const inboundEmailRuleSet = new ses.ReceiptRuleSet(this, 'InboundEmailRuleSet', {
			receiptRuleSetName: INBOUND_EMAIL_RULE_SET_NAME,
		});

		const setActiveInboundRuleSetCall: cr.AwsSdkCall = {
			service: 'SES',
			action: 'setActiveReceiptRuleSet',
			parameters: { RuleSetName: INBOUND_EMAIL_RULE_SET_NAME },
			physicalResourceId: cr.PhysicalResourceId.of('active-inbound-rule-set'),
		};

		const activateRuleSet = new cr.AwsCustomResource(this, 'ActivateInboundRuleSet', {
			onCreate: setActiveInboundRuleSetCall,
			onUpdate: setActiveInboundRuleSetCall,
			onDelete: {
				service: 'SES',
				action: 'setActiveReceiptRuleSet',
				parameters: {},
			},
			installLatestAwsSdk: false,
			policy: cr.AwsCustomResourcePolicy.fromSdkCalls({
				resources: cr.AwsCustomResourcePolicy.ANY_RESOURCE,
			}),
		});
		activateRuleSet.node.addDependency(inboundEmailRuleSet);

		new cdk.CfnOutput(this, 'InboundRuleSetName', {
			value: INBOUND_EMAIL_RULE_SET_NAME,
		});
	}
}
