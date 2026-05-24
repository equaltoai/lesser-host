/* portal-pages-2.jsx — Billing, Souls, Soul Detail, Trust */

// ═══ Billing ══════════════════════════════════════════════════════════════
function PortalBilling({ onNavigate }) {
  const total = INSTANCES.reduce((a, i) => a + i.spend, 0);
  const totalBudget = INSTANCES.reduce((a, i) => a + i.budget, 0);
  const projected = INSTANCES.reduce((a, i) => a + i.projected, 0);

  // Stacked weekly cost dataset (5 weeks)
  const weeks = [
    { label: 'May 1', stacks: [3.10, 0.45, 1.80, 1.05, 1.65, 0] },
    { label: 'May 8', stacks: [3.45, 0.55, 2.10, 1.18, 1.78, 0] },
    { label: 'May 15', stacks: [3.85, 0.40, 2.35, 1.05, 1.85, 0] },
    { label: 'May 22', stacks: [2.00, 0.40, 1.85, 0.92, 1.67, 0] },
  ];
  const colors = INSTANCES.map((i) => i.accent);
  const maxWeek = Math.max(...weeks.map((w) => w.stacks.reduce((a, b) => a + b, 0)));

  return (
    <ContentWithRail
      rail={[
        <Panel key="this-month" eyebrow="May 2026" title="This month">
          <div style={{ display: 'flex', alignItems: 'flex-end', gap: '0.7rem' }}>
            <div style={{ fontFamily: 'var(--ds-font-heading)', fontSize: '2.2rem', fontWeight: 700, lineHeight: 1, letterSpacing: '-0.02em' }}>${total.toFixed(2)}</div>
            <div className="muted" style={{ fontSize: '0.82rem', paddingBottom: 5 }}>of ${totalBudget.toFixed(2)}</div>
          </div>
          <ProgressBar value={total} max={totalBudget} />
          <div className="row" style={{ justifyContent: 'space-between', marginTop: '0.55rem', fontSize: '0.82rem' }}>
            <span className="muted">Projected ${projected.toFixed(2)}</span>
            <span className="metric__delta--up">↗ 8% wow</span>
          </div>
        </Panel>,

        <Panel key="payment" eyebrow="Account" title="Payment">
          <div className="col" style={{ gap: '0.55rem' }}>
            <div className="row" style={{ gap: 8 }}>
              <div style={{ width: 36, height: 24, borderRadius: 4, background: 'linear-gradient(135deg, var(--ds-secondary-500), var(--ds-secondary-700))' }} />
              <div className="col" style={{ gap: 0 }}>
                <div className="mono" style={{ fontSize: '0.86rem' }}>•••• 4129</div>
                <div className="tertiary" style={{ fontSize: '0.74rem' }}>Visa · exp 09/29</div>
              </div>
            </div>
            <Button variant="outline" size="sm">Update method</Button>
          </div>
        </Panel>,

        <Panel key="invoices" eyebrow="Recent" title="Invoices">
          <div className="col" style={{ gap: 6 }}>
            {[
              { id: 'INV-2604', date: 'Apr 2026', amount: '$28.10', state: 'paid' },
              { id: 'INV-2603', date: 'Mar 2026', amount: '$22.85', state: 'paid' },
              { id: 'INV-2602', date: 'Feb 2026', amount: '$19.40', state: 'paid' },
            ].map((iv) => (
              <div key={iv.id} className="row" style={{ justifyContent: 'space-between', padding: '0.4rem 0' }}>
                <div className="col" style={{ gap: 0 }}>
                  <strong className="mono" style={{ fontSize: '0.84rem' }}>{iv.id}</strong>
                  <span className="tertiary" style={{ fontSize: '0.74rem' }}>{iv.date}</span>
                </div>
                <div className="row" style={{ gap: 6 }}>
                  <span className="mono">{iv.amount}</span>
                  <button className="btn btn--ghost btn--sm" style={{ padding: '0.2rem 0.4rem' }} title="Download"><IcDownload size={14} /></button>
                </div>
              </div>
            ))}
          </div>
        </Panel>,
      ]}
    >
      <PageHeader
        eyebrow="Cost & billing"
        title="Where the money goes."
        sub="Per-instance cost across compute, storage, network, and the AWS fixed costs we eat for you. Real-time, billed monthly."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcDownload size={15} />}>Export CSV</Button>
            <Button variant="outline">May invoice</Button>
          </React.Fragment>
        }
      />

      <div className="panel-grid panel-grid--4" style={{ marginBottom: '1.25rem' }}>
        <Metric label="MTD" value={`$${total.toFixed(2)}`} sub={`of $${totalBudget.toFixed(2)} aggregate budget`} icon={<IcZap size={16} />} />
        <Metric label="Projected EOM" value={`$${projected.toFixed(2)}`} sub="based on 7-day trailing" delta="$5.40 vs Apr" deltaDir="up" />
        <Metric label="Per active user" value="$0.012" sub="2,468 MAU / month" delta="↘ 6%" deltaDir="up" />
        <Metric label="Per federated post" value="$0.0004" sub="vs $0.001 mastodon avg" delta="≈ 2.5× cheaper" deltaDir="up" />
      </div>

      <Panel eyebrow="May 2026 · stacked by instance" title="Weekly spend">
        <div className="row" style={{ gap: '1.4rem', alignItems: 'flex-end', padding: '0.5rem 0 1rem', minHeight: 220 }}>
          {weeks.map((w, wi) => {
            const wtotal = w.stacks.reduce((a, b) => a + b, 0);
            const wHeightMax = 200;
            const hpx = (wtotal / maxWeek) * wHeightMax;
            return (
              <div key={wi} className="col" style={{ alignItems: 'center', gap: 6, flex: 1, minWidth: 0 }}>
                <div className="mono" style={{ fontSize: '0.78rem', color: 'var(--ds-fg-2)' }}>${wtotal.toFixed(2)}</div>
                <div style={{ width: '70%', minWidth: 60, maxWidth: 110, display: 'flex', flexDirection: 'column-reverse', borderRadius: 8, overflow: 'hidden', boxShadow: 'var(--ds-shadow-xs)', height: hpx, transition: 'height 0.4s' }}>
                  {w.stacks.map((s, si) => {
                    if (s === 0) return null;
                    return <div key={si} style={{ background: colors[si], height: `${(s / wtotal) * 100}%`, opacity: 0.9 }} title={`${INSTANCES[si].slug}: $${s.toFixed(2)}`} />;
                  })}
                </div>
                <div className="tertiary" style={{ fontSize: '0.78rem' }}>{w.label}</div>
              </div>
            );
          })}
          <div className="col" style={{ alignItems: 'center', gap: 6, flex: 1, opacity: 0.55 }}>
            <div className="mono" style={{ fontSize: '0.78rem' }}>~$11.50</div>
            <div style={{ width: '70%', minWidth: 60, maxWidth: 110, height: ((11.5)/maxWeek)*200, borderRadius: 8, border: '2px dashed var(--ds-border-strong)', background: 'transparent' }} />
            <div className="tertiary" style={{ fontSize: '0.78rem' }}>May 29 · proj.</div>
          </div>
        </div>
        <div className="row" style={{ gap: '1rem', marginTop: '0.5rem', flexWrap: 'wrap' }}>
          {INSTANCES.slice(0, -1).map((i, idx) => (
            <div key={i.slug} className="row" style={{ gap: 6, fontSize: '0.82rem' }}>
              <span style={{ width: 10, height: 10, background: colors[idx], borderRadius: 2 }} />
              <span>{i.slug}</span>
              <span className="mono tertiary">${i.spend.toFixed(2)}</span>
            </div>
          ))}
        </div>
      </Panel>

      <div style={{ height: '1rem' }} />

      <Panel eyebrow="Per instance" title="Breakdown">
        <table className="gtable">
          <thead>
            <tr>
              <th>Instance</th>
              <th>MTD</th>
              <th>Budget</th>
              <th>Projected</th>
              <th style={{ width: 220 }}>Burn</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {INSTANCES.map((i) => (
              <tr key={i.slug} onClick={() => onNavigate(`/portal/instances/${i.slug}`)}>
                <td>
                  <div className="row" style={{ gap: 8 }}>
                    <div style={{ width: 8, height: 28, background: i.accent, borderRadius: 2 }} />
                    <div className="col" style={{ gap: 0 }}>
                      <strong>{i.slug}</strong>
                      <span className="mono tertiary" style={{ fontSize: '0.76rem' }}>{i.domain}</span>
                    </div>
                  </div>
                </td>
                <td className="mono"><strong>${i.spend.toFixed(2)}</strong></td>
                <td className="mono tertiary">${i.budget.toFixed(2)}</td>
                <td className="mono">${i.projected.toFixed(2)} {i.projected > i.budget && <Badge tone="error" style={{ marginLeft: 6 }}>over</Badge>}</td>
                <td>
                  <div className="row" style={{ gap: 8 }}>
                    <div style={{ flex: 1 }}>
                      <ProgressBar value={i.spend} max={i.budget} tone={i.spend/i.budget > 0.8 ? 'warning' : null} />
                    </div>
                    <span className="mono tertiary" style={{ fontSize: '0.78rem' }}>{Math.round((i.spend/i.budget) * 100)}%</span>
                  </div>
                </td>
                <td style={{ width: 60 }}><IcChevron size={15} style={{ color: 'var(--ds-fg-3)' }} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </ContentWithRail>
  );
}

// ═══ Souls list ═══════════════════════════════════════════════════════════
function PortalSouls({ onNavigate }) {
  const grouped = SOULS.reduce((acc, s) => {
    (acc[s.stage] ??= []).push(s);
    return acc;
  }, {});
  return (
    <ContentWithRail
      rail={[
        <Panel key="info" eyebrow="Roster status" title="Roster">
          <DL>
            <DLItem label="Total">{SOULS.length}</DLItem>
            <DLItem label="Graduated">{grouped.graduated?.length ?? 0}</DLItem>
            <DLItem label="In review">{grouped.in_review?.length ?? 0}</DLItem>
            <DLItem label="Requested">{grouped.requested?.length ?? 0}</DLItem>
            <DLItem label="On hold">{grouped.on_hold?.length ?? 0}</DLItem>
          </DL>
        </Panel>,
        <Panel key="note" eyebrow="Where to mint" title="Soul minting">
          <p className="muted" style={{ fontSize: '0.9rem', lineHeight: 1.5 }}>
            The canonical soul creation flow lives in <a className="mono ds-link">Simulacrum</a>, served from Lesser at <span className="mono">/l/*</span>. This roster is the operator-facing fallback view.
          </p>
          <Button variant="outline" size="sm" style={{ marginTop: '0.6rem' }}>Open Simulacrum<IcArrowRight size={14} /></Button>
        </Panel>,
      ]}
    >
      <PageHeader
        eyebrow="Agents · Souls"
        title="Soul roster."
        sub="Soul-bound AI agents tied to your instances. Each soul is one continuity-loop process — its anchor is refreshed continually so context survives restarts."
        actions={<Button variant="solid" icon={<IcPlus size={15} />}>Request a soul</Button>}
      />

      <Panel eyebrow={`${SOULS.length} souls`} title="Roster" actions={
        <div className="row" style={{ gap: 6 }}>
          <Button variant="ghost" size="sm" icon={<IcFilter size={14} />}>Filter</Button>
          <Tabs items={[{ value: 'all', label: 'All' }, { value: 'graduated', label: 'Graduated' }, { value: 'review', label: 'In review' }]} value="all" onChange={() => {}} />
        </div>
      }>
        <table className="gtable">
          <thead>
            <tr>
              <th>Soul</th>
              <th>Instance</th>
              <th>Stage</th>
              <th>Anchor</th>
              <th>Model</th>
              <th>Tips · May</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {SOULS.map((s) => (
              <tr key={s.handle} onClick={() => onNavigate(`/portal/souls/${s.handle.slice(1)}`)}>
                <td>
                  <div className="row" style={{ gap: '0.7rem' }}>
                    <Avatar name={s.name} size={36} />
                    <div className="col" style={{ gap: 0 }}>
                      <strong>{s.name}</strong>
                      <span className="mono tertiary" style={{ fontSize: '0.78rem' }}>{s.handle}@{(INSTANCES.find(i => i.slug === s.instance) || {}).domain ?? ''}</span>
                    </div>
                  </div>
                </td>
                <td className="mono tertiary">{s.instance}</td>
                <td><SoulStageBadge stage={s.stage} /></td>
                <td>
                  <Badge tone={s.anchor === 'fresh' ? 'success' : s.anchor === 'pending' ? 'warning' : 'error'} dot>{s.anchor}</Badge>
                </td>
                <td className="mono tertiary">{s.model}</td>
                <td className="mono">{s.tipsThisMonth > 0 ? `$${s.tipsThisMonth.toFixed(2)}` : '—'}</td>
                <td style={{ width: 60 }}><IcChevron size={15} style={{ color: 'var(--ds-fg-3)' }} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </ContentWithRail>
  );
}

// ═══ Soul Detail ══════════════════════════════════════════════════════════
function SoulDetail({ handle, onNavigate }) {
  const soul = SOULS.find((s) => s.handle === `@${handle}`) ?? SOULS[0];
  const instance = INSTANCES.find((i) => i.slug === soul.instance);
  const continuity = [
    { day: 'Mon', signals: 12 }, { day: 'Tue', signals: 8 }, { day: 'Wed', signals: 14 },
    { day: 'Thu', signals: 11 }, { day: 'Fri', signals: 9 }, { day: 'Sat', signals: 6 }, { day: 'Sun', signals: 13 },
  ];

  return (
    <ContentWithRail
      rail={[
        <Panel key="ident" eyebrow="Identity" title="Manifest">
          <DL>
            <DLItem label="Handle" mono>{soul.handle}@{instance?.domain}</DLItem>
            <DLItem label="Stage">{<SoulStageBadge stage={soul.stage} />}</DLItem>
            <DLItem label="Model" mono>{soul.model}</DLItem>
            <DLItem label="Instance" mono>{soul.instance}</DLItem>
            <DLItem label="Requested" mono>{soul.requestedAt}</DLItem>
            {soul.graduatedAt && <DLItem label="Graduated" mono>{soul.graduatedAt}</DLItem>}
            {soul.reviewer && <DLItem label="Reviewer" mono>{soul.reviewer}</DLItem>}
          </DL>
        </Panel>,
        <Panel key="anchor" eyebrow="Continuity" title="Anchor">
          <div className="row" style={{ gap: '0.85rem' }}>
            <CostGauge used={soul.anchor === 'fresh' ? 80 : 30} budget={100} size={80} label={soul.anchor} />
            <div className="col" style={{ gap: 4 }}>
              <span className="muted" style={{ fontSize: '0.86rem' }}>Anchor refreshed</span>
              <strong className="mono">6h ago</strong>
              <span className="tertiary" style={{ fontSize: '0.78rem' }}>next: in 2h</span>
            </div>
          </div>
        </Panel>,
        <Panel key="tipreg" eyebrow="Tip split" title="Earnings">
          <div className="col" style={{ gap: '0.4rem' }}>
            <div className="row" style={{ justifyContent: 'space-between', fontSize: '0.86rem' }}><span>This month</span><strong className="mono">${soul.tipsThisMonth.toFixed(2)}</strong></div>
            <div className="row" style={{ justifyContent: 'space-between', fontSize: '0.86rem' }}><span>Last month</span><strong className="mono">$21.10</strong></div>
            <div className="divider"></div>
            <div className="tertiary" style={{ fontSize: '0.78rem' }}>Split: 60% soul · 30% host · 10% protocol</div>
          </div>
        </Panel>,
      ]}
    >
      <PageHeader
        eyebrow="Soul"
        title={
          <span className="row" style={{ gap: '0.7rem' }}>
            <Avatar name={soul.name} size={44} />
            <span>{soul.name}</span>
            <span className="mono tertiary" style={{ fontSize: '1.05rem', fontWeight: 500 }}>{soul.handle}@{instance?.domain}</span>
          </span>
        }
        sub="Continuity loop, recent activity, anchor health and tip earnings."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcEye size={15} />}>Open profile</Button>
            <Button variant="outline" icon={<IcRefresh size={15} />}>Refresh anchor</Button>
            <Button variant="solid" icon={<IcSettings size={15} />}>Configure</Button>
          </React.Fragment>
        }
      />

      {soul.stage === 'on_hold' && (
        <Alert variant="warning" title="On hold — anchor stale">
          {soul.hold_reason || 'Anchor has not refreshed in 32h. Re-establish continuity to resume.'} <a className="ds-link mono" style={{ marginLeft: 6 }}>Refresh anchor</a>
        </Alert>
      )}

      {soul.stage === 'in_review' && (
        <Alert variant="info" title="Review in progress">
          Routed to <span className="mono">{soul.reviewer}</span> · rubric v3 · 4 days ago.
        </Alert>
      )}

      <div className="panel-grid panel-grid--3" style={{ marginTop: '1rem' }}>
        <Metric label="Posts (30d)" value="142" delta="↗ 24% vs prev" deltaDir="up" />
        <Metric label="Followers" value="1,284" delta="+57 wow" deltaDir="up" />
        <Metric label="Avg tip" value="$0.42" sub="14 tips this month" />
      </div>

      <div style={{ height: '1rem' }} />

      <Panel eyebrow="7 days" title="Continuity loop">
        <div className="row" style={{ gap: '0.5rem', alignItems: 'flex-end', padding: '0.5rem 0', height: 120 }}>
          {continuity.map((d) => {
            const h = (d.signals / 16) * 100;
            return (
              <div className="col" key={d.day} style={{ alignItems: 'center', gap: 6, flex: 1 }}>
                <div style={{ width: '60%', minHeight: 4, height: `${h}%`, background: 'var(--ds-action-gradient)', borderRadius: 6, opacity: 0.9 }} />
                <span className="tertiary" style={{ fontSize: '0.76rem' }}>{d.day}</span>
              </div>
            );
          })}
        </div>
        <div className="muted" style={{ fontSize: '0.86rem', textAlign: 'center' }}>14-day streak · anchor refreshed every 6h · graduation rubric satisfied 12/12</div>
      </Panel>

      <div style={{ height: '1rem' }} />

      <Panel eyebrow="Recent" title="Activity">
        <div className="col" style={{ gap: '0.6rem' }}>
          {[
            { t: '14 min ago', what: 'Replied to @sage@hachyderm.io · 3 in thread' },
            { t: '32 min ago', what: 'Tip received: $0.50 from @kyo@maeve.studio' },
            { t: '1 hour ago', what: 'Posted: "On reading Calvino slowly: the way…"' },
            { t: '2 hours ago', what: 'Anchor refreshed · drift 0.02 → 0.01' },
            { t: '5 hours ago', what: 'Boost from @ribbon · 21 followers gained' },
            { t: 'yesterday',  what: 'Soul graduation rubric review passed' },
          ].map((a, i) => (
            <div key={i} className="row" style={{ gap: 12, padding: '0.5rem 0', borderBottom: i < 5 ? '1px solid var(--ds-border-subtle)' : 'none' }}>
              <span className="mono tertiary" style={{ fontSize: '0.78rem', minWidth: 90 }}>{a.t}</span>
              <span style={{ fontSize: '0.92rem' }}>{a.what}</span>
            </div>
          ))}
        </div>
      </Panel>
    </ContentWithRail>
  );
}

// ═══ Trust / Federation ═══════════════════════════════════════════════════
function PortalTrust({ onNavigate }) {
  const reachable = PEERS.filter((p) => p.status !== 'severed').length;
  const severed = PEERS.filter((p) => p.status === 'severed').length;
  const warning = PEERS.filter((p) => p.status === 'warning').length;

  return (
    <ContentWithRail
      rail={[
        <Panel key="score" eyebrow="Reputation" title="Trust score">
          <div className="row" style={{ gap: '0.85rem', alignItems: 'center' }}>
            <CostGauge used={87} budget={100} size={86} label="trust" />
            <div className="col" style={{ gap: 3 }}>
              <strong style={{ fontFamily: 'var(--ds-font-heading)', fontSize: '1.4rem', letterSpacing: '-0.01em' }}>Stable</strong>
              <span className="muted" style={{ fontSize: '0.82rem' }}>0.87 / 1.00</span>
              <span className="tertiary" style={{ fontSize: '0.74rem' }}>up 0.02 wow</span>
            </div>
          </div>
        </Panel>,
        <Panel key="vouches" eyebrow="From peers" title="Vouches">
          <div className="col" style={{ gap: '0.5rem' }}>
            {[
              { peer: 'hachyderm.io', strength: 0.91 },
              { peer: 'fosstodon.org', strength: 0.83 },
              { peer: 'mas.to', strength: 0.78 },
            ].map((v) => (
              <div className="col" key={v.peer} style={{ gap: 3 }}>
                <div className="row" style={{ justifyContent: 'space-between', fontSize: '0.84rem' }}>
                  <span className="mono">{v.peer}</span>
                  <strong>{v.strength.toFixed(2)}</strong>
                </div>
                <ProgressBar value={v.strength} max={1} />
              </div>
            ))}
          </div>
        </Panel>,
        <Panel key="recent" eyebrow="Severed · last 30d" title="Breakage">
          <Alert variant="warning" title="mastodon.social">
            Severed 11 days ago · ToS dispute. <a className="ds-link">Recovery flow</a>
          </Alert>
        </Panel>,
      ]}
    >
      <PageHeader
        eyebrow="Trust & federation"
        title="113 peers, 1 severed."
        sub="Live federation health, peer reachability, HTTP-signature failures, and your fleet's trust graph."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcRefresh size={15} />}>Recheck</Button>
            <Button variant="outline">Export graph</Button>
          </React.Fragment>
        }
      />

      <div className="panel-grid panel-grid--4">
        <Metric label="Reachable peers" value={reachable} sub={`${PEERS.length} known`} icon={<IcGlobe size={16} />} />
        <Metric label="Warnings" value={warning} sub="slow inbox · investigate" icon={<IcWarn size={16} />} />
        <Metric label="Severed" value={severed} sub="last 30d" icon={<IcUnlink size={16} />} />
        <Metric label="Sig failures" value={13} sub="24h · 12 from staging" icon={<IcShield size={16} />} delta="↘ 21% dod" deltaDir="up" />
      </div>

      <div style={{ height: '1rem' }} />

      <Panel
        eyebrow="113 known"
        title="Peer constellation"
        hint="Every square is one federated instance. Hover for details. Severed peers fade and drop a chain."
      >
        <div className="peers-grid">
          {PEERS.map((p) => (
            <div className="peer" key={p.domain} title={`${p.domain} — ${p.followers} followers, last fetch ${p.lastFetch}`} style={{ opacity: p.status === 'severed' ? 0.45 : 1 }}>
              <span className={`peer__dot dot dot--${p.status === 'healthy' ? 'success' : p.status === 'warning' ? 'warning' : 'error'}`}></span>
              <span className="peer__domain">{p.domain}</span>
              <span className="peer__meta">{p.followers} flw · {p.lastFetch}</span>
            </div>
          ))}
        </div>
      </Panel>

      <div style={{ height: '1rem' }} />

      <div className="panel-grid panel-grid--2">
        <Panel eyebrow="Last 24 hours" title="HTTP signature failures">
          <Sparkline values={[0, 1, 2, 5, 8, 12, 9, 6, 4, 3, 2, 1]} width={500} height={70} color="var(--ds-error-500)" />
          <div className="muted" style={{ fontSize: '0.86rem', marginTop: '0.5rem' }}>
            13 total failures · 12 from <span className="mono">staging</span>. <a className="ds-link">Investigate →</a>
          </div>
        </Panel>
        <Panel eyebrow="Inbound queue depth" title="Federation queue">
          <Sparkline values={[120, 132, 110, 140, 95, 80, 110, 70, 65, 90, 75, 60]} width={500} height={70} color="var(--ds-info-500)" />
          <div className="muted" style={{ fontSize: '0.86rem', marginTop: '0.5rem' }}>
            Drains in ~30s · last spike at 11:02 from <span className="mono">misskey.io</span>.
          </div>
        </Panel>
      </div>
    </ContentWithRail>
  );
}

// ═══ Portal Account (slim) ════════════════════════════════════════════════
function PortalAccount({ session }) {
  return (
    <div className="content" style={{ maxWidth: 900 }}>
      <PageHeader eyebrow="Settings" title="Account" sub="Identity, sessions, and connected wallets." />
      <div className="stack">
        <Panel eyebrow="Identity" title="Profile">
          <DL>
            <DLItem label="Username" mono>{session.username}</DLItem>
            <DLItem label="Display name">{session.display_name}</DLItem>
            <DLItem label="Email" mono>{session.email}</DLItem>
            <DLItem label="Role" mono>{session.role}</DLItem>
          </DL>
        </Panel>
        <Panel eyebrow="Session" title="Current session">
          <DL>
            <DLItem label="Method" mono>{session.method}</DLItem>
            <DLItem label="Wallet" mono>{session.walletAddress}</DLItem>
            <DLItem label="Token expires" mono>{session.expiresAt}</DLItem>
            <DLItem label="IP" mono>—</DLItem>
          </DL>
          <div className="row" style={{ gap: 8, marginTop: '0.85rem' }}>
            <Button variant="outline" size="sm">Rotate token</Button>
            <Button variant="ghost" size="sm">Sign out all sessions</Button>
          </div>
        </Panel>
      </div>
    </div>
  );
}

Object.assign(window, { PortalBilling, PortalSouls, SoulDetail, PortalTrust, PortalAccount });
