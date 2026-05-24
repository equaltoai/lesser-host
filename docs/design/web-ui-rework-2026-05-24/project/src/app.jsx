/* app.jsx — Top-level router, command palette state, and crumb plumbing. */

function App() {
  const [path, setPath] = React.useState('/portal');
  const [cmdkOpen, setCmdkOpen] = React.useState(false);
  const [session, setSession] = React.useState(SESSION);

  // ⌘K / Ctrl-K binding
  React.useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setCmdkOpen((v) => !v);
      } else if (e.key === 'Escape') {
        setCmdkOpen(false);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const navigate = React.useCallback((to) => {
    setPath(to);
    window.scrollTo({ top: 0 });
  }, []);

  // ─── Render the matching shell + page ────────────────────────────────
  if (path === '/login') {
    return (
      <LoginScreen
        onSubmit={(s) => {
          setSession({ ...SESSION, ...s });
          navigate(s.role === 'admin' ? '/operator' : '/portal');
        }}
      />
    );
  }

  const isOperator = path.startsWith('/operator');
  const Shell = isOperator ? OperatorShell : PortalShell;
  const crumbs = buildCrumbs(path, navigate);
  const page = renderPage(path, navigate);

  return (
    <React.Fragment>
      <Shell
        session={session}
        currentPath={path}
        onNavigate={navigate}
        onOpenCmdK={() => setCmdkOpen(true)}
        crumbs={crumbs}
      >
        {page}
      </Shell>
      <CommandPalette open={cmdkOpen} onClose={() => setCmdkOpen(false)} onNavigate={navigate} />
    </React.Fragment>
  );
}

function buildCrumbs(path, navigate) {
  const out = [];
  const op = path.startsWith('/operator');
  if (op) {
    out.push({ label: 'Operator', path: '/operator', onClick: () => navigate('/operator') });
  } else {
    out.push({ label: 'Portal', path: '/portal', onClick: () => navigate('/portal') });
  }

  if (path === '/portal' || path === '/operator') return [{ label: out[0].label }];

  // Portal
  if (path.startsWith('/portal/instances/')) {
    const slug = path.split('/')[3];
    out.push({ label: 'Instances', onClick: () => navigate('/portal') });
    out.push({ label: slug });
  } else if (path === '/portal/billing') {
    out.push({ label: 'Billing' });
  } else if (path === '/portal/souls') {
    out.push({ label: 'Souls' });
  } else if (path.startsWith('/portal/souls/')) {
    out.push({ label: 'Souls', onClick: () => navigate('/portal/souls') });
    out.push({ label: '@' + path.split('/')[3] });
  } else if (path === '/portal/trust') {
    out.push({ label: 'Trust' });
  } else if (path === '/portal/account') {
    out.push({ label: 'Account' });
  }

  // Operator
  if (path === '/operator/audit') out.push({ label: 'Audit log' });
  if (path === '/operator/releases') out.push({ label: 'Releases' });
  if (path === '/operator/provisioning') out.push({ label: 'Provisioning' });
  if (path.startsWith('/operator/provisioning/')) {
    out.push({ label: 'Provisioning', onClick: () => navigate('/operator/provisioning') });
    out.push({ label: path.split('/')[3] });
  }
  if (path.startsWith('/operator/approvals/')) {
    out.push({ label: 'Approvals' });
    out.push({ label: path.split('/')[3] });
  }
  if (path === '/operator/instances') out.push({ label: 'Instances' });
  if (path === '/operator/tip-registry') out.push({ label: 'Tip registry' });
  if (path === '/operator/soul') out.push({ label: 'Soul registry' });

  return out;
}

function renderPage(path, navigate) {
  if (path === '/portal')             return <PortalHome onNavigate={navigate} />;
  if (path === '/portal/billing')     return <PortalBilling onNavigate={navigate} />;
  if (path === '/portal/souls')       return <PortalSouls onNavigate={navigate} />;
  if (path.startsWith('/portal/souls/')) {
    return <SoulDetail handle={path.split('/')[3]} onNavigate={navigate} />;
  }
  if (path === '/portal/trust')       return <PortalTrust onNavigate={navigate} />;
  if (path === '/portal/account')     return <PortalAccount session={SESSION} />;
  if (path.startsWith('/portal/instances/')) {
    return <InstanceDetail slug={path.split('/')[3]} onNavigate={navigate} />;
  }
  // Operator
  if (path === '/operator')                     return <OperatorDashboard onNavigate={navigate} />;
  if (path === '/operator/audit')               return <OperatorAudit />;
  if (path === '/operator/releases')            return <OperatorReleases onNavigate={navigate} />;
  if (path === '/operator/provisioning')        return <ProvisioningList onNavigate={navigate} />;
  if (path.startsWith('/operator/provisioning/')) return <ProvisioningJobDetail jobId={path.split('/')[3]} onNavigate={navigate} />;
  if (path.startsWith('/operator/approvals/'))   return <OperatorApprovals kind={path.split('/')[3]} onNavigate={navigate} />;
  if (path === '/operator/instances')           return <OperatorInstances onNavigate={navigate} />;
  if (path === '/operator/tip-registry')        return <OperatorTipRegistry />;
  if (path === '/operator/soul')                return <OperatorSoulRegistry onNavigate={navigate} />;

  return (
    <div className="content">
      <Alert variant="warning" title="Not found">Unknown path: <code className="mono">{path}</code></Alert>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById('root')).render(<App />);
