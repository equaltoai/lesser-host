# Hosted Genesis model selection

Hosted Genesis deliberately exposes exactly two operator-facing model aliases:

| Alias | Provider route | Concrete model | Reasoning effort |
| --- | --- | --- | --- |
| `gpt-5.6-luna` | OpenAI | `gpt-5.6-luna` | `medium` |
| `claude-sonnet-5` | Claude | `claude-sonnet-5` | `medium` |

The alias registry is the single mapping location at
`internal/ai/modelselection/modelselection.go`. A future provider is added as
another registry row plus its adapter wiring; callers do not grow provider
prefixes, concrete identifiers, or reasoning-level choices.

## Request behavior

At the Hosted Genesis advance request boundary:

- omitting `model` selects the OpenAI alias `gpt-5.6-luna`;
- the two aliases are accepted exactly (surrounding whitespace is trimmed);
- an explicit empty value, a bare model such as `gpt-5-mini`, or a provider-
  prefixed value is rejected immediately with `app.bad_request` and an error
  naming both valid aliases;
- rejection happens before durable turn/session work or MicroVM guest dispatch,
  so an invalid choice cannot become a deferred provider API failure.

The persisted Hosted Genesis session keeps the selected alias for the caller-
visible model field. The existing declaration candidate binding continues to
use the canonical `provider:model` form internally; alias/canonical comparison
is normalized without changing the declaration or five-body contract.

## Advanced compatibility path

The free-form `provider:model` spelling remains available only as an
internal/advanced escape hatch for generic AI configuration, legacy stored
conversations, and provider adapter tests. It is not required and is not
accepted as a new Hosted Genesis model selection.

OpenAI sends `ReasoningEffort=medium` through the SDK's
`ChatCompletionNewParams.ReasoningEffort`. Claude sends
`OutputConfig.Effort=medium` through the SDK's `MessageNewParams`; both the
phase-tool and retained streaming provider paths use the registry setting.
