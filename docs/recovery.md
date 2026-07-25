# Host recovery notes

## Hosted genesis durable-session backfill (Project 50 Milestone A)

Milestone A adds a dry-run migration/recovery planner for existing hosted/off-chain soul genesis state. The planner maps
safe summaries of legacy `SOUL_REG` / `MINT_CONVERSATION` rows into `HostedGenesisSession` seeds; it does not import raw
transcripts, prompt text, message lists, provider credentials, Instance API keys, wallet signatures, signing material,
SSM values, AWS credentials, provider secrets, MicroVM endpoint tokens, or browser Host credentials.

Recovery rules after the Project 48 M11 typed-candidate hard cutover:

- every legacy lane lacks the authoritative versioned/hash-bound `DeclarationCandidate`, so it is ineligible for
  section retry, owner affirmation, deterministic finalization, or `declaration_ready`
- legacy `created`, `in_progress`, `assistant_turn_ready`, `completed`, `declaration_ready`, and `failed` rows therefore
  become typed `failed` recovery with `restart_soul_bootstrap`; an old retryable provider failure is not resumed
- produced declaration JSON and transcript prose are never used to reconstruct a candidate, review, or affirmation
- the retired extraction target state has no migration or retry path
- all planned rows preserve the durable `conversation_id` for correlation, but callers start a fresh registration and
  conversation lane through `/api/v1/soul/instance/agents/register/begin`

Operators should run any future executable backfill in dry-run mode first, review the generated recovery actions, and only
then apply with explicit operator authorization. This repository slice does not mutate cloud state.
