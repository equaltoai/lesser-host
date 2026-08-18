import * as path from 'node:path';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';

import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as events from 'aws-cdk-lib/aws-events';
import * as targets from 'aws-cdk-lib/aws-events-targets';
import * as lambda from 'aws-cdk-lib/aws-lambda';

import { synthGoBuildEnv } from './go-build-env';

/**
 * Properties for CostTelemetryWorker.
 *
 * M3.7 scaffold: creates a scheduled Lambda for cost telemetry collection.
 * No business logic yet — EventBridge entrypoint only.
 * M3.8–M3.10 add CloudWatch / Cost Explorer / DynamoDB logic.
 */
export interface CostTelemetryWorkerProps {
	/** Stack name prefix (e.g. "lesser-host-lab"). */
	namePrefix: string;
	/** Repository root directory for Go build. */
	repoRoot: string;
	/** Deployment stage. */
	stage: string;
}

/**
 * CostTelemetryWorker pairs a Lambda function with a scheduled EventBridge rule
 * for periodic cost telemetry collection.
 */
export class CostTelemetryWorker extends Construct {
	public readonly fn: lambda.Function;

	constructor(scope: Construct, id: string, props: CostTelemetryWorkerProps) {
		super(scope, id);

		const buildDir = path.join(props.repoRoot, 'cdk', '.build', id);
		fs.mkdirSync(buildDir, { recursive: true });
		execFileSync('go', ['build', '-o', path.join(buildDir, 'bootstrap'), './cmd/cost-telemetry-worker'], {
			cwd: props.repoRoot,
			stdio: 'inherit',
			env: synthGoBuildEnv(props.repoRoot),
		});

		this.fn = new lambda.Function(this, 'Fn', {
			functionName: `${props.namePrefix}-cost-telemetry-worker`,
			code: lambda.Code.fromAsset(buildDir),
			handler: 'bootstrap',
			runtime: lambda.Runtime.PROVIDED_AL2023,
			memorySize: 256,
			timeout: cdk.Duration.seconds(10),
			environment: {
				STAGE: props.stage,
			},
		});

		const rule = new events.Rule(this, 'CollectRule', {
			ruleName: `${props.namePrefix}-cost-telemetry-collect`,
			schedule: events.Schedule.rate(cdk.Duration.hours(1)),
		});
		rule.addTarget(new targets.LambdaFunction(this.fn));
	}
}
