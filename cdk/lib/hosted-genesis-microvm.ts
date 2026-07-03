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
  if (props.stage !== "lab") {
    throw new Error(
      "hosted genesis AppTheory MicroVM controller is lab-only; disable hostedGenesisMicrovmLabEnabled outside lab",
    );
  }

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
        stageName: "lab",
        accessLogging: true,
        throttlingRateLimit: 10,
        throttlingBurstLimit: 20,
      },
    },
  );
  props.stateTable.grantReadData(controller.controllerFunction);

  new cdk.CfnOutput(scope, "HostedGenesisMicrovmControllerEndpoint", {
    value: controller.endpoint,
    description:
      "Lab-only AppTheory MicroVM controller endpoint; operator canary only, never browser/public Host response.",
  });
  new cdk.CfnOutput(scope, "HostedGenesisMicrovmSessionRegistryTable", {
    value: controller.sessionTable.tableName,
    description:
      "AppTheory MicroVM session registry cache; Host HostedGenesisSession remains business truth.",
  });

  return { enabled: true, controller };
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
