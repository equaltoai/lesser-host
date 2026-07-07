// P52 H1 MAI-1 split: hosted-genesis MicroVM CDK wiring tests, extracted from
// cdk/test/lesser-host-stack.test.ts so neither file exceeds the gov-infra
// TS/JS 2000-line file budget. Shared synth/lookup helpers live in
// ./_lesser-host-test-helpers. Coverage and assertions are unchanged.

import {
	findLambdaEntryByFunctionName,
	findResourceEntries,
	findResources,
	hostedGenesisMicrovmContext,
	hostedGenesisMicrovmRequiredContext,
	lambdaEnvironment,
	roleServicePrincipals,
	synthesizeTemplate,
	synthTemplateForStage,
	synthTemplateWithContext,
	webLookupContext,
	webStackEnv,
} from './_lesser-host-test-helpers';
import { HOSTED_GENESIS_MICROVM_BASE_IMAGE_ARN } from '../lib/hosted-genesis-microvm';

import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as path from 'node:path';
import test from 'node:test';

test('hosted genesis AppTheory MicroVM deployed stages require NO credential-pair context (CDK-owned token)', () => {
	// P52 corrective #873: the authorizer bearer token is CDK-owned (a custom
	// resource generates it at deploy and writes the raw value to a deterministic
	// SSM SecureString). Synth must SUCCEED without
	// hostedGenesisMicrovmAuthorizerTokenSha256 / hostedGenesisMicrovmAuthTokenSSMParamName
	// context. As of F1 the base-image ARN is also a pinned literal, so no
	// MicroVM context at all is required here.
	const template = synthesizeTemplate('lab', { ...webLookupContext, ...hostedGenesisMicrovmContext('lab') }, 'TestLesserHostStackMissingMicrovmContext', { env: webStackEnv });
	assert.ok(
		findResourceEntries(template, 'AWS::CloudFormation::CustomResource').some(
			([, res]) => {
				const props = res.Properties ?? {};
				return props.AuthTokenSSMParamName === '/lesser-host/lab/hosted-genesis/microvm/auth-token';
			},
		),
		'expected the CDK-owned auth-token custom resource to synthesize without credential-pair context',
	);
	assert.throws(
		() => synthesizeTemplate('lab', { ...webLookupContext, hostedGenesisMicrovmDevTestOptOut: 'true' }, 'TestLesserHostStackMicrovmOptOutLab', { env: webStackEnv }),
		/hostedGenesisMicrovmDevTestOptOut is dev\/test-only and cannot be used for deployed stage lab/,
	);
});

test('hosted genesis AppTheory MicroVM deployed-stage wiring emits the digest as a custom-resource getAtt (not a literal raw token)', () => {
	const template = synthTemplateWithContext(hostedGenesisMicrovmRequiredContext);
	// The authorizer + controller env digest must be a CloudFormation getAtt
	// token referencing the CDK-owned custom resource, NOT a literal raw token
	// and NOT a literal hash. A literal would mean the raw token (or a static
	// hash) leaked into the template; the getAtt form proves the digest resolves
	// at deploy time from the custom resource that generated the token.
	const authorizerFn = findResourceEntries(template, 'AWS::Lambda::Function').find(([, fn]) =>
		fn.Properties?.FunctionName === 'lesser-host-lab-hosted-genesis-microvm-authorizer'
	);
	assert.ok(authorizerFn, 'expected controller authorizer Lambda');
	const authorizerEnv = lambdaEnvironment(authorizerFn[1].Properties ?? {});
	const digestValue = authorizerEnv.HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256;
	assert.ok(
		typeof digestValue === 'object' && JSON.stringify(digestValue).includes('"Fn::GetAtt"'),
		'expected authorizer token digest env to be a CloudFormation getAtt token, not a literal raw token or hash',
	);
	// grep-proof: no literal raw-secret shapes and no literal sha256 of a known
	// test token anywhere in the template. The digest is a token, so it cannot
	// leak the generated token's hash at synth time.
	const templateJson = JSON.stringify(template);
	assert.ok(!templateJson.includes('sk-'), 'no raw OpenAI key shape in synthesized template');
	assert.ok(!templateJson.includes('sk_ant'), 'no raw Anthropic key shape in synthesized template');
	// The construct synthesizes for live as well as lab; the operator still
	// deploys lab before live. Fail-closed auth is preserved by the
	// AppTheoryMicrovmController construct (AUTH_REQUIRED=true, AUTH_DEFAULT=deny,
	// authorizer on every route) and the controller runtime re-check, not by an
	// optional synth-time gate.
	const liveTemplate = synthTemplateForStage('live');
	assert.ok(
		findResourceEntries(liveTemplate, 'AWS::Lambda::Function').some(([, fn]) =>
			String(fn.Properties?.FunctionName ?? '').includes('hosted-genesis-microvm-controller'),
		),
		'expected the AppTheory MicroVM controller Lambda to synthesize for live',
	);
});

test('hosted genesis AppTheory MicroVM deployed-stage wiring uses AppTheory constructs with protected routes', () => {
	const template = synthTemplateWithContext(hostedGenesisMicrovmRequiredContext);
	// P52 corrective #873: egress uses the AWS-managed internetEgress connector,
	// which synthesizes NO AWS::Lambda::NetworkConnector resource (it is a typed
	// reference to the AWS-managed connector, not a caller-VPC connector). The
	// image + controller still receive a valid EGRESS-kind connector ref.
	const connectors = findResourceEntries(template, 'AWS::Lambda::NetworkConnector');
	const images = findResourceEntries(template, 'AWS::Lambda::MicrovmImage');
	assert.equal(connectors.length, 0, 'internetEgress synthesizes no AWS::Lambda::NetworkConnector resource');
	assert.equal(images.length, 1, 'expected AppTheoryMicrovmImage L1 resource');

	// P52 H1 step 2 (F1): the MicroVM image uses the pinned AWS-managed base ARN
	// (HOSTED_GENESIS_MICROVM_BASE_IMAGE_ARN) + host-owned build role. AppTheory
	// passes baseImageArn through verbatim, so it must appear in BaseImageArn
	// exactly. BaseImageArn renders as an Fn::Join (partition is a CFN token), so
	// stringify before the substring check; `:aws:microvm-image:al2023-1` carries
	// the load-bearing `aws` literal account, COLON separator, and al2023-1 runtime.
	// BaseImageVersion "0" is the only available managed version (factory-verified
	// via list-managed-microvm-image-versions, 2026-07-04); "1" failed the lab deploy.
	const imageProps = images[0][1].Properties ?? {};
	assert.equal(imageProps.BaseImageVersion, '0', 'expected deterministic base image version 0 (only available managed version)');
	assert.ok(
		JSON.stringify(imageProps.BaseImageArn ?? '').includes(HOSTED_GENESIS_MICROVM_BASE_IMAGE_ARN),
		`expected BaseImageArn to be the pinned AWS-managed base runtime ARN (${HOSTED_GENESIS_MICROVM_BASE_IMAGE_ARN}), not a placeholder or operator-supplied context value`,
	);
	assert.ok(
		JSON.stringify(imageProps.BaseImageArn ?? '').includes(':aws:microvm-image:al2023-1'),
		'expected managed base ARN to use the `aws` literal account + colon separator + al2023-1 runtime (not a numeric account, not a slash, not `base`)',
	);
	const egressRefs = imageProps.EgressNetworkConnectors as unknown[] | undefined;
	assert.ok(Array.isArray(egressRefs) && egressRefs.length === 1, 'expected one egress connector ref on the image');
	assert.ok(
		JSON.stringify(egressRefs?.[0] ?? '').includes('aws-network-connector:INTERNET_EGRESS'),
		'expected AWS-managed INTERNET_EGRESS egress connector ref',
	);

	// P52 H1 (endpoint-based architecture, 2026-07-05): the MicroVM image is
	// built with a raw cdk.CfnResource (AWS::Lambda::MicrovmImage) carrying an
	// EMPTY Hooks config — NO Port, NO MicrovmImageHooks, NO MicrovmHooks — i.e.
	// NO AWS-invoked build-time hooks. This bypasses the AppTheory v1.15.2
	// AppTheoryMicrovmImage construct's renderHooks guard
	// (microvm-image.js:234: "AppTheoryMicrovmImage requires props.hooks.
	// microvmHooks or props.hooks.microvmImageHooks"), which refuses to render a
	// no-hooks image. The AWS Lambda MicroVM BUILD environment does not route
	// inbound HTTP to the container's :8080 hook port (proven across lab deploys
	// #10/#11/#12 — PR #882's loggingListener saw zero `connection accepted`
	// events from the build service), so an image with /ready ENABLED cannot
	// satisfy the readiness probe and the build hangs → CREATE_FAILED "did not
	// stabilize". A prior attempt set Hooks: { Port: 8080 } with no hook groups;
	// AWS rejects that ("At least one MicroVM hook or MicroVM image hook must be
	// enabled when the hooks port is specified"), so the working shape is an
	// empty Hooks config — no Port, no hooks. With no Port specified, Lambda
	// routes inbound runtime traffic to the default port 8080 (AWS docs: "By
	// default, Lambda routes inbound traffic to port 8080"), so the workload on
	// :8080 stays reachable via the runtime endpoint, matching the
	// getting-started example (no --hooks → CREATED). The workload still serves
	// /ready + /validate + the runtime hooks on :8080 (unchanged); AWS simply
	// does not invoke them at build time. Turn execution is via the controller
	// POSTing to the runtime endpoint (separate brief). The proper fix (relax
	// renderHooks + support endpoint invocation) is routed upstream to
	// AppTheory — this is a principal-approved framework-gap exception.
	const hooks = imageProps.Hooks as {
		MicrovmImageHooks?: { Ready?: string; Validate?: string };
		MicrovmHooks?: { Run?: string; Suspend?: string; Resume?: string; Terminate?: string };
		Port?: number;
	} | undefined;
	assert.ok(hooks, 'expected the MicroVM image to carry a Hooks config');
	assert.equal(hooks?.Port, undefined, 'expected NO Port on the no-hooks image (AWS rejects a Port with no hook groups; default port 8080 applies at runtime)');
	assert.equal(hooks?.MicrovmImageHooks, undefined, 'expected NO MicrovmImageHooks on the no-hooks image (build env cannot reach :8080 for /ready)');
	assert.equal(hooks?.MicrovmHooks, undefined, 'expected NO MicrovmHooks on the no-hooks image (endpoint-based architecture; controller POSTs to the runtime endpoint)');

	// P52 H1: CloudWatch logging must be ENABLED (not disabled) so a failing
	// image build emits diagnosable logs. The 2026-07-04 lab deploy failed
	// undiagnosably because logging was { disabled: true }. The AWS Lambda
	// MicroVM developer guide publishes the build-log location as
	// /aws/lambda/microvms/<image-name> (microvms-images.html "Image states and
	// build states"; getting-started Step 3). The image name is
	// lesser-host-lab_hosted_genesis, so the LogGroup must follow that
	// convention and the framework must render CloudWatch (not Disabled).
	const logging = imageProps.Logging as { CloudWatch?: { LogGroup?: string; LogStream?: string }; Disabled?: boolean } | undefined;
	assert.ok(logging, 'expected the MicroVM image to carry a Logging config');
	assert.ok(
		logging?.CloudWatch && !logging.Disabled,
		'expected MicroVM image Logging to use CloudWatch (not disabled) so build failures are diagnosable',
	);
	assert.equal(
		logging?.CloudWatch?.LogStream,
		'build',
		'expected MicroVM image CloudWatch LogStream "build"',
	);
	assert.ok(
		typeof logging?.CloudWatch?.LogGroup === 'string' &&
			logging.CloudWatch.LogGroup === '/aws/lambda/microvms/lesser-host-lab_hosted_genesis',
		'expected MicroVM image CloudWatch LogGroup to follow the AWS-documented /aws/lambda/microvms/<image-name> convention',
	);

	// P52 H1: the code artifact zip MUST contain a Dockerfile. AWS Lambda MicroVM
	// developer guide (microvms-images.html): "you provide a zip package that
	// contains a Dockerfile and your application artifacts". The 2026-07-04 lab
	// deploy failed with HostedGenesisMicrovmImage CREATE_FAILED "did not
	// stabilize" because the asset zip contained ONLY the binary. Synth runs
	// buildHostedGenesisMicrovmWorkloadAsset which writes the Dockerfile into the
	// asset buildDir (cdk/.build/HostedGenesisMicrovmWorkloadArtifact) alongside
	// the binary, so s3assets.Asset packages binary + Dockerfile. Assert the
	// generated Dockerfile exists, FROM a plain container base (alpine:3.20)
	// matching the AWS getting-started's working plain-base pattern
	// (node:24-alpine), and CMD launches the workload binary.
	const workloadDockerfile = path.join(
		process.cwd(), '.build', 'HostedGenesisMicrovmWorkloadArtifact', 'Dockerfile',
	);
	assert.ok(fs.existsSync(workloadDockerfile), 'expected the hosted-genesis MicroVM workload asset buildDir to contain a generated Dockerfile');
	const dockerfileContent = fs.readFileSync(workloadDockerfile, 'utf8');
	assert.ok(
		dockerfileContent.includes('FROM alpine:3.20'),
		'expected the workload Dockerfile FROM a plain container base (alpine:3.20) matching the AWS getting-started plain-base pattern',
	);
	assert.ok(
		dockerfileContent.includes('CMD ["/app/hosted-genesis-microvm-workload"]'),
		'expected the workload Dockerfile CMD to launch the hosted-genesis-microvm-workload binary',
	);
	assert.ok(
		dockerfileContent.includes('EXPOSE 8080'),
		'expected the workload Dockerfile to EXPOSE the M16 lifecycle-hook port 8080',
	);

	// The build role is host-created (lambda.amazonaws.com trust — the principal
	// AWS Lambda MicroVM docs specify for the image build service; the
	// microvms.lambda.amazonaws.com form is rejected by IAM as invalid), not a
	// hand-supplied ARN. Verify it synthesizes with the correct trust + name.
	const buildRole = findResourceEntries(template, 'AWS::IAM::Role').find(([, role]) =>
		String(role.Properties?.RoleName ?? '').includes('hosted-genesis-microvm-image-build'),
	);
	assert.ok(buildRole, 'expected host-owned MicroVM image build role to synthesize');
	const buildServicePrincipals = roleServicePrincipals(buildRole[1]);
	// Assert EXACT service-principal membership via element `===` (not
	// Array.prototype.includes on a URL-like string, which CodeQL's
	// js/incomplete-url-substring-sanitization flags even on string[]
	// receivers because it cannot prove the receiver is not a string).
	assert.ok(
		buildServicePrincipals.some((p) => p === 'lambda.amazonaws.com') &&
			!buildServicePrincipals.some((p) => p === 'microvms.lambda.amazonaws.com'),
		'expected MicroVM image build role to trust lambda.amazonaws.com (not microvms.lambda.amazonaws.com)',
	);
	// The image's BuildRoleArn must reference the host-created role (Fn::GetAtt
	// or Ref), not a hand-supplied string ARN.
	const buildRoleArn = imageProps.BuildRoleArn;
	assert.ok(
		typeof buildRoleArn === 'object',
		'expected BuildRoleArn to be a CDK reference to the host-created role, not a literal ARN',
	);

	// P52 H1: the build role must carry CloudWatch Logs permissions so the
	// MicroVM image build service can write build logs when logging is enabled.
	// The AWS Lambda MicroVM getting-started IAM example grants the build role
	// logs:CreateLogGroup / logs:CreateLogStream / logs:PutLogEvents on
	// arn:aws:logs:*:*:* (microvms-getting-started.html "Prerequisites"). The
	// 2026-07-04 lab deploy produced no build logs partly because the build role
	// lacked these. The trust principal (lambda.amazonaws.com) is unchanged.
	const buildRoleLogicalId = buildRole[0];
	const buildRolePolicies = findResourceEntries(template, 'AWS::IAM::Policy').filter(([, policy]) => {
		const roles = policy.Properties?.Roles;
		return Array.isArray(roles) && roles.some((role) => role && typeof role === 'object' &&
			'Ref' in role && (role as { Ref?: string }).Ref === buildRoleLogicalId);
	});
	const buildRolePolicyJson = JSON.stringify(buildRolePolicies.map(([, policy]) => policy.Properties ?? {}));
	for (const action of ['logs:CreateLogGroup', 'logs:CreateLogStream', 'logs:PutLogEvents']) {
		assert.ok(buildRolePolicyJson.includes(action), `expected MicroVM image build role to include ${action} for CloudWatch build logs`);
	}
	assert.ok(
		buildRolePolicyJson.includes('arn:aws:logs:*:*:*'),
		'expected MicroVM image build role logs permissions scoped to arn:aws:logs:*:*:* (the AWS-documented build-role logs resource)',
	);

	const authorizerFn = findResourceEntries(template, 'AWS::Lambda::Function').find(([, fn]) =>
		fn.Properties?.FunctionName === 'lesser-host-lab-hosted-genesis-microvm-authorizer'
	);
	assert.ok(authorizerFn, 'expected controller authorizer Lambda');
	const authorizerEnv = lambdaEnvironment(authorizerFn[1].Properties ?? {});
	assert.equal(authorizerEnv.STAGE, 'lab');
	// P52 corrective #873: the digest env is a CloudFormation getAtt token from
	// the CDK-owned custom resource, NOT a literal hash. Both env vars carry the
	// same getAtt token (the custom resource returns one digest shared by the
	// authorizer + controller). Asserting object form proves the raw token (and
	// even its hash) never enters the template at synth time.
	const authorizerDigestEnv = authorizerEnv.HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256;
	assert.ok(
		typeof authorizerDigestEnv === 'object' && JSON.stringify(authorizerDigestEnv).includes('"Fn::GetAtt"'),
		'expected authorizer token digest env to be a CloudFormation getAtt token from the CDK-owned custom resource',
	);
	assert.ok(
		JSON.stringify(authorizerEnv.APPTHEORY_MICROVM_AUTHORIZER_TOKEN_SHA256) === JSON.stringify(authorizerDigestEnv),
		'expected APPTHEORY_MICROVM_AUTHORIZER_TOKEN_SHA256 and HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256 to be the same custom-resource digest token',
	);

	const controllerFn = findResourceEntries(template, 'AWS::Lambda::Function').find(([, fn]) =>
		fn.Properties?.FunctionName === 'lesser-host-lab-hosted-genesis-microvm-controller'
	);
	assert.ok(controllerFn, 'expected AppTheory-created controller Lambda');
	const controllerEnv = lambdaEnvironment(controllerFn[1].Properties ?? {});
	assert.equal(controllerEnv.STAGE, 'lab');
	assert.equal(controllerEnv.APPTHEORY_MICROVM_CONTROLLER_AUTH_REQUIRED, 'true');
	assert.equal(controllerEnv.APPTHEORY_MICROVM_CONTROLLER_AUTH_DEFAULT, 'deny');
	assert.equal(controllerEnv.APPTHEORY_MICROVM_CONTRACT_VERSION, 'm16.microvm/v1');
	assert.equal(
		controllerEnv.APPTHEORY_MICROVM_CONTROLLER_OPERATIONS,
		'run,get,list,suspend,resume,terminate,auth-token,shell-auth-token',
	);
	assert.ok(
		String(controllerEnv.APPTHEORY_MICROVM_CONTROLLER_ROUTES ?? '').includes('POST /microvms/{session_id}/auth-token'),
		'expected AppTheory M16 route manifest in controller env',
	);
	assert.ok(controllerEnv.APPTHEORY_MICROVM_SESSION_REGISTRY_TABLE, 'expected AppTheory registry table env');
	assert.ok(controllerEnv.APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS, 'expected ingress connector refs env');
	assert.ok(
		JSON.stringify(controllerEnv.APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS).includes('aws-network-connector:HTTP_INGRESS'),
		'expected HTTP_INGRESS, not ALL_INGRESS, for endpoint auth-token compatibility with shell ingress',
	);
	assert.ok(
		!JSON.stringify(controllerEnv.APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS).includes('aws-network-connector:ALL_INGRESS'),
		'ALL_INGRESS must not be combined with shell ingress',
	);
	assert.ok(controllerEnv.APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS, 'expected egress connector refs env');
	assert.ok(controllerEnv.APPTHEORY_MICROVM_SHELL_INGRESS_NETWORK_CONNECTOR_REF, 'expected shell ingress connector ref env');
	assert.ok(
		typeof controllerEnv.HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256 === 'object' &&
			JSON.stringify(controllerEnv.HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256).includes('"Fn::GetAtt"'),
		'expected controller token digest env to be a CloudFormation getAtt token from the CDK-owned custom resource',
	);
	assert.ok(controllerEnv.STATE_TABLE_NAME, 'expected Host state table env for HostedGenesisSession reconstruction');
	assert.ok(
		!('HOSTED_GENESIS_MICROVM_ADAPTER_FEEDBACK' in controllerEnv),
		'v1.15 adoption must retire the provisional adapter feedback env',
	);

	const routes = findResources(template, 'AWS::ApiGatewayV2::Route').filter((route) =>
		String(route.RouteKey ?? '').includes('/microvms')
	);
	assert.deepEqual(
		routes.map((route) => route.RouteKey).sort(),
		[
			'DELETE /microvms/{session_id}',
			'GET /microvms',
			'GET /microvms/{session_id}',
			'POST /microvms',
			'POST /microvms/{session_id}/auth-token',
			'POST /microvms/{session_id}/resume',
			'POST /microvms/{session_id}/shell-auth-token',
			'POST /microvms/{session_id}/suspend',
		].sort(),
	);
	for (const route of routes) {
		assert.equal(route.AuthorizationType, 'CUSTOM', `route ${route.RouteKey} must use the fail-closed authorizer`);
		assert.ok(route.AuthorizerId, `route ${route.RouteKey} must attach authorizer id`);
	}

	const sessionTable = findResourceEntries(template, 'AWS::DynamoDB::Table').find(([, table]) =>
		JSON.stringify(table.Properties?.TableName).includes('hosted-genesis-microvm-sessions')
	);
	assert.ok(sessionTable, 'expected AppTheory controller-owned session registry table');
	const ttl = sessionTable[1].Properties?.TimeToLiveSpecification as { AttributeName?: unknown; Enabled?: unknown } | undefined;
	assert.deepEqual(ttl, { AttributeName: 'ttl', Enabled: true });

	const controllerRoleRef = controllerFn[1].Properties?.Role as { 'Fn::GetAtt'?: unknown[] } | undefined;
	assert.ok(controllerRoleRef && Array.isArray(controllerRoleRef['Fn::GetAtt']), 'expected controller Lambda role reference');
	const controllerRoleLogicalId = String(controllerRoleRef['Fn::GetAtt'][0] ?? '');
	assert.ok(controllerRoleLogicalId, 'expected controller Lambda role logical id');
	const controllerPolicies = findResourceEntries(template, 'AWS::IAM::Policy').filter(([, policy]) => {
		const roles = policy.Properties?.Roles;
		return Array.isArray(roles) && roles.some((role) => role && typeof role === 'object' &&
			'Ref' in role && (role as { Ref?: string }).Ref === controllerRoleLogicalId);
	});
	const controllerPolicyJson = JSON.stringify(controllerPolicies.map(([, policy]) => policy.Properties ?? {}));
	for (const action of [
		'lambda:RunMicrovm',
		'lambda:GetMicrovm',
		'lambda:ListMicrovms',
		'lambda:SuspendMicrovm',
		'lambda:ResumeMicrovm',
		'lambda:TerminateMicrovm',
		'lambda:CreateMicrovmAuthToken',
		'lambda:CreateMicrovmShellAuthToken',
		'lambda:PassNetworkConnector',
		'dynamodb:GetItem',
		'dynamodb:Query',
		'dynamodb:PutItem',
		'dynamodb:UpdateItem',
		'dynamodb:DeleteItem',
	]) {
		assert.ok(controllerPolicyJson.includes(action), `expected controller IAM to include ${action}`);
	}
	const wildcardMicrovmActions = [
		'lambda:CreateMicrovmAuthToken',
		'lambda:CreateMicrovmShellAuthToken',
		'lambda:GetMicrovm',
		'lambda:ResumeMicrovm',
		'lambda:RunMicrovm',
		'lambda:SuspendMicrovm',
		'lambda:TerminateMicrovm',
	];
	const hasWildcardMicrovmControlGrant = controllerPolicies.some(([, policy]) => {
		const statements = policy.Properties?.PolicyDocument as { Statement?: unknown[] } | undefined;
		if (!Array.isArray(statements?.Statement)) {
			return false;
		}
		return statements.Statement.some((statement) => {
			const typed = statement as { Action?: unknown; Resource?: unknown };
			const actions = typeof typed.Action === 'string'
				? [typed.Action]
				: Array.isArray(typed.Action) ? typed.Action.filter((action): action is string => typeof action === 'string') : [];
			const resources = typeof typed.Resource === 'string'
				? [typed.Resource]
				: Array.isArray(typed.Resource) ? typed.Resource.filter((resource): resource is string => typeof resource === 'string') : [];
			return resources.some((resource) => resource === '*') &&
				wildcardMicrovmActions.every((action) => actions.some((candidate) => candidate === action));
		});
	});
	assert.ok(
		hasWildcardMicrovmControlGrant,
		'expected controller IAM to include an action-scoped Resource "*" supplement for Lambda MicroVM control actions',
	);

	// P52 H1.5: the controller Lambda must carry provisioned concurrency so the
	// governed HTTP API is warm and the control plane's accept-path dispatch
	// (POST /microvms) meets the <2s budget without a controller cold-start hit.
	// CDK expresses currentVersionOptions.provisionedConcurrentExecutions as an
	// AWS::Lambda::Alias resource carrying a ProvisionedConcurrencyConfig.
	const aliases = findResourceEntries(template, 'AWS::Lambda::Alias');
	const provisioned = aliases.some(([, alias]) => {
		const cfg = (alias.Properties ?? {}) as { ProvisionedConcurrencyConfig?: { ProvisionedConcurrentExecutions?: unknown } };
		return cfg.ProvisionedConcurrencyConfig && Number(cfg.ProvisionedConcurrencyConfig.ProvisionedConcurrentExecutions) > 0;
	});
	assert.ok(provisioned, 'expected the MicroVM controller Lambda to carry provisioned concurrency (currentVersionOptions)');
});

test('P52 H1.5 corrective (AppTheory v1.15.2): host-owned MicroVM execution role propagates to RunMicrovm via the controller', () => {
	const template = synthTemplateWithContext(hostedGenesisMicrovmRequiredContext);
	const roles = findResourceEntries(template, 'AWS::IAM::Role');

	// The host-owned execution role synthesizes with lambda.amazonaws.com trust
	// (the principal AWS Lambda MicroVMs assume for the in-VM workload; the
	// microvms.lambda.amazonaws.com form is rejected by IAM as invalid).
	const executionRole = roles.find(([, role]) =>
		String(role.Properties?.RoleName ?? '').includes('hosted-genesis-microvm-execution'),
	);
	assert.ok(executionRole, 'expected host-owned MicroVM execution role to synthesize');
	const execRoleLogicalId = executionRole[0];
	const execServicePrincipals = roleServicePrincipals(executionRole[1]);
	// Exact service-principal membership via element `===` — see the build-role
	// assertion above for why this is not Array.prototype.includes.
	assert.ok(
		execServicePrincipals.some((p) => p === 'lambda.amazonaws.com') &&
			!execServicePrincipals.some((p) => p === 'microvms.lambda.amazonaws.com'),
		'expected MicroVM execution role to trust lambda.amazonaws.com (not microvms.lambda.amazonaws.com)',
	);

	// The controller Lambda env carries APPTHEORY_MICROVM_EXECUTION_ROLE_ARN,
	// pointing at the host-owned execution role (AppTheory v1.15.2 propagation:
	// controller reads env -> ProviderRunInput -> RunMicrovmInput). The env value
	// must be a CDK reference (Fn::GetAtt on the role), never a literal ARN.
	const controllerFn = findResourceEntries(template, 'AWS::Lambda::Function').find(([, fn]) =>
		fn.Properties?.FunctionName === 'lesser-host-lab-hosted-genesis-microvm-controller',
	);
	assert.ok(controllerFn, 'expected AppTheory-created controller Lambda');
	const controllerEnv = lambdaEnvironment(controllerFn[1].Properties ?? {});
	const execRoleArnEnv = controllerEnv.APPTHEORY_MICROVM_EXECUTION_ROLE_ARN;
	assert.ok(execRoleArnEnv, 'expected APPTHEORY_MICROVM_EXECUTION_ROLE_ARN on the controller Lambda');
	const execRoleArnStr = JSON.stringify(execRoleArnEnv);
	assert.ok(
		execRoleArnStr.includes('"Fn::GetAtt"') && execRoleArnStr.includes(execRoleLogicalId),
		'expected APPTHEORY_MICROVM_EXECUTION_ROLE_ARN to reference the host-owned execution role via Fn::GetAtt',
	);

	// iam:PassRole on the execution role is granted to the controller Lambda's
	// role (AppTheory construct does this in grantMicrovmControlPlane when
	// executionRole is supplied). Find the controller role, then the inline
	// policy carrying PassRole scoped to the execution role ARN.
	const controllerRoleRef = controllerFn[1].Properties?.Role as { 'Fn::GetAtt'?: unknown[] } | undefined;
	assert.ok(controllerRoleRef && Array.isArray(controllerRoleRef['Fn::GetAtt']), 'expected controller Lambda role reference');
	const controllerRoleLogicalId = String(controllerRoleRef['Fn::GetAtt'][0] ?? '');
	const controllerPolicies = findResourceEntries(template, 'AWS::IAM::Policy').filter(([, policy]) => {
		const rolesList = policy.Properties?.Roles;
		return Array.isArray(rolesList) && rolesList.some((role) => role && typeof role === 'object' &&
			'Ref' in role && (role as { Ref?: string }).Ref === controllerRoleLogicalId);
	});
	const controllerPolicyJson = JSON.stringify(controllerPolicies.map(([, policy]) => policy.Properties ?? {}));
	assert.ok(
		controllerPolicyJson.includes('iam:PassRole'),
		'expected controller IAM to grant iam:PassRole for the execution role',
	);
	// PassRole must be scoped to the execution role ARN (Fn::GetAtt), not wildcard.
	assert.ok(
		controllerPolicyJson.includes('"Fn::GetAtt"') && controllerPolicyJson.includes(execRoleLogicalId),
		'expected iam:PassRole resource to scope to the host-owned execution role ARN, not wildcard',
	);
	assert.ok(
		!controllerPolicyJson.includes('"iam:PassRole","Resource":["*"]'),
		'iam:PassRole must not be wildcard; scope to the execution role only',
	);

	// Execution role is least-privilege: DynamoDB R/W on the Host state table,
	// ssm:GetParameter on the two provider-key params only, kms:Decrypt scoped
	// via SSM, and the basic execution managed policy. No PassRole, no wildcard
	// DynamoDB, no wildcard SSM, no IAM mutation, no Secrets Manager.
	const execRolePolicies = findResourceEntries(template, 'AWS::IAM::Policy').filter(([, policy]) => {
		const rolesList = policy.Properties?.Roles;
		return Array.isArray(rolesList) && rolesList.some((role) => role && typeof role === 'object' &&
			'Ref' in role && (role as { Ref?: string }).Ref === execRoleLogicalId);
	});
	const execRolePolicyJson = JSON.stringify(execRolePolicies.map(([, policy]) => policy.Properties ?? {}));
	for (const action of ['dynamodb:GetItem', 'dynamodb:PutItem', 'dynamodb:UpdateItem', 'dynamodb:Query', 'ssm:GetParameter', 'kms:Decrypt']) {
		assert.ok(execRolePolicyJson.includes(action), `expected execution role to include ${action}`);
	}
	assert.ok(
		execRolePolicyJson.includes('/lesser-host/api/openai/service') && execRolePolicyJson.includes('/lesser-host/api/claude'),
		'expected ssm:GetParameter scoped to the two provider-key SecureString params only',
	);
	assert.ok(
		execRolePolicyJson.includes('"kms:ViaService"') && execRolePolicyJson.includes('ssm.'),
		'expected kms:Decrypt constrained via SSM ViaService',
	);
	const forbiddenExecActions = ['iam:PassRole', 'iam:CreateRole', 'iam:*', 'ssm:PutParameter', 'secretsmanager:GetSecretValue', 'dynamodb:*'];
	for (const forbidden of forbiddenExecActions) {
		assert.ok(!execRolePolicyJson.includes(`"${forbidden}"`), `execution role must not grant ${forbidden}`);
	}
	// No wildcard SSM resource and no wildcard DynamoDB resource on the execution role.
	assert.ok(!execRolePolicyJson.includes('"ssm:GetParameter","Resource":["*"]'), 'execution role ssm:GetParameter must not be wildcard');
	assert.ok(!execRolePolicyJson.includes('"Resource":["*"]') || execRolePolicyJson.includes('"kms:ViaService"'), 'wildcard resource only allowed for kms:Decrypt via SSM');

	// STATE_TABLE_NAME reaches the in-VM MicroVM image env (the workload resolves
	// the Host state table via models.MainTableName()). The image's
	// environmentVariables render as Fn::GetAtt/Ref on the table name, so
	// stringify before substring check.
	const images = findResourceEntries(template, 'AWS::Lambda::MicrovmImage');
	assert.equal(images.length, 1, 'expected AppTheoryMicrovmImage L1 resource');
	const imageEnv = (images[0][1].Properties?.EnvironmentVariables ?? []) as Array<{ Key?: string; Value?: unknown }>;
	const stateTableVar = imageEnv.find((env) => env?.Key === 'STATE_TABLE_NAME');
	assert.ok(stateTableVar, 'expected STATE_TABLE_NAME in the MicroVM image environment');
	assert.ok(
		typeof stateTableVar?.Value === 'object',
		'expected STATE_TABLE_NAME image value to be a CDK reference to the state table, not a literal',
	);

	// AWS_REGION is a RESERVED environment variable key for the Lambda Microvms
	// service (AWS::Lambda::MicrovmImage): the service injects it into the guest
	// env, so the caller cannot set it — setting it causes CREATE_FAILED
	// "Environment variable key 'AWS_REGION' is reserved". Assert it is ABSENT from
	// the image environmentVariables; the workload reads the service-provided
	// AWS_REGION at runtime.
	const regionVar = imageEnv.find((env) => env?.Key === 'AWS_REGION');
	assert.equal(regionVar, undefined, 'AWS_REGION must NOT be set in MicroVM image environmentVariables (reserved by the Lambda Microvms service)');

	// No raw secret values anywhere in the synthesized template. P52 corrective
	// #873: the auth bearer token is CDK-owned (custom resource), so neither the
	// raw token nor its sha256 hash is present at synth (the digest is an
	// Fn::GetAtt token resolving at deploy). The auth-token SSM param NAME is
	// present, the value is not; raw provider keys never enter CDK.
	const templateJson = JSON.stringify(template);
	assert.ok(!templateJson.includes('sk-'), 'no raw OpenAI key shape in synthesized template');
	assert.ok(!templateJson.includes('sk_ant'), 'no raw Anthropic key shape in synthesized template');
});

test('P52 #873: CDK-owned auth-token custom resource generates the bearer token + writes only the digest + param name to CloudFormation', () => {
	const template = synthTemplateWithContext(hostedGenesisMicrovmRequiredContext);

	// The custom resource synthesizes with the deterministic param name as its
	// only non-secret property. No credential-pair context was supplied.
	const customResources = findResourceEntries(template, 'AWS::CloudFormation::CustomResource');
	const authTokenResource = customResources.find(([, res]) => {
		const props = res.Properties ?? {};
		return props.AuthTokenSSMParamName === '/lesser-host/lab/hosted-genesis/microvm/auth-token';
	});
	assert.ok(authTokenResource, 'expected the CDK-owned auth-token custom resource to synthesize with the deterministic param name');

	// The provisioner Lambda (inline Node 20 handler) synthesizes with the
	// param name in its env and the deterministic function name.
	const provisionerFn = findResourceEntries(template, 'AWS::Lambda::Function').find(([, fn]) =>
		fn.Properties?.FunctionName === 'lab-hosted-genesis-microvm-auth-token-provisioner'
	);
	assert.ok(provisionerFn, 'expected the auth-token provisioner Lambda to synthesize');
	const provisionerEnv = lambdaEnvironment(provisionerFn[1].Properties ?? {});
	assert.equal(
		provisionerEnv.AUTH_TOKEN_SSM_PARAM_NAME,
		'/lesser-host/lab/hosted-genesis/microvm/auth-token',
		'expected the provisioner Lambda to carry the deterministic auth-token SSM param name',
	);
	// The inline handler must never contain the raw token. It contains the
	// generation logic (crypto.randomBytes) but no literal secret.
	const provisionerCodeJson = JSON.stringify(provisionerFn[1].Properties?.Code ?? {});
	assert.ok(provisionerCodeJson.includes('randomBytes'), 'expected the inline handler to generate the token with crypto.randomBytes');
	assert.ok(!provisionerCodeJson.includes('sk-'), 'no raw OpenAI key shape in the provisioner handler');
	assert.ok(!provisionerCodeJson.includes('sk_ant'), 'no raw Anthropic key shape in the provisioner handler');

	// The provisioner role must carry least-privilege SSM grants on the
	// deterministic auth-token param ONLY (PutParameter + GetParameter +
	// DeleteParameter) and kms:Decrypt scoped via SSM. No wildcard SSM, no
	// other params, no iam:PassRole, no MicroVM IAM.
	const provisionerRoleRef = provisionerFn[1].Properties?.Role as { 'Fn::GetAtt'?: unknown[] } | undefined;
	assert.ok(provisionerRoleRef && Array.isArray(provisionerRoleRef['Fn::GetAtt']), 'expected provisioner Lambda role reference');
	const provisionerRoleLogicalId = String(provisionerRoleRef['Fn::GetAtt'][0] ?? '');
	const provisionerPolicies = findResourceEntries(template, 'AWS::IAM::Policy').filter(([, policy]) => {
		const roles = policy.Properties?.Roles;
		return Array.isArray(roles) && roles.some((role) => role && typeof role === 'object' &&
			'Ref' in role && (role as { Ref?: string }).Ref === provisionerRoleLogicalId);
	});
	const provisionerPolicyJson = JSON.stringify(provisionerPolicies.map(([, policy]) => policy.Properties ?? {}));
	for (const action of ['ssm:PutParameter', 'ssm:GetParameter', 'ssm:DeleteParameter', 'kms:Decrypt']) {
		assert.ok(provisionerPolicyJson.includes(action), `expected provisioner IAM to include ${action} for the auth-token secret`);
	}
	assert.ok(
		provisionerPolicyJson.includes('/lesser-host/lab/hosted-genesis/microvm/auth-token'),
		'expected provisioner ssm grants scoped to the deterministic auth-token SSM parameter',
	);
	assert.ok(
		provisionerPolicyJson.includes('"kms:ViaService"') && provisionerPolicyJson.includes('ssm.'),
		'expected provisioner kms:Decrypt constrained via SSM ViaService',
	);
	assert.ok(
		!provisionerPolicyJson.includes('"ssm:PutParameter","Resource":["*"]'),
		'provisioner ssm:PutParameter must not be wildcard; scope to the auth-token param only',
	);
	for (const forbidden of ['iam:PassRole', 'lambda:RunMicrovm', 'ssm:PutParameter","Resource":["*"]']) {
		assert.ok(!provisionerPolicyJson.includes(forbidden), `provisioner Lambda must NOT carry ${forbidden}`);
	}

	// The control plane still gets the deterministic param name + SSM read grant
	// (it loads the raw token at runtime). This grant path is unchanged from the
	// HTTP-transport rework; the param name is now deterministic (CDK-owned)
	// rather than context-supplied, but the grant shape is identical.
	const controlPlaneFn = findLambdaEntryByFunctionName(template, 'control-plane-api');
	assert.ok(controlPlaneFn, 'expected control-plane Lambda to synthesize');
	const controlPlaneEnv = lambdaEnvironment(controlPlaneFn[1].Properties ?? {});
	assert.equal(
		controlPlaneEnv.HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SSM_PARAM,
		'/lesser-host/lab/hosted-genesis/microvm/auth-token',
		'expected the deterministic auth-token SSM param name on the control-plane Lambda',
	);

	// grep-proof: no raw token, no literal sha256 of a known test token, no
	// CloudFormation Output that emits the digest or raw token. The digest is a
	// getAtt token that resolves at deploy time; it cannot leak at synth time.
	const fullTemplateJson = JSON.stringify(template);
	assert.ok(!fullTemplateJson.includes('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'), 'no literal test sha256 digest in the synthesized template');
	assert.ok(!fullTemplateJson.includes('Bearer '), 'no raw bearer token shape in the synthesized template');
	for (const outputName of Object.keys(template.Outputs ?? {})) {
		assert.ok(
			!/AuthToken|MicrovmAuthToken/i.test(outputName),
			`expected no CloudFormation output emitting the auth token or its digest (found ${outputName})`,
		);
	}
});

test('P52 H1.5: control-plane Lambda receives HTTP dispatch env + SSM auth-token grant (no MicroVM IAM, no registry writes)', () => {
	const template = synthTemplateWithContext(hostedGenesisMicrovmRequiredContext);
	const controlPlaneFn = findLambdaEntryByFunctionName(template, 'control-plane-api');
	assert.ok(controlPlaneFn, 'expected control-plane Lambda to synthesize');
	const controlPlaneEnv = lambdaEnvironment(controlPlaneFn[1].Properties ?? {});
	// controlplane.NewServer reads these env vars to construct the
	// HTTPControllerDispatcher (HostedGenesisMicroVM config block). The
	// controller endpoint + auth-token SSM param name + image/egress refs are
	// the HTTP-transport inputs; the raw auth token is fetched from SSM at
	// runtime, never committed.
	assert.equal(controlPlaneEnv.HOSTED_GENESIS_MICROVM_ENABLED, 'true', 'expected HOSTED_GENESIS_MICROVM_ENABLED on control-plane Lambda');
	assert.ok(controlPlaneEnv.APPTHEORY_MICROVM_CONTROLLER_ENDPOINT, 'expected APPTHEORY_MICROVM_CONTROLLER_ENDPOINT on control-plane Lambda');
	assert.equal(
		controlPlaneEnv.HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SSM_PARAM,
		'/lesser-host/lab/hosted-genesis/microvm/auth-token',
		'expected auth-token SSM param name env on control-plane Lambda',
	);
	assert.ok(controlPlaneEnv.APPTHEORY_MICROVM_IMAGE_REF, 'expected APPTHEORY_MICROVM_IMAGE_REF on control-plane Lambda');
	assert.ok(controlPlaneEnv.APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS, 'expected ingress connector refs on control-plane Lambda');
	assert.ok(
		JSON.stringify(controlPlaneEnv.APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS).includes('aws-network-connector:HTTP_INGRESS'),
		'expected control-plane dispatch to use HTTP_INGRESS for endpoint auth-token compatibility',
	);
	assert.ok(
		!JSON.stringify(controlPlaneEnv.APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS).includes('aws-network-connector:ALL_INGRESS'),
		'control-plane dispatch must not use ALL_INGRESS with shell ingress enabled',
	);
	assert.ok(controlPlaneEnv.APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS, 'expected egress connector refs on control-plane Lambda');
	// The control plane must NOT receive session-registry table env: it does
	// not touch the registry directly (the controller Lambda owns it).
	assert.ok(
		!('APPTHEORY_MICROVM_SESSION_REGISTRY_TABLE' in controlPlaneEnv),
		'control-plane Lambda must not carry the session registry table env (HTTP transport, controller owns the registry)',
	);

	// The control-plane Lambda role must carry ssm:GetParameter on the
	// auth-token SecureString + kms:Decrypt for it — and must NOT carry any
	// MicroVM control-plane IAM (RunMicrovm/GetMicrovm/...) or session-registry
	// DynamoDB writes. The controller Lambda is the single governed surface.
	const controlPlaneRoleRef = controlPlaneFn[1].Properties?.Role as { 'Fn::GetAtt'?: unknown[] } | undefined;
	assert.ok(controlPlaneRoleRef && Array.isArray(controlPlaneRoleRef['Fn::GetAtt']), 'expected control-plane Lambda role reference');
	const controlPlaneRoleLogicalId = String(controlPlaneRoleRef['Fn::GetAtt'][0] ?? '');
	assert.ok(controlPlaneRoleLogicalId, 'expected control-plane Lambda role logical id');
	const controlPlanePolicies = findResourceEntries(template, 'AWS::IAM::Policy').filter(([, policy]) => {
		const roles = policy.Properties?.Roles;
		return Array.isArray(roles) && roles.some((role) => role && typeof role === 'object' &&
			'Ref' in role && (role as { Ref?: string }).Ref === controlPlaneRoleLogicalId);
	});
	const controlPlanePolicyJson = JSON.stringify(controlPlanePolicies.map(([, policy]) => policy.Properties ?? {}));
	assert.ok(controlPlanePolicyJson.includes('ssm:GetParameter'), 'expected control-plane IAM to include ssm:GetParameter for the auth-token secret');
	assert.ok(controlPlanePolicyJson.includes('kms:Decrypt'), 'expected control-plane IAM to include kms:Decrypt for the auth-token SecureString');
	assert.ok(
		controlPlanePolicyJson.includes('/lesser-host/lab/hosted-genesis/microvm/auth-token'),
		'expected control-plane ssm:GetParameter to be scoped to the auth-token SSM parameter',
	);
	// grep-proof: the control plane must NOT carry MicroVM control-plane IAM.
	for (const forbidden of [
		'lambda:RunMicrovm',
		'lambda:GetMicrovm',
		'lambda:ListMicrovms',
		'lambda:SuspendMicrovm',
		'lambda:ResumeMicrovm',
		'lambda:TerminateMicrovm',
		'lambda:CreateMicrovmAuthToken',
		'lambda:CreateMicrovmShellAuthToken',
		'lambda:PassNetworkConnector',
	]) {
		assert.ok(
			!controlPlanePolicyJson.includes(forbidden),
			`control-plane Lambda must NOT carry ${forbidden} (HTTP transport — controller Lambda is the single governed surface)`,
		);
	}
	// The control plane must not hold session-registry DynamoDB grants scoped to
	// the controller-owned sessions table (it does not touch the registry).
	assert.ok(
		!controlPlanePolicyJson.includes('hosted-genesis-microvm-sessions'),
		'control-plane Lambda must not hold session-registry DynamoDB grants (controller owns the registry)',
	);
});
