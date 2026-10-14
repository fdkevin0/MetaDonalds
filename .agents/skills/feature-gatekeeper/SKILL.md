---
name: feature-gatekeeper
description: Coordinate feature implementation with developer subagents and independent review when a managed review gate is requested.
---

# Feature Gatekeeper

Keep the current session as the gatekeeper. Use native subagents for development and review; do not create independent top-level sessions. It owns requirements, subagent management, model selection, and the final completion decision. Delegate implementation and independent review to separate subagents.

## Roles and models

- **Gatekeeper:** GPT-6 Astra (`gpt-6-astra`). Analyze requirements, resolve ambiguity, assign work, adjudicate findings, and verify completion.
- **Developer subagent:** Default to GPT-6 Sol (`gpt-6-sol`) for scoped implementation and fixes. The gatekeeper may select another available Sol model or promote difficult work to Astra when complexity, risk, or repeated failures justify it.
- **Reviewer subagent:** A separate subagent, defaulting to Sol for routine changes and Astra for difficult correctness, security, concurrency, or architectural questions. The gatekeeper chooses the model and reasoning effort independently of the developer's assignment.

Verify the current model through runtime metadata or an available session API. A skill cannot itself change the current session's model. If the current session is not Astra, use a supported in-place switch if available; otherwise ask the user to switch this session to Astra before starting orchestration. If model identity cannot be verified, ask for confirmation. Do not silently replace the current session with a subagent gatekeeper or claim that a model switch happened.

Check available model identifiers and native subagent capabilities before spawning. If the requested model is unavailable, explain the limitation and obtain the user's choice of fallback. Do not silently perform both implementation and review in the gatekeeper when delegation is unavailable.

## Implementation and review

Define the requested behavior, acceptance criteria, affected scope, and relevant
validation from the task and repository. Preserve a baseline of existing changes
so the reviewer can distinguish this task's edits without resetting user work.
Keep compact working notes on the baseline, assignments, current change,
validation, and unresolved findings for handoffs and resumption.

Delegate implementation with a self-contained brief: requirements, repository,
edit scope, baseline, and validation. When selecting a model through
`collaboration.spawn_agent`, use `fork_turns="none"`. Reuse agents for follow-up
work. The gatekeeper manages developer and reviewer directly; neither delegates
further. Keep one writer per overlapping scope and stop edits during review.

After implementation and affected checks, give a separate reviewer the original
requirements, complete current change (including untracked files), baseline, and
validation evidence. Review is read-only and includes connected code and
regressions. Findings identify locations, impact, and required validation;
approval identifies the reviewed state and verification limits.

Adjudicate findings against the task using code/test evidence. Send accepted
findings to the developer, then re-review the resulting change for closure and
regressions. Record why a finding is dismissed; ask about material scope changes
only when existing authorization does not resolve them. Continue through fixes
and review without stopping for approval of routine local work.

Use `collaboration.followup_task` to restart idle agents for fixes or re-review;
`send_message` alone does not start a turn. Track which change each result covers;
a stale result or tool failure is not approval. On resumption or agent failure,
check live assignments and stop an old writer before replacing it.

## Completion and stopping conditions

“Perfect review” means no unresolved valid findings within the agreed feature scope, satisfied acceptance criteria, and passing required checks on the final reviewed state. It is not a promise that software has no possible defects. Do not create speculative requirements or cosmetic churn to prolong the loop.

Before announcing completion, verify that:

- The final integrated change satisfies the contract and preserves unrelated work.
- Required checks passed for that state; later edits invalidate affected results and require re-review.
- The reviewer approved that same state, with every finding fixed or explicitly adjudicated using evidence.
- No subagent is still editing or carrying an unresolved assignment.

Do not retry an unchanged failure indefinitely. If a blocker repeats without new evidence, reassess the cause and change the assignment, model, or approach. If progress requires unavailable access, an external dependency, or a product decision, report the specific blocker and leave the feature incomplete. Required checks that cannot run are blockers unless the user explicitly accepts a revised completion condition.

In MetaDonalds, follow `AGENTS.md` for validation. Local tests use fakes; live submissions and image publishing require explicit task scope. A timed-out submission may already have succeeded, so never use automatic submission retries as a review or validation technique.

Only the gatekeeper announces completion. Report the delivered behavior, validation, review outcome, and material limitations. Delegation does not authorize merging, deploying, publishing, or external messaging beyond the user's request.
