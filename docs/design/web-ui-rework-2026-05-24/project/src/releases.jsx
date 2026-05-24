/* releases.jsx — Stack & Releases components for Lesser + Lesser-body.
 *
 * Surface 1 — instance Overview: <StackCard instance />
 * Surface 2 — operator console:  <OperatorReleases />
 *
 * Job kinds (used by Provisioning):
 *   provision      — full CDK deploy of a new instance
 *   update-lesser  — bumps the lesser stack on an existing instance
 *   update-body    — bumps lesser-body on an existing instance.
 *                    REQUIRES a wire-mcp follow-up against the partner lesser.
 *   wire-mcp       — registers the body's MCP tools in lesser (the linking step)
 */

// ─── version compare (lexical works for our v2026.MM.DD scheme) ──────────
function isOutdated(have, latest) {
  if (!have || !latest) return false;
  // Ignore -beta tags as outdated targets
  if (have.includes('beta') && !latest.includes('beta')) return true;
  return have !== latest && have < latest;
}

function ChannelBadge({ channel }) {
  if (channel === 'beta') return <Badge tone="accent">beta</Badge>;
  return <Badge>stable</Badge>;
}

// ───────────────────────────────────────────────────────────────────────
//  Stack card — shows lesser + body versions for a single instance
// ───────────────────────────────────────────────────────────────────────

function StackCard({ instance, onNavigate }) {
  const stack = instance.stack || {};
  const lesserOutdated = isOutdated(stack.lesser, LATEST_LESSER);
  const bodyOutdated   = stack.body && isOutdated(stack.body, LATEST_BODY);
  const mcpDrift       = stack.body && (!stack.mcpWired || stack.mcpDrift);

  return (
    <Panel
      eyebrow="Stack & releases"
      title="Stack"
      actions={
        <div className="row" style={{ gap: 6 }}>
          <Button variant="ghost" size="sm" icon={<IcGitBranch size={14} />} onClick={() => onNavigate?.('/operator/releases')}>Releases</Button>
        </div>
      }
    >
      <div className="stack-grid">
        <StackRow
          name="Lesser"
          sub="ActivityPub server · core"
          version={stack.lesser}
          latest={LATEST_LESSER}
          outdated={lesserOutdated}
          icon={<IcServer size={20} />}
          accent="var(--ds-secondary-500)"
          installed
          onUpdate={() => onNavigate?.(`/operator/provisioning/upd-${instance.slug}-lesser`)}
        />
        <StackRow
          name="Lesser-body"
          sub={stack.body ? 'Agentic worker · soul host' : 'Not installed · enables soul agents'}
          version={stack.body}
          latest={LATEST_BODY}
          outdated={bodyOutdated}
          icon={<IcCpu size={20} />}
          accent="var(--ds-primary-500)"
          installed={!!stack.body}
          ctaLabel={stack.body ? null : 'Add agentic'}
          onUpdate={() => onNavigate?.(`/operator/provisioning/upd-${instance.slug}-body`)}
        />
        {stack.body && (
          <McpWireRow
            instance={instance}
            mcpWired={stack.mcpWired}
            mcpDrift={mcpDrift}
            onWire={() => onNavigate?.(`/operator/provisioning/wire-${instance.slug}`)}
          />
        )}
      </div>
    </Panel>
  );
}

function StackRow({ name, sub, version, latest, outdated, icon, accent, installed, ctaLabel, onUpdate }) {
  return (
    <div className="stack-row">
      <div className="stack-row__icon" style={{ color: accent }}>{icon}</div>
      <div className="stack-row__body">
        <div className="row" style={{ gap: 10, flexWrap: 'wrap', alignItems: 'baseline' }}>
          <strong style={{ fontFamily: 'var(--ds-font-heading)', fontSize: '1.1rem', letterSpacing: '-0.01em' }}>{name}</strong>
          {installed ? (
            <React.Fragment>
              <span className="mono" style={{ fontSize: '0.84rem' }}>{version}</span>
              {outdated && <Badge tone="warning" dot>update available</Badge>}
              {!outdated && installed && <Badge tone="success" dot>up to date</Badge>}
            </React.Fragment>
          ) : (
            <Badge>not installed</Badge>
          )}
        </div>
        <div className="tertiary" style={{ fontSize: '0.8rem', marginTop: 2 }}>
          {sub}{installed && outdated && <span> · latest is <span className="mono">{latest}</span></span>}
        </div>
      </div>
      <div className="stack-row__cta">
        {ctaLabel ? (
          <Button variant="outline" size="sm" icon={<IcPlus size={14} />} onClick={onUpdate}>{ctaLabel}</Button>
        ) : outdated ? (
          <Button variant="solid" size="sm" icon={<IcArrowRight size={14} />} onClick={onUpdate}>
            Update to {latest}
          </Button>
        ) : (
          <Button variant="ghost" size="sm" onClick={onUpdate}>Manage</Button>
        )}
      </div>
    </div>
  );
}

function McpWireRow({ instance, mcpWired, mcpDrift, onWire }) {
  return (
    <div className="stack-row stack-row--coupling" style={{ borderLeftColor: mcpDrift ? 'var(--ds-warning-500)' : 'var(--ds-success-500)' }}>
      <div className="stack-row__icon" style={{ color: mcpDrift ? 'var(--ds-warning-500)' : 'var(--ds-success-500)' }}>
        <IcLink size={20} />
      </div>
      <div className="stack-row__body">
        <div className="row" style={{ gap: 10, flexWrap: 'wrap', alignItems: 'baseline' }}>
          <strong style={{ fontFamily: 'var(--ds-font-heading)', fontSize: '1.1rem', letterSpacing: '-0.01em' }}>MCP wiring</strong>
          {mcpDrift
            ? <Badge tone="warning" dot>re-wire required</Badge>
            : <Badge tone="success" dot>linked</Badge>}
        </div>
        <div className="tertiary" style={{ fontSize: '0.8rem', marginTop: 2 }}>
          {mcpDrift
            ? <span>Body was updated; Lesser hasn't yet registered the new MCP surface. Runs in <span className="mono">~30s</span>.</span>
            : <span>Body's MCP tools are registered in Lesser. Re-runs automatically after any body update.</span>}
        </div>
      </div>
      <div className="stack-row__cta">
        {mcpDrift
          ? <Button variant="solid" size="sm" icon={<IcZap size={14} />} onClick={onWire}>Wire now</Button>
          : <Button variant="ghost" size="sm" onClick={onWire}>Re-wire</Button>}
      </div>
    </div>
  );
}

// ───────────────────────────────────────────────────────────────────────
//  Operator > Releases — fleet-wide release management
// ───────────────────────────────────────────────────────────────────────

function OperatorReleases({ onNavigate }) {
  // adoption stats
  const total = INSTANCES.length;
  const lesserOnLatest = INSTANCES.filter((i) => i.stack?.lesser === LATEST_LESSER).length;
  const bodyInstalled  = INSTANCES.filter((i) => i.stack?.body).length;
  const bodyOnLatest   = INSTANCES.filter((i) => i.stack?.body === LATEST_BODY).length;
  const mcpDrift       = INSTANCES.filter((i) => i.stack?.body && (!i.stack.mcpWired || i.stack.mcpDrift)).length;

  // adoption-by-version
  const adoptionLesser = RELEASES.lesser.map((r) => ({
    ...r,
    count: INSTANCES.filter((i) => i.stack?.lesser === r.version).length,
  }));
  const adoptionBody = RELEASES.body.map((r) => ({
    ...r,
    count: INSTANCES.filter((i) => i.stack?.body === r.version).length,
  }));

  return (
    <ContentWithRail
      rail={[
        <Panel key="latest-l" eyebrow="Lesser · stable" title={LATEST_LESSER}>
          <p className="muted" style={{ fontSize: '0.88rem', lineHeight: 1.55 }}>
            {RELEASES.lesser.find((r) => r.version === LATEST_LESSER)?.summary}
          </p>
          <div className="row" style={{ gap: 6, marginTop: '0.55rem', flexWrap: 'wrap' }}>
            <Button variant="solid" size="sm" icon={<IcArrowRight size={14} />}>Roll fleet to latest</Button>
            <Button variant="ghost" size="sm">Notes</Button>
          </div>
        </Panel>,
        <Panel key="latest-b" eyebrow="Lesser-body · stable" title={LATEST_BODY}>
          <p className="muted" style={{ fontSize: '0.88rem', lineHeight: 1.55 }}>
            {RELEASES.body.find((r) => r.version === LATEST_BODY)?.summary}
          </p>
          <Alert variant="info" title="After any body update">
            An MCP-wiring job runs automatically against each partner Lesser instance.
          </Alert>
          <div className="row" style={{ gap: 6, marginTop: '0.55rem', flexWrap: 'wrap' }}>
            <Button variant="solid" size="sm" icon={<IcArrowRight size={14} />}>Roll agentic fleet</Button>
            <Button variant="ghost" size="sm">Notes</Button>
          </div>
        </Panel>,
        mcpDrift > 0 && (
          <Panel key="drift" eyebrow="Heads up" title="MCP drift">
            <Alert variant="warning" title={`${mcpDrift} instance${mcpDrift === 1 ? '' : 's'} need re-wiring`}>
              Body was updated but Lesser hasn't yet registered the new tools.
            </Alert>
            <div style={{ marginTop: '0.6rem' }}>
              <Button variant="outline" size="sm" icon={<IcZap size={14} />}>Wire all now</Button>
            </div>
          </Panel>
        ),
      ].filter(Boolean)}
    >
      <PageHeader
        eyebrow="Operator · releases"
        title="Stack releases."
        sub="Two independent release channels: Lesser core and Lesser-body. Body updates require an MCP-wiring step against the partner Lesser; the system queues it automatically."
        actions={
          <React.Fragment>
            <Button variant="outline" icon={<IcRefresh size={15} />}>Refresh manifests</Button>
            <Button variant="outline" icon={<IcDownload size={15} />}>Export adoption</Button>
          </React.Fragment>
        }
      />

      <div className="panel-grid panel-grid--4">
        <Metric label="Instances" value={total} sub={`${INSTANCES.filter((i) => i.status === 'healthy').length} healthy`} icon={<IcServer size={16} />} />
        <Metric label="Lesser · on latest" value={`${lesserOnLatest} / ${total}`} sub={`current: ${LATEST_LESSER}`} icon={<IcServer size={16} />} />
        <Metric label="Body · installed" value={`${bodyInstalled} / ${total}`} sub={`${bodyOnLatest} on latest`} icon={<IcCpu size={16} />} />
        <Metric label="MCP drift" value={mcpDrift} sub="instances needing re-wire" icon={<IcLink size={16} />} delta={mcpDrift > 0 ? '↗ investigate' : 'all clear'} deltaDir={mcpDrift > 0 ? 'down' : 'up'} />
      </div>

      <div style={{ height: '1rem' }} />

      {/* The two release timelines, side by side */}
      <div className="panel-grid panel-grid--2">
        <ReleaseTimeline
          title="Lesser"
          subtitle="core ActivityPub server"
          icon={<IcServer size={18} />}
          accent="var(--ds-secondary-500)"
          releases={adoptionLesser}
          latest={LATEST_LESSER}
          total={total}
          onUpdateAll={() => {}}
        />
        <ReleaseTimeline
          title="Lesser-body"
          subtitle="agentic worker · soul host"
          icon={<IcCpu size={18} />}
          accent="var(--ds-primary-500)"
          releases={adoptionBody}
          latest={LATEST_BODY}
          total={bodyInstalled}
          totalLabel="agentic instances"
          requiresWire
          onUpdateAll={() => {}}
        />
      </div>

      <div style={{ height: '1rem' }} />

      {/* Per-instance stack matrix */}
      <Panel eyebrow="Per instance" title="Stack matrix" hint="What's running where. Click any row to act on a single instance.">
        <table className="gtable">
          <thead>
            <tr>
              <th>Instance</th>
              <th>Lesser</th>
              <th>Lesser-body</th>
              <th>MCP wiring</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {INSTANCES.map((i) => {
              const lOut = isOutdated(i.stack?.lesser, LATEST_LESSER);
              const bOut = i.stack?.body && isOutdated(i.stack?.body, LATEST_BODY);
              const drift = i.stack?.body && (!i.stack.mcpWired || i.stack.mcpDrift);
              return (
                <tr key={i.slug} onClick={() => onNavigate?.(`/portal/instances/${i.slug}`)}>
                  <td>
                    <div className="col" style={{ gap: 0 }}>
                      <strong>{i.slug}</strong>
                      <span className="mono tertiary" style={{ fontSize: '0.76rem' }}>{i.domain}</span>
                    </div>
                  </td>
                  <td>
                    <div className="row" style={{ gap: 6 }}>
                      <span className="mono">{i.stack?.lesser}</span>
                      {lOut && <Badge tone="warning" dot>update</Badge>}
                      {i.stack?.lesser?.includes('beta') && <Badge tone="accent">beta</Badge>}
                    </div>
                  </td>
                  <td>
                    {i.stack?.body ? (
                      <div className="row" style={{ gap: 6 }}>
                        <span className="mono">{i.stack.body}</span>
                        {bOut && <Badge tone="warning" dot>update</Badge>}
                      </div>
                    ) : <span className="tertiary">—</span>}
                  </td>
                  <td>
                    {!i.stack?.body
                      ? <span className="tertiary">n/a</span>
                      : drift
                        ? <Badge tone="warning" dot>re-wire required</Badge>
                        : <Badge tone="success" dot>linked</Badge>}
                  </td>
                  <td style={{ width: 220 }}>
                    {drift && (
                      <Button variant="outline" size="sm" icon={<IcZap size={13} />} onClick={(e) => { e.stopPropagation(); onNavigate?.(`/operator/provisioning/wire-${i.slug}`); }}>Wire MCP</Button>
                    )}
                    {!drift && (lOut || bOut) && (
                      <Button variant="outline" size="sm" icon={<IcArrowRight size={13} />} onClick={(e) => { e.stopPropagation(); onNavigate?.(`/operator/provisioning/upd-${i.slug}-${lOut ? 'lesser' : 'body'}`); }}>Update</Button>
                    )}
                    {!drift && !lOut && !bOut && (
                      <span className="tertiary mono" style={{ fontSize: '0.78rem' }}>nothing to do</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Panel>
    </ContentWithRail>
  );
}

function ReleaseTimeline({ title, subtitle, icon, accent, releases, latest, total, totalLabel = 'instances', requiresWire, onUpdateAll }) {
  return (
    <Panel
      eyebrow={subtitle}
      title={<span className="row" style={{ gap: 8 }}><span style={{ color: accent }}>{icon}</span>{title}</span>}
      actions={<Button variant="outline" size="sm" icon={<IcArrowRight size={13} />} onClick={onUpdateAll}>Roll all</Button>}
    >
      <div className="release-list">
        {releases.map((r) => {
          const pct = total > 0 ? (r.count / total) * 100 : 0;
          const isLatest = r.version === latest;
          return (
            <div
              key={r.version}
              className={['release', isLatest && 'release--latest'].filter(Boolean).join(' ')}
            >
              <div className="release__head">
                <div className="release__dot" style={{ background: isLatest ? accent : 'var(--ds-border-default)' }} />
                <div className="col" style={{ gap: 2, flex: 1, minWidth: 0 }}>
                  <div className="row" style={{ gap: 8, alignItems: 'baseline', flexWrap: 'wrap' }}>
                    <span className="mono" style={{ fontWeight: 700, fontSize: '0.95rem' }}>{r.version}</span>
                    <ChannelBadge channel={r.channel} />
                    {isLatest && <Badge tone="accent">latest</Badge>}
                    {r.breaking && <Badge tone="warning">breaking</Badge>}
                    <span className="tertiary mono" style={{ fontSize: '0.74rem', marginLeft: 'auto' }}>{r.released}</span>
                  </div>
                  <p className="muted" style={{ fontSize: '0.84rem', lineHeight: 1.5, margin: 0 }}>{r.summary}</p>
                  {r.breaking && r.breakingNote && (
                    <p style={{ fontSize: '0.78rem', color: 'var(--ds-warning-700)', marginTop: 2 }} className="row">
                      <IcWarn size={13} /> {r.breakingNote}
                    </p>
                  )}
                </div>
              </div>
              <div className="release__adoption">
                <span className="tertiary mono" style={{ fontSize: '0.74rem', minWidth: 60 }}>{r.count} / {total} {totalLabel}</span>
                <div style={{ flex: 1 }}>
                  <ProgressBar value={pct} max={100} tone={isLatest ? 'success' : null} />
                </div>
                <span className="mono tertiary" style={{ fontSize: '0.74rem', minWidth: 36, textAlign: 'right' }}>{Math.round(pct)}%</span>
              </div>
            </div>
          );
        })}
      </div>
      {requiresWire && (
        <p className="muted" style={{ fontSize: '0.78rem', marginTop: '0.6rem', display: 'flex', gap: 6, alignItems: 'center' }}>
          <IcLink size={13} /> Every body update auto-triggers a <span className="mono">wire-mcp</span> job against the partner Lesser.
        </p>
      )}
    </Panel>
  );
}

Object.assign(window, { StackCard, OperatorReleases, isOutdated, ChannelBadge });
