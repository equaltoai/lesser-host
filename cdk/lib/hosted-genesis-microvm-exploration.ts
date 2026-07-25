import type {
  AppTheoryMicrovmControllerProps,
  AppTheoryMicrovmImageProps,
  AppTheoryMicrovmNetworkConnectorProps,
} from "@theory-cloud/apptheory-cdk";

export const HOSTED_GENESIS_MICROVM_NAMESPACE = "hosted-genesis" as const;
export const HOSTED_GENESIS_MICROVM_SOURCE_OF_TRUTH = "host-dynamodb-hosted-genesis-session" as const;

export interface HostedGenesisMicrovmCdkMapping {
  readonly networkConnector: Array<keyof AppTheoryMicrovmNetworkConnectorProps>;
  readonly image: Array<keyof AppTheoryMicrovmImageProps>;
  readonly controller: Array<keyof AppTheoryMicrovmControllerProps>;
  readonly nonAuthoritativeState: typeof HOSTED_GENESIS_MICROVM_SOURCE_OF_TRUTH;
}

// Compile-only AppTheory v2.0 MicroVM CDK API mapping for Project 49. This
// file intentionally creates no constructs and is not imported by the stack, so
// `cdk synth` remains a non-deploying dependency/API proof for the exploration.
export const hostedGenesisMicrovmCdkMapping: HostedGenesisMicrovmCdkMapping = {
  networkConnector: ["vpc", "subnets", "securityGroups", "connectorName", "networkProtocol", "operatorRole"],
  image: [
    "name",
    "description",
    "baseImageArn",
    "baseImageVersion",
    "buildRoleArn",
    "codeArtifact",
    "egressNetworkConnectors",
    "hooks",
    "logging",
    "resources",
    "environmentVariables",
  ],
  controller: [
    "controller",
    "authorizer",
    "microvmImage",
    "egressNetworkConnectors",
    "executionRole",
    "sessionTableName",
    "sessionTableRemovalPolicy",
    "enableSessionTablePointInTimeRecovery",
  ],
  nonAuthoritativeState: HOSTED_GENESIS_MICROVM_SOURCE_OF_TRUTH,
};
