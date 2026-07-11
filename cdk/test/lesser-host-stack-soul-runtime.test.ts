import assert from "node:assert/strict";
import test from "node:test";

import {
  type SynthesizedTemplate,
  findLambdaEntryByFunctionName,
  findResourceEntries,
  lambdaEnvironment,
  synthTemplateForStage,
} from "./_lesser-host-test-helpers";

const syntheticRpcParam = "/lesser-host/api/google/rpc/mainnet-test";
const syntheticMintSignerParam = "/lesser-host/soul/live/mint-signer-key-test";
const syntheticSoulContext = {
  soulEnabledLive: "true",
  soulChainIdLive: "1",
  soulRegistryContractAddressLive: "0x1111111111111111111111111111111111111111",
  soulReputationAttestationContractAddressLive:
    "0x2222222222222222222222222222222222222222",
  soulValidationAttestationContractAddressLive:
    "0x3333333333333333333333333333333333333333",
  soulRpcUrlSsmParamLive: syntheticRpcParam,
  soulMintSignerKeySsmParamLive: syntheticMintSignerParam,
  soulAdminSafeAddressLive: "0x4444444444444444444444444444444444444444",
  soulTxModeLive: "safe",
  soulSupportedCapabilitiesLive: "social,commerce",
  tipEnabledLive: "false",
  tipContractAddressLive: "",
  tipRpcUrlSsmParamLive: "",
  ensGatewayEnabledLive: "false",
  ensGatewayResolverAddressLive: "",
};

type LambdaEntry = NonNullable<
  ReturnType<typeof findLambdaEntryByFunctionName>
>;

type IamStatement = Record<string, unknown>;

function requireLambdaEntry(
  template: SynthesizedTemplate,
  namePart: string,
): LambdaEntry {
  const entry = findLambdaEntryByFunctionName(template, namePart);
  assert.ok(entry, `expected Lambda function matching ${namePart}`);
  return entry;
}

function lambdaRoleLogicalId(entry: LambdaEntry): string {
  const role = entry[1].Properties?.Role;
  assert.ok(role && typeof role === "object", "expected Lambda role token");
  const getAtt = (role as { "Fn::GetAtt"?: unknown })["Fn::GetAtt"];
  assert.ok(Array.isArray(getAtt), "expected Lambda role Fn::GetAtt");
  assert.equal(typeof getAtt[0], "string", "expected role logical id");
  return getAtt[0] as string;
}

function policyStatementsForRole(
  template: SynthesizedTemplate,
  roleLogicalId: string,
): IamStatement[] {
  return findResourceEntries(template, "AWS::IAM::Policy")
    .filter(([, policy]) => {
      const roles = policy.Properties?.Roles;
      return (
        Array.isArray(roles) &&
        roles.some(
          (role) =>
            role &&
            typeof role === "object" &&
            (role as { Ref?: unknown }).Ref === roleLogicalId,
        )
      );
    })
    .flatMap(([, policy]) => {
      const statements = (
        policy.Properties?.PolicyDocument as { Statement?: unknown } | undefined
      )?.Statement;
      return Array.isArray(statements)
        ? statements.filter(
            (statement): statement is IamStatement =>
              statement !== null && typeof statement === "object",
          )
        : [];
    });
}

function stringArray(value: unknown): string[] {
  if (typeof value === "string") {
    return [value];
  }
  if (Array.isArray(value)) {
    return value.filter((item): item is string => typeof item === "string");
  }
  return [];
}

function statementHasExactSsmGetParameterGrant(
  statement: IamStatement,
  paramName: string,
): boolean {
  const actions = stringArray(statement.Action);
  const resource = JSON.stringify(statement.Resource ?? "");
  const resourceSuffix = `:parameter/${paramName.replace(/^\//, "")}`;
  return (
    actions.length === 1 &&
    actions[0] === "ssm:GetParameter" &&
    resource.includes(resourceSuffix) &&
    !resource.includes("*")
  );
}

function assertExactSsmGetParameterGrant(
  statements: IamStatement[],
  paramName: string,
): void {
  assert.ok(
    statements.some((statement) =>
      statementHasExactSsmGetParameterGrant(statement, paramName),
    ),
    `expected exact ssm:GetParameter grant for ${paramName}`,
  );
}

test("live Soul runtime context projects only the reviewed mainnet surfaces", () => {
  const template = synthTemplateForStage("live", syntheticSoulContext);
  const controlPlaneEntry = requireLambdaEntry(template, "control-plane-api");
  const trustEntry = requireLambdaEntry(template, "trust-api");
  const renderEntry = requireLambdaEntry(template, "render-worker");
  const controlPlaneEnv = lambdaEnvironment(controlPlaneEntry[1].Properties);
  const trustEnv = lambdaEnvironment(trustEntry[1].Properties);
  const renderEnv = lambdaEnvironment(renderEntry[1].Properties);

  for (const env of [controlPlaneEnv, trustEnv]) {
    assert.equal(env.SOUL_ENABLED, "true");
    assert.equal(env.SOUL_CHAIN_ID, "1");
    assert.equal(env.SOUL_RPC_URL_SSM_PARAM, syntheticRpcParam);
    assert.equal(
      env.SOUL_REGISTRY_CONTRACT_ADDRESS,
      syntheticSoulContext.soulRegistryContractAddressLive,
    );
    assert.equal(
      env.SOUL_REPUTATION_ATTESTATION_CONTRACT_ADDRESS,
      syntheticSoulContext.soulReputationAttestationContractAddressLive,
    );
    assert.equal(
      env.SOUL_VALIDATION_ATTESTATION_CONTRACT_ADDRESS,
      syntheticSoulContext.soulValidationAttestationContractAddressLive,
    );
    assert.equal(
      env.SOUL_ADMIN_SAFE_ADDRESS,
      syntheticSoulContext.soulAdminSafeAddressLive,
    );
    assert.equal(env.SOUL_TX_MODE, "safe");
    assert.equal(env.SOUL_SUPPORTED_CAPABILITIES, "social,commerce");
  }

  assert.equal(
    controlPlaneEnv.SOUL_MINT_SIGNER_KEY_SSM_PARAM,
    syntheticMintSignerParam,
  );
  assert.equal(trustEnv.SOUL_MINT_SIGNER_KEY_SSM_PARAM, undefined);

  assert.equal(controlPlaneEnv.TIP_ENABLED, "false");
  assert.equal(controlPlaneEnv.TIP_CONTRACT_ADDRESS, "");
  assert.equal(controlPlaneEnv.TIP_RPC_URL_SSM_PARAM, "");
  assert.equal(trustEnv.ENS_GATEWAY_ENABLED, "false");
  assert.equal(trustEnv.ENS_GATEWAY_RESOLVER_ADDRESS, "");

  for (const key of Object.keys(renderEnv)) {
    assert.ok(
      !key.startsWith("SOUL_") &&
        !key.startsWith("TIP_") &&
        !key.startsWith("ENS_GATEWAY_"),
      `render-worker must not receive Soul/Tip/ENS runtime key ${key}`,
    );
  }

  const controlPlaneStatements = policyStatementsForRole(
    template,
    lambdaRoleLogicalId(controlPlaneEntry),
  );
  const trustStatements = policyStatementsForRole(
    template,
    lambdaRoleLogicalId(trustEntry),
  );

  assertExactSsmGetParameterGrant(controlPlaneStatements, syntheticRpcParam);
  assertExactSsmGetParameterGrant(controlPlaneStatements, syntheticMintSignerParam);
  assertExactSsmGetParameterGrant(trustStatements, syntheticRpcParam);
  assert.ok(
    !trustStatements.some((statement) =>
      JSON.stringify(statement.Resource ?? "").includes(
        syntheticMintSignerParam.replace(/^\//, ""),
      ),
    ),
    "trust-api must not receive a Mint-signer SSM grant",
  );
});
