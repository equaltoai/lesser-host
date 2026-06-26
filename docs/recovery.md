# Host recovery notes

## Hosted genesis durable-session backfill (Project 50 Milestone A)

Milestone A adds a dry-run migration/recovery planner for existing hosted/off-chain soul genesis state. The planner maps
safe summaries of legacy `SOUL_REG` / `MINT_CONVERSATION` rows into `HostedGenesisSession` seeds; it does not import raw
transcripts, prompt text, message lists, provider credentials, Instance API keys, wallet signatures, signing material,
SSM values, AWS credentials, provider secrets, MicroVM endpoint tokens, or browser Host credentials.

Recovery rules:

- legacy `completed` / `declaration_ready` rows become `declaration_ready` only when they have a valid declaration
  checkpoint bound to the same registration, conversation, and agent
- terminal rows with missing or invalid produced declarations become `failed` with `restart_soul_bootstrap`
- legacy `created`, `in_progress`, and `assistant_turn_ready` rows become typed retry recovery (`retry_same_step`) rather
  than deriving progress from SQS or transport state
- legacy `declaration_extraction_pending` rows become typed declaration-extraction retry recovery
- all planned rows preserve the durable `conversation_id` so Lesser can keep early conversation-id persistence intact

Operators should run any future executable backfill in dry-run mode first, review the generated recovery actions, and only
then apply with explicit operator authorization. This repository slice does not mutate cloud state.
