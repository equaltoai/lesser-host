/* primitives.jsx — React mirrors of the greater-components primitives.
 * (Card → Panel, Button, Badge, Alert, Tabs, DefinitionList, etc.)
 * All consume the --ds-* tokens from assets/tokens.css.
 */

// ─── Eyebrow ─────────────────────────────────────────────────────────────
function Eyebrow({ children, as: As = 'p', style }) {
  return <As className="eyebrow" style={style}>{children}</As>;
}

// ─── Button ──────────────────────────────────────────────────────────────
function Button({ variant = 'solid', size = 'md', icon, children, className = '', ...rest }) {
  const cls = ['btn', `btn--${variant}`, size === 'sm' && 'btn--sm', icon && !children && 'btn--icon', className].filter(Boolean).join(' ');
  return (
    <button className={cls} {...rest}>
      {icon}
      {children}
    </button>
  );
}

// ─── Badge ───────────────────────────────────────────────────────────────
function Badge({ tone = 'default', children, dot = false, className = '' }) {
  const cls = ['badge', tone !== 'default' && `badge--${tone}`, className].filter(Boolean).join(' ');
  return (
    <span className={cls}>
      {dot && <span className={`dot dot--${tone === 'default' ? '' : tone}`} style={{ width: 6, height: 6 }} />}
      {children}
    </span>
  );
}

// ─── Alert ───────────────────────────────────────────────────────────────
function Alert({ variant = 'info', title, children, icon }) {
  const Ic = icon ?? (variant === 'success' ? IcCheck : variant === 'warning' ? IcWarn : variant === 'error' ? IcX : IcInfo);
  return (
    <div className={`alert alert--${variant}`}>
      <div className="alert__icon"><Ic size={20} /></div>
      <div style={{ flex: 1, minWidth: 0 }}>
        {title && <div className="alert__title">{title}</div>}
        <div className="alert__body">{children}</div>
      </div>
    </div>
  );
}

// ─── Panel (= greater-components Card) ───────────────────────────────────
function Panel({ title, eyebrow, hint, actions, children, feature = false, className = '', padding, style }) {
  return (
    <section
      className={['panel', feature && 'panel--feature', className].filter(Boolean).join(' ')}
      style={{ padding, ...style }}
    >
      {(title || actions || eyebrow) && (
        <header className="panel__header">
          <div style={{ minWidth: 0 }}>
            {eyebrow && <Eyebrow>{eyebrow}</Eyebrow>}
            {title && <h2 className="panel__title" style={{ marginTop: eyebrow ? 4 : 0 }}>{title}</h2>}
            {hint && <p className="panel__hint">{hint}</p>}
          </div>
          {actions && <div className="panel__actions">{actions}</div>}
        </header>
      )}
      {children}
    </section>
  );
}

// ─── Tabs ────────────────────────────────────────────────────────────────
function Tabs({ items, value, onChange }) {
  return (
    <div className="tabs" role="tablist">
      {items.map((it) => (
        <button
          key={it.value}
          className={['tab', value === it.value && 'tab--active'].filter(Boolean).join(' ')}
          onClick={() => onChange(it.value)}
          role="tab"
          aria-selected={value === it.value}
        >
          {it.icon && <span style={{ display: 'inline-flex' }}>{it.icon}</span>}
          {it.label}
          {it.badge != null && <span className="badge" style={{ padding: '0 6px', fontSize: 10 }}>{it.badge}</span>}
        </button>
      ))}
    </div>
  );
}

// ─── DefinitionList ──────────────────────────────────────────────────────
function DL({ children }) { return <dl className="dl">{children}</dl>; }
function DLItem({ label, children, mono = false }) {
  return (
    <React.Fragment>
      <dt>{label}</dt>
      <dd className={mono ? 'mono' : ''}>{children}</dd>
    </React.Fragment>
  );
}

// ─── Metric tile ─────────────────────────────────────────────────────────
function Metric({ label, value, sub, delta, deltaDir, icon, accent }) {
  return (
    <div className="metric" style={{ position: 'relative' }}>
      <div className="row" style={{ justifyContent: 'space-between' }}>
        <span className="metric__label">{label}</span>
        {icon && <span style={{ color: 'var(--ds-fg-3)' }}>{icon}</span>}
      </div>
      <div className="metric__value" style={accent ? { color: accent } : null}>{value}</div>
      {sub && <div className="metric__delta">{sub}</div>}
      {delta && (
        <div className={`metric__delta ${deltaDir === 'up' ? 'metric__delta--up' : deltaDir === 'down' ? 'metric__delta--down' : ''}`}>
          {deltaDir === 'up' && '↗ '}
          {deltaDir === 'down' && '↘ '}
          {delta}
        </div>
      )}
    </div>
  );
}

// ─── Progress bar ────────────────────────────────────────────────────────
function ProgressBar({ value, max = 100, tone }) {
  const pct = Math.min(100, Math.max(0, (value / max) * 100));
  return (
    <div className="bar">
      <div className={`bar__fill ${tone ? `bar__fill--${tone}` : ''}`} style={{ width: `${pct}%` }} />
    </div>
  );
}

// ─── Sparkline (tiny SVG) ────────────────────────────────────────────────
function Sparkline({ values, width = 120, height = 32, color = 'var(--ds-secondary-500)', fill = true }) {
  if (!values?.length) return null;
  const min = Math.min(...values), max = Math.max(...values);
  const range = max - min || 1;
  const step = width / (values.length - 1 || 1);
  const pts = values.map((v, i) => [i * step, height - ((v - min) / range) * (height - 4) - 2]);
  const d = pts.map((p, i) => `${i === 0 ? 'M' : 'L'}${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ');
  const area = `${d} L${width},${height} L0,${height} Z`;
  return (
    <svg className="sparkline-svg" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" style={{ height }}>
      {fill && <path d={area} fill={color} opacity="0.12" />}
      <path d={d} fill="none" stroke={color} strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// ─── Switch ──────────────────────────────────────────────────────────────
function Switch({ checked, onChange }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      className={`switch ${checked ? 'switch--on' : ''}`}
      onClick={() => onChange?.(!checked)}
    />
  );
}

// ─── Copy chip ───────────────────────────────────────────────────────────
function CopyChip({ value, label }) {
  const [done, setDone] = React.useState(false);
  return (
    <button
      type="button"
      className="btn btn--ghost btn--sm"
      style={{ fontFamily: 'var(--ds-font-mono)', fontSize: '0.8rem', padding: '0.2rem 0.55rem', gap: 6 }}
      onClick={() => { navigator.clipboard?.writeText(value); setDone(true); setTimeout(() => setDone(false), 1300); }}
      title="Copy"
    >
      <span>{label ?? value}</span>
      {done ? <IcCheck size={13} /> : <IcCopy size={13} />}
    </button>
  );
}

// ─── Avatar (initials, gradient bg) ──────────────────────────────────────
function Avatar({ name, size = 36, hue }) {
  const initials = (name || '?').split(/[\s@_-]/).filter(Boolean).slice(0, 2).map((s) => s[0]?.toUpperCase()).join('');
  const h = hue ?? ((name?.charCodeAt?.(0) ?? 200) * 9 % 360);
  return (
    <div
      style={{
        width: size, height: size, borderRadius: 999,
        background: `linear-gradient(135deg, oklch(0.75 0.14 ${h}), oklch(0.62 0.16 ${(h + 50) % 360}))`,
        color: 'white', fontWeight: 700, fontSize: size * 0.36,
        display: 'grid', placeItems: 'center',
        fontFamily: 'var(--ds-font-sans)',
        flexShrink: 0,
      }}
    >{initials}</div>
  );
}

// ─── Cost gauge: circular gauge for budget% ──────────────────────────────
function CostGauge({ used, budget, size = 72, label }) {
  const pct = Math.min(100, (used / budget) * 100);
  const r = (size - 8) / 2;
  const c = 2 * Math.PI * r;
  const off = c - (pct / 100) * c;
  const tone = pct >= 90 ? 'var(--ds-error-500)' : pct >= 70 ? 'var(--ds-warning-500)' : 'var(--ds-success-500)';
  return (
    <div style={{ position: 'relative', width: size, height: size }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} style={{ transform: 'rotate(-90deg)' }}>
        <circle cx={size/2} cy={size/2} r={r} fill="none" stroke="var(--ds-border-subtle)" strokeWidth="4" />
        <circle cx={size/2} cy={size/2} r={r} fill="none" stroke={tone} strokeWidth="4" strokeLinecap="round" strokeDasharray={c} strokeDashoffset={off} style={{ transition: 'stroke-dashoffset 0.5s' }} />
      </svg>
      <div style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', textAlign: 'center', lineHeight: 1.0 }}>
        <div>
          <div style={{ fontFamily: 'var(--ds-font-heading)', fontWeight: 700, fontSize: size * 0.24 }}>{Math.round(pct)}%</div>
          {label && <div style={{ fontSize: 9, color: 'var(--ds-fg-3)', letterSpacing: '0.1em', textTransform: 'uppercase', fontWeight: 700, marginTop: 1 }}>{label}</div>}
        </div>
      </div>
    </div>
  );
}

Object.assign(window, { Eyebrow, Button, Badge, Alert, Panel, Tabs, DL, DLItem, Metric, ProgressBar, Sparkline, Switch, CopyChip, Avatar, CostGauge });
