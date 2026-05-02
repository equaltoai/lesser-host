# Reachability privacy hardening — 2026-05-02

A sim-routed Codex Security report found that host's soul reachability endpoints could be called without a control-plane session and used to enumerate email / phone linkage or fetch raw contact channel details for a known agent id.

## Hardened contract

The following control-plane soul registry endpoints now require a valid session token and domain access for the resolved / requested agent:

- `GET /api/v1/soul/agents/{agentId}/channels`
- `GET /api/v1/soul/agents/{agentId}/channels/preferences`
- `GET /api/v1/soul/resolve/email/{emailAddress}`
- `GET /api/v1/soul/resolve/phone/{phoneNumber}`

Raw email addresses, raw phone numbers, provider details, verification status, and contact preferences are no longer publicly reachable through these endpoints. Authenticated reverse email / phone lookups normalize access-denied cases to `404` so callers cannot distinguish "not registered" from "registered to another domain".

ENS resolution remains public because ENS names are public identifiers, but it still requires an active identity and active ENS channel.

## Consumer impact

- host portal calls to the channels endpoint must include the bearer session token.
- generated Greater / sim consumers should treat email / phone reverse lookup and channel/preference reads as authenticated capability surfaces, not anonymous public discovery.
- Body-facing `/api/v1/soul/comm/contactability/{agentId}` remains instance-key authenticated through the mailbox request context and is unchanged.
