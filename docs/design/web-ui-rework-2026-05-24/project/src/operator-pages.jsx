/* operator-pages.jsx — Internal Operator Console: Dashboard, Provisioning Job Detail,
 *   Approvals (Vanity Domains, Users, External Regs), Audit Log, Registries.
 */

// ═══ Operator Dashboard ═══════════════════════════════════════════════════
function OperatorDashboard({ onNavigate }) {
  const provisioning = INSTANCES.find((i) => i.status === 'provisioning');
  const totalCustomers = 47;
  const totalInstances = 86;

  return (
    <ContentWithRail
      rail={[
        <Panel key="live" eyebrow="Right now" title="Live ops" actions={<span className="live-dot">live</span>}>
          <div className="col" style={{ gap: '0.85rem' }}>
            <OpsLiveRow label="Provisioning jobs" value={1} hint="lab · us-west-2" tone="accent" />
            <OpsLiveRow label="Approvals queue"   value={6} hint="3 domains · 2 users · 1 ext" tone="warning" />
            <OpsLiveRow label="Soul reviews"      value={1} hint="@iris · rubric v3" tone="info" />
            <OpsLiveRow label="Tip operations"    value={2} hint="0.31 ETH pending settlement" tone="default" />
          </div>
        </Panel>,
        <Panel key="anc" eyebrow="Fleet" title="Anchors">
          <div className="col" style={{ gap: '0.5rem' }}>
            <div className="row" style={{ justifyContent: 'space-between', fontSize: '0.86rem' }}><span>Fresh (≤ 12h)</span><strong className="mono">81 / 86</strong></div>
            <div className="row" style={{ justifyContent: 'space-between', fontSize: '0.86rem' }}><span>Aging (12–24h)</span><strong className="mono">4 / 86</strong></div>
            <div className="row" style={{ justifyContent: 'space-between', fontSize: '0.86rem' }}><span>Stale (&gt; 24h)</span><strong className="mono" style={{ color: 'var(--ds-warning-300)' }}>1 / 86</strong></div>
            <Button variant="outline" size="sm" style={{ marginTop: '0.6rem' }}>Force refresh stale</Button>
          </div>
        </Panel>,
        <Panel key="alarms" eyebrow="Last 24h" title="Alarms">
          <div className="col" style={{ gap: '0.5rem' }}>
            <Alert variant="warning" title="sharkey.boundless · 502 burst">3 failures in 8 minutes</Alert>
            <Alert variant="info"    title="staging · approaching budget">81% of $10 · throttle armed</Alert>
          </div>
        </Panel>,
      ]}
    >
      <PageHeader
        eyebrow="Operator console"
        title="Engine room."
        sub="Live fleet state across 47 customers and 86 instances. Right rail is real-time; tables are last-second sampled."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcRefresh size={15} />}>Refresh</Button>
            <Button variant="solid" icon={<IcSparkles size={15} />}>Run health probe</Button>
          </React.Fragment>
        }
      />

      <div className="panel-grid panel-grid--4">
        <Metric label="Customers" value={totalCustomers} sub="+3 this week" delta="↗ trending up" deltaDir="up" />
        <Metric label="Instances" value={totalInstances} sub="84 live · 1 staging · 1 dev" />
        <Metric label="MTD revenue" value="$2,418" sub="of $2,640 forecast" delta="$220 to go" />
        <Metric label="Alarms" value={3} sub="last 24h · 1 ack'd" delta="↘ 40% wow" deltaDir="up" />
      </div>

      <div style={{ height: '1rem' }} />

      <div className="panel-grid panel-grid--2">
        <Panel
          eyebrow="In progress"
          title="Provisioning"
          actions={<span className="live-dot">live</span>}
        >
          {provisioning && (
            <div className="col" style={{ gap: '0.7rem' }}>
              <div className="row" style={{ justifyContent: 'space-between' }}>
                <div className="col" style={{ gap: 2 }}>
                  <strong className="mono">{provisioning.slug}</strong>
                  <span className="tertiary" style={{ fontSize: '0.8rem' }}>{provisioning.region} · prv-3f9c-1a2b · started 2 min ago</span>
                </div>
                <Badge tone="accent" dot>step 4 / 7</Badge>
              </div>
              <ProgressBar value={4} max={7} />
              <div className="row" style={{ justifyContent: 'space-between', fontSize: '0.82rem' }}>
                <span className="mono tertiary">lesser-inbox-fn · uploading 4.2MB layer…</span>
                <Button variant="ghost" size="sm" onClick={() => onNavigate('/operator/provisioning/prv-3f9c')}>Open <IcArrowRight size={14} /></Button>
              </div>
            </div>
          )}
          <div className="divider"></div>
          <div className="muted" style={{ fontSize: '0.86rem' }}>2 jobs completed in the last hour · 1 failed (rolled back) · 0 queued</div>
        </Panel>

        <Panel eyebrow="Awaiting your call" title="Approvals queue">
          <div className="col" style={{ gap: '0.55rem' }}>
            {APPROVALS.domains.slice(0, 2).map((d) => (
              <ApprovalRow key={d.id} icon={<IcGlobe size={16} />} title={d.domain} sub={`${d.applicant} · ${d.evidence}`} onOpen={() => onNavigate('/operator/approvals/domains')} />
            ))}
            {APPROVALS.users.slice(0, 1).map((u) => (
              <ApprovalRow key={u.id} icon={<IcUsers size={16} />} title={u.handle} sub={`${u.method} · ${u.email}`} onOpen={() => onNavigate('/operator/approvals/users')} />
            ))}
            {APPROVALS.externals.slice(0, 1).map((e) => (
              <ApprovalRow key={e.id} icon={<IcLink size={16} />} title={e.domain} sub={`${e.operator} · ${e.stage}`} onOpen={() => onNavigate('/operator/approvals/external')} />
            ))}
          </div>
        </Panel>
      </div>

      <div style={{ height: '1rem' }} />

      <Panel eyebrow="Last 12 hours" title="Audit feed" actions={<Button variant="ghost" size="sm" onClick={() => onNavigate('/operator/audit')}>Full log<IcArrowRight size={14} /></Button>}>
        <div className="col" style={{ gap: 2 }}>
          {AUDIT.slice(0, 6).map((a, i) => (
            <div key={i} className="row" style={{ gap: 12, padding: '0.5rem 0.4rem', borderBottom: i < 5 ? '1px solid var(--ds-border-subtle)' : 'none' }}>
              <span className="mono tertiary" style={{ fontSize: '0.78rem', minWidth: 75 }}>{a.at}</span>
              <span className="mono" style={{ fontSize: '0.82rem', minWidth: 130 }}>{a.actor}</span>
              <code className="mono" style={{ fontSize: '0.78rem', background: 'var(--ds-bg-raised)', padding: '0.05rem 0.45rem', borderRadius: 5, color: 'var(--ds-secondary-200)' }}>{a.action}</code>
              <span className="mono" style={{ fontSize: '0.82rem', color: 'var(--ds-fg-2)' }}>{a.target}</span>
              <span className="tertiary" style={{ fontSize: '0.78rem', marginLeft: 'auto' }}>{a.meta}</span>
            </div>
          ))}
        </div>
      </Panel>
    </ContentWithRail>
  );
}

function OpsLiveRow({ label, value, hint, tone }) {
  return (
    <div className="row" style={{ justifyContent: 'space-between', alignItems: 'center', gap: 12 }}>
      <div className="col" style={{ gap: 0 }}>
        <span style={{ fontSize: '0.88rem', fontWeight: 600 }}>{label}</span>
        <span className="tertiary" style={{ fontSize: '0.76rem' }}>{hint}</span>
      </div>
      <Badge tone={tone} dot={value > 0}>{value}</Badge>
    </div>
  );
}

function ApprovalRow({ icon, title, sub, onOpen }) {
  return (
    <div className="row" style={{ gap: '0.7rem', padding: '0.65rem 0.7rem', borderRadius: 'var(--ds-radius-md)', background: 'var(--ds-bg-raised)', border: '1px solid var(--ds-border-subtle)' }}>
      <span style={{ width: 32, height: 32, borderRadius: 8, background: 'var(--ds-bg-glass)', display: 'grid', placeItems: 'center', color: 'var(--ds-fg-2)' }}>{icon}</span>
      <div className="col" style={{ gap: 0, flex: 1, minWidth: 0 }}>
        <strong>{title}</strong>
        <span className="tertiary" style={{ fontSize: '0.78rem' }}>{sub}</span>
      </div>
      <Button variant="outline" size="sm">Review</Button>
      <Button variant="ghost" size="sm" onClick={onOpen}><IcArrowRight size={14} /></Button>
    </div>
  );
}

// ═══ Provisioning Job Detail ══════════════════════════════════════════════
// Supports 4 job kinds:
//   provision        — full CDK deploy (the original 7 steps)
//   update-lesser    — bumps Lesser core on an existing instance
//   update-body      — bumps Lesser-body; appends a gated wire-mcp follow-up
//   wire-mcp         — registers body's MCP tools in the partner Lesser
const JOB_DEFS = {
  'provision': {
    label: 'Provision',
    badge: 'accent',
    steps: PROVISIONING_STEPS,
    runningStep: 5,
    note: 'CDK deploy with cold-start tuning. Output streams from CloudWatch.',
  },
  'update-lesser': {
    label: 'Update Lesser',
    badge: 'info',
    steps: [
      { id: 'plan',     title: 'Plan',                     meta: 'CDK diff · 2 stack drifts',                state: 'done',    at: '15:02:01' },
      { id: 'build',    title: 'Build artifacts',          meta: 'lambda zips · arm64 · 12 fns',             state: 'done',    at: '15:02:48' },
      { id: 'stage',    title: 'Stage rollout',            meta: 'lesser-canary alias · 5% traffic',         state: 'done',    at: '15:03:31' },
      { id: 'drain',    title: 'Drain & cutover',          meta: 'shifting 100% traffic · 30s soft drain',   state: 'running', at: '15:04:14', sub: 'lesser-inbox-fn:live alias swap…' },
      { id: 'health',   title: 'Health probe',             meta: 'webfinger · OK · /actor responding',       state: 'pending' },
    ],
    runningStep: 4,
    note: 'In-place update of the Lesser stack on the running instance. Zero-downtime cutover via Lambda alias shift.',
  },
  'update-body': {
    label: 'Update Lesser-body',
    badge: 'accent',
    steps: [
      { id: 'plan',     title: 'Plan',                     meta: 'agentic stack diff · 1 stack drift',       state: 'done',    at: '15:18:02' },
      { id: 'build',    title: 'Build body artifact',     meta: 'continuity loop · soul runtime · 18MB',   state: 'done',    at: '15:18:49' },
      { id: 'deploy',   title: 'Deploy body',              meta: 'lambda + ECS task · cold start preload',   state: 'running', at: '15:19:30', sub: 'rolling soul-runtime:live · 2 of 3 replicas…' },
      { id: 'health',   title: 'Body health probe',        meta: 'rubric harness · anchor mock OK',          state: 'pending' },
      { id: 'wire-mcp', title: 'Wire MCP in Lesser',       meta: 'auto-queued · runs against the partner lesser instance',  state: 'pending', required: true },
    ],
    runningStep: 3,
    note: 'Updates the agentic worker independently. The trailing wire-mcp step runs against the partner Lesser; required after every body update.',
  },
  'wire-mcp': {
    label: 'Wire MCP',
    badge: 'warning',
    steps: [
      { id: 'plan',     title: 'Plan',                     meta: 'diff body manifest against lesser registry', state: 'done',    at: '15:23:11' },
      { id: 'register', title: 'Register tools in Lesser', meta: 'POST /mcp/tools · 14 tool surfaces',         state: 'running', at: '15:23:22', sub: 'registering soul.tip, soul.boost, soul.reply, soul.thread…' },
      { id: 'verify',   title: 'Verify roundtrip',         meta: 'ping body via lesser MCP proxy',             state: 'pending' },
    ],
    runningStep: 2,
    note: 'Re-registers the agentic worker\'s MCP tools in the partner Lesser instance. Runs automatically after every body update or on demand if drift is detected.',
  },
};

function parseJobId(id) {
  if (id?.startsWith('upd-') && id?.endsWith('-lesser')) return { kind: 'update-lesser', slug: id.slice(4, -7), id };
  if (id?.startsWith('upd-') && id?.endsWith('-body'))   return { kind: 'update-body',   slug: id.slice(4, -5), id };
  if (id?.startsWith('wire-'))                            return { kind: 'wire-mcp',      slug: id.slice(5),     id };
  return { kind: 'provision', slug: 'lab', id };
}

function ProvisioningJobDetail({ jobId, onNavigate }) {
  const job = parseJobId(jobId);
  const def = JOB_DEFS[job.kind];
  const instance = INSTANCES.find((i) => i.slug === job.slug) ?? INSTANCES[0];

  const [step, setStep] = React.useState(def.runningStep);
  const maxStep = def.steps.length;
  // Auto-advance for demo flavor
  React.useEffect(() => {
    setStep(def.runningStep);
  }, [job.kind]);
  React.useEffect(() => {
    if (step >= maxStep) return;
    const t = setTimeout(() => setStep((s) => Math.min(s + 1, maxStep)), 4500);
    return () => clearTimeout(t);
  }, [step, maxStep]);

  const steps = def.steps.map((s, i) => ({
    ...s,
    state: i < step - 1 ? 'done' : i === step - 1 ? 'running' : 'pending',
  }));

  const targetVersion = job.kind === 'update-lesser'
    ? LATEST_LESSER
    : job.kind === 'update-body'
      ? LATEST_BODY
      : null;

  return (
    <ContentWithRail
      rail={[
        <Panel key="job" eyebrow="Job" title="Manifest">
          <DL>
            <DLItem label="Job kind" mono>{job.kind}</DLItem>
            <DLItem label="Job ID" mono>{job.id}</DLItem>
            <DLItem label="Instance" mono>{instance.slug}</DLItem>
            <DLItem label="Domain" mono>{instance.domain}</DLItem>
            <DLItem label="Region" mono>{instance.region}</DLItem>
            <DLItem label="Operator" mono>@alice</DLItem>
            {targetVersion && <DLItem label="Target version" mono>{targetVersion}</DLItem>}
            {job.kind === 'update-lesser' && <DLItem label="From" mono>{instance.stack?.lesser}</DLItem>}
            {job.kind === 'update-body' && <DLItem label="From" mono>{instance.stack?.body ?? '—'}</DLItem>}
          </DL>
        </Panel>,

        job.kind === 'provision' && (
          <Panel key="costproj" eyebrow="Once live" title="Cost projection">
            <div className="row" style={{ alignItems: 'baseline', gap: '0.55rem', marginBottom: '0.4rem' }}>
              <span style={{ fontFamily: 'var(--ds-font-heading)', fontSize: '1.5rem', fontWeight: 700 }}>$0.42</span>
              <span className="muted" style={{ fontSize: '0.82rem' }}>/ day · idle baseline</span>
            </div>
            <div className="muted" style={{ fontSize: '0.86rem', lineHeight: 1.5 }}>
              Climbs to <strong style={{ color: 'var(--ds-fg-1)' }}>$1.20</strong>/day with 100 active users; budget cap at <strong className="mono">$15.00</strong>/mo.
            </div>
          </Panel>
        ),

        job.kind === 'update-body' && (
          <Panel key="mcp" eyebrow="Required follow-up" title="MCP wiring">
            <p className="muted" style={{ fontSize: '0.86rem', lineHeight: 1.55 }}>
              When the body deploy completes, the trailing <span className="mono">wire-mcp</span> step registers the new tool surface against the partner Lesser at <span className="mono">{instance.domain}</span>.
            </p>
            <p className="muted" style={{ fontSize: '0.78rem', marginTop: '0.4rem' }}>
              The body's actor never talks to peers directly — it goes through Lesser. So the wiring is what makes the new body visible at all.
            </p>
          </Panel>
        ),

        job.kind === 'wire-mcp' && (
          <Panel key="why" eyebrow="Why this job" title="MCP wiring">
            <p className="muted" style={{ fontSize: '0.86rem', lineHeight: 1.55 }}>
              Lesser-body's tools are registered in the partner Lesser via the MCP protocol. Body updates can change the tool surface (new tools, renamed tools, schema changes), so the registration must be re-run.
            </p>
            <p className="muted" style={{ fontSize: '0.86rem', lineHeight: 1.55, marginTop: '0.4rem' }}>
              This job runs automatically after every <span className="mono">update-body</span>. Triggering it manually is safe — idempotent.
            </p>
          </Panel>
        ),

        <Panel key="actions" eyebrow="Operator" title="Job controls">
          <div className="col" style={{ gap: 6 }}>
            <Button variant="outline" size="sm">Pause job</Button>
            <Button variant="outline" size="sm">Stream logs to S3</Button>
            <Button variant="ghost" size="sm" style={{ color: 'var(--ds-error-300)' }}>Cancel & rollback</Button>
          </div>
        </Panel>,
      ].filter(Boolean)}
    >
      <PageHeader
        eyebrow={`Provisioning · ${def.label} · ${job.id}`}
        title={<span className="row" style={{ gap: '0.6rem', flexWrap: 'wrap' }}>
          <span className="mono">{instance.slug}</span>
          <span className="tertiary" style={{ fontFamily: 'var(--ds-font-sans)', fontWeight: 500, fontSize: '1.3rem' }}>·</span>
          <span className="muted" style={{ fontFamily: 'var(--ds-font-sans)', fontWeight: 500, fontSize: '1.4rem' }}>{instance.domain}</span>
          {targetVersion && <span className="mono tertiary" style={{ fontSize: '1.05rem', fontFamily: 'var(--ds-font-sans)' }}>→ {targetVersion}</span>}
          <Badge tone={def.badge} dot>{step >= maxStep ? 'done' : 'running'}</Badge>
        </span>}
        sub={<span>Step <strong>{Math.min(step, maxStep)}</strong> of <strong>{maxStep}</strong> · {def.note}</span>}
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcDownload size={15} />}>Export logs</Button>
            <Button variant="outline" onClick={() => onNavigate('/operator/provisioning')}>Back to list</Button>
          </React.Fragment>
        }
      />

      <Panel eyebrow="Live timeline" title="Stack rollout" actions={<span className="live-dot">streaming</span>}>
        <div className="timeline">
          {steps.map((s, i) => (
            <div key={s.id} className="timeline__item">
              <div className={`timeline__node timeline__node--${s.state}`}>
                {s.state === 'done' && <IcCheck size={16} />}
                {s.state === 'running' && <IcSpinner size={16} style={{ animation: 'spin 1.2s linear infinite' }} />}
                {s.state === 'pending' && <IcDot size={16} />}
              </div>
              <div className="timeline__body">
                <div className="row" style={{ gap: 10, alignItems: 'baseline', flexWrap: 'wrap' }}>
                  <span className="timeline__title">{s.title}</span>
                  {s.state === 'done' && <Badge tone="success">done</Badge>}
                  {s.state === 'running' && <Badge tone="accent" dot>running</Badge>}
                  {s.state === 'pending' && !s.required && <Badge>pending</Badge>}
                  {s.required && s.state === 'pending' && <Badge tone="warning" dot>auto-queued</Badge>}
                  {s.at && <span className="mono tertiary" style={{ fontSize: '0.78rem', marginLeft: 'auto' }}>{s.at}</span>}
                </div>
                <div className="timeline__meta">{s.meta}</div>
                {s.state === 'running' && s.sub && (
                  <div className="timeline__detail">
                    [INFO] {s.sub}
                    <span className="ticker-cursor"></span>
                  </div>
                )}
                {s.id === 'wire-mcp' && s.state === 'pending' && (
                  <div className="timeline__detail" style={{ background: 'color-mix(in srgb, var(--ds-warning-500) 10%, transparent)' }}>
                    [QUEUED] wire-mcp will run against <span style={{ color: 'var(--ds-warning-300)' }}>lesser@{instance.domain}</span> as soon as body health passes.
                    {'\n'}[QUEUED] tool registrations: <span style={{ color: 'var(--ds-secondary-200)' }}>soul.tip · soul.boost · soul.reply · soul.thread · …</span>
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      </Panel>

      <style>{`@keyframes spin { 100% { transform: rotate(360deg); } }`}</style>

      <div style={{ height: '1rem' }} />
      <Panel eyebrow="From CloudWatch · last 200 lines" title="Stream" hint="Auto-tails. Filter coming soon.">
        <pre style={{
          margin: 0,
          padding: '1rem',
          background: 'rgba(15, 15, 20, 0.55)',
          color: 'rgba(250, 244, 234, 0.92)',
          fontFamily: 'var(--ds-font-mono)',
          fontSize: '0.78rem',
          borderRadius: 'var(--ds-radius-md)',
          maxHeight: 220,
          overflow: 'auto',
          lineHeight: 1.55,
        }}>
{job.kind === 'wire-mcp' ?
`15:23:11  plan              ✓  body manifest @ ${LATEST_BODY}
15:23:18  diff              ✓  14 tool surfaces (3 new, 11 unchanged)
15:23:22  → POST /mcp/tools  registering soul.tip
15:23:22  → POST /mcp/tools  registering soul.boost
15:23:22  → POST /mcp/tools  registering soul.reply
15:23:22  events            8 / 14 registered
` : job.kind === 'update-body' ?
`15:18:02  cdk diff          ✓  1 stack drift (LesserBodyStack)
15:18:49  build artifact    ✓  18.4MB · arm64
15:19:30  → ECS rollout      shifting 2/3 replicas
15:19:31  health probe      pending · soul-runtime:live
15:19:32  events            7 / 12 received
` : job.kind === 'update-lesser' ?
`15:02:01  cdk diff          ✓  2 stack drifts
15:02:48  build lambdas     ✓  12 fns · 4.6MB total
15:03:31  canary 5%         ✓  no error rate change · 90s baseline
15:04:14  → alias shift      lesser-inbox-fn:live ← :v2026.05.21
15:04:14  events            8 / 11 received
` :
`14:21:08  cdk synth         ✓  7 stacks
14:21:14  bootstrap         ✓  CDKToolkit/dev/us-west-2
14:21:38  → LesserStorageStack
14:22:31  storage           ✓  dynamo + s3
14:22:34  → LesserEdgeStack
14:23:55  edge              ✓  cloudfront + acm + r53
14:24:00  → LesserLambdaStack
14:24:01  package           ✓  lesser-inbox-fn (4.2MB)
14:24:02  upload            …  s3://cdk-hnb659-lesser-inbox-fn-uswest2/0a9c.zip
14:24:02  events            12 / 31 received
`}<span className="ticker-cursor"></span>
        </pre>
      </Panel>
    </ContentWithRail>
  );
}

// ═══ Approvals — Vanity Domains ═══════════════════════════════════════════
function OperatorApprovals({ kind = 'domains', onNavigate }) {
  const title = kind === 'domains' ? 'Vanity domains' : kind === 'users' ? 'User approvals' : 'External registrations';
  const sub = {
    domains: 'Customers asking to attach a custom domain to one of their instances. Evidence of ownership lives below.',
    users:   'Account approvals: users who registered but need an operator sign-off before sessions are issued.',
    external:'External (non-managed) instances asking to register against the tip / soul registries.',
  }[kind] ?? '';
  const items = APPROVALS[kind === 'external' ? 'externals' : kind] ?? [];

  return (
    <div className="content">
      <PageHeader
        eyebrow={`Approvals · ${title}`}
        title={`${items.length} awaiting review.`}
        sub={sub}
        actions={
          <Tabs
            value={kind}
            onChange={(v) => onNavigate(`/operator/approvals/${v === 'external' ? 'external' : v}`)}
            items={[
              { value: 'domains', label: 'Domains', badge: APPROVALS.domains.length },
              { value: 'users',   label: 'Users', badge: APPROVALS.users.length },
              { value: 'external',label: 'External', badge: APPROVALS.externals.length },
            ]}
          />
        }
      />

      <div className="stack">
        {items.length === 0
          ? <Panel><div className="muted" style={{ textAlign: 'center', padding: '2rem' }}>Inbox zero. Take a moment.</div></Panel>
          : items.map((it) => <ApprovalCard key={it.id} kind={kind} item={it} />)}
      </div>
    </div>
  );
}

function ApprovalCard({ kind, item }) {
  return (
    <Panel>
      <div className="row" style={{ gap: '1rem', flexWrap: 'wrap' }}>
        <div className="col" style={{ gap: 0, minWidth: 240, flex: 1 }}>
          <Eyebrow>{kind === 'domains' ? 'Vanity domain' : kind === 'users' ? 'Account' : 'External instance'}</Eyebrow>
          <div className="row" style={{ gap: 10, alignItems: 'baseline', marginTop: 4 }}>
            <span style={{ fontFamily: 'var(--ds-font-heading)', fontWeight: 700, fontSize: '1.4rem', letterSpacing: '-0.01em' }}>
              {item.domain || item.handle}
            </span>
            {kind === 'domains' && <Badge tone="info">{item.evidence}</Badge>}
            {kind === 'users'   && <Badge tone="info">{item.method}</Badge>}
            {kind === 'external'&& <Badge tone="info">{item.stage}</Badge>}
          </div>
          <div className="tertiary mono" style={{ marginTop: 4, fontSize: '0.84rem' }}>
            {item.applicant || item.email || item.operator} · {item.requestedAt}
          </div>
        </div>

        <div className="col" style={{ gap: 4, minWidth: 200 }}>
          {kind === 'domains' && (
            <DL>
              <DLItem label="TXT record" mono>_lesser={item.id}</DLItem>
              <DLItem label="Resolved" mono>{item.evidence}</DLItem>
            </DL>
          )}
          {kind === 'users' && (
            <DL>
              <DLItem label="ID" mono>{item.id}</DLItem>
              <DLItem label="Email" mono>{item.email}</DLItem>
            </DL>
          )}
          {kind === 'external' && (
            <DL>
              <DLItem label="ID" mono>{item.id}</DLItem>
              <DLItem label="Stage" mono>{item.stage}</DLItem>
            </DL>
          )}
        </div>

        <div className="col" style={{ gap: 8, justifyContent: 'center', alignItems: 'stretch', minWidth: 200 }}>
          <Button variant="solid" icon={<IcCheck size={15} />}>Approve</Button>
          <Button variant="outline" icon={<IcMail size={15} />}>Ask for info</Button>
          <Button variant="ghost" style={{ color: 'var(--ds-error-300)' }}>Reject…</Button>
        </div>
      </div>
    </Panel>
  );
}

// ═══ Audit Log ════════════════════════════════════════════════════════════
function OperatorAudit() {
  const [filter, setFilter] = React.useState('all');
  const filters = [
    { value: 'all', label: 'All' },
    { value: 'instance', label: 'Instance' },
    { value: 'soul', label: 'Soul' },
    { value: 'auth', label: 'Auth' },
    { value: 'system', label: 'System' },
  ];
  const filtered = filter === 'all' ? AUDIT : AUDIT.filter((a) => a.action.startsWith(filter) || a.actor === filter);

  return (
    <div className="content">
      <PageHeader
        eyebrow="Operator · audit"
        title="Audit log."
        sub="Every operator-touchable change, plus the system events worth knowing. Searchable. Exportable."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcDownload size={15} />}>Export</Button>
            <Button variant="outline" icon={<IcFilter size={15} />}>Filter</Button>
          </React.Fragment>
        }
      />

      <div className="row" style={{ marginBottom: '1rem' }}>
        <Tabs value={filter} onChange={setFilter} items={filters} />
      </div>

      <Panel eyebrow={`${filtered.length} events`} title="Recent">
        <table className="gtable">
          <thead><tr><th>Time</th><th>Actor</th><th>Action</th><th>Target</th><th>Detail</th></tr></thead>
          <tbody>
            {filtered.map((a, i) => (
              <tr key={i}>
                <td className="mono tertiary" style={{ whiteSpace: 'nowrap' }}>{a.at}</td>
                <td className="mono">{a.actor}</td>
                <td><code className="mono" style={{ fontSize: '0.8rem', background: 'var(--ds-bg-raised)', padding: '0.1rem 0.5rem', borderRadius: 5, color: 'var(--ds-secondary-200)' }}>{a.action}</code></td>
                <td className="mono">{a.target}</td>
                <td className="mono tertiary">{a.meta}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </div>
  );
}

// ═══ Soul Registry / Tip Registry / Instances (operator views, slim) ════
function OperatorInstances({ onNavigate }) {
  // The operator sees ALL instances across all customers; we'll simulate with our 6 + a few more
  const extra = [
    { slug: 'okina-cafe',    domain: 'okina.cafe',     stage: 'live', customer: '@hana',    spend: 4.10,   budget: 20.0,   status: 'healthy' },
    { slug: 'novelty-press', domain: 'novelty.press',  stage: 'live', customer: '@dom',     spend: 9.80,   budget: 25.0,   status: 'healthy' },
    { slug: 'darkroom',      domain: 'darkroom.is',    stage: 'live', customer: '@jules',   spend: 17.40,  budget: 25.0,   status: 'warning' },
  ];
  const all = [...INSTANCES.map((i) => ({ ...i, customer: '@alice' })), ...extra];
  return (
    <div className="content">
      <PageHeader
        eyebrow="Operator · instances"
        title={`${all.length} instances across the fleet.`}
        sub="Operator view: all customers, all instances. Click an instance to open it in support mode."
        actions={<Button variant="outline" icon={<IcFilter size={15} />}>Filter</Button>}
      />

      <Panel>
        <table className="gtable">
          <thead><tr><th>Instance</th><th>Customer</th><th>Stage</th><th>MTD</th><th>Budget</th><th>Status</th><th></th></tr></thead>
          <tbody>
            {all.map((i) => (
              <tr key={i.slug + (i.customer ?? '')} onClick={() => onNavigate(`/portal/instances/${i.slug}`)}>
                <td>
                  <div className="col" style={{ gap: 0 }}>
                    <strong>{i.slug}</strong>
                    <span className="mono tertiary" style={{ fontSize: '0.76rem' }}>{i.domain}</span>
                  </div>
                </td>
                <td className="mono">{i.customer}</td>
                <td><Badge tone={i.stage === 'live' ? 'success' : i.stage === 'staging' ? 'warning' : 'info'}>{i.stage}</Badge></td>
                <td className="mono">${i.spend.toFixed(2)}</td>
                <td className="mono tertiary">${i.budget.toFixed(2)}</td>
                <td><Badge tone={i.status === 'healthy' ? 'success' : 'warning'} dot>{i.status}</Badge></td>
                <td><IcChevron size={15} style={{ color: 'var(--ds-fg-3)' }} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </div>
  );
}

function OperatorTipRegistry() {
  const ops = [
    { id: 'tip-04812', soul: '@maeve', from: '@kyo@maeve.studio', amount: '$0.50', stage: 'settled',   at: '14:18:21' },
    { id: 'tip-04811', soul: '@ribbon', from: '@hex@guild',       amount: '$0.25', stage: 'settling',  at: '14:11:08' },
    { id: 'tip-04810', soul: '@atlas', from: '@bobby',            amount: '$1.00', stage: 'pending',   at: '13:55:11' },
    { id: 'tip-04809', soul: '@maeve', from: '@nora',             amount: '$0.50', stage: 'settled',   at: '13:21:55' },
    { id: 'tip-04808', soul: '@mae',   from: '@hana',             amount: '$0.10', stage: 'settled',   at: '11:09:32' },
  ];
  return (
    <div className="content">
      <PageHeader
        eyebrow="Operator · tip registry"
        title="Tip operations."
        sub="Settled, settling, and pending tip operations across the fleet. Cross-chain settlement happens every 10 minutes."
        actions={<Button variant="outline" icon={<IcDownload size={15} />}>Export</Button>}
      />
      <div className="panel-grid panel-grid--3">
        <Metric label="Volume · 30d" value="$418.20" sub="312 tips · 87% settled in &lt;10m" />
        <Metric label="Operator share" value="$41.82" sub="10% protocol fee" />
        <Metric label="Pending" value={2} sub="0.31 ETH equiv · age 2m" />
      </div>
      <div style={{ height: '1rem' }} />
      <Panel eyebrow={`${ops.length} recent`} title="Operations">
        <table className="gtable">
          <thead><tr><th>ID</th><th>Soul</th><th>From</th><th>Amount</th><th>Stage</th><th>Time</th></tr></thead>
          <tbody>
            {ops.map((o) => (
              <tr key={o.id}>
                <td className="mono">{o.id}</td>
                <td className="mono">{o.soul}</td>
                <td className="mono tertiary">{o.from}</td>
                <td className="mono"><strong>{o.amount}</strong></td>
                <td><Badge tone={o.stage === 'settled' ? 'success' : o.stage === 'settling' ? 'accent' : 'warning'} dot>{o.stage}</Badge></td>
                <td className="mono tertiary">{o.at}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </div>
  );
}

function OperatorSoulRegistry({ onNavigate }) {
  return (
    <div className="content">
      <PageHeader
        eyebrow="Operator · soul registry"
        title="Soul registry."
        sub="All soul-bound agents across the fleet. Approve graduations, refresh anchors, run continuity checks."
      />
      <div className="panel-grid panel-grid--3">
        <Metric label="Registered" value={32} sub="across 47 customers" />
        <Metric label="Graduated" value={28} sub="rubric v3 · 100% pass" delta="↗" deltaDir="up" />
        <Metric label="Awaiting review" value={2} sub="@iris · @hex" />
      </div>
      <div style={{ height: '1rem' }} />
      <Panel eyebrow="Roster" title="Souls">
        <table className="gtable">
          <thead><tr><th>Soul</th><th>Owner</th><th>Stage</th><th>Anchor</th><th>Tips · May</th><th></th></tr></thead>
          <tbody>
            {SOULS.map((s) => (
              <tr key={s.handle} onClick={() => onNavigate(`/portal/souls/${s.handle.slice(1)}`)}>
                <td>
                  <div className="row" style={{ gap: 8 }}>
                    <Avatar name={s.name} size={28} />
                    <div className="col" style={{ gap: 0 }}>
                      <strong>{s.name}</strong>
                      <span className="mono tertiary" style={{ fontSize: '0.76rem' }}>{s.handle}@{(INSTANCES.find(i=>i.slug===s.instance)||{}).domain}</span>
                    </div>
                  </div>
                </td>
                <td className="mono tertiary">@alice</td>
                <td><SoulStageBadge stage={s.stage} /></td>
                <td><Badge tone={s.anchor === 'fresh' ? 'success' : s.anchor === 'pending' ? 'warning' : 'error'} dot>{s.anchor}</Badge></td>
                <td className="mono">{s.tipsThisMonth > 0 ? `$${s.tipsThisMonth.toFixed(2)}` : '—'}</td>
                <td><IcChevron size={15} style={{ color: 'var(--ds-fg-3)' }} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </div>
  );
}

function ProvisioningList({ onNavigate }) {
  const jobs = [
    { id: 'prv-3f9c-1a2b',    kind: 'provision',     slug: 'lab',          state: 'running', started: '14:21:08', operator: '@alice', step: '4/7', target: LATEST_LESSER },
    { id: 'upd-staging-body', kind: 'update-body',   slug: 'staging',      state: 'running', started: '15:19:30', operator: '@alice', step: '3/5', target: LATEST_BODY },
    { id: 'wire-press-room',  kind: 'wire-mcp',      slug: 'press-room',   state: 'queued',  started: '—',         operator: 'system', step: '0/3', target: '—' },
    { id: 'upd-guild-lesser', kind: 'update-lesser', slug: 'guild',        state: 'done',    started: '13:08:11', operator: '@alice', step: '5/5', target: LATEST_LESSER },
    { id: 'prv-3f9a-7b48',    kind: 'provision',     slug: 'darkroom',     state: 'done',    started: '11:42:33', operator: '@alice', step: '7/7', target: 'v2026.05.14' },
    { id: 'prv-3f99-2014',    kind: 'provision',     slug: 'novelty-bench',state: 'failed',  started: '10:18:09', operator: '@bobby', step: '5/7', target: 'v2026.05.14' },
  ];
  const drift = INSTANCES.filter((i) => i.stack?.body && (!i.stack.mcpWired || i.stack.mcpDrift));
  return (
    <div className="content">
      <PageHeader
        eyebrow="Operator · provisioning"
        title="Provisioning."
        sub="CDK deployments, in-place updates, and MCP wirings — live, queued, and failed. Click a job to follow its timeline."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcGitBranch size={15} />} onClick={() => onNavigate('/operator/releases')}>Releases</Button>
            <Button variant="solid" icon={<IcPlus size={15} />}>New job</Button>
          </React.Fragment>
        }
      />

      {drift.length > 0 && (
        <div style={{ marginBottom: '1rem' }}>
          <Alert variant="warning" title={`${drift.length} instance${drift.length === 1 ? '' : 's'} need MCP re-wiring`}>
            Body was updated but Lesser hasn't yet registered the new tools: {drift.map((d) => <span key={d.slug} className="mono" style={{ marginRight: 8 }}>{d.slug}</span>)}.
            <a className="ds-link" onClick={() => onNavigate(`/operator/provisioning/wire-${drift[0].slug}`)}> Wire all →</a>
          </Alert>
        </div>
      )}

      <Panel>
        <table className="gtable">
          <thead>
            <tr>
              <th>Job</th>
              <th>Kind</th>
              <th>Instance</th>
              <th>Target</th>
              <th>State</th>
              <th>Step</th>
              <th>Operator</th>
              <th>Started</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id} onClick={() => onNavigate(`/operator/provisioning/${j.id}`)}>
                <td className="mono" style={{ fontSize: '0.82rem' }}>{j.id}</td>
                <td><JobKindBadge kind={j.kind} /></td>
                <td><strong>{j.slug}</strong></td>
                <td className="mono tertiary">{j.target}</td>
                <td>
                  <Badge tone={j.state === 'running' ? 'accent' : j.state === 'done' ? 'success' : j.state === 'queued' ? 'info' : 'error'} dot={j.state !== 'done'}>{j.state}</Badge>
                </td>
                <td className="mono">{j.step}</td>
                <td className="mono">{j.operator}</td>
                <td className="mono tertiary">{j.started}</td>
                <td><IcChevron size={15} style={{ color: 'var(--ds-fg-3)' }} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </div>
  );
}

function JobKindBadge({ kind }) {
  const map = {
    'provision':     { tone: 'accent',  label: 'provision',     icon: <IcPlus size={11} /> },
    'update-lesser': { tone: 'info',    label: 'update lesser', icon: <IcServer size={11} /> },
    'update-body':   { tone: 'accent',  label: 'update body',   icon: <IcCpu size={11} /> },
    'wire-mcp':      { tone: 'warning', label: 'wire mcp',      icon: <IcLink size={11} /> },
  };
  const c = map[kind] ?? { tone: 'default', label: kind };
  return <Badge tone={c.tone}>{c.icon}{c.label}</Badge>;
}

Object.assign(window, { OperatorDashboard, ProvisioningJobDetail, OperatorApprovals, OperatorAudit, OperatorInstances, OperatorTipRegistry, OperatorSoulRegistry, ProvisioningList });
