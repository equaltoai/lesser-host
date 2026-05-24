/* shell.jsx — Three-column app chassis.
 * Sidebar (instance picker + nav) + topbar (crumbs + ⌘K) + content (+ optional rail).
 * Plus the Portal vs Operator variants, and the command palette overlay.
 */

// ─── Sidebar ─────────────────────────────────────────────────────────────

function BrandMark({ size = 36 }) {
  return (
    <div
      className="brand__mark"
      style={{ width: size, height: size, fontSize: size * 0.6 }}
      aria-label="Lesser brand mark"
    >&lt;</div>
  );
}

function Sidebar({ edition, groups, currentPath, onNavigate, session, footerExtra, operator = false }) {
  return (
    <aside className="sidebar">
      <div className="brand">
        <BrandMark />
        <div className="brand__wordmark">
          <span className="brand__wordmark-main">&lt;esser.host</span>
          <span className="brand__wordmark-edition">{edition}</span>
        </div>
      </div>

      <nav className="nav">
        {groups.map((g, gi) => (
          <div className="nav__group" key={gi}>
            {g.label && <div className="nav__label">{g.label}</div>}
            {g.items.map((it) => {
              const active = currentPath === it.path ||
                (it.startsWith && currentPath.startsWith(it.path) && it.path !== '/portal' && it.path !== '/operator') ||
                (it.path === '/portal' && currentPath === '/portal') ||
                (it.path === '/operator' && currentPath === '/operator');
              return (
                <div
                  key={it.path}
                  className={['nav__item', active && 'nav__item--active', it.alert && 'nav__item--alert'].filter(Boolean).join(' ')}
                  onClick={() => onNavigate(it.path)}
                  role="button"
                  tabIndex={0}
                >
                  <span className="nav__item-icon">{it.icon}</span>
                  <span>{it.label}</span>
                  {it.badge != null && <span className="nav__item-badge">{it.badge}</span>}
                </div>
              );
            })}
          </div>
        ))}
      </nav>

      <div className="sidebar__footer">
        {footerExtra}
        <div className="user-chip">
          <Avatar name={session.username} size={32} />
          <div style={{ minWidth: 0, flex: 1 }}>
            <div className="user-chip__name">@{session.username}</div>
            <div className="user-chip__role">{session.role} · {session.method}</div>
          </div>
          <button className="btn btn--ghost btn--icon" title="Sign out" onClick={() => onNavigate('/login')}>
            <IcLogout size={15} />
          </button>
        </div>
      </div>
    </aside>
  );
}

// ─── Topbar ──────────────────────────────────────────────────────────────

function Topbar({ crumbs, onOpenCmdK, right, operator = false }) {
  return (
    <div className="topbar">
      <div className="crumbs">
        {crumbs.map((c, i) => (
          <React.Fragment key={i}>
            {i > 0 && <span className="crumbs__sep">/</span>}
            {c.path
              ? <a className="crumbs__link" onClick={() => c.onClick?.()}>{c.label}</a>
              : <span className="crumbs__current">{c.label}</span>}
          </React.Fragment>
        ))}
      </div>
      <div className="topbar__spacer" />
      <button className="cmdk-trigger" onClick={onOpenCmdK}>
        <IcSearch size={15} />
        <span>Search instances, souls, jobs…</span>
        <span className="kbd">⌘K</span>
      </button>
      {right}
      <button className="topbar__action" title="Notifications"><IcBell size={16} /></button>
    </div>
  );
}

// ─── Right rail container ───────────────────────────────────────────────

function ContentWithRail({ children, rail }) {
  return (
    <div className="content content--with-rail">
      <div style={{ minWidth: 0 }}>{children}</div>
      {rail && <aside className="rail">{rail}</aside>}
    </div>
  );
}

// ─── Page header ─────────────────────────────────────────────────────────

function PageHeader({ eyebrow, title, sub, actions }) {
  return (
    <div className="page-header">
      <div style={{ minWidth: 0 }}>
        {eyebrow && <Eyebrow>{eyebrow}</Eyebrow>}
        <h1 className="page-header__title" style={{ marginTop: eyebrow ? 6 : 0 }}>{title}</h1>
        {sub && <p className="page-header__sub">{sub}</p>}
      </div>
      {actions && <div className="page-header__actions">{actions}</div>}
    </div>
  );
}

// ─── Portal Shell ────────────────────────────────────────────────────────

function PortalShell({ session, currentPath, onNavigate, onOpenCmdK, children, crumbs, topRight }) {
  const groups = [
    {
      label: 'Overview',
      items: [
        { path: '/portal',          label: 'Fleet',           icon: <IcDashboard size={16} /> },
        { path: '/portal/billing',  label: 'Cost & billing',  icon: <IcBilling size={16} />, startsWith: true },
        { path: '/portal/trust',    label: 'Trust',           icon: <IcTrust size={16} />, startsWith: true },
      ],
    },
    {
      label: 'Instances',
      items: INSTANCES.map((i) => ({
        path: `/portal/instances/${i.slug}`,
        label: i.slug,
        icon: <span className={`dot dot--${i.status === 'healthy' ? 'success' : i.status === 'warning' ? 'warning' : i.status === 'provisioning' ? '' : 'error'}`} />,
        startsWith: true,
        alert: i.status === 'warning',
        badge: i.status === 'warning' ? '!' : null,
      })),
    },
    {
      label: 'Agents',
      items: [
        { path: '/portal/souls',    label: 'Souls',           icon: <IcSouls size={16} />, startsWith: true, badge: SOULS.filter((s) => s.stage === 'requested' || s.stage === 'in_review').length || null },
      ],
    },
    {
      label: 'Settings',
      items: [
        { path: '/portal/account',  label: 'Account',         icon: <IcAccount size={16} /> },
      ],
    },
  ];

  return (
    <div className="shell">
      <Sidebar
        edition="Customer portal"
        groups={groups}
        currentPath={currentPath}
        onNavigate={onNavigate}
        session={session}
        footerExtra={
          (session.role === 'admin' || session.role === 'operator') && (
            <button className="btn btn--outline btn--sm" onClick={() => onNavigate('/operator')}>
              <IcShield size={14} />
              Operator console
            </button>
          )
        }
      />
      <div className="main">
        <Topbar crumbs={crumbs ?? [{ label: 'Portal', path: '/portal', onClick: () => onNavigate('/portal') }]} onOpenCmdK={onOpenCmdK} right={topRight} />
        {children}
      </div>
    </div>
  );
}

// ─── Operator Shell ──────────────────────────────────────────────────────

function OperatorShell({ session, currentPath, onNavigate, onOpenCmdK, children, crumbs, topRight }) {
  const counts = {
    domains: APPROVALS.domains.filter((d) => d.state === 'awaiting').length,
    users: APPROVALS.users.length,
    externals: APPROVALS.externals.length,
  };
  const groups = [
    {
      label: 'Operations',
      items: [
        { path: '/operator',                 label: 'Dashboard',  icon: <IcDashboard size={16} /> },
        { path: '/operator/provisioning',    label: 'Provisioning', icon: <IcProvision size={16} />, startsWith: true, badge: 1 },
        { path: '/operator/releases',        label: 'Releases',     icon: <IcGitBranch size={16} />, startsWith: true, badge: INSTANCES.filter((i) => i.stack?.body && (!i.stack.mcpWired || i.stack.mcpDrift)).length || null, alert: INSTANCES.some((i) => i.stack?.body && (!i.stack.mcpWired || i.stack.mcpDrift)) },
        { path: '/operator/audit',           label: 'Audit log',  icon: <IcAudit size={16} /> },
      ],
    },
    {
      label: 'Approvals',
      items: [
        { path: '/operator/approvals/domains',  label: 'Vanity domains', icon: <IcGlobe size={16} />, alert: counts.domains > 0, badge: counts.domains || null },
        { path: '/operator/approvals/users',    label: 'Users',          icon: <IcUsers size={16} />, badge: counts.users || null },
        { path: '/operator/approvals/external', label: 'External regs',  icon: <IcLink size={16} />, badge: counts.externals || null },
      ],
    },
    {
      label: 'Registries',
      items: [
        { path: '/operator/instances',     label: 'Instances',    icon: <IcServer size={16} /> },
        { path: '/operator/tip-registry',  label: 'Tip registry', icon: <IcWallet size={16} /> },
        { path: '/operator/soul',          label: 'Soul registry',icon: <IcSouls size={16} /> },
      ],
    },
  ];
  return (
    <div className="shell shell--operator">
      <Sidebar
        edition="Operator console"
        groups={groups}
        currentPath={currentPath}
        onNavigate={onNavigate}
        session={session}
        operator
        footerExtra={
          <button className="btn btn--outline btn--sm" onClick={() => onNavigate('/portal')}>
            <IcArrowLeft size={14} />
            Back to portal
          </button>
        }
      />
      <div className="main">
        <Topbar crumbs={crumbs ?? [{ label: 'Operator', path: '/operator', onClick: () => onNavigate('/operator') }]} onOpenCmdK={onOpenCmdK} right={topRight} operator />
        {children}
      </div>
    </div>
  );
}

// ─── Command palette ─────────────────────────────────────────────────────

function CommandPalette({ open, onClose, onNavigate }) {
  const [q, setQ] = React.useState('');
  const inputRef = React.useRef(null);
  React.useEffect(() => {
    if (open) {
      setQ('');
      setTimeout(() => inputRef.current?.focus(), 30);
    }
  }, [open]);
  if (!open) return null;

  const baseGroups = COMMAND_PALETTE;
  const instanceItems = INSTANCES.map((i) => ({
    id: `inst-${i.slug}`, label: i.slug,
    hint: i.domain,
    path: `/portal/instances/${i.slug}`,
    meta: i.stage,
  }));
  const soulItems = SOULS.map((s) => ({
    id: `soul-${s.handle}`, label: s.handle,
    hint: s.name,
    path: `/portal/souls/${s.handle.slice(1)}`,
    meta: s.stage.replace('_', ' '),
  }));

  const groups = [
    ...baseGroups,
    { group: 'Instances', items: instanceItems },
    { group: 'Souls', items: soulItems },
  ];

  const ql = q.trim().toLowerCase();
  const filtered = groups.map((g) => ({
    ...g,
    items: g.items.filter((it) =>
      !ql || it.label.toLowerCase().includes(ql) || it.hint?.toLowerCase().includes(ql)
    ),
  })).filter((g) => g.items.length > 0);

  return (
    <div className="cmdk-backdrop" onClick={onClose}>
      <div className="cmdk" onClick={(e) => e.stopPropagation()} role="dialog" aria-label="Command palette">
        <div className="cmdk__input">
          <IcSearch size={18} />
          <input
            ref={inputRef}
            placeholder="Search instances, souls, actions…"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Escape') onClose(); }}
          />
          <span className="kbd">esc</span>
        </div>
        <div style={{ maxHeight: '60vh', overflowY: 'auto' }}>
          {filtered.map((g, gi) => (
            <div className="cmdk__group" key={gi}>
              <div className="cmdk__group-label">{g.group}</div>
              {g.items.map((it) => (
                <div
                  key={it.id}
                  className="cmdk__item"
                  onClick={() => {
                    if (it.path) { onNavigate(it.path); onClose(); }
                  }}
                >
                  <IcArrowRight size={14} style={{ color: 'var(--ds-fg-3)' }} />
                  <span>{it.label}</span>
                  {it.hint && <span className="cmdk__item-meta">{it.hint}</span>}
                  {it.meta && <span className="badge" style={{ marginLeft: 8 }}>{it.meta}</span>}
                </div>
              ))}
            </div>
          ))}
          {filtered.length === 0 && (
            <div style={{ padding: '1.4rem', color: 'var(--ds-fg-3)', textAlign: 'center' }}>No matches.</div>
          )}
        </div>
      </div>
    </div>
  );
}

Object.assign(window, { Sidebar, Topbar, BrandMark, ContentWithRail, PageHeader, PortalShell, OperatorShell, CommandPalette });
