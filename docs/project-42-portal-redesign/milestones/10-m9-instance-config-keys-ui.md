# M9 — Instance Detail Configuration + Keys UI

**Branch.** `aron/portal-m9-instance-config-keys-ui`
**Concern.** Re-skin the Configuration tab (now actually called
"Configuration", not "Config") with three sections (Instance
identity, Federation policy, Rate limits) and re-skin Keys with
formatted IDs, copy buttons, and table layout.

## Scope (≤ 7 tasks)

1. Rename tab label "Config" → "Configuration".
2. Instance identity section (Display name, Description, Default
   visibility, Reg open).
3. Federation policy section with ConfigToggle rows (Accept
   federation, Allow quote posts, Auto-thread sync, AI moderation
   hint, Public webfinger).
4. Rate limits section (DL of Posts/hour, Inbox delivery, Search
   QPS, Outbound HTTP).
5. Normalise toggle sizing (audit row P3.3).
6. Keys table: formatted token IDs with copy chip + masking.
7. Side-by-side artifact + tests.

Detail filled in when M8 merges.
