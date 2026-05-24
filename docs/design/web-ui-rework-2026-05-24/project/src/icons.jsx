/* Icons — Feather/Lucide-style inline SVGs.
 * 24×24 viewBox, stroke=currentColor, stroke-width=2, fill=none, rounded caps.
 * Matches the greater-components icon style.
 */

const svgProps = {
  width: 18, height: 18, viewBox: '0 0 24 24',
  fill: 'none', stroke: 'currentColor', strokeWidth: 2,
  strokeLinecap: 'round', strokeLinejoin: 'round',
};

const Icon = ({ children, size = 18, ...rest }) => (
  <svg {...svgProps} width={size} height={size} {...rest}>{children}</svg>
);

const IcServer       = (p) => <Icon {...p}><rect x="2" y="3" width="20" height="8" rx="2"/><rect x="2" y="13" width="20" height="8" rx="2"/><line x1="6" y1="7" x2="6" y2="7"/><line x1="6" y1="17" x2="6" y2="17"/></Icon>;
const IcSouls        = (p) => <Icon {...p}><circle cx="12" cy="8" r="4"/><path d="M4 21c0-4 4-7 8-7s8 3 8 7"/></Icon>;
const IcTrust        = (p) => <Icon {...p}><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></Icon>;
const IcBilling      = (p) => <Icon {...p}><rect x="2" y="5" width="20" height="14" rx="2"/><line x1="2" y1="10" x2="22" y2="10"/></Icon>;
const IcAccount      = (p) => <Icon {...p}><circle cx="12" cy="8" r="4"/><path d="M5 21v-1a7 7 0 0114 0v1"/></Icon>;
const IcDashboard    = (p) => <Icon {...p}><rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/></Icon>;
const IcAudit        = (p) => <Icon {...p}><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/><line x1="8" y1="17" x2="14" y2="17"/></Icon>;
const IcProvision    = (p) => <Icon {...p}><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></Icon>;
const IcGlobe        = (p) => <Icon {...p}><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15 15 0 010 20M12 2a15 15 0 000 20"/></Icon>;
const IcKey          = (p) => <Icon {...p}><circle cx="7" cy="14" r="4"/><path d="M21 3l-9.5 9.5M14 6l4 4M17 9l3-3"/></Icon>;
const IcRegistry     = (p) => <Icon {...p}><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></Icon>;
const IcChevron      = (p) => <Icon {...p}><polyline points="9 18 15 12 9 6"/></Icon>;
const IcChevronDown  = (p) => <Icon {...p}><polyline points="6 9 12 15 18 9"/></Icon>;
const IcSearch       = (p) => <Icon {...p}><circle cx="11" cy="11" r="7"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></Icon>;
const IcBell         = (p) => <Icon {...p}><path d="M6 8a6 6 0 1112 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10 21a2 2 0 004 0"/></Icon>;
const IcPlus         = (p) => <Icon {...p}><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></Icon>;
const IcArrowRight   = (p) => <Icon {...p}><line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/></Icon>;
const IcArrowLeft    = (p) => <Icon {...p}><line x1="19" y1="12" x2="5" y2="12"/><polyline points="12 19 5 12 12 5"/></Icon>;
const IcCheck        = (p) => <Icon {...p}><polyline points="20 6 9 17 4 12"/></Icon>;
const IcX            = (p) => <Icon {...p}><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></Icon>;
const IcWarn         = (p) => <Icon {...p}><path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></Icon>;
const IcInfo         = (p) => <Icon {...p}><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></Icon>;
const IcRefresh      = (p) => <Icon {...p}><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/></Icon>;
const IcCopy         = (p) => <Icon {...p}><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></Icon>;
const IcLogout       = (p) => <Icon {...p}><path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></Icon>;
const IcSettings     = (p) => <Icon {...p}><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 11-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 11-2.83-2.83l.06-.06a1.65 1.65 0 00.33-1.82 1.65 1.65 0 00-1.51-1H3a2 2 0 110-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 112.83-2.83l.06.06a1.65 1.65 0 001.82.33H9a1.65 1.65 0 001-1.51V3a2 2 0 114 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 112.83 2.83l-.06.06a1.65 1.65 0 00-.33 1.82V9a1.65 1.65 0 001.51 1H21a2 2 0 110 4h-.09a1.65 1.65 0 00-1.51 1z"/></Icon>;
const IcTrendUp      = (p) => <Icon {...p}><polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/></Icon>;
const IcTrendDown    = (p) => <Icon {...p}><polyline points="23 18 13.5 8.5 8.5 13.5 1 6"/><polyline points="17 18 23 18 23 12"/></Icon>;
const IcZap          = (p) => <Icon {...p}><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></Icon>;
const IcActivity     = (p) => <Icon {...p}><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></Icon>;
const IcCpu          = (p) => <Icon {...p}><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><line x1="9" y1="2" x2="9" y2="4"/><line x1="15" y1="2" x2="15" y2="4"/><line x1="9" y1="20" x2="9" y2="22"/><line x1="15" y1="20" x2="15" y2="22"/><line x1="20" y1="9" x2="22" y2="9"/><line x1="20" y1="15" x2="22" y2="15"/><line x1="2" y1="9" x2="4" y2="9"/><line x1="2" y1="15" x2="4" y2="15"/></Icon>;
const IcShield       = (p) => <Icon {...p}><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><polyline points="9 12 11 14 15 10"/></Icon>;
const IcUsers        = (p) => <Icon {...p}><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87"/><path d="M16 3.13a4 4 0 010 7.75"/></Icon>;
const IcLink         = (p) => <Icon {...p}><path d="M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71"/></Icon>;
const IcUnlink       = (p) => <Icon {...p}><path d="M18.84 12.25l1.72-1.71a5 5 0 10-7.07-7.07l-1.72 1.71M5.16 11.75l-1.72 1.71a5 5 0 107.07 7.07l1.72-1.71"/><line x1="2" y1="2" x2="22" y2="22"/></Icon>;
const IcGitBranch    = (p) => <Icon {...p}><line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 01-9 9"/></Icon>;
const IcWallet       = (p) => <Icon {...p}><rect x="2" y="6" width="20" height="14" rx="2"/><path d="M2 10h20"/><circle cx="16" cy="15" r="1"/></Icon>;
const IcCog          = IcSettings;
const IcDot          = (p) => <Icon {...p}><circle cx="12" cy="12" r="2" fill="currentColor"/></Icon>;
const IcLightning    = IcZap;
const IcSparkles     = (p) => <Icon {...p}><path d="M12 3l2.5 5 5.5 2-5.5 2L12 17l-2.5-5L4 10l5.5-2L12 3z"/><path d="M19 13l1 3 3 1-3 1-1 3-1-3-3-1 3-1 1-3z"/></Icon>;
const IcEye          = (p) => <Icon {...p}><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></Icon>;
const IcMoreH        = (p) => <Icon {...p}><circle cx="12" cy="12" r="1.4" fill="currentColor"/><circle cx="19" cy="12" r="1.4" fill="currentColor"/><circle cx="5" cy="12" r="1.4" fill="currentColor"/></Icon>;
const IcDownload     = (p) => <Icon {...p}><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></Icon>;
const IcFilter       = (p) => <Icon {...p}><polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3"/></Icon>;
const IcSpinner      = (p) => <Icon {...p}><line x1="12" y1="2" x2="12" y2="6"/><line x1="12" y1="18" x2="12" y2="22"/><line x1="4.93" y1="4.93" x2="7.76" y2="7.76"/><line x1="16.24" y1="16.24" x2="19.07" y2="19.07"/><line x1="2" y1="12" x2="6" y2="12"/><line x1="18" y1="12" x2="22" y2="12"/><line x1="4.93" y1="19.07" x2="7.76" y2="16.24"/><line x1="16.24" y1="7.76" x2="19.07" y2="4.93"/></Icon>;
const IcMail         = (p) => <Icon {...p}><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22 6 12 13 2 6"/></Icon>;
const IcBox          = (p) => <Icon {...p}><path d="M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/></Icon>;

Object.assign(window, {
  IcServer, IcSouls, IcTrust, IcBilling, IcAccount, IcDashboard, IcAudit,
  IcProvision, IcGlobe, IcKey, IcRegistry, IcChevron, IcChevronDown,
  IcSearch, IcBell, IcPlus, IcArrowRight, IcArrowLeft, IcCheck, IcX,
  IcWarn, IcInfo, IcRefresh, IcCopy, IcLogout, IcSettings, IcCog,
  IcTrendUp, IcTrendDown, IcZap, IcActivity, IcCpu, IcShield, IcUsers,
  IcLink, IcUnlink, IcGitBranch, IcWallet, IcDot, IcLightning, IcSparkles,
  IcEye, IcMoreH, IcDownload, IcFilter, IcSpinner, IcMail, IcBox,
});
