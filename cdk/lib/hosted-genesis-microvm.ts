import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";

import {
  AppTheoryMicrovmController,
  AppTheoryMicrovmHookMode,
  AppTheoryMicrovmImage,
  AppTheoryMicrovmNetworkConnector,
  AppTheoryMicrovmNetworkProtocol,
} from "@theory-cloud/apptheory-cdk";
import * as cdk from "aws-cdk-lib";
import * as dynamodb from "aws-cdk-lib/aws-dynamodb";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import * as iam from "aws-cdk-lib/aws-iam";
import * as lambda from "aws-cdk-lib/aws-lambda";
import * as s3assets from "aws-cdk-lib/aws-s3-assets";
import type { Construct } from "constructs";

export const HOSTED_GENESIS_MICROVM_NAMESPACE = "hosted-genesis" as const;
export const HOSTED_GENESIS_MICROVM_SOURCE_OF_TRUTH =
  "host-dynamodb-hosted-genesis-session" as const;

const contextKeys = {
  enabled: "hostedGenesisMicrovmLabEnabled",
  vpcId: "hostedGenesisMicrovmVpcId",
  privateSubnetId: "hostedGenesisMicrovmPrivateSubnetId",
  privateSubnetAvailabilityZone:
    "hostedGenesisMicrovmPrivateSubnetAvailabilityZone",
  securityGroupId: "hostedGenesisMicrovmSecurityGroupId",
  baseImageArn: "hostedGenesisMicrovmBaseImageArn",
  baseImageVersion: "hostedGenesisMicrovmBaseImageVersion",
  buildRoleArn: "hostedGenesisMicrovmBuildRoleArn",
  authorizerTokenSha256: "hostedGenesisMicrovmAuthorizerTokenSha256",
} as const;

export interface HostedGenesisMicrovmLabProps {
  readonly stage: string;
  readonly namePrefix: string;
  readonly repoRoot: string;
  readonly removalPolicy: cdk.RemovalPolicy;
  readonly stateTable: dynamodb.ITable;
  // controlPlaneFunction is the control-plane API Lambda that, under P52 H1.5,
  // dispatches MicroVM runs in-process via a ControllerRuntimeDispatcher wired
  // in controlplane.NewServer. When provided, the construct grants it the
  // MicroVM control-plane IAM actions, PassNetworkConnector, read/write on the
  // session registry table, and the image/network-connector/session-table env
  // vars so NewServer can build the real dispatcher. Fail-closed auth is
  // preserved: the controller routes stay authorizer-required + deny-by-default
  // regardless of this grant.
  readonly controlPlaneFunction?: lambda.Function;
}

export interface HostedGenesisMicrovmLabResult {
  readonly enabled: boolean;
  readonly controller?: AppTheoryMicrovmController;
}

export function configureHostedGenesisMicrovmLab(
  scope: Construct,
  props: HostedGenesisMicrovmLabProps,
): HostedGenesisMicrovmLabResult {
  if (!contextBoolean(scope, contextKeys.enabled)) {
    return { enabled: false };
  }
  // P52 H1.5: the stage === "lab" throw is removed so deployed stages (lab AND
  // live) get the MicroVM construct. Fail-closed auth is preserved: the
  // AppTheoryMicrovmController construct always sets AUTH_REQUIRED=true and
  // AUTH_DEFAULT=deny, the authorizer is attached to every route, and the
  // controller runtime re-checks both env vars at startup (refusing to serve if
  // either is loosened). The principal chooses the stage at deploy time (lab
  // first), not at synth time.

  const cfg = readRequiredContext(scope);
  const vpc = ec2.Vpc.fromVpcAttributes(scope, "HostedGenesisMicrovmVpc", {
    vpcId: cfg.vpcId,
    availabilityZones: [cfg.privateSubnetAvailabilityZone],
    privateSubnetIds: [cfg.privateSubnetId],
  });
  const privateSubnet = ec2.Subnet.fromSubnetAttributes(
    scope,
    "HostedGenesisMicrovmPrivateSubnet",
    {
      subnetId: cfg.privateSubnetId,
      availabilityZone: cfg.privateSubnetAvailabilityZone,
    },
  );
  const securityGroup = ec2.SecurityGroup.fromSecurityGroupId(
    scope,
    "HostedGenesisMicrovmSecurityGroup",
    cfg.securityGroupId,
    {
      mutable: false,
    },
  );

  const egressConnector = new AppTheoryMicrovmNetworkConnector(
    scope,
    "HostedGenesisMicrovmNetworkConnector",
    {
      vpc,
      subnets: [privateSubnet],
      securityGroups: [securityGroup],
      connectorName:
        `${props.namePrefix}_hosted_genesis_microvm_egress`.replace(/-/g, "_"),
      networkProtocol: AppTheoryMicrovmNetworkProtocol.IPV4,
      tags: {
        Service: "lesser-host",
        Stage: props.stage,
        Boundary: HOSTED_GENESIS_MICROVM_NAMESPACE,
      },
    },
  );
  const ingressConnector = AppTheoryMicrovmNetworkConnector.allIngress(
    scope,
    "HostedGenesisMicrovmIngressConnector",
  );
  const shellIngressConnector = AppTheoryMicrovmNetworkConnector.shellIngress(
    scope,
    "HostedGenesisMicrovmShellIngressConnector",
  );

  // The MicroVM image consumes a repo-built artifact (the in-VM hosted-genesis
  // workload at cmd/hosted-genesis-microvm-workload), not an external
  // codeArtifactUri CDK context value (kills G4). The workload binary is built
  // at synth time and uploaded as a CDK S3 asset; the image's codeArtifact.uri
  // points at that asset's S3 object URL.
  const workloadArtifact = buildHostedGenesisMicrovmWorkloadAsset(
    scope,
    "HostedGenesisMicrovmWorkloadArtifact",
    props.repoRoot,
  );

  const microvmImage = new AppTheoryMicrovmImage(
    scope,
    "HostedGenesisMicrovmImage",
    {
      name: `${props.namePrefix}_hosted_genesis`,
      description:
        "Lab-only AppTheory MicroVM image for hosted-genesis dogfood",
      baseImageArn: cfg.baseImageArn,
      baseImageVersion: cfg.baseImageVersion,
      buildRoleArn: cfg.buildRoleArn,
      codeArtifact: { uri: workloadArtifact.s3ObjectUrl },
      egressNetworkConnectors: [egressConnector],
      hooks: {
        port: 8080,
        microvmImageHooks: {
          ready: AppTheoryMicrovmHookMode.ENABLED,
          readyTimeoutInSeconds: 120,
          validate: AppTheoryMicrovmHookMode.ENABLED,
          validateTimeoutInSeconds: 300,
        },
        microvmHooks: {
          run: AppTheoryMicrovmHookMode.ENABLED,
          runTimeoutInSeconds: 30,
          suspend: AppTheoryMicrovmHookMode.ENABLED,
          suspendTimeoutInSeconds: 30,
          resume: AppTheoryMicrovmHookMode.ENABLED,
          resumeTimeoutInSeconds: 30,
          terminate: AppTheoryMicrovmHookMode.ENABLED,
          terminateTimeoutInSeconds: 30,
        },
      },
      logging: { disabled: true },
      resources: [{ minimumMemoryInMiB: 2048 }],
      environmentVariables: [
        {
          key: "HOSTED_GENESIS_MICROVM_NAMESPACE",
          value: HOSTED_GENESIS_MICROVM_NAMESPACE,
        },
        {
          key: "HOSTED_GENESIS_MICROVM_SOURCE_OF_TRUTH",
          value: HOSTED_GENESIS_MICROVM_SOURCE_OF_TRUTH,
        },
      ],
      tags: {
        Service: "lesser-host",
        Stage: props.stage,
        Purpose: "hosted-genesis-microvm-dogfood",
      },
    },
  );

  const authorizer = new lambda.Function(
    scope,
    "HostedGenesisMicrovmAuthorizer",
    {
      functionName: `${props.namePrefix}-hosted-genesis-microvm-authorizer`,
      description:
        "Lab-only fail-closed AppTheory MicroVM controller authorizer",
      code: buildGoBootstrapAsset(
        props.repoRoot,
        "HostedGenesisMicrovmAuthorizer",
        "./cmd/hosted-genesis-microvm-authorizer",
      ),
      handler: "bootstrap",
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.X86_64,
      memorySize: 128,
      timeout: cdk.Duration.seconds(5),
      environment: {
        STAGE: props.stage,
        HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256: cfg.authorizerTokenSha256,
      },
    },
  );

  const controller = new AppTheoryMicrovmController(
    scope,
    "HostedGenesisMicrovmController",
    {
      apiName: `${props.namePrefix}-hosted-genesis-microvm`,
      controller: {
        functionName: `${props.namePrefix}-hosted-genesis-microvm-controller`,
        runtime: lambda.Runtime.PROVIDED_AL2023,
        handler: "bootstrap",
        code: buildGoBootstrapAsset(
          props.repoRoot,
          "HostedGenesisMicrovmController",
          "./cmd/hosted-genesis-microvm-controller",
        ),
        description:
          "Lab-only hosted-genesis AppTheory MicroVM controller using runtime/microvm primitives",
        architecture: lambda.Architecture.X86_64,
        memorySize: 512,
        timeout: cdk.Duration.seconds(30),
        environment: {
          STAGE: props.stage,
          HOSTED_GENESIS_MICROVM_NAMESPACE: HOSTED_GENESIS_MICROVM_NAMESPACE,
          HOSTED_GENESIS_MICROVM_SOURCE_OF_TRUTH:
            HOSTED_GENESIS_MICROVM_SOURCE_OF_TRUTH,
          HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256:
            cfg.authorizerTokenSha256,
          STATE_TABLE_NAME: props.stateTable.tableName,
        },
      },
      authorizer,
      authorizerName: `${props.namePrefix}-hosted-genesis-microvm-authorizer`,
      authorizerHeaderName: "Authorization",
      authorizerCacheTtl: cdk.Duration.seconds(0),
      microvmImage,
      ingressNetworkConnectors: [ingressConnector],
      egressNetworkConnectors: [egressConnector],
      shellIngressNetworkConnector: shellIngressConnector,
      sessionTableName: `${props.namePrefix}-hosted-genesis-microvm-sessions`,
      sessionTableRemovalPolicy: props.removalPolicy,
      sessionTableDeletionProtection: false,
      enableSessionTablePointInTimeRecovery: true,
      stage: {
        stageName: props.stage,
        accessLogging: true,
        throttlingRateLimit: 10,
        throttlingBurstLimit: 20,
      },
    },
  );
  props.stateTable.grantReadData(controller.controllerFunction);

  // P52 H1.5: grant the control-plane Lambda the MicroVM control-plane IAM
  // actions, PassNetworkConnector, read/write on the session registry table,
  // and the image/network-connector/session-table env vars so
  // controlplane.NewServer can construct the in-process
  // ControllerRuntimeDispatcher. This mirrors what the AppTheoryMicrovmController
  // construct grants its own controllerFunction, applied to the control-plane
  // Lambda so dispatch is in-process (no HTTP hop, meets the <2s accept
  // budget). Fail-closed auth on the controller routes is unaffected.
  if (props.controlPlaneFunction) {
    grantControlPlaneMicroVMDispatch(
      scope,
      props.controlPlaneFunction,
      controller.sessionTable,
      microvmImage.microvmImageArn,
      [ingressConnector.networkConnectorArn],
      [egressConnector.networkConnectorArn],
    );
  }

  new cdk.CfnOutput(scope, "HostedGenesisMicrovmControllerEndpoint", {
    value: controller.endpoint,
    description:
      "AppTheory MicroVM controller endpoint (operator canary only, never browser/public Host response).",
  });
  new cdk.CfnOutput(scope, "HostedGenesisMicrovmSessionRegistryTable", {
    value: controller.sessionTable.tableName,
    description:
      "AppTheory MicroVM session registry cache; Host HostedGenesisSession remains business truth.",
  });

  return { enabled: true, controller };
}

// grantControlPlaneMicroVMDispatch grants the control-plane Lambda the
// constrained MicroVM control-plane IAM actions + PassNetworkConnector + session
// registry read/write, and injects the env vars controlplane.NewServer reads to
// build the in-process ControllerRuntimeDispatcher. The IAM shape mirrors the
// AppTheoryMicrovmController construct's grantMicrovmControlPlane so the
// control-plane Lambda can RunMicrovm/GetMicrovm/ListMicrovms/Suspend/Resume/
// Terminate + create auth tokens against the constrained MicroVM instance
// resource, without ever receiving a raw AWS SDK client.
function grantControlPlaneMicroVMDispatch(
  scope: Construct,
  fn: lambda.Function,
  sessionTable: dynamodb.ITable,
  imageArn: string,
  ingressConnectorArns: string[],
  egressConnectorArns: string[],
): void {
  const microvmInstanceArn = cdk.Stack.of(scope).formatArn({
    service: "lambda",
    resource: "microvm",
    resourceName: "*",
    arnFormat: cdk.ArnFormat.COLON_RESOURCE_NAME,
  });

  fn.addToRolePolicy(
    new iam.PolicyStatement({
      sid: "HostedGenesisMicrovmControlPlane",
      actions: [
        "lambda:CreateMicrovmAuthToken",
        "lambda:CreateMicrovmShellAuthToken",
        "lambda:GetMicrovm",
        "lambda:ResumeMicrovm",
        "lambda:RunMicrovm",
        "lambda:SuspendMicrovm",
        "lambda:TerminateMicrovm",
      ],
      resources: [microvmInstanceArn],
    }),
  );

  fn.addToRolePolicy(
    new iam.PolicyStatement({
      sid: "HostedGenesisMicrovmList",
      actions: ["lambda:ListMicrovms"],
      resources: ["*"],
    }),
  );

  // PassNetworkConnector is permission-only (no resource-level support, per
  // AppTheory). The permitted connector set is constrained through the typed
  // props + fail-closed env wiring, not raw request strings.
  fn.addToRolePolicy(
    new iam.PolicyStatement({
      sid: "HostedGenesisMicrovmPassNetworkConnectors",
      actions: ["lambda:PassNetworkConnector"],
      resources: ["*"],
    }),
  );

  sessionTable.grantReadWriteData(fn);

  // Inject the env vars controlplane.NewServer's dispatcher constructor reads.
  // These mirror the AppTheoryMicrovmController construct's controller env
  // wiring so the in-process runtime binds the same image + connectors +
  // session registry table. addEnvironment is the CDK-supported way to append
  // env vars to a Function constructed elsewhere.
  fn.addEnvironment("APPTHEORY_MICROVM_IMAGE_REF", imageArn);
  fn.addEnvironment(
    "APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS",
    ingressConnectorArns.join(","),
  );
  fn.addEnvironment(
    "APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS",
    egressConnectorArns.join(","),
  );
  fn.addEnvironment(
    "APPTHEORY_MICROVM_NETWORK_CONNECTOR_REFS",
    egressConnectorArns.join(","),
  );
  fn.addEnvironment(
    "APPTHEORY_MICROVM_SESSION_REGISTRY_TABLE",
    sessionTable.tableName,
  );
  fn.addEnvironment("HOSTED_GENESIS_MICROVM_ENABLED", "true");
}

function readRequiredContext(
  scope: Construct,
): Record<keyof Omit<typeof contextKeys, "enabled">, string> {
  return {
    vpcId: requiredContext(scope, contextKeys.vpcId),
    privateSubnetId: requiredContext(scope, contextKeys.privateSubnetId),
    privateSubnetAvailabilityZone: requiredContext(
      scope,
      contextKeys.privateSubnetAvailabilityZone,
    ),
    securityGroupId: requiredContext(scope, contextKeys.securityGroupId),
    baseImageArn: requiredContext(scope, contextKeys.baseImageArn),
    baseImageVersion: requiredContext(scope, contextKeys.baseImageVersion),
    buildRoleArn: requiredContext(scope, contextKeys.buildRoleArn),
    authorizerTokenSha256: normalizeTokenHash(
      requiredContext(scope, contextKeys.authorizerTokenSha256),
    ),
  };
}

function contextBoolean(scope: Construct, key: string): boolean {
  const raw = scope.node.tryGetContext(key);
  if (typeof raw === "boolean") return raw;
  if (typeof raw !== "string") return false;
  return raw.trim().toLowerCase() === "true";
}

function requiredContext(scope: Construct, key: string): string {
  const raw = scope.node.tryGetContext(key);
  const value = typeof raw === "string" ? raw.trim() : "";
  if (value === "") {
    throw new Error(`hostedGenesisMicrovmLabEnabled requires ${key}`);
  }
  return value;
}

function normalizeTokenHash(value: string): string {
  const normalized = value
    .trim()
    .toLowerCase()
    .replace(/^sha256:/, "");
  if (!/^[a-f0-9]{64}$/.test(normalized)) {
    throw new Error(
      "hostedGenesisMicrovmAuthorizerTokenSha256 must be a sha256 digest, not a raw token",
    );
  }
  return normalized;
}

function buildGoBootstrapAsset(
  repoRoot: string,
  id: string,
  entry: string,
): lambda.Code {
  const buildDir = path.join(repoRoot, "cdk", ".build", id);
  fs.mkdirSync(buildDir, { recursive: true });
  execFileSync("go", ["build", "-o", path.join(buildDir, "bootstrap"), entry], {
    cwd: repoRoot,
    stdio: "inherit",
    env: {
      ...process.env,
      CGO_ENABLED: "0",
      GOOS: "linux",
      GOARCH: "amd64",
    },
  });
  return lambda.Code.fromAsset(buildDir);
}

// buildHostedGenesisMicrovmWorkloadAsset builds the in-VM hosted-genesis
// workload entrypoint (cmd/hosted-genesis-microvm-workload) for the MicroVM
// image's linux/arm64 runtime and packages it as a CDK S3 asset. The returned
// asset's s3ObjectUrl is the AppTheoryMicrovmImage codeArtifact.uri — a
// repo-built artifact, not an external CDK context value (kills G4).
//
// The workload is a long-running HTTP server inside the MicroVM image (not a
// Lambda handler), so it is packaged as a tarball the image build extracts. The
// binary is built with CGO disabled for a static image payload.
function buildHostedGenesisMicrovmWorkloadAsset(
  scope: Construct,
  id: string,
  repoRoot: string,
): s3assets.Asset {
  const buildDir = path.join(repoRoot, "cdk", ".build", id);
  fs.mkdirSync(buildDir, { recursive: true });
  const binaryPath = path.join(buildDir, "hosted-genesis-microvm-workload");
  execFileSync(
    "go",
    ["build", "-o", binaryPath, "./cmd/hosted-genesis-microvm-workload"],
    {
      cwd: repoRoot,
      stdio: "inherit",
      env: {
        ...process.env,
        CGO_ENABLED: "0",
        GOOS: "linux",
        GOARCH: "arm64",
      },
    },
  );
  return new s3assets.Asset(scope, id, { path: buildDir });
}
