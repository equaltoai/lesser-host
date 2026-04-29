# Codex Security Finding Ledger — 2026-04-28

This ledger is the M0 evidence baseline for GitHub Project [#26](https://github.com/orgs/equaltoai/projects/26), tracking the Codex Security report at `.theory/codex-security-findings-2026-04-28T21-19-20.568Z.csv`. The CSV itself is not committed; this document records the repository-safe triage index, owner milestone, and disposition for every finding.

## Disposition meanings

- **confirmed-static** — M0 directly inspected current code and the reported vulnerable shape is present.
- **partially-stale/residual-risk** — current code has changed enough that the original wording is no longer fully accurate, but a related security risk remains assigned.
- **assigned-for-verification** — finding is in-mission and assigned to an implementation cluster; the owning milestone must add a reproducer or evidence-backed not-applicable disposition before closing.

## Findings

| ID | Severity | Disposition | Owner | Title | Relevant paths |
|---:|---|---|---|---|---|
| 1 | high | confirmed-static | M1 / [#188](https://github.com/equaltoai/lesser-host/issues/188) | Mailbox content can be overwritten via Message-ID replay | internal/commworker/mailbox_capture.go<br>internal/commmailbox/content_store.go<br>internal/commworker/store.go<br>internal/store/models/soul_comm_mailbox.go |
| 2 | high | confirmed-static | M4 / [#204](https://github.com/equaltoai/lesser-host/issues/204) | Unauthenticated soul lookups trigger expensive on-chain RPC fanout | internal/controlplane/server.go<br>internal/controlplane/handlers_soul_public.go<br>internal/controlplane/handlers_soul_discovery_v3.go<br>internal/controlplane/soul_public_agent_view.go |
| 3 | high | confirmed-static | M1 / [#189](https://github.com/equaltoai/lesser-host/issues/189) | SES email ingress trusts spoofable From identity | cdk/lib/lesser-host-stack.ts<br>internal/emailingress/server.go<br>internal/commworker/server.go |
| 4 | high | confirmed-static | M1 / [#191](https://github.com/equaltoai/lesser-host/issues/191) | Email provisioning can hijack existing mailboxes | internal/controlplane/handlers_soul_channels_email_provision.go<br>internal/controlplane/deps_migadu.go<br>internal/controlplane/handlers_soul_update_registration.go |
| 5 | high | confirmed-static | M3 / [#198](https://github.com/equaltoai/lesser-host/issues/198) | Managed provisioning now defaults to unpinned `latest` releases | cdk/cdk.json<br>cdk/lib/lesser-host-stack.ts<br>internal/controlplane/handlers_provisioning.go<br>internal/provisionworker/server.go |
| 6 | high | confirmed-static | M2 / [#194](https://github.com/equaltoai/lesser-host/issues/194) | Mint execution endpoint accepts arbitrary tx hashes | internal/controlplane/server.go<br>internal/controlplane/handlers_soul_agent_mint_operation.go<br>internal/controlplane/handlers_soul_operations.go |
| 7 | high | confirmed-static | M1 / [#190](https://github.com/equaltoai/lesser-host/issues/190) | Voice webhook enables unauthenticated credit-drain billing abuse | internal/controlplane/handlers_comm_webhooks.go<br>internal/controlplane/server.go |
| 8 | high | confirmed-static | M1 / [#189](https://github.com/equaltoai/lesser-host/issues/189) | Unauthenticated webhooks allow forged inbound comm injection | internal/controlplane/server.go<br>internal/controlplane/handlers_comm_webhooks.go<br>internal/commworker/message.go<br>internal/commworker/server.go |
| 9 | high | confirmed-static | M1 / [#191](https://github.com/equaltoai/lesser-host/issues/191) | Email provisioning allows arbitrary lessersoul.ai address claims | internal/controlplane/handlers_soul_channels_email_provision.go<br>internal/controlplane/deps_migadu.go |
| 10 | high | confirmed-static | M1 / [#191](https://github.com/equaltoai/lesser-host/issues/191) | V3 channel index writes allow email/phone/ENS takeover | internal/controlplane/handlers_soul_update_registration.go<br>internal/store/models/soul_agent_channel_index_items.go<br>internal/store/models/soul_agent_ens_resolution.go |
| 11 | high | confirmed-static | M1 / [#192](https://github.com/equaltoai/lesser-host/issues/192) | Unauthenticated public exposure of dispute evidence data | internal/controlplane/server.go<br>internal/controlplane/handlers_soul_sovereignty.go<br>internal/store/models/soul_agent_dispute.go |
| 12 | high | confirmed-static | M3 / [#203](https://github.com/equaltoai/lesser-host/issues/203) | Provisioning now force-enables open remote agent registration | cdk/lib/lesser-host-stack.ts |
| 13 | high | partially-stale/residual-risk | M3 / [#198](https://github.com/equaltoai/lesser-host/issues/198) | Unverified lesser-body tarball executed in privileged runner | cdk/lib/lesser-host-stack.ts<br>internal/store/models/instance.go<br>internal/controlplane/handlers_instances.go |
| 14 | medium | assigned-for-verification | M1 / [#188](https://github.com/equaltoai/lesser-host/issues/188) | Mailbox reply endpoint enables RFC5322 header injection | internal/controlplane/handlers_soul_comm_mailbox_reply.go<br>internal/controlplane/handlers_soul_comm_send.go<br>internal/commworker/mailbox_capture.go |
| 15 | medium | assigned-for-verification | M3 / [#198](https://github.com/equaltoai/lesser-host/issues/198) | Unvalidated git_sha allows mutable source ref checkout | cdk/lib/provision-runner/helpers.sh |
| 16 | medium | assigned-for-verification | M3 / [#199](https://github.com/equaltoai/lesser-host/issues/199) | Lesser-body template certification silently disabled by default | cdk/lib/provision-runner-buildspec.ts<br>internal/provisionworker/update_jobs.go |
| 17 | medium | assigned-for-verification | M3 / [#200](https://github.com/equaltoai/lesser-host/issues/200) | Canary token can leak to arbitrary or plaintext base URLs | github/workflows/managed-release-canary.yml<br>scripts/managed-release-certification/main.go |
| 18 | medium | assigned-for-verification | M3 / [#201](https://github.com/equaltoai/lesser-host/issues/201) | Instance key secret names lost stage isolation | internal/provisionworker/server.go<br>internal/provisionworker/managed_instance_key_secrets.go |
| 19 | medium | assigned-for-verification | M1 / [#191](https://github.com/equaltoai/lesser-host/issues/191) | Phone notifications can forge recipient addresses | internal/commworker/server.go<br>internal/controlplane/handlers_soul_update_registration.go |
| 20 | medium | assigned-for-verification | M1 / [#189](https://github.com/equaltoai/lesser-host/issues/189) | Telnyx webhook can be set from request Host | internal/controlplane/handlers_soul_channels_phone_provision.go<br>internal/controlplane/handlers_comm_voice_outbound.go<br>internal/controlplane/deps_telnyx.go<br>cdk/lib/lesser-host-stack.ts |
| 21 | medium | assigned-for-verification | M1 / [#188](https://github.com/equaltoai/lesser-host/issues/188) | Unescaped inbound message IDs allow log injection | internal/commworker/server.go<br>internal/commworker/message.go<br>internal/controlplane/server.go<br>internal/controlplane/handlers_comm_webhooks.go |
| 22 | medium | assigned-for-verification | M2 / [#194](https://github.com/equaltoai/lesser-host/issues/194) | Rotation execution accepts unrelated transaction hashes | internal/controlplane/server.go<br>internal/controlplane/handlers_soul_agent_rotation_operation.go<br>internal/controlplane/handlers_soul_operations.go |
| 23 | medium | assigned-for-verification | M1 / [#193](https://github.com/equaltoai/lesser-host/issues/193) | First-contact preferences ignore email CC/BCC recipients | internal/controlplane/handlers_soul_comm_send.go |
| 24 | medium | confirmed-static | M4 / [#211](https://github.com/equaltoai/lesser-host/issues/211) | Unsafe eval in deploy command enables shell command injection | app-theory/app.json<br>cdk/bin/lesser-host.ts |
| 25 | medium | assigned-for-verification | M4 / [#204](https://github.com/equaltoai/lesser-host/issues/204) | Authenticated DoS via conflict-path GitHub release resolution | internal/controlplane/handlers_portal_updates.go<br>internal/controlplane/lesser_releases.go |
| 26 | medium | assigned-for-verification | M1 / [#190](https://github.com/equaltoai/lesser-host/issues/190) | Unauthenticated voice webhooks permit reply/status spoofing | internal/controlplane/server.go<br>internal/controlplane/handlers_comm_voice_outbound.go |
| 27 | medium | assigned-for-verification | M4 / [#205](https://github.com/equaltoai/lesser-host/issues/205) | Communication score can be inflated without any actual responses | internal/soulreputationworker/server.go<br>internal/soulreputation/v0.go<br>internal/config/config.go |
| 28 | medium | assigned-for-verification | M2 / [#197](https://github.com/equaltoai/lesser-host/issues/197) | CCIP callback lacks target binding, allowing proof replay | contracts/contracts/OffchainResolver.sol |
| 29 | medium | assigned-for-verification | M1 / [#193](https://github.com/equaltoai/lesser-host/issues/193) | Outbound email boundary check bypass via unvalidated inReplyTo | internal/controlplane/handlers_soul_comm_send.go |
| 30 | medium | assigned-for-verification | M1 / [#191](https://github.com/equaltoai/lesser-host/issues/191) | Unauthenticated channel resolution leaks and trusts unverified claims | internal/controlplane/server.go<br>internal/controlplane/handlers_soul_discovery_v3.go<br>internal/controlplane/handlers_soul_update_registration.go |
| 31 | medium | assigned-for-verification | M2 / [#195](https://github.com/equaltoai/lesser-host/issues/195) | Mint finalize can publish pending agents as active | internal/controlplane/handlers_soul_mint_conversation.go |
| 32 | medium | assigned-for-verification | M2 / [#195](https://github.com/equaltoai/lesser-host/issues/195) | Lifecycle signatures are replayable for up to 10 years | internal/controlplane/handlers_soul_lifecycle.go |
| 33 | medium | assigned-for-verification | M2 / [#195](https://github.com/equaltoai/lesser-host/issues/195) | Principal signature can be replayed across soul agents | internal/controlplane/handlers_soul_registry.go<br>internal/soul/registration_v2.go |
| 34 | medium | assigned-for-verification | M2 / [#195](https://github.com/equaltoai/lesser-host/issues/195) | V2 registration can bypass existing version history | internal/controlplane/handlers_soul_update_registration.go<br>internal/controlplane/soul_registration_publish_v2.go |
| 35 | medium | assigned-for-verification | M4 / [#204](https://github.com/equaltoai/lesser-host/issues/204) | Public versions endpoint now performs unbounded DB reads | internal/controlplane/handlers_soul_versions.go |
| 36 | medium | assigned-for-verification | M4 / [#204](https://github.com/equaltoai/lesser-host/issues/204) | Public soul search now enables unbounded DB-scan DoS | internal/controlplane/handlers_soul_public.go |
| 37 | medium | assigned-for-verification | M4 / [#205](https://github.com/equaltoai/lesser-host/issues/205) | OpenAI mint stream lacks output token cap enabling DoS/cost abuse | internal/controlplane/handlers_soul_mint_conversation.go<br>internal/ai/llm/mint_conversation_stream.go |
| 38 | medium | assigned-for-verification | M2 / [#196](https://github.com/equaltoai/lesser-host/issues/196) | Unbounded endorsement merge enables public read DoS | internal/controlplane/handlers_soul_relationships.go |
| 39 | medium | assigned-for-verification | M1 / [#192](https://github.com/equaltoai/lesser-host/issues/192) | Opt-in endpoint lets non-operators finalize challenge results | internal/controlplane/handlers_soul_sovereignty.go<br>internal/controlplane/handlers_soul_validation.go<br>internal/controlplane/server.go |
| 40 | medium | assigned-for-verification | M2 / [#197](https://github.com/equaltoai/lesser-host/issues/197) | Burned Soul IDs can be reminted and reassigned by owner | contracts/contracts/SoulRegistry.sol |
| 41 | medium | assigned-for-verification | M1 / [#192](https://github.com/equaltoai/lesser-host/issues/192) | Validation responses are exposed through public API | internal/controlplane/handlers_soul_validation.go<br>internal/store/models/soul_agent_validation_record.go<br>internal/controlplane/server.go<br>internal/controlplane/handlers_soul_public.go |
| 42 | medium | assigned-for-verification | M4 / [#205](https://github.com/equaltoai/lesser-host/issues/205) | Suspended agents retain stale public reputation | internal/soulreputationworker/server.go<br>internal/controlplane/handlers_soul_public.go |
| 43 | medium | assigned-for-verification | M1 / [#192](https://github.com/equaltoai/lesser-host/issues/192) | Public validation API leaks raw challenge transcripts | internal/controlplane/server.go<br>internal/controlplane/handlers_soul_public.go<br>internal/store/models/soul_agent_validation_record.go |
| 44 | medium | assigned-for-verification | M2 / [#194](https://github.com/equaltoai/lesser-host/issues/194) | Soul operation receipts are accepted without validation | internal/controlplane/server.go<br>internal/controlplane/handlers_soul_operations.go |
| 45 | medium | assigned-for-verification | M2 / [#197](https://github.com/equaltoai/lesser-host/issues/197) | Wallet rotations leave pending funds at old addresses | contracts/contracts/TipSplitter.sol |
| 46 | medium | assigned-for-verification | M4 / [#205](https://github.com/equaltoai/lesser-host/issues/205) | Portal users can self-enable Soul provisioning | internal/controlplane/server.go<br>internal/controlplane/handlers_portal_instances.go<br>internal/controlplane/handlers_instances.go |
| 47 | medium | assigned-for-verification | M3 / [#201](https://github.com/equaltoai/lesser-host/issues/201) | Trust verification skips key auth when AI is disabled | internal/provisionworker/update_jobs.go |
| 48 | low | assigned-for-verification | M1 / [#188](https://github.com/equaltoai/lesser-host/issues/188) | Mailbox preview exposes full short message content | internal/controlplane/handlers_soul_comm_mailbox.go<br>internal/store/models/soul_comm_mailbox.go<br>internal/controlplane/comm_mailbox_capture.go<br>internal/commworker/mailbox_capture.go |
| 49 | low | assigned-for-verification | M1 / [#188](https://github.com/equaltoai/lesser-host/issues/188) | Unbounded preview normalization can cause memory/CPU DoS | internal/store/models/soul_comm_mailbox.go |
| 50 | low | assigned-for-verification | M4 / [#206](https://github.com/equaltoai/lesser-host/issues/206) | CMP-4 verifier can be bypassed with insecure wording | gov-infra/verifiers/gov-verify-rubric.sh |
| 51 | low | assigned-for-verification | M3 / [#199](https://github.com/equaltoai/lesser-host/issues/199) | Readiness check accepts mismatched lesser-body evidence | scripts/managed-release-readiness/main.go |
| 52 | low | assigned-for-verification | M3 / [#199](https://github.com/equaltoai/lesser-host/issues/199) | Lesser-body evidence can report pass when deployment failed | scripts/managed-release-certification/main.go |
| 53 | low | assigned-for-verification | M3 / [#200](https://github.com/equaltoai/lesser-host/issues/200) | Untrusted issue comment marker can DoS readiness sync | scripts/managed-release-readiness/main.go<br>github/workflows/managed-release-canary.yml |
| 54 | low | assigned-for-verification | M3 / [#198](https://github.com/equaltoai/lesser-host/issues/198) | Prerelease tags bypass new managed minimum-version check | internal/provisionworker/release_compatibility.go<br>internal/controlplane/handlers_portal_updates.go<br>internal/controlplane/lesser_releases.go |
| 55 | low | assigned-for-verification | M3 / [#202](https://github.com/equaltoai/lesser-host/issues/202) | Body-only updates can overwrite recorded LesserVersion without deploy | internal/controlplane/handlers_portal_updates.go<br>internal/provisionworker/update_jobs.go |
| 56 | low | confirmed-static | M4 / [#207](https://github.com/equaltoai/lesser-host/issues/207) | MarkdownRenderer allows sanitization bypass (XSS risk) | web/src/lib/greater/content/components/MarkdownRenderer.svelte |
| 57 | low | assigned-for-verification | M4 / [#204](https://github.com/equaltoai/lesser-host/issues/204) | Public ENS cache allows unbounded memory DoS via random names | internal/trust/server.go<br>internal/trust/handlers_ens_gateway.go |
| 58 | low | assigned-for-verification | M1 / [#193](https://github.com/equaltoai/lesser-host/issues/193) | Rejected claim-level updates still overwrite public S3 metadata | internal/controlplane/handlers_soul_update_registration.go<br>internal/controlplane/handlers_soul_capabilities.go |
| 59 | low | assigned-for-verification | M1 / [#191](https://github.com/equaltoai/lesser-host/issues/191) | Unverified domains can enumerate Soul agents | internal/controlplane/server.go<br>internal/controlplane/handlers_soul_mine.go<br>internal/controlplane/handlers_portal_instances.go<br>internal/controlplane/handlers_soul_registry.go |
| 60 | low | assigned-for-verification | M3 / [#202](https://github.com/equaltoai/lesser-host/issues/202) | Tip config can wedge managed deploys | internal/controlplane/handlers_instances.go<br>internal/provisionworker/server.go<br>internal/provisionworker/update_jobs.go<br>cdk/lib/lesser-host-stack.ts |
| 61 | informational | assigned-for-verification | M1 / [#188](https://github.com/equaltoai/lesser-host/issues/188) | Queue listing can miss valid queued messages | internal/controlplane/handlers_soul_comm_portal.go |
| 62 | informational | assigned-for-verification | M1 / [#188](https://github.com/equaltoai/lesser-host/issues/188) | One-shot S3 init error causes persistent mailbox write DoS | internal/commmailbox/content_store.go |
| 63 | informational | assigned-for-verification | M3 / [#199](https://github.com/equaltoai/lesser-host/issues/199) | Template-cert check can report pass on body phase failures | scripts/managed-release-certification/main.go<br>cdk/lib/provision-runner-buildspec.ts |
| 64 | informational | assigned-for-verification | M3 / [#202](https://github.com/equaltoai/lesser-host/issues/202) | Managed Lesser deploys now depend on missing metadata file | cdk/lib/provision-runner-buildspec.ts |
| 65 | informational | confirmed-static | M3 / [#202](https://github.com/equaltoai/lesser-host/issues/202) | Blank version validation breaks config and key update buttons | web/src/lib/utils/versionTags.ts<br>web/src/pages/portal/InstanceDetail.svelte<br>web/src/pages/operator/InstanceSupport.svelte<br>internal/controlplane/handlers_portal_updates.go |
| 66 | informational | assigned-for-verification | M4 / [#208](https://github.com/equaltoai/lesser-host/issues/208) | Trust soul update route added without required infra permissions | cdk/lib/lesser-host-stack.ts<br>internal/trust/server.go<br>internal/controlplane/handlers_soul_update_registration.go |
| 67 | informational | assigned-for-verification | M1 / [#191](https://github.com/equaltoai/lesser-host/issues/191) | Email/phone index keys permit contact-to-agent mapping takeover | internal/store/models/soul_agent_channel_index_items.go |
| 68 | informational | assigned-for-verification | M2 / [#195](https://github.com/equaltoai/lesser-host/issues/195) | Timestamp canonicalization breaks signed soul write requests | web/src/pages/portal/SoulAgentDetail.svelte<br>internal/controlplane/handlers_soul_continuity.go<br>internal/controlplane/handlers_soul_relationships.go |
| 69 | informational | confirmed-static | M4 / [#208](https://github.com/equaltoai/lesser-host/issues/208) | Soul models are not registered with TableTheory | internal/store/db.go<br>internal/store/models/soul_agent_identity.go<br>internal/store/models/soul_operation.go<br>internal/store/models/soul_agent_index_items.go<br>internal/store/models/soul_agent_reputation.go<br>internal/store/models/soul_agent_validation_record.go<br>internal/store/models/soul_agent_endorsement.go |

## M0 direct-confirmation notes

- **#1**: Mailbox content write happens before conditional row insert; S3 PutObject is unconditional and duplicate row insert condition failure is suppressed.
- **#2**: Public soul agent read calls per-request on-chain avatar enrichment, including EVM dial and unbounded renderer log scan.
- **#3**: SES ingress parses and trusts sender identity without M0-observed verdict gating before comm-worker enqueue.
- **#4**: Email provisioning accepts caller-supplied local part and proceeds through Migadu mailbox/forwarding path.
- **#5**: CDK defaults contain `managedLesserDefaultVersion` and `managedLesserBodyDefaultVersion` set to `latest`; runner resolves latest dynamically.
- **#6**: Soul operation execution recording only validates hash shape and successful receipt before status/side-effect update.
- **#7**: Voice webhook route is public and metering reads request-provided duration/call fields.
- **#8**: Comm webhook routes are public and accept normalized inbound payloads before enqueue without M0-observed provider signature validation.
- **#9**: Provisioned email address can be constructed from caller-supplied local part.
- **#10**: Email/phone/ENS indexes use global identifier keys and CreateOrUpdate semantics.
- **#11**: Public dispute handlers return stored dispute objects including evidence/statement/resolution fields.
- **#12**: Provision runner `enable_agents` force-sets agent registration and remote-agent options.
- **#13**: Current HEAD verifies named lesser-body release assets before executing deploy script, making the original tarball wording stale; residual deterministic-release/certification risks remain in M3.
- **#24**: AppTheory app contract uses shell `eval` with templated profile substitution; owning issue must determine safe host contract change vs framework feedback.
- **#56**: MarkdownRenderer exposes optional sanitization around a raw HTML sink; current usages do not pass `sanitize=false`, but the component-level footgun is real.
- **#65**: Web validation rejects blank release versions while backend supports blank version for config/key-rotation update actions.
- **#69**: Store Lambda init registration is an explicit allowlist; the reported soul models are not all present in the registration list on current HEAD.

## Milestone owner summary

- **M1**: findings #1, #14, #21, #48, #49, #61, #62, #3, #8, #20, #26, #7, #4, #9, #10, #19, #30, #59, #67, #11, #39, #41, #43, #23, #29, #58
- **M2**: findings #6, #22, #44, #31, #32, #33, #34, #68, #38, #28, #40, #45
- **M3**: findings #5, #13, #15, #54, #16, #51, #52, #63, #17, #53, #18, #47, #55, #60, #64, #65, #12
- **M4**: findings #2, #25, #35, #36, #57, #27, #37, #42, #46, #50, #56, #66, #69, #24
- **M4.5**: cross-repo lesser M9 structured `init-admin` consent alignment — [#217](https://github.com/equaltoai/lesser-host/issues/217)
  through [#221](https://github.com/equaltoai/lesser-host/issues/221). This is not a new Codex CSV finding; it is the
  managed-provisioning handoff needed before host consumes/certifies lesser M9 after the original remediation project.

## Closure rule

A finding may leave this ledger only when the owning issue links a merged PR with regression/evidence, or records a reviewed not-applicable/stale disposition with code evidence. High and medium findings should not be closed from source review alone unless the finding is demonstrably stale on current HEAD.
