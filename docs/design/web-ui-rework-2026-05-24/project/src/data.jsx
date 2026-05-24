/* data.jsx — Hardcoded fixtures for a mid-size team with 4–8 instances. */

const SESSION = {
  username: 'alice',
  display_name: 'Alice Sun',
  role: 'admin',
  method: 'wallet',
  email: 'alice@equalto.ai',
  walletAddress: '0x4f29...f9c2',
  expiresAt: '2026-05-25T14:32:00Z',
};

// Six instances, varied stages/health/budgets
const INSTANCES = [
  {
    slug: 'equaltoai', domain: 'equalto.ai', stage: 'live', status: 'healthy',
    region: 'us-west-2', createdAt: '2025-09-14',
    spend: 12.40, budget: 50.00, projected: 14.20,
    souls: 4, peers: 24, severed: 1,
    sparkCost: [0.32, 0.41, 0.38, 0.45, 0.5, 0.48, 0.52],
    sparkActivity: [180, 210, 245, 220, 260, 280, 305],
    activeUsers: 1284, posts24h: 482, sigFails: 0,
    accent: 'var(--ds-secondary-500)',
    stack: { lesser: 'v2026.05.21', body: 'v2026.05.18', mcpWired: true },
  },
  {
    slug: 'maeve-studio', domain: 'maeve.studio', stage: 'dev', status: 'healthy',
    region: 'us-west-2', createdAt: '2026-01-22',
    spend: 1.80, budget: 20.00, projected: 2.10,
    souls: 1, peers: 9, severed: 0,
    sparkCost: [0.04, 0.05, 0.06, 0.05, 0.06, 0.07, 0.06],
    sparkActivity: [25, 28, 32, 30, 34, 41, 38],
    activeUsers: 42, posts24h: 18, sigFails: 0,
    accent: 'var(--ds-primary-500)',
    stack: { lesser: 'v2026.05.14', body: null, mcpWired: false },
  },
  {
    slug: 'staging', domain: 'staging.equalto.ai', stage: 'staging', status: 'warning',
    region: 'us-west-2', createdAt: '2025-11-03',
    spend: 8.10, budget: 10.00, projected: 11.40,
    souls: 2, peers: 6, severed: 0,
    sparkCost: [0.18, 0.22, 0.24, 0.28, 0.32, 0.34, 0.38],
    sparkActivity: [88, 95, 92, 108, 124, 132, 140],
    activeUsers: 86, posts24h: 41, sigFails: 12,
    accent: 'var(--ds-warning-500)',
    stack: { lesser: 'v2026.06.beta1', body: 'v2026.05.18', mcpWired: true },
  },
  {
    slug: 'press-room', domain: 'press.equalto.ai', stage: 'live', status: 'healthy',
    region: 'us-east-1', createdAt: '2025-12-08',
    spend: 4.20, budget: 25.00, projected: 4.80,
    souls: 3, peers: 18, severed: 0,
    sparkCost: [0.11, 0.13, 0.15, 0.14, 0.16, 0.17, 0.18],
    sparkActivity: [62, 68, 75, 81, 88, 92, 98],
    activeUsers: 412, posts24h: 96, sigFails: 1,
    accent: 'var(--ds-success-500)',
    stack: { lesser: 'v2026.05.21', body: 'v2026.05.04', mcpWired: false, mcpDrift: true },
  },
  {
    slug: 'guild', domain: 'guild.equalto.ai', stage: 'live', status: 'healthy',
    region: 'eu-west-1', createdAt: '2026-02-19',
    spend: 6.95, budget: 30.00, projected: 7.40,
    souls: 5, peers: 14, severed: 0,
    sparkCost: [0.17, 0.19, 0.21, 0.20, 0.22, 0.24, 0.24],
    sparkActivity: [120, 138, 142, 155, 168, 174, 180],
    activeUsers: 642, posts24h: 178, sigFails: 0,
    accent: 'var(--ds-info-500)',
    stack: { lesser: 'v2026.05.14', body: 'v2026.05.18', mcpWired: true },
  },
  {
    slug: 'lab', domain: 'lab.equalto.ai', stage: 'dev', status: 'provisioning',
    region: 'us-west-2', createdAt: '2026-05-23',
    spend: 0.00, budget: 15.00, projected: 0.00,
    souls: 0, peers: 0, severed: 0,
    sparkCost: [0, 0, 0, 0, 0, 0, 0],
    sparkActivity: [0, 0, 0, 0, 0, 0, 0],
    activeUsers: 0, posts24h: 0, sigFails: 0,
    accent: 'var(--ds-fg-3)',
    provisioning: { step: 4, total: 7, eta: '2 min' },
    stack: { lesser: 'v2026.05.21', body: null, mcpWired: false },
  },
];

// ─── Releases ───────────────────────────────────────────────────────────
const RELEASES = {
  lesser: [
    { version: 'v2026.06.beta1', channel: 'beta',   released: '2026-05-22', summary: 'Quote post permissions, draft for inbox v2 spec.', breaking: false },
    { version: 'v2026.05.21',    channel: 'stable', released: '2026-05-21', summary: 'AI-moderation hint inline; fix HTTP-sig replay window.', breaking: false, latest: true },
    { version: 'v2026.05.14',    channel: 'stable', released: '2026-05-14', summary: 'Thread sync v3; CDN cache key change.', breaking: false },
    { version: 'v2026.04.29',    channel: 'stable', released: '2026-04-29', summary: 'WebFinger cache; trust score recompute window.', breaking: false },
    { version: 'v2026.04.12',    channel: 'stable', released: '2026-04-12', summary: 'Lambda arm64 baseline; budget alarm dispatch.', breaking: true, breakingNote: 'requires Route53 hosted-zone permissions update' },
  ],
  body: [
    { version: 'v2026.05.18',    channel: 'stable', released: '2026-05-18', summary: 'Continuity loop tuning; tip-split routing.', breaking: false, latest: true },
    { version: 'v2026.05.04',    channel: 'stable', released: '2026-05-04', summary: 'Anchor refresh QoS; rubric v3 hooks.', breaking: false },
    { version: 'v2026.04.10',    channel: 'stable', released: '2026-04-10', summary: 'Initial GA; soul graduation pipeline.', breaking: false },
  ],
};

const LATEST_LESSER = 'v2026.05.21';
const LATEST_BODY = 'v2026.05.18';

const SOULS = [
  { handle: '@maeve', name: 'Maeve', instance: 'equaltoai', stage: 'graduated', anchor: 'fresh', requestedAt: '2025-09-22', graduatedAt: '2025-10-04', model: 'sonnet-4.5', tipsThisMonth: 18.40 },
  { handle: '@atlas', name: 'Atlas', instance: 'equaltoai', stage: 'graduated', anchor: 'fresh', requestedAt: '2025-12-01', graduatedAt: '2025-12-09', model: 'sonnet-4.5', tipsThisMonth: 7.20 },
  { handle: '@iris',  name: 'Iris',  instance: 'equaltoai', stage: 'in_review', anchor: 'pending', requestedAt: '2026-05-19', reviewer: '@reviewer-04', model: 'sonnet-4.5', tipsThisMonth: 0 },
  { handle: '@drone-04', name: 'Drone-04', instance: 'press-room', stage: 'on_hold', anchor: 'stale', requestedAt: '2026-04-11', model: 'haiku-4.5', tipsThisMonth: 0, hold_reason: 'requires anchor refresh' },
  { handle: '@mae', name: 'Mae', instance: 'maeve-studio', stage: 'graduated', anchor: 'fresh', requestedAt: '2026-02-01', graduatedAt: '2026-02-10', model: 'sonnet-4.5', tipsThisMonth: 3.10 },
  { handle: '@ribbon', name: 'Ribbon', instance: 'guild', stage: 'graduated', anchor: 'fresh', requestedAt: '2026-03-04', graduatedAt: '2026-03-12', model: 'sonnet-4.5', tipsThisMonth: 5.80 },
  { handle: '@hex', name: 'Hex', instance: 'guild', stage: 'requested', anchor: 'pending', requestedAt: '2026-05-22', model: 'sonnet-4.5', tipsThisMonth: 0 },
];

const PEERS = [
  { domain: 'mastodon.social', status: 'healthy', followers: 412, lastFetch: '2 min ago' },
  { domain: 'fosstodon.org', status: 'healthy', followers: 124, lastFetch: '4 min ago' },
  { domain: 'hachyderm.io', status: 'healthy', followers: 89, lastFetch: '3 min ago' },
  { domain: 'mastodon.online', status: 'healthy', followers: 178, lastFetch: '1 min ago' },
  { domain: 'tooot.im', status: 'healthy', followers: 31, lastFetch: '8 min ago' },
  { domain: 'mas.to', status: 'healthy', followers: 67, lastFetch: '2 min ago' },
  { domain: 'mstdn.social', status: 'healthy', followers: 54, lastFetch: '6 min ago' },
  { domain: 'mastodon.world', status: 'healthy', followers: 102, lastFetch: '4 min ago' },
  { domain: 'pixelfed.social', status: 'healthy', followers: 12, lastFetch: '12 min ago' },
  { domain: 'pleroma.site', status: 'healthy', followers: 19, lastFetch: '7 min ago' },
  { domain: 'firefish.cafe', status: 'healthy', followers: 28, lastFetch: '5 min ago' },
  { domain: 'sharkey.boundless', status: 'warning', followers: 14, lastFetch: '34 min ago', note: 'slow inbox' },
  { domain: 'iceshrimp.net', status: 'healthy', followers: 22, lastFetch: '2 min ago' },
  { domain: 'misskey.io', status: 'healthy', followers: 41, lastFetch: '3 min ago' },
  { domain: 'akkoma.dev', status: 'healthy', followers: 8, lastFetch: '9 min ago' },
  { domain: 'sciences.re', status: 'healthy', followers: 56, lastFetch: '5 min ago' },
  { domain: 'lemmy.world', status: 'healthy', followers: 7, lastFetch: '15 min ago' },
  { domain: 'kbin.social', status: 'healthy', followers: 11, lastFetch: '11 min ago' },
  { domain: 'bookwyrm.social', status: 'healthy', followers: 4, lastFetch: '22 min ago' },
  { domain: 'gotosocial.org', status: 'healthy', followers: 18, lastFetch: '6 min ago' },
  { domain: 'snac.lol', status: 'healthy', followers: 3, lastFetch: '18 min ago' },
  { domain: 'mitra.social', status: 'healthy', followers: 9, lastFetch: '13 min ago' },
  { domain: 'mastodon.scot', status: 'healthy', followers: 25, lastFetch: '7 min ago' },
  { domain: 'mastodon.social', status: 'severed', followers: 0, lastFetch: '11 days ago', note: 'severed · ToS' },
];

// Provisioning timeline steps
const PROVISIONING_STEPS = [
  { id: 'plan',        title: 'Plan',                  meta: 'CDK synth · 7 stacks',                       state: 'done', at: '14:21:08' },
  { id: 'bootstrap',   title: 'Bootstrap account',     meta: 'CDKToolkit/dev/us-west-2',                   state: 'done', at: '14:21:14' },
  { id: 'storage',     title: 'Storage layer',         meta: 'lesser-instances-dynamo · lesser-blob-s3',   state: 'done', at: '14:22:31' },
  { id: 'network',     title: 'Edge & DNS',            meta: 'CloudFront · Route53 · ACM',                 state: 'done', at: '14:23:55' },
  { id: 'compute',     title: 'Lambdas',               meta: '12 functions · arm64 · 256MB',               state: 'running', at: '14:24:02', sub: 'lesser-inbox-fn · uploading 4.2MB layer…' },
  { id: 'pubsub',      title: 'EventBridge & SQS',     meta: 'federation queue · webfinger cache',         state: 'pending' },
  { id: 'health',      title: 'Health & metrics',      meta: 'Datadog forwarder · alarms',                 state: 'pending' },
];

const AUDIT = [
  { at: '14:24:02', actor: '@alice', action: 'provisioning.job.start', target: 'lab/us-west-2', meta: 'job_id=prv-3f9c-1a2b' },
  { at: '14:18:44', actor: '@alice', action: 'instance.budget.update', target: 'staging', meta: '7.50 → 10.00' },
  { at: '13:52:18', actor: '@reviewer-04', action: 'soul.review.assigned', target: '@iris@equalto.ai', meta: 'rubric=v3' },
  { at: '13:11:09', actor: 'system', action: 'federation.peer.warn', target: 'sharkey.boundless', meta: 'http 502 (3/3)' },
  { at: '12:48:22', actor: '@alice', action: 'instance.config.update', target: 'equaltoai', meta: 'rate_limit: 60 → 120' },
  { at: '11:34:51', actor: '@bobby', action: 'auth.session.create', target: '@bobby', meta: 'method=passkey' },
  { at: '11:21:14', actor: 'system', action: 'soul.continuity.tick', target: '@maeve@equalto.ai', meta: 'streak=14d' },
  { at: '11:02:33', actor: 'system', action: 'webfinger.lookup.miss', target: 'sharkey.boundless', meta: 'http 404' },
  { at: '10:55:17', actor: '@alice', action: 'tip.split.create', target: 'equaltoai', meta: '60/30/10' },
  { at: '10:14:00', actor: 'system', action: 'budget.alarm.cleared', target: 'staging', meta: 'spend < 80%' },
];

const APPROVALS = {
  domains: [
    { id: 'vd-9a12', domain: 'maeve.studio', applicant: '@alice', requestedAt: '2026-05-23 09:11', evidence: 'whois match', state: 'awaiting' },
    { id: 'vd-8b04', domain: 'guild.gay', applicant: '@bobby', requestedAt: '2026-05-22 17:42', evidence: 'DNS TXT verified', state: 'awaiting' },
    { id: 'vd-7c33', domain: 'press.equalto.ai', applicant: '@alice', requestedAt: '2026-05-19 11:08', evidence: 'subdomain delegation', state: 'awaiting' },
  ],
  users: [
    { id: 'uap-2042', handle: '@nora', email: 'nora@equalto.ai', method: 'passkey', requestedAt: '2026-05-23 08:55' },
    { id: 'uap-2041', handle: '@bobby', email: 'bobby@equalto.ai', method: 'wallet', requestedAt: '2026-05-22 16:21' },
  ],
  externals: [
    { id: 'ext-051', domain: 'okina.cafe', operator: 'Hana K.', requestedAt: '2026-05-23 07:42', stage: 'attestation pending' },
  ],
};

const COMMAND_PALETTE = [
  { group: 'Navigate', items: [
    { id: 'home',    label: 'Portal home',           hint: 'g h',  path: '/portal' },
    { id: 'souls',   label: 'Souls',                 hint: 'g s',  path: '/portal/souls' },
    { id: 'billing', label: 'Billing',               hint: 'g b',  path: '/portal/billing' },
    { id: 'trust',   label: 'Trust & federation',    hint: 'g t',  path: '/portal/trust' },
    { id: 'op',      label: 'Operator Console',     hint: 'g o',  path: '/operator' },
  ]},
  { group: 'Actions', items: [
    { id: 'new-inst', label: 'New instance…',        hint: 'n i',  action: 'newInstance' },
    { id: 'new-soul', label: 'Request a soul…',     hint: 'n s',  action: 'newSoul' },
    { id: 'refresh',  label: 'Refresh data',         hint: '⌘ r',  action: 'refresh' },
  ]},
];

Object.assign(window, { SESSION, INSTANCES, SOULS, PEERS, PROVISIONING_STEPS, AUDIT, APPROVALS, COMMAND_PALETTE, RELEASES, LATEST_LESSER, LATEST_BODY });
