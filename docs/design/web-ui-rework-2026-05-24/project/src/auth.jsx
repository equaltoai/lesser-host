/* auth.jsx — Sign-in screen matching the system. */

function LoginScreen({ onSubmit }) {
  const [mode, setMode] = React.useState('passkey'); // 'passkey' | 'wallet' | 'op'
  const [username, setUsername] = React.useState('alice');
  return (
    <div className="auth-page">
      <div className="auth-card">
        <div className="auth-card__brand">
          <div className="auth-card__mark">&lt;</div>
          <div className="auth-card__title">Sign in to Lesser</div>
          <div className="auth-card__sub">Managed ActivityPub server, with souls.</div>
        </div>

        <div style={{ marginBottom: '1rem' }}>
          <label className="label">Username</label>
          <input className="input" value={username} onChange={(e) => setUsername(e.target.value)} placeholder="alice" />
        </div>

        <div className="col" style={{ gap: '0.55rem' }}>
          <Button variant="gradient" onClick={() => onSubmit({ role: 'customer' })} icon={<IcKey size={16} />}>Authenticate with passkey</Button>
          <Button variant="outline" onClick={() => onSubmit({ role: 'customer', method: 'wallet' })} icon={<IcWallet size={16} />}>Connect wallet</Button>
        </div>

        <div className="auth-divider">or</div>

        <Button variant="ghost" onClick={() => onSubmit({ role: 'admin' })} icon={<IcShield size={16} />} style={{ width: '100%', justifyContent: 'center' }}>
          Sign in as operator
        </Button>

        <p className="muted" style={{ fontSize: '0.78rem', textAlign: 'center', marginTop: '1.25rem' }}>
          By signing in you accept the <a className="ds-link">terms</a>. Sessions expire in 24h.
        </p>
      </div>
    </div>
  );
}

Object.assign(window, { LoginScreen });
