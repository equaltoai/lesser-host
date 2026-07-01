import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import yaml from 'yaml';

const { parse: parseYaml } = yaml;

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webDir = path.resolve(scriptDir, '..');
const repoRoot = path.resolve(webDir, '..');
const openapiPath = path.join(repoRoot, 'docs', 'contracts', 'openapi.yaml');
const sseContractPath = path.join(repoRoot, 'docs', 'contracts', 'soul-mint-conversation-sse.json');
const specV3SchemasDir = path.join(repoRoot, 'docs', 'spec', 'v3', 'schemas');
const specV3FixturesDir = path.join(repoRoot, 'docs', 'spec', 'v3', 'fixtures');
const hostedGenesisContractPath = path.join(repoRoot, 'docs', 'contracts', 'hosted-genesis-conversation.md');
const generatedAdapterPath = path.join(webDir, 'src', 'lib', 'greater', 'adapters', 'rest', 'generated', 'lesser-host-api.ts');
const openapiTypescriptBin = path.join(webDir, 'node_modules', '.bin', 'openapi-typescript');

const requiredPaths = [
  '/api/v1/soul/agents/register/{id}/principal-declaration/preflight',
  '/api/v1/soul/agents/register/{id}/mint-conversation',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/complete',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize/preflight',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize/begin',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize',
  '/api/v1/soul/agents/{agentId}/mint-conversations',
  '/api/v1/soul/agents/{agentId}/mint-conversation',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}',
  '/api/v1/soul/instance/agents/{agentId}/mint-conversations',
  '/api/v1/soul/instance/agents/{agentId}/mint-conversations/{conversationId}',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/complete',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize/preflight',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize/begin',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize',
  '/api/v1/soul/instance/agents/register/begin',
  '/api/v1/soul/instance/agents/register/{id}/principal-declaration/preflight',
  '/api/v1/soul/instance/agents/register/{id}/verify',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/recover',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/complete',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/finalize/preflight',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/finalize/begin',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/finalize',
  '/api/v1/soul/x402/grants',
  '/api/v1/soul/x402/grants/{grantId}/consume'
];

const requiredInstanceBootstrapPaths = [
  '/api/v1/soul/instance/agents/register/begin',
  '/api/v1/soul/instance/agents/register/{id}/principal-declaration/preflight',
  '/api/v1/soul/instance/agents/register/{id}/verify',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/recover',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/complete',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/finalize/preflight',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/finalize/begin',
  '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/finalize'
];

const requiredSchemas = [
  'AIUsage',
  'SoulAgentRegistrationBeginRequest',
  'SoulAgentRegistrationBeginResponse',
  'SoulAgentRegistrationVerifyRequest',
  'SoulAgentRegistrationVerifyResponse',
  'SoulMintConversationSSEInput',
  'SoulHostedGenesisMintConversationRequest',
  'SoulHostedGenesisConversationResponse',
  'SoulMintConversation',
  'SoulMintConversationCompleteRequest',
  'SoulAgentMintConversationsResponse',
  'SoulInstanceMintConversationSummary',
  'SoulInstanceMintConversationsResponse',
  'SoulInstanceMintConversationResponse',
  'SoulMintConversationInstanceReadErrorEnvelope',
  'SoulAgentRegistrationPrincipalDeclarationPreflightRequest',
  'SoulAgentRegistrationPrincipalDeclarationPreflightResponse',
  'SoulMintConversationFinalizeBeginRequest',
  'SoulMintConversationFinalizeRequest',
  'SoulMintConversationFinalizePreflightResponse',
  'SoulMintConversationFinalizeResponse',
  'SoulInstanceBootstrapErrorEnvelope',
  'SoulX402InvocationGrant',
  'SoulX402InvocationGrantIssueRequest',
  'SoulX402InvocationGrantIssueResponse',
  'SoulX402InvocationGrantConsumeRequest',
  'SoulX402InvocationGrantConsumeResponse'
];

const requiredSseEvents = [
  'conversation_start',
  'delta',
  'conversation_done',
  'error'
];

const requiredSpecV3Files = [
  path.join(specV3SchemasDir, 'hosted-genesis.conversation.response.schema.json'),
  path.join(specV3FixturesDir, 'hosted-genesis.conversation.in-progress.example.json'),
  path.join(specV3FixturesDir, 'hosted-genesis.conversation.completed-declaration-ready.example.json'),
  path.join(specV3FixturesDir, 'hosted-genesis.conversation.failed.example.json'),
  path.join(specV3SchemasDir, 'soul-instance-bootstrap.error.schema.json'),
  path.join(specV3SchemasDir, 'soul-instance-bootstrap.finalize.response.schema.json'),
  path.join(specV3FixturesDir, 'soul-instance-bootstrap.error.boundary-violation.example.json'),
  path.join(specV3FixturesDir, 'soul-instance-bootstrap.finalize.response.hosted-offchain.example.json')
];

const requiredInstanceBootstrapErrorCodes = [
  'soul_instance.unauthorized',
  'soul_instance.invalid_request',
  'soul_instance.boundary_violation',
  'soul_instance.conflict',
  'soul_instance.not_found',
  'soul_instance.internal'
];

const soulAgentIDPattern = '0x[0-9a-fA-F]{64}';
const runtimeRegistrationS3KeyPattern = `^registry/v1/agents/${soulAgentIDPattern}/registration\\.json$`;
const runtimeVersionedRegistrationS3KeyPattern = `^registry/v1/agents/${soulAgentIDPattern}/versions/[1-9][0-9]*/registration\\.json$`;
const runtimeRegistrationURIPattern = `^s3://[^/]+/registry/v1/agents/${soulAgentIDPattern}/registration\\.json$`;
const runtimeVersionedRegistrationURIPattern = `^s3://[^/]+/registry/v1/agents/${soulAgentIDPattern}/versions/[1-9][0-9]*/registration\\.json$`;

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function jsonSchemaRef(operation, status, contentType) {
  return operation?.responses?.[status]?.content?.[contentType]?.schema?.$ref ?? '';
}

function verifyOpenApiSurface() {
  const openapi = parseYaml(readFileSync(openapiPath, 'utf8'));

  for (const route of requiredPaths) {
    assert(openapi?.paths?.[route], `missing OpenAPI path: ${route}`);
  }

  for (const schema of requiredSchemas) {
    assert(openapi?.components?.schemas?.[schema], `missing OpenAPI schema: ${schema}`);
  }

  assert(
    openapi.paths['/api/v1/soul/agents/register/{id}/mint-conversation']?.post?.responses?.['200']?.content?.['text/event-stream'],
    'registration-scoped mint-conversation route must publish a text/event-stream response'
  );
  assert(
    openapi.paths['/api/v1/soul/agents/{agentId}/mint-conversation']?.post?.responses?.['200']?.content?.['text/event-stream'],
    'agent-scoped mint-conversation route must publish a text/event-stream response'
  );

  const instancePost =
    openapi.paths['/api/v1/soul/instance/agents/register/{id}/mint-conversation']?.post;
  const instanceGet =
    openapi.paths['/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}']?.get;
  const instanceComplete =
    openapi.paths['/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/complete']?.post;
  assert(instancePost, 'missing instance-key registration mint-conversation POST operation');
  assert(instanceGet, 'missing instance-key registration mint-conversation GET operation');
  assert(instanceComplete, 'missing instance-key registration mint-conversation complete operation');
  assert(
    instancePost.operationId === 'soulInstanceStartRegistrationMintConversation',
    'instance-key registration mint-conversation POST must not be locked to an SSE operation id'
  );
  assert(
    instancePost['x-authoritative-completion'] === 'durable-json',
    'instance-key registration mint-conversation POST must declare durable JSON authoritative completion'
  );
  assert(
    !instancePost?.responses?.['200']?.content?.['text/event-stream'],
    'instance-key registration mint-conversation POST must not be documented as text/event-stream authoritative'
  );
  assert(
    jsonSchemaRef(instancePost, '200', 'application/json') === '#/components/schemas/SoulHostedGenesisConversationResponse',
    'instance-key registration mint-conversation POST 200 must return SoulHostedGenesisConversationResponse JSON'
  );
  assert(
    jsonSchemaRef(instancePost, '202', 'application/json') === '#/components/schemas/SoulHostedGenesisConversationResponse',
    'instance-key registration mint-conversation POST 202 must return SoulHostedGenesisConversationResponse JSON'
  );
  assert(
    jsonSchemaRef(instanceGet, '200', 'application/json') === '#/components/schemas/SoulHostedGenesisConversationResponse',
    'instance-key registration mint-conversation GET must return SoulHostedGenesisConversationResponse JSON'
  );
  assert(
    instancePost?.requestBody?.content?.['application/json']?.schema?.$ref ===
      '#/components/schemas/SoulHostedGenesisMintConversationRequest',
    'instance-key registration mint-conversation POST must use the hosted-genesis JSON request schema'
  );
  assert(
    openapi.components?.schemas?.SoulHostedGenesisMintConversationRequest?.properties?.lesser_request_id,
    'hosted-genesis POST request schema must include optional lesser_request_id for trace echoing'
  );
  assert(
    jsonSchemaRef(instanceComplete, '202', 'application/json') === '#/components/schemas/SoulHostedGenesisConversationResponse',
    'instance-key registration mint-conversation complete 202 must return progress-safe HostConversation JSON'
  );
  assert(
    jsonSchemaRef(instanceComplete, '200', 'application/json') === '#/components/schemas/SoulHostedGenesisConversationResponse',
    'instance-key registration mint-conversation complete 200 must return compact HostConversation JSON without raw transcript fields'
  );

  for (const route of requiredInstanceBootstrapPaths) {
    const method = route === '/api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}' ? 'get' : 'post';
    const operation = openapi.paths[route]?.[method];
    assert(operation, `missing OpenAPI operation for instance bootstrap route: ${method.toUpperCase()} ${route}`);
    assert(
      JSON.stringify(operation.security ?? []) === JSON.stringify([{ instanceKeyAuth: [] }]),
      `instance bootstrap route must use only instanceKeyAuth: ${method.toUpperCase()} ${route}`
    );
    assert(
      !JSON.stringify(operation.security ?? []).includes('sessionAuth'),
      `instance bootstrap route must not document portal/control-plane session auth: ${method.toUpperCase()} ${route}`
    );
    for (const status of ['400', '401', '403', '404', '409', '500']) {
      if (status === '404' && route === '/api/v1/soul/instance/agents/register/begin') {
        continue;
      }
      assert(
        operation.responses?.[status]?.content?.['application/json']?.schema?.$ref === '#/components/schemas/SoulInstanceBootstrapErrorEnvelope',
        `instance bootstrap route must publish soul_instance error envelope for ${status}: ${method.toUpperCase()} ${route}`
      );
    }
  }

  const instanceAuthDescription = openapi.components?.securitySchemes?.instanceKeyAuth?.description ?? '';
  assert(instanceAuthDescription.includes('sha256(raw_key)'), 'instanceKeyAuth must document sha256(raw_key) matching');
  assert(!instanceAuthDescription.toLowerCase().includes('session token'), 'instanceKeyAuth must not describe session-token auth');

  const finalizeRef = openapi.components?.schemas?.SoulMintConversationFinalizeResponse?.$ref ?? '';
  assert(
    finalizeRef.endsWith('/soul-instance-bootstrap.finalize.response.schema.json') ||
      finalizeRef === '../spec/v3/schemas/soul-instance-bootstrap.finalize.response.schema.json',
    'finalize response must reference the v3 hosted/off-chain finalize response schema'
  );
  const beginRequired = openapi.components?.schemas?.SoulAgentRegistrationBeginRequest?.required ?? [];
  assert(!beginRequired.includes('wallet_address'), 'registration begin request must not require wallet_address for instance_trust');
  const beginAuthorityEnum = openapi.components?.schemas?.SoulAgentRegistrationBeginRequest?.properties?.authority_model?.enum ?? [];
  assert(beginAuthorityEnum.includes('instance_trust'), 'registration begin request must document authority_model=instance_trust');

  const finalizeRequestRequired = openapi.components?.schemas?.SoulMintConversationFinalizeRequest?.required ?? [];
  assert(!finalizeRequestRequired.includes('self_attestation'), 'finalize request must not require self_attestation for instance_trust');
  const preflightRequired = openapi.components?.schemas?.SoulMintConversationFinalizePreflightResponse?.required ?? [];
  assert(preflightRequired.includes('authority_model'), 'finalize preflight must require authority_model evidence');
  assert(!preflightRequired.includes('self_attestation_signing'), 'finalize preflight must not require wallet signing material for instance_trust');
}

function verifySseCompanionSurface() {
  const sseContract = JSON.parse(readFileSync(sseContractPath, 'utf8'));

  assert(sseContract?.version === '1', 'SSE companion contract must declare version "1"');
  assert(Array.isArray(sseContract?.routes), 'SSE companion contract must declare routes');

  for (const route of [
    '/api/v1/soul/agents/register/{id}/mint-conversation',
    '/api/v1/soul/agents/{agentId}/mint-conversation'
  ]) {
    assert(sseContract.routes.includes(route), `SSE companion contract missing route: ${route}`);
  }
  assert(
    !sseContract.routes.includes('/api/v1/soul/instance/agents/register/{id}/mint-conversation'),
    'SSE companion contract must not claim the Lesser instance-key mint-conversation route'
  );

  for (const eventName of requiredSseEvents) {
    assert(sseContract?.events?.[eventName]?.schema, `SSE companion contract missing event schema: ${eventName}`);
  }
}

function assertHostedGenesisConversationExample(file, expectedStatus) {
  const payload = JSON.parse(readFileSync(path.join(specV3FixturesDir, file), 'utf8'));
  assert(payload?.version === '1', `${file} must declare version=1`);
  assert(typeof payload?.request_id === 'string' && payload.request_id.length > 0, `${file} must include request_id`);

  const conversation = payload?.conversation;
  assert(conversation, `${file} must include conversation`);
  for (const field of ['registration_id', 'conversation_id', 'agent_id', 'status', 'message_count', 'request_id']) {
    assert(conversation[field] !== undefined && conversation[field] !== '', `${file} conversation missing ${field}`);
  }
  assert(conversation.status === expectedStatus, `${file} expected status=${expectedStatus}, got ${conversation.status}`);
  assert(conversation.request_id === payload.request_id, `${file} conversation.request_id must match top-level request_id`);
  assert(!('messages' in conversation), `${file} must not expose raw conversation messages`);

  if (expectedStatus === 'declaration_ready') {
    const produced = conversation.produced_declarations;
    assert(produced, `${file} declaration_ready must include produced_declarations`);
    assert(/^sha256:[0-9a-f]{64}$/.test(produced.declaration_hash ?? ''), `${file} must include declaration_hash evidence`);
    for (const field of ['selfDescription', 'capabilities', 'boundaries', 'transparency']) {
      assert(produced.declarations?.[field] !== undefined, `${file} produced_declarations missing ${field}`);
    }
    assert(produced.evidence?.conversation_id === conversation.conversation_id, `${file} evidence must bind conversation_id`);
    assert(produced.evidence?.registration_id === conversation.registration_id, `${file} evidence must bind registration_id`);
    assert(produced.evidence?.agent_id === conversation.agent_id, `${file} evidence must bind agent_id`);
  } else {
    assert(
      !('produced_declarations' in conversation),
      `${file} must not include produced_declarations before declaration_ready`
    );
  }

  if (expectedStatus === 'failed') {
    const failure = conversation.failure;
    assert(failure?.code, `${file} failed status must include failure.code`);
    assert(['refresh_state', 'retry_same_step', 'restart_soul_bootstrap', 'operator_action'].includes(failure?.recovery?.action), `${file} failed status must include typed recovery.action`);
  } else {
    assert(!('failure' in conversation), `${file} must not include failure outside failed status`);
  }
}

function verifyHostedGenesisConversationSurface() {
  const contractDoc = readFileSync(hostedGenesisContractPath, 'utf8');
  assert(
    contractDoc.includes('collapses `created` to `in_progress`') ||
      contractDoc.includes('collapses `created` snapshots'),
    'hosted-genesis contract must document the Lesser projection decision for created status'
  );

  const schema = JSON.parse(readFileSync(path.join(specV3SchemasDir, 'hosted-genesis.conversation.response.schema.json'), 'utf8'));
  const statusEnum = schema?.$defs?.status?.enum ?? [];
  for (const status of [
    'created',
    'in_progress',
    'assistant_turn_ready',
    'declaration_extraction_pending',
    'declaration_ready',
    'failed'
  ]) {
    assert(statusEnum.includes(status), `hosted-genesis conversation schema missing status: ${status}`);
  }
  assert(!statusEnum.includes('completed'), 'hosted-genesis contract must use declaration_ready instead of legacy completed');
  const requiredFields = schema?.$defs?.conversation?.required ?? [];
  for (const field of ['registration_id', 'conversation_id', 'agent_id', 'status', 'message_count', 'request_id']) {
    assert(requiredFields.includes(field), `hosted-genesis conversation schema missing required field: ${field}`);
  }
  const recoveryActions = schema?.$defs?.failure?.properties?.recovery?.properties?.action?.enum ?? [];
  for (const action of ['refresh_state', 'retry_same_step', 'restart_soul_bootstrap', 'operator_action']) {
    assert(recoveryActions.includes(action), `hosted-genesis failure recovery missing action: ${action}`);
  }

  assertHostedGenesisConversationExample('hosted-genesis.conversation.in-progress.example.json', 'in_progress');
  assertHostedGenesisConversationExample(
    'hosted-genesis.conversation.completed-declaration-ready.example.json',
    'declaration_ready'
  );
  assertHostedGenesisConversationExample('hosted-genesis.conversation.failed.example.json', 'failed');
}

function verifySpecV3BootstrapSurface() {
  for (const file of requiredSpecV3Files) {
    JSON.parse(readFileSync(file, 'utf8'));
  }

  const errorSchema = JSON.parse(readFileSync(path.join(specV3SchemasDir, 'soul-instance-bootstrap.error.schema.json'), 'utf8'));
  const errorCodes = errorSchema?.properties?.error?.properties?.code?.enum ?? [];
  for (const code of requiredInstanceBootstrapErrorCodes) {
    assert(errorCodes.includes(code), `instance bootstrap error schema missing code: ${code}`);
  }

  const finalizeSchema = JSON.parse(readFileSync(path.join(specV3SchemasDir, 'soul-instance-bootstrap.finalize.response.schema.json'), 'utf8'));
  for (const field of ['version', 'agent_id', 'agent', 'published_version', 'publication']) {
    assert(finalizeSchema?.required?.includes(field), `instance bootstrap finalize response missing required field: ${field}`);
  }
  assert(
    finalizeSchema?.properties?.agent?.$ref === 'soul-agent-identity.schema.json',
    'instance bootstrap finalize response must bind to the canonical soul-agent-identity schema'
  );

  const publicationProps = finalizeSchema?.$defs?.publication?.properties ?? {};
  assert(
    publicationProps.registration_uri?.pattern === runtimeRegistrationURIPattern,
    'finalize publication registration_uri must require the runtime registry/v1/agents S3 URI shape'
  );
  assert(
    publicationProps.registration_s3_key?.pattern === runtimeRegistrationS3KeyPattern,
    'finalize publication registration_s3_key must require the runtime registry/v1/agents key shape'
  );
  assert(
    publicationProps.versioned_registration_uri?.pattern === runtimeVersionedRegistrationURIPattern,
    'finalize publication versioned_registration_uri must require the runtime registry/v1/agents versioned S3 URI shape'
  );
  assert(
    publicationProps.versioned_registration_s3_key?.pattern === runtimeVersionedRegistrationS3KeyPattern,
    'finalize publication versioned_registration_s3_key must require the runtime registry/v1/agents versioned key shape'
  );

  const finalizeFixture = JSON.parse(
    readFileSync(path.join(specV3FixturesDir, 'soul-instance-bootstrap.finalize.response.hosted-offchain.example.json'), 'utf8')
  );
  const fixtureAgentID = finalizeFixture?.agent_id;
  const fixturePublishedVersion = finalizeFixture?.published_version;
  const expectedRegistrationS3Key = `registry/v1/agents/${fixtureAgentID}/registration.json`;
  const expectedVersionedRegistrationS3Key = `registry/v1/agents/${fixtureAgentID}/versions/${fixturePublishedVersion}/registration.json`;
  assert(
    finalizeFixture?.publication?.registration_s3_key === expectedRegistrationS3Key,
    'hosted/off-chain finalize fixture must use the runtime soulRegistrationS3Key registry/v1/agents key shape'
  );
  assert(
    finalizeFixture?.publication?.versioned_registration_s3_key === expectedVersionedRegistrationS3Key,
    'hosted/off-chain finalize fixture must use the runtime soulRegistrationVersionedS3Key registry/v1/agents key shape'
  );
  assert(
    finalizeFixture?.publication?.registration_uri === `s3://lesser-host-lab-soul-packs/${expectedRegistrationS3Key}`,
    'hosted/off-chain finalize fixture registration_uri must use the runtime registry/v1/agents URI shape'
  );
  assert(
    finalizeFixture?.publication?.versioned_registration_uri === `s3://lesser-host-lab-soul-packs/${expectedVersionedRegistrationS3Key}`,
    'hosted/off-chain finalize fixture versioned_registration_uri must use the runtime registry/v1/agents URI shape'
  );
  assert(finalizeFixture?.agent?.authority_model === 'instance_trust', 'hosted/off-chain finalize fixture agent must use instance_trust authority');
  assert(finalizeFixture?.agent?.anchor_state === 'hosted_offchain', 'hosted/off-chain finalize fixture agent must use hosted_offchain anchor state');
  assert(!('wallet' in finalizeFixture.agent), 'hosted/off-chain finalize fixture agent must not include wallet');
  assert(!('principal_address' in finalizeFixture.agent), 'hosted/off-chain finalize fixture agent must not include principal_address');
  assert(finalizeFixture?.publication?.authority_model === 'instance_trust', 'hosted/off-chain publication evidence must use instance_trust authority');
  assert(finalizeFixture?.promotion?.authority_model === 'instance_trust', 'hosted/off-chain promotion evidence must use instance_trust authority');
}

function verifyGeneratedAdapter() {
  const tmpDir = mkdtempSync(path.join(os.tmpdir(), 'lesser-host-openapi-'));
  const tmpOutput = path.join(tmpDir, 'lesser-host-api.ts');

  try {
    execFileSync(openapiTypescriptBin, [openapiPath, '-o', tmpOutput], {
      cwd: webDir,
      stdio: 'pipe'
    });
    const expected = readFileSync(tmpOutput, 'utf8');
    const actual = readFileSync(generatedAdapterPath, 'utf8');

    assert(
      actual === expected,
      'generated lesser-host adapter is stale; run `cd web && npm run generate:lesser-host-api`'
    );

    for (const route of requiredPaths) {
      assert(actual.includes(`"${route}"`), `generated adapter missing route: ${route}`);
    }
    for (const schema of requiredSchemas) {
      assert(actual.includes(`${schema}:`), `generated adapter missing schema: ${schema}`);
    }
  } finally {
    rmSync(tmpDir, { recursive: true, force: true });
  }
}

verifyOpenApiSurface();
verifySseCompanionSurface();
verifyHostedGenesisConversationSurface();
verifySpecV3BootstrapSurface();
verifyGeneratedAdapter();

process.stdout.write('PASS: lesser-host REST contracts are complete and in sync\n');
