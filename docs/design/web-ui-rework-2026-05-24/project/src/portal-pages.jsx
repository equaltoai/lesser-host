/* portal-pages.jsx — Customer-facing surfaces: Fleet, Instance detail, Billing, Souls, Trust */

// ═══ PortalHome — "Fleet" overview ═══════════════════════════════════════
function PortalHome({ onNavigate }) {
  const totalSpend = INSTANCES.reduce((a, i) => a + i.spend, 0);
  const totalBudget = INSTANCES.reduce((a, i) => a + i.budget, 0);
  const live = INSTANCES.filter((i) => i.stage === 'live').length;
  const totalSouls = INSTANCES.reduce((a, i) => a + i.souls, 0);
  const warning = INSTANCES.find((i) => i.status === 'warning');

  return (
    <ContentWithRail
      rail={[
        <Panel key="live-cost" eyebrow="May 2026 · live" title="Cost pulse" actions={<span className="live-dot">live</span>}>
          <div style={{ display: 'flex', alignItems: 'flex-end', gap: '0.8rem', marginBottom: '0.55rem' }}>
            <div style={{ fontFamily: 'var(--ds-font-heading)', fontSize: '2.2rem', fontWeight: 700, letterSpacing: '-0.02em', lineHeight: 1 }}>
              ${totalSpend.toFixed(2)}
            </div>
            <div className="muted" style={{ fontSize: '0.82rem', paddingBottom: '0.3rem' }}>of ${totalBudget.toFixed(2)} budget</div>
          </div>
          <ProgressBar value={totalSpend} max={totalBudget} />
          <div className="row" style={{ justifyContent: 'space-between', marginTop: '0.5rem', fontSize: '0.78rem', color: 'var(--ds-fg-3)' }}>
            <span>Projected EOM: <strong style={{ color: 'var(--ds-fg-1)' }}>$39.90</strong></span>
            <span className="metric__delta--up">↗ 8% wow</span>
          </div>
          <div style={{ marginTop: '0.85rem' }}>
            <Sparkline values={[18.2, 19.1, 20.5, 22.8, 24.2, 26.9, 28.4, 30.1, 31.2, 33.5]} width={260} height={40} />
          </div>
        </Panel>,

        <Panel key="provisioning" eyebrow="Right now" title="Provisioning" actions={<span className="live-dot">live</span>}>
          <div className="col" style={{ gap: '0.75rem' }}>
            <div className="row" style={{ justifyContent: 'space-between' }}>
              <div className="col" style={{ gap: 2 }}>
                <strong className="mono" style={{ fontSize: '0.9rem' }}>lab</strong>
                <span className="muted" style={{ fontSize: '0.78rem' }}>us-west-2 · step 4 of 7 · Lambdas</span>
              </div>
              <Badge tone="accent" dot>2 min</Badge>
            </div>
            <ProgressBar value={4} max={7} />
            <Button variant="ghost" size="sm" onClick={() => onNavigate('/operator/provisioning/prv-3f9c')} style={{ alignSelf: 'flex-start' }}>
              View timeline <IcArrowRight size={14} />
            </Button>
          </div>
        </Panel>,

        <Panel key="alerts" eyebrow="Heads up" title="Alerts">
          <div className="col" style={{ gap: '0.65rem' }}>
            {warning && (
              <Alert variant="warning" title={`${warning.slug} · approaching budget`}>
                Projected to hit 114% of ${warning.budget.toFixed(2)} budget by end of month.
              </Alert>
            )}
            <Alert variant="info" title="@iris awaiting review">
              Soul request routed to <span className="mono">@reviewer-04</span> · 4 days ago.
            </Alert>
          </div>
        </Panel>,
      ]}
    >
      <PageHeader
        eyebrow="Customer portal"
        title="Good morning, Alice."
        sub="Six instances, four souls graduated, all within budget. Lab is finishing provisioning."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcRefresh size={15} />}>Refresh</Button>
            <Button variant="solid" icon={<IcPlus size={15} />}>Provision instance</Button>
          </React.Fragment>
        }
      />

      <div className="panel-grid panel-grid--4" style={{ marginBottom: '1.25rem' }}>
        <Metric label="Live instances" value={live} sub={`${INSTANCES.length} total · 1 dev provisioning`} icon={<IcServer size={16} />} />
        <Metric label="Souls" value={totalSouls} sub="11 graduated · 1 in review · 1 on hold" icon={<IcSouls size={16} />} />
        <Metric label="MTD spend" value={`$${totalSpend.toFixed(2)}`} delta="$5.40 vs Apr" deltaDir="up" icon={<IcZap size={16} />} />
        <Metric label="Federation" value="113 peers" sub="112 reachable · 1 severed" icon={<IcGlobe size={16} />} />
      </div>

      <Panel
        eyebrow="6 instances"
        title="Fleet"
        actions={
          <div className="row" style={{ gap: 6 }}>
            <Button variant="ghost" size="sm" icon={<IcFilter size={14} />}>Filter</Button>
            <Tabs items={[{ value: 'cards', label: 'Cards' }, { value: 'table', label: 'Table' }]} value="cards" onChange={() => {}} />
          </div>
        }
      >
        <div className="panel-grid panel-grid--3">
          {INSTANCES.map((i) => <InstanceCard key={i.slug} instance={i} onClick={() => onNavigate(`/portal/instances/${i.slug}`)} />)}
        </div>
      </Panel>
    </ContentWithRail>
  );
}

function InstanceCard({ instance, onClick }) {
  const pct = (instance.spend / instance.budget) * 100;
  const tone = pct >= 90 ? 'error' : pct >= 70 ? 'warning' : 'success';
  return (
    <div className="instance-card" onClick={onClick} role="button" tabIndex={0}>
      <div
        style={{
          position: 'absolute', top: 0, left: 0, right: 0, height: 3,
          background: `linear-gradient(90deg, ${instance.accent}, transparent 80%)`,
        }}
      />
      <div className="instance-card__head">
        <div className="col" style={{ gap: 2, minWidth: 0 }}>
          <div className="row" style={{ gap: 6 }}>
            <div className="instance-card__slug">{instance.slug}</div>
            {instance.status === 'provisioning' && <Badge tone="accent" dot>provisioning</Badge>}
          </div>
          <div className="instance-card__domain">{instance.domain}</div>
        </div>
        <div className="col" style={{ alignItems: 'flex-end', gap: 4 }}>
          <Badge tone={instance.stage === 'live' ? 'success' : instance.stage === 'staging' ? 'warning' : 'info'}>{instance.stage}</Badge>
          <div className="tertiary" style={{ fontSize: '0.72rem' }}>{instance.region}</div>
        </div>
      </div>

      {instance.status === 'provisioning' ? (
        <div className="col" style={{ gap: 6 }}>
          <div className="muted" style={{ fontSize: '0.84rem' }}>Step 4 of 7 · Lambdas</div>
          <ProgressBar value={4} max={7} />
          <div className="row" style={{ justifyContent: 'space-between', fontSize: '0.78rem' }}>
            <span className="tertiary">ETA · 2 min</span>
            <span className="live-dot">live</span>
          </div>
        </div>
      ) : (
        <React.Fragment>
          <div className="row" style={{ alignItems: 'center', gap: '0.85rem' }}>
            <CostGauge used={instance.spend} budget={instance.budget} size={72} label="of budget" />
            <div className="col" style={{ gap: 3 }}>
              <div className="tertiary" style={{ fontSize: '0.72rem', textTransform: 'uppercase', letterSpacing: '0.1em', fontWeight: 700 }}>MTD spend</div>
              <div style={{ fontFamily: 'var(--ds-font-heading)', fontSize: '1.4rem', fontWeight: 700, letterSpacing: '-0.01em' }}>${instance.spend.toFixed(2)}</div>
              <div className="muted" style={{ fontSize: '0.78rem' }}>of ${instance.budget.toFixed(2)} · projected ${instance.projected.toFixed(2)}</div>
            </div>
          </div>
          <Sparkline values={instance.sparkActivity} width={300} height={28} />
          <div className="instance-card__metrics">
            <div className="instance-card__metric">
              <span className="instance-card__metric-label">Souls</span>
              <span className="instance-card__metric-value">{instance.souls}</span>
            </div>
            <div className="instance-card__metric">
              <span className="instance-card__metric-label">Active users</span>
              <span className="instance-card__metric-value">{instance.activeUsers.toLocaleString()}</span>
            </div>
            <div className="instance-card__metric">
              <span className="instance-card__metric-label">Posts 24h</span>
              <span className="instance-card__metric-value">{instance.posts24h}</span>
            </div>
            <div className="instance-card__metric">
              <span className="instance-card__metric-label">Federation</span>
              <span className="instance-card__metric-value">{instance.peers - instance.severed}<span className="muted" style={{ fontSize: '0.75rem', fontWeight: 500 }}> / {instance.peers}</span></span>
            </div>
          </div>
        </React.Fragment>
      )}
    </div>
  );
}

// ═══ Instance Detail ═════════════════════════════════════════════════════
function InstanceDetail({ slug, onNavigate }) {
  const instance = INSTANCES.find((i) => i.slug === slug) ?? INSTANCES[0];
  const [tab, setTab] = React.useState('overview');
  const souls = SOULS.filter((s) => s.instance === instance.slug);

  return (
    <ContentWithRail
      rail={[
        <Panel key="snap" eyebrow="Snapshot" title="At a glance">
          <DL>
            <DLItem label="Instance ID" mono>les-{instance.slug}-9f</DLItem>
            <DLItem label="Region" mono>{instance.region}</DLItem>
            <DLItem label="Created" mono>{instance.createdAt}</DLItem>
            <DLItem label="Domain" mono>{instance.domain}</DLItem>
            <DLItem label="Anchor" mono>fresh · 6h ago</DLItem>
          </DL>
        </Panel>,

        <Panel key="actions" eyebrow="Operate" title="Quick actions">
          <div className="col" style={{ gap: 6 }}>
            <Button variant="outline" size="sm" icon={<IcRefresh size={14} />}>Refresh anchor</Button>
            <Button variant="outline" size="sm" icon={<IcDownload size={14} />}>Export config</Button>
            <Button variant="ghost" size="sm" icon={<IcCog size={14} />}>Open config…</Button>
          </div>
        </Panel>,

        <Panel key="contacts" eyebrow="Owners" title="People">
          <div className="col" style={{ gap: '0.55rem' }}>
            {['alice', 'bobby'].map((u) => (
              <div className="row" style={{ gap: '0.55rem' }} key={u}>
                <Avatar name={u} size={28} />
                <div className="col" style={{ gap: 0 }}>
                  <span style={{ fontWeight: 600, fontSize: '0.88rem' }}>@{u}</span>
                  <span className="tertiary" style={{ fontSize: '0.74rem' }}>{u === 'alice' ? 'admin' : 'owner'}</span>
                </div>
              </div>
            ))}
          </div>
        </Panel>,
      ]}
    >
      <PageHeader
        eyebrow={`Instance · ${instance.region}`}
        title={
          <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: '0.8rem' }}>
            <span>{instance.slug}</span>
            <span className="mono tertiary" style={{ fontSize: '1.1rem', fontWeight: 500 }}>{instance.domain}</span>
            <Badge tone={instance.stage === 'live' ? 'success' : instance.stage === 'staging' ? 'warning' : 'info'} dot>{instance.stage}</Badge>
          </span>
        }
        sub="Configuration, budgets, domains, keys, and the soul roster bound to this instance."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcEye size={15} />}>Open public</Button>
            <Button variant="outline" icon={<IcRefresh size={15} />}>Refresh</Button>
            <Button variant="solid" icon={<IcSettings size={15} />}>Configure</Button>
          </React.Fragment>
        }
      />

      <div style={{ marginBottom: '1.25rem' }}>
        <Tabs
          value={tab} onChange={setTab}
          items={[
            { value: 'overview', label: 'Overview' },
            { value: 'cost',     label: 'Cost & usage' },
            { value: 'config',   label: 'Configuration' },
            { value: 'domains',  label: 'Domains', badge: 2 },
            { value: 'keys',     label: 'Keys' },
            { value: 'souls',    label: 'Souls', badge: souls.length || null },
          ]}
        />
      </div>

      {tab === 'overview' && <InstanceOverview instance={instance} souls={souls} onNavigate={onNavigate} />}
      {tab === 'cost' && <InstanceCostUsage instance={instance} />}
      {tab === 'config' && <InstanceConfig instance={instance} />}
      {tab === 'domains' && <InstanceDomains instance={instance} />}
      {tab === 'keys' && <InstanceKeys instance={instance} />}
      {tab === 'souls' && <InstanceSouls instance={instance} souls={souls} onNavigate={onNavigate} />}
    </ContentWithRail>
  );
}

function InstanceOverview({ instance, souls, onNavigate }) {
  return (
    <div className="stack">
      <div className="panel-grid panel-grid--4">
        <Metric label="MTD spend" value={`$${instance.spend.toFixed(2)}`} sub={`of $${instance.budget.toFixed(2)} budget`} delta={`projected $${instance.projected.toFixed(2)}`} />
        <Metric label="Active users" value={instance.activeUsers.toLocaleString()} sub="rolling 30d" delta="+12% vs prev 30d" deltaDir="up" />
        <Metric label="Posts (24h)" value={instance.posts24h} sub="federation incl." />
        <Metric label="Sig failures" value={instance.sigFails} sub="last 24h" delta={instance.sigFails > 0 ? '↗ investigate' : 'all clear'} deltaDir={instance.sigFails > 0 ? 'down' : 'up'} />
      </div>

      <StackCard instance={instance} onNavigate={onNavigate} />

      <Panel eyebrow="7 days" title="Activity">
        <div className="panel-grid panel-grid--2">
          <div>
            <Eyebrow>Posts (federated + local)</Eyebrow>
            <Sparkline values={instance.sparkActivity} width={500} height={70} color="var(--ds-secondary-500)" />
          </div>
          <div>
            <Eyebrow>Daily spend</Eyebrow>
            <Sparkline values={instance.sparkCost} width={500} height={70} color="var(--ds-primary-500)" />
          </div>
        </div>
      </Panel>

      <Panel eyebrow={`${souls.length} bound`} title="Souls" actions={<Button variant="ghost" size="sm" onClick={() => onNavigate('/portal/souls')}>View all<IcArrowRight size={14}/></Button>}>
        {souls.length === 0 ? (
          <div className="muted" style={{ fontSize: '0.92rem' }}>No souls bound to this instance yet.</div>
        ) : (
          <div className="col" style={{ gap: '0.55rem' }}>
            {souls.map((s) => (
              <div className="row" style={{ gap: '0.85rem', padding: '0.6rem 0.7rem', borderRadius: 'var(--ds-radius-md)', background: 'var(--ds-bg-raised)' }} key={s.handle}>
                <Avatar name={s.name} size={36} />
                <div className="col" style={{ gap: 0, minWidth: 0, flex: 1 }}>
                  <div className="row" style={{ gap: 6 }}>
                    <span style={{ fontWeight: 700 }}>{s.name}</span>
                    <span className="mono tertiary" style={{ fontSize: '0.82rem' }}>{s.handle}@{instance.domain}</span>
                  </div>
                  <span className="tertiary" style={{ fontSize: '0.78rem' }}>{s.model} · {s.stage.replace('_',' ')}{s.graduatedAt && ` · graduated ${s.graduatedAt}`}</span>
                </div>
                <SoulStageBadge stage={s.stage} />
                <Button variant="ghost" size="sm" onClick={() => onNavigate(`/portal/souls/${s.handle.slice(1)}`)}>Open</Button>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}

function InstanceCostUsage({ instance }) {
  return (
    <div className="stack">
      <div className="panel-grid panel-grid--3">
        <Panel eyebrow="May 2026" title={<span style={{ fontFamily: 'var(--ds-font-heading)' }}>${instance.spend.toFixed(2)}</span>} hint={`of $${instance.budget.toFixed(2)} budget · projected $${instance.projected.toFixed(2)}`}>
          <div style={{ marginTop: '0.4rem' }}>
            <ProgressBar value={instance.spend} max={instance.budget} tone={instance.spend/instance.budget > 0.8 ? 'warning' : null} />
          </div>
        </Panel>
        <Panel eyebrow="Compute" title="1.85M GB-sec" hint="Lambda · 12 functions · arm64">
          <Sparkline values={instance.sparkCost.map(v => v*100)} width={260} height={36} color="var(--ds-primary-500)" />
        </Panel>
        <Panel eyebrow="Egress" title="14.8 GB" hint="CloudFront + Lambda response">
          <div className="row" style={{ gap: 6 }}>
            <Badge tone="success" dot>healthy</Badge>
            <span className="muted" style={{ fontSize: '0.82rem' }}>under $0.30/day</span>
          </div>
        </Panel>
      </div>

      <Panel eyebrow="Breakdown" title="Where the dollars go" hint="May 1 – May 24, 2026">
        <table className="gtable">
          <thead><tr><th>Service</th><th>Unit</th><th>Quantity</th><th style={{ textAlign: 'right' }}>Cost</th><th style={{ textAlign: 'right' }}>% of total</th></tr></thead>
          <tbody>
            {[
              ['Lambda (compute)', 'GB-sec',     '1,847,201', 4.85, 39],
              ['DynamoDB (read/write units)', 'WCU+RCU', '12.4M', 3.12, 25],
              ['CloudFront (egress)', 'GB',     '14.8',      0.86, 7],
              ['S3 (blob storage)', 'GB-month','2.4',       0.18, 1],
              ['EventBridge + SQS', 'events',  '87,402',    0.41, 3],
              ['Route53 + ACM', 'hosted zone','2',         0.50, 4],
              ['Datadog logs', 'GB',           '0.45',      1.12, 9],
              ['NAT + idle',  '—',             '—',         1.36, 11],
            ].map((row, i) => (
              <tr key={i}>
                <td>{row[0]}</td>
                <td className="mono tertiary">{row[1]}</td>
                <td className="mono">{row[2]}</td>
                <td className="mono" style={{ textAlign: 'right' }}>${row[3].toFixed(2)}</td>
                <td style={{ textAlign: 'right', width: 180 }}>
                  <div className="row" style={{ justifyContent: 'flex-end', gap: 8 }}>
                    <div style={{ width: 80 }}><ProgressBar value={row[4]} max={50} /></div>
                    <span className="mono tertiary" style={{ fontSize: '0.78rem' }}>{row[4]}%</span>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>

      <Panel eyebrow="Budget alarms" title="Guardrails">
        <div className="col" style={{ gap: '0.55rem' }}>
          <BudgetRow level="warning" pct={70} amount={`$${(instance.budget*0.7).toFixed(2)}`} channel="email + slack" />
          <BudgetRow level="critical" pct={90} amount={`$${(instance.budget*0.9).toFixed(2)}`} channel="email + slack + pagerduty" />
          <BudgetRow level="cap"      pct={100} amount={`$${instance.budget.toFixed(2)}`} channel="auto-throttle inbox" />
        </div>
      </Panel>
    </div>
  );
}

function BudgetRow({ level, pct, amount, channel }) {
  const tone = level === 'cap' ? 'error' : level === 'critical' ? 'warning' : 'accent';
  return (
    <div className="row" style={{ gap: '0.9rem', padding: '0.65rem 0.8rem', borderRadius: 'var(--ds-radius-md)', background: 'var(--ds-bg-raised)', border: '1px solid var(--ds-border-subtle)' }}>
      <Badge tone={tone} dot>{level}</Badge>
      <div className="col" style={{ gap: 0, flex: 1 }}>
        <div style={{ fontWeight: 600 }}>{pct}% threshold · {amount}</div>
        <div className="tertiary" style={{ fontSize: '0.78rem' }}>Notify: {channel}</div>
      </div>
      <Switch checked />
      <Button variant="ghost" size="sm">Edit</Button>
    </div>
  );
}

function InstanceConfig({ instance }) {
  return (
    <div className="stack">
      <Panel eyebrow="Identity" title="Instance identity">
        <DL>
          <DLItem label="Display name">{instance.slug.replace(/-/g, ' ').replace(/^./, (c) => c.toUpperCase())}</DLItem>
          <DLItem label="Description">A small ActivityPub server run by Equal-to-AI.</DLItem>
          <DLItem label="Default visibility" mono>public</DLItem>
          <DLItem label="Reg open" mono>false (invite-only)</DLItem>
        </DL>
      </Panel>
      <Panel eyebrow="Federation" title="Federation policy">
        <div className="col" style={{ gap: '0.55rem' }}>
          <ConfigToggle label="Accept federation" sub="Default ALLOW; per-instance overrides below" on />
          <ConfigToggle label="Allow quote posts" sub="Lesser-native; respects post-level permission" on />
          <ConfigToggle label="Auto-thread sync" sub="Fetch missing replies on hit" on />
          <ConfigToggle label="AI moderation hint" sub="Inline toxicity + spam scores" on />
          <ConfigToggle label="Public webfinger" sub="Required for inbound resolution" on />
        </div>
      </Panel>
      <Panel eyebrow="Limits" title="Rate limits">
        <DL>
          <DLItem label="Posts / hour" mono>120 (was 60)</DLItem>
          <DLItem label="Inbox delivery" mono>500 / minute</DLItem>
          <DLItem label="Search QPS" mono>10</DLItem>
          <DLItem label="Outbound HTTP" mono>30 / sec / peer</DLItem>
        </DL>
      </Panel>
    </div>
  );
}

function ConfigToggle({ label, sub, on }) {
  const [v, setV] = React.useState(on);
  return (
    <div className="row" style={{ gap: '0.85rem', padding: '0.65rem 0.8rem', borderRadius: 'var(--ds-radius-md)', background: 'var(--ds-bg-raised)', border: '1px solid var(--ds-border-subtle)' }}>
      <div className="col" style={{ gap: 0, flex: 1 }}>
        <div style={{ fontWeight: 600 }}>{label}</div>
        <div className="tertiary" style={{ fontSize: '0.78rem' }}>{sub}</div>
      </div>
      <Switch checked={v} onChange={setV} />
    </div>
  );
}

function InstanceDomains({ instance }) {
  const domains = [
    { name: instance.domain, role: 'apex', state: 'verified', tls: 'A+ · valid', issued: '2025-09-14' },
    { name: `www.${instance.domain}`, role: 'redirect → apex', state: 'verified', tls: 'A · valid', issued: '2025-09-14' },
  ];
  return (
    <Panel eyebrow="2 attached" title="Domains" actions={<Button variant="outline" size="sm" icon={<IcPlus size={14} />}>Add domain</Button>}>
      <table className="gtable">
        <thead><tr><th>Domain</th><th>Role</th><th>State</th><th>TLS</th><th>Issued</th><th></th></tr></thead>
        <tbody>
          {domains.map((d) => (
            <tr key={d.name}>
              <td className="mono">{d.name}</td>
              <td>{d.role}</td>
              <td><Badge tone="success" dot>{d.state}</Badge></td>
              <td className="mono">{d.tls}</td>
              <td className="mono tertiary">{d.issued}</td>
              <td style={{ width: 60 }}><Button variant="ghost" size="sm" icon={<IcMoreH size={15} />}></Button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  );
}

function InstanceKeys({ instance }) {
  const keys = [
    { id: 'sk_live_4f29...e1c4', label: 'Production API', created: '2025-09-14', lastUsed: '4 min ago', scopes: 'inbox.write, follow.write' },
    { id: 'sk_live_2b71...a3df', label: 'CI / build',     created: '2025-12-02', lastUsed: '1 day ago', scopes: 'admin.read' },
    { id: 'sk_pkey_8810...f9c2', label: 'Wallet PKI',     created: '2025-09-14', lastUsed: '2 min ago', scopes: 'sign.actor' },
  ];
  return (
    <Panel eyebrow={`${keys.length} active`} title="API keys" actions={<Button variant="outline" size="sm" icon={<IcPlus size={14} />}>Issue key</Button>} hint="Scoped tokens; rotate routinely. Never share a `sk_live_` token in chat or logs.">
      <table className="gtable">
        <thead><tr><th>Label</th><th>Token</th><th>Scopes</th><th>Created</th><th>Last used</th><th></th></tr></thead>
        <tbody>
          {keys.map((k) => (
            <tr key={k.id}>
              <td><strong>{k.label}</strong></td>
              <td><CopyChip value={k.id} /></td>
              <td className="mono tertiary" style={{ fontSize: '0.8rem' }}>{k.scopes}</td>
              <td className="mono tertiary">{k.created}</td>
              <td className="mono">{k.lastUsed}</td>
              <td style={{ width: 90 }}><Button variant="ghost" size="sm">Revoke</Button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  );
}

function InstanceSouls({ instance, souls, onNavigate }) {
  return (
    <Panel eyebrow={`${souls.length} bound`} title="Souls on this instance" actions={<Button variant="solid" size="sm" icon={<IcPlus size={14} />}>Request soul</Button>}>
      {souls.length === 0
        ? <div className="muted">No souls bound yet. Request one to begin.</div>
        : (
          <table className="gtable">
            <thead><tr><th>Handle</th><th>Stage</th><th>Model</th><th>Anchor</th><th>Tips (May)</th><th></th></tr></thead>
            <tbody>
              {souls.map((s) => (
                <tr key={s.handle} onClick={() => onNavigate(`/portal/souls/${s.handle.slice(1)}`)}>
                  <td>
                    <div className="row" style={{ gap: '0.65rem' }}>
                      <Avatar name={s.name} size={30} />
                      <div className="col" style={{ gap: 0 }}>
                        <strong>{s.name}</strong>
                        <span className="mono tertiary" style={{ fontSize: '0.78rem' }}>{s.handle}@{instance.domain}</span>
                      </div>
                    </div>
                  </td>
                  <td><SoulStageBadge stage={s.stage} /></td>
                  <td className="mono tertiary">{s.model}</td>
                  <td><Badge tone={s.anchor === 'fresh' ? 'success' : s.anchor === 'pending' ? 'warning' : 'error'} dot>{s.anchor}</Badge></td>
                  <td className="mono">${(s.tipsThisMonth || 0).toFixed(2)}</td>
                  <td style={{ width: 60 }}><IcChevron size={15} style={{ color: 'var(--ds-fg-3)' }} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
    </Panel>
  );
}

function SoulStageBadge({ stage }) {
  const map = {
    graduated: { tone: 'success', label: 'graduated' },
    in_review: { tone: 'accent',  label: 'in review' },
    requested: { tone: 'info',    label: 'requested' },
    on_hold:   { tone: 'warning', label: 'on hold' },
  };
  const c = map[stage] ?? { tone: 'default', label: stage };
  return <Badge tone={c.tone} dot>{c.label}</Badge>;
}

Object.assign(window, { PortalHome, InstanceCard, InstanceDetail, SoulStageBadge });
