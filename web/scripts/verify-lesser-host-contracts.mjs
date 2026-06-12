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

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
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
  assert(
    openapi.paths['/api/v1/soul/instance/agents/register/{id}/mint-conversation']?.post?.responses?.['200']?.content?.['text/event-stream'],
    'instance-key registration mint-conversation route must publish a text/event-stream response'
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
}

function verifySseCompanionSurface() {
  const sseContract = JSON.parse(readFileSync(sseContractPath, 'utf8'));

  assert(sseContract?.version === '1', 'SSE companion contract must declare version "1"');
  assert(Array.isArray(sseContract?.routes), 'SSE companion contract must declare routes');

  for (const route of [
    '/api/v1/soul/agents/register/{id}/mint-conversation',
    '/api/v1/soul/agents/{agentId}/mint-conversation',
    '/api/v1/soul/instance/agents/register/{id}/mint-conversation'
  ]) {
    assert(sseContract.routes.includes(route), `SSE companion contract missing route: ${route}`);
  }

  for (const eventName of requiredSseEvents) {
    assert(sseContract?.events?.[eventName]?.schema, `SSE companion contract missing event schema: ${eventName}`);
  }
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
verifySpecV3BootstrapSurface();
verifyGeneratedAdapter();

process.stdout.write('PASS: lesser-host REST contracts are complete and in sync\n');
