---
name: feature-gatekeeper
description: Coordinate feature development through an Astra gatekeeper in the current session, delegated implementation, and an independent review-and-fix loop. Use when developing a feature with managed subagents and a review gate.
---

# Feature Gatekeeper

Keep the current session as the gatekeeper. Use native subagents for development and review; do not create independent top-level sessions. It owns requirements, subagent management, model selection, and the final completion decision. Delegate implementation and independent review to separate subagents.

## Roles and models

- **Gatekeeper:** GPT-6 Astra (`gpt-6-astra`). Analyze requirements, resolve ambiguity, assign work, adjudicate findings, and verify completion.
- **Developer subagent:** Default to GPT-6 Sol (`gpt-6-sol`) for scoped implementation and fixes. The gatekeeper may select another available Sol model or promote difficult work to Astra when complexity, risk, or repeated failures justify it.
- **Reviewer subagent:** A separate subagent, defaulting to Sol for routine changes and Astra for difficult correctness, security, concurrency, or architectural questions. The gatekeeper chooses the model and reasoning effort independently of the developer's assignment.

Verify the current model through runtime metadata or an available session API. A skill cannot itself change the current session's model. If the current session is not Astra, use a supported in-place switch if available; otherwise ask the user to switch this session to Astra before starting orchestration. If model identity cannot be verified, ask for confirmation. Do not silently replace the current session with a subagent gatekeeper or claim that a model switch happened.

Check available model identifiers and native subagent capabilities before spawning. If the requested model is unavailable, explain the limitation and obtain the user's choice of fallback. Do not silently perform both implementation and review in the gatekeeper when delegation is unavailable.

## Establish the feature contract

Read applicable `AGENTS.md` instructions and the code relevant to the feature. Establish:

- Expected behavior, acceptance criteria, and any material unanswered question.
- The callers, tests, fixtures, configuration, and documentation the change must reach.
- Relevant validation commands and existing workspace changes to preserve.

Record the starting revision and working-tree state. In a dirty tree, preserve a baseline of relevant existing changes so the reviewer can distinguish this task's edits from earlier work. Do not reset, stash, or overwrite user changes to manufacture a clean baseline.

Maintain a compact task ledger in the gatekeeper's working notes: acceptance criteria, baseline, subagent IDs and roles, selected models, file ownership, current iteration/revision, check results, and unresolved findings. Update it after each handoff so resumed work does not duplicate subagents or accept stale results.

## Delegate implementation

Create one developer subagent initially. Reuse it for subsequent fixes so it retains implementation context. Supply the feature contract, repository path, relevant instructions, baseline, permitted edit scope, validation expectations, and gatekeeper ID. The developer implements and validates the complete change, then reports back; it does not declare the overall feature complete or create its own review hierarchy.

Use `collaboration.spawn_agent` with an explicit `model` and `fork_turns="none"` when selecting a subagent model. Supply a self-contained brief because this mode does not inherit conversation history. The gatekeeper creates and manages both developer and reviewer directly; neither subagent delegates further.

Keep one writer for overlapping files. Add developers only for independently owned work that benefits from parallel execution; integrate their results before review. Never let a developer edit the reviewed files while the reviewer is inspecting them.

## Notifications and review loop

Use actual subagent messages or delivered completion events, not an assumption that another agent saw a status update. Every handoff includes the task/iteration, revision or reproducible diff snapshot, status, evidence, and next owner. Track uncommitted and untracked task files as part of the snapshot; a commit hash alone does not identify a dirty tree.

1. The developer sends **IMPLEMENTATION_READY** to the gatekeeper with changed files, behavior delivered, tests and outcomes, and any blockers or known limitations. It then stops editing pending follow-up.
2. After implementation is ready, the gatekeeper creates a separate reviewer subagent. Give it the original requirements, baseline, complete current change, relevant repository instructions, and validation evidence. Provide both gatekeeper and developer IDs. Do not bias its verdict with a requested approval or the developer's conclusions.
3. The reviewer stays read-only and inspects connected code and acceptance criteria, not only the diff. If available, read and apply `ponytail:ponytail-review` or `ponytail-review` from the installed skill catalog. That skill supplements review; it must not narrow away correctness, regressions, error handling, or missing requirements. If unavailable, perform those checks directly.
4. The reviewer sends **REVIEW_CHANGES_REQUESTED**, **REVIEW_APPROVED**, or **BLOCKED** to the gatekeeper and notifies the developer. Findings include stable IDs, file locations, concrete impact, and the validation needed to close them. Approval identifies the exact reviewed snapshot and any remaining verification limits.
5. The gatekeeper evaluates each finding against the contract. Send actionable findings to the developer, including the finding IDs and reviewer ID. Resolve disagreements with code or test evidence; record why a finding is invalid or out of scope rather than silently dropping it. If scope must change, obtain the user's decision.
6. The developer fixes the accepted findings, runs affected checks, and sends **FIXES_READY** to the gatekeeper and reviewer, mapping each finding to its fix and evidence. The gatekeeper confirms that editing has stopped and dispatches re-review of the new snapshot.
7. Reuse the reviewer for re-review so it checks both finding closure and regressions introduced by fixes. Repeat until the reviewer approves the current complete change and the gatekeeper's completion conditions hold.

Use `collaboration.send_message` for notifications and `collaboration.followup_task` to assign fixes or re-review to an existing idle subagent; a notification alone does not start a new turn. Subagent final responses are delivered to the gatekeeper automatically. Peer notifications report status; only the gatekeeper authorizes the next implementation or review phase. Use `collaboration.wait_agent` to await results and check the ledger when messages arrive. Ignore duplicate or stale completion events for acceptance, and never mistake a tool/subagent failure for review approval.

Keep the user informed of meaningful progress and blockers while subagents work. On a stalled or failed subagent, use `collaboration.list_agents` to inspect its status and preserve its work before restarting or reassigning. Use `collaboration.interrupt_agent` to stop the old writer before authorizing a replacement. The gatekeeper may increase reasoning effort or choose a stronger model when a failure demonstrates the need.

Do not assume subagents survive closure of the parent session. When resuming, check which agents still exist and reconstruct missing assignments from the ledger before continuing.

## Completion and stopping conditions

“Perfect review” means no unresolved valid findings within the agreed feature scope, satisfied acceptance criteria, and passing required checks on the final reviewed state. It is not a promise that software has no possible defects. Do not create speculative requirements or cosmetic churn to prolong the loop.

Before announcing completion, verify that:

- The final integrated change satisfies the contract and preserves unrelated work.
- Required checks passed for that state; later edits invalidate affected results and require re-review.
- The reviewer approved that same state, with every finding fixed or explicitly adjudicated using evidence.
- No subagent is still editing or carrying an unresolved assignment.

Do not retry an unchanged failure indefinitely. If the same blocker or disputed finding survives two fix/review cycles without new evidence, reassess the cause and change the assignment, model, or approach. If progress requires unavailable access, an external dependency, or a product decision, report the specific blocker and leave the feature incomplete. Required checks that cannot run are blockers unless the user explicitly accepts a revised completion condition.

In MetaDonalds, follow `AGENTS.md` for validation. Local tests use fakes; live submissions and image publishing require explicit task scope. A timed-out submission may already have succeeded, so never use automatic submission retries as a review or validation technique.

Only the gatekeeper announces completion. Report the delivered behavior, validation, review outcome, and material limitations. Delegation does not authorize merging, deploying, publishing, or external messaging beyond the user's request.
