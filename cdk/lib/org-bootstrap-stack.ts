import * as cdk from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';

export interface OrgBootstrapStackProps extends cdk.StackProps {
	controlPlaneAccountId: string;
	roleName: string;
}

export class OrgBootstrapStack extends cdk.Stack {
	constructor(scope: cdk.App, id: string, props: OrgBootstrapStackProps) {
		super(scope, id, props);

		const controlPlaneAccountId = props.controlPlaneAccountId.trim();
		if (!controlPlaneAccountId) {
			throw new Error('controlPlaneAccountId is required');
		}

		const roleName = props.roleName.trim() || 'lesser-host-org-vending';

		const role = new iam.Role(this, 'OrgVendingRole', {
			roleName,
			assumedBy: new iam.AccountPrincipal(controlPlaneAccountId),
			description: 'Org vending role for lesser-host managed provisioning',
			maxSessionDuration: cdk.Duration.hours(1),
		});

		role.addToPolicy(
			new iam.PolicyStatement({
				actions: [
					'organizations:CreateAccount',
					'organizations:DescribeCreateAccountStatus',
					'organizations:ListAccounts',
					'organizations:ListParents',
					'organizations:MoveAccount',
				],
				resources: ['*'],
			}),
		);

		new cdk.CfnOutput(this, 'OrgVendingRoleArn', {
			value: role.roleArn,
		});
	}
}
