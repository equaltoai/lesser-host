# Prototype instance frontend roadmap (dev.simulacrum.greater.website)

This roadmap defines how to ship a **Mastodon-like frontend** for the prototype instance that lives in the `AWS_PROFILE=Sim`
account. The instance is already **activated**, but has **no frontend enabled** (or not installed). We will use
`greater-components` as the UI foundation.

This is a **prototype** effort intended to harden the provisioning pipeline and define how future instances should be
fronted.

## Goals

- Provide a **standard social media experience** (timeline, compose, profile, notifications) on the instance domain.
- Use **greater-components** so UI/UX is consistent across `lesser` and `lesser.host`.
- Ensure the instance can be **provisioned + upgraded** without manual front-end steps.
- Capture learnings to improve the next instance build.

## Non-goals (prototype)

- Full theming/branding UI for tenants.
- Multi-tenant UI customization.
- Growth/analytics or advanced moderation dashboards.

## Assumptions

- Instance base domain: `dev.simulacrum.greater.website`.
- Backend API is running and healthy (ActivityPub + app API).
- We can modify the `lesser` repo to add/ship a frontend bundle.
- Static assets can be served from the instance (S3 + CloudFront) or from the app server.

## Phases

### P0 — Discovery + contract alignment

Deliverables:
- Confirm current instance endpoints and health checks.
- Identify the **frontend delivery mechanism** (static CDN vs app server).
- Define the **frontend API contract** (auth, timelines, compose, notifications).
- Decide how frontend config is injected (env + runtime config JSON).

Acceptance criteria:
- A concrete list of required API endpoints and auth flow exists.
- A decision is made for where the frontend assets are hosted.

### P1 — UI foundation (greater-components)

Deliverables:
- Add `greater-components` to the `lesser` frontend build.
- Establish typography, layout grid, and navigation shell using those components.
- Basic routes/pages:
  - Home timeline
  - Explore
  - Notifications
  - Compose
  - Profile
  - Settings

Acceptance criteria:
- The app builds locally and renders stub data for core screens.
- The UI is CSP-safe and uses no inline scripts/styles.

### P2 — Authentication + session

Deliverables:
- Implement login flow (wallet/passkey or existing account flow in Lesser).
- Session management with refresh and logout.
- Gate authenticated routes.

Acceptance criteria:
- User can login and see authenticated views.
- Session expiry is handled gracefully.

### P3 — Core social features

Deliverables:
- Timeline rendering (home + local + public).
- Compose + post submission.
- Post detail view with replies and boosts.
- Notifications feed.
- Profile view (bio, follower/following, posts).

Acceptance criteria:
- A user can post, reply, and see updates without page reloads.
- Home timeline and notifications update correctly.

### P4 — Settings + admin surface

Deliverables:
- Account settings (profile, display name, avatar/bio).
- Preferences (language, content filters if supported).
- Moderator/admin console (role-gated) covering report queue actions, user management, instance configuration, federation controls, and audit/insights views where available.

Acceptance criteria:
- Settings changes persist and reflect after reload.
- Admin routes are role-gated and operational for a seeded admin.

### P5 — Deployment + automation

Deliverables:
- Integrate frontend build into the `lesser up` flow.
- Ensure new instance provisioning **automatically ships the frontend**.
- Document upgrade path for frontend assets (new release → instance update).
- Support direct deploy for prototypes via `lesser client deploy` (uploads `dist/` to the client bucket and invalidates CloudFront).

Acceptance criteria:
- A fresh instance deploy includes the frontend without manual steps.
- Re-deploying `lesser` updates the frontend assets in place.

## Cross-repo work (lesser + lesser-host)

- `lesser`:
  - Build frontend app using `greater-components` (via `greater` CLI).
  - Provide runtime config and API client wrappers.
  - Ensure deploy packaging includes UI assets.
- `lesser-host`:
  - Track instance UI capability in provisioning receipt (optional).
  - Document the frontend contract and update testing plan.

## Risks + mitigations

- API contract drift: document a versioned contract and pin frontend to the same release.
- Hosting differences: normalize to a single delivery mechanism for v1.
- Auth mismatch: align on a single supported auth flow for prototype.

## Next steps

1. Confirm current `dev.simulacrum.greater.website` backend endpoints and auth flow.
2. Choose frontend hosting path for the prototype.
3. Kick off `lesser` frontend scaffolding with `greater-components`.
4. Update testing plan to include prototype instance UI verification.
