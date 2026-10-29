# Typed decisions

Policy for every place this app asks a model for a decision or structured output. One page; the libraries do the validate-and-retry loop, this file sets the rules.

| Stack | Library | Pattern |
|---|---|---|
| TypeScript | Vercel AI SDK `ai` 7.0.143 with Zod | `Output.object({ schema })` on a provider instance (`anthropic('<model>')`, never a bare `"anthropic/<model>"` string, which routes through the paid Vercel AI Gateway) |
| Python | Pydantic AI 2.57.0 | an `Agent` with `output_type=<PydanticModel>` |
| Python, bare client | Instructor 1.17.0 | `client.messages.create(..., response_model=<PydanticModel>)` |

Rules:

1. **Schema first.** Every decision has a typed schema (Zod or Pydantic) committed next to the call site. No free-text parsing of model output.
2. **Validation failures retry, with a budget.** At most <n> retries (default 2) with the validation error fed back; after the budget, the fallback runs. Never loop without a cap.
3. **Fallback to code or a person.** Each decision names its fallback: a deterministic rule, a default value, or a queue for a human. The fallback is tested like any other branch.
4. **Model and version are pinned** in config, not in the call site; a change is a reviewed PR.
5. **Log the decision**, its schema version, model id, latency and whether a retry or the fallback fired, into the app's structured logs (OpenObserve); one SLO per decision endpoint in CONSTRAINTS.md.
6. **Prompt text lives in the repository**, reviewed like code; no prompt is built from unvalidated user input without escaping or a separate field.
7. **No secrets or personal data in prompts** unless the data flow is documented in this file.

Decisions in this app:

| Decision | Schema file | Retry budget | Fallback | Owner |
|---|---|---|---|---|
| <name> | `<path>` | 2 | <rule or queue> | @<owner> |
