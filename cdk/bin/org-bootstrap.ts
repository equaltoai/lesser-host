#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { OrgBootstrapStack } from '../lib/org-bootstrap-stack';

const app = new cdk.App();

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
