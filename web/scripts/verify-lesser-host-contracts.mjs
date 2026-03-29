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
const generatedAdapterPath = path.join(webDir, 'src', 'lib', 'greater', 'adapters', 'rest', 'generated', 'lesser-host-api.ts');
const openapiTypescriptBin = path.join(webDir, 'node_modules', '.bin', 'openapi-typescript');

const requiredPaths = [
  '/api/v1/soul/agents/register/{id}/mint-conversation',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/complete',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize/preflight',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize/begin',
  '/api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize',
  '/api/v1/soul/agents/{agentId}/mint-conversations',
  '/api/v1/soul/agents/{agentId}/mint-conversation',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/complete',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize/preflight',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize/begin',
  '/api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize'
];

const requiredSchemas = [
  'AIUsage',
  'SoulMintConversationSSEInput',
  'SoulMintConversation',
  'SoulMintConversationCompleteRequest',
  'SoulAgentMintConversationsResponse',
  'SoulMintConversationFinalizeBeginRequest',
  'SoulMintConversationFinalizeRequest',
  'SoulMintConversationFinalizePreflightResponse',
  'SoulMintConversationFinalizeResponse'
];

const requiredSseEvents = [
  'conversation_start',
  'delta',
  'conversation_done',
  'error'
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

  for (const eventName of requiredSseEvents) {
    assert(sseContract?.events?.[eventName]?.schema, `SSE companion contract missing event schema: ${eventName}`);
  }
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
verifyGeneratedAdapter();

process.stdout.write('PASS: lesser-host mint-conversation contracts are complete and in sync\n');
