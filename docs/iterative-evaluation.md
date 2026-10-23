# Iterative evaluation runtime

`security.autonomy_level = "auto"` opts an agent into the iterative confidence loop. The `/auto` and `/build` commands also select auto mode. Other autonomy levels keep single-pass completion. An embedding caller can disable the loop explicitly with `agent.AutoMode().SetSelfEval(nil)` after selecting auto mode.

Both `Agent.Run` and `Agent.Stream` invoke the loop after an otherwise successful assistant completion. The first iteration evaluates the answer already produced; it does not issue another provider call. If the configured confidence policy requests revision, the agent adds reflection context, or a generic correction request when no reflector is available, and executes additional steps. Revision work shares the original turn's admission, goal accounting context, context cancellation, context budget and total step limit. It does not recursively call `Run` or create a second goal turn. The loop's own iteration limit applies in addition to the remaining step budget.

An accepted evaluation returns the final answer. A deferred evaluation returns a blocked result explaining that the confidence policy deferred; it does not report successful completion. Provider, verification, cancellation and budget failures terminate evaluation and record a failed lifecycle observation. A failed post-write verification on the initial answer bypasses iterative evaluation. Existing tool approval and post-write checks also apply to revision steps.

Streaming revisions currently use the provider's `Complete` path. The stream emits evaluation status phases and a final `EventDone.Result.Response`, but no revision token deltas. Consumers must use the final result as authoritative, even when they already displayed tokens from the first answer. Cancellation can prevent delivery of a final stream event; persisted lifecycle observations still record evaluation termination when the caller unwinds.

## Desktop preference and final answers

Desktop Settings includes **Autonomous mode with answer review**, off by default. This selects the existing `auto` autonomy level, including its behavior of skipping tool approval prompts; it is not only a display preference. The UI explains the additional provider calls and retained safety/turn limits. Saving reconnects the backend with this explicit setting. Both desktop and terminal clients replace initial streamed text with the authoritative final response after revisions. The native source scenario enables the setting, observes two actual iterations against a controlled local provider and checks the revised answer and persisted status.

## Observed status and persistence

`self.eval.status` preserves post-write verification and heuristic confidence observations and adds `iterative_evaluation` when the optional loop actually ran. Its fields include a distinct `source` (`iterative_self_evaluation`), `status`, `phase`, `iteration`, `max_iterations`, start/completion timestamps and a bounded final note. Running phases are `executing`, `assessing` and, when a separately wired red-team gate runs, `red_team`. Final statuses are `accepted`, `deferred` or `failed`. `self_evaluation_available` indicates an observed invocation, rather than merely a configured autonomy level. An untouched session has no iterative observation.

The loop observation is persisted in the existing per-session runtime-status record, independently of transcript saves. A cold server can read completed observations without constructing an agent. Persisted running work becomes `unconfirmed` on recovery; the reader does not claim ownership of another runtime or infer whether that runtime is alive. Persistence availability and write failure remain separately visible. Snapshots are copied, validated and bounded before restoration.

Confidence acceptance is not test evidence. The default loop uses heuristic confidence and has no production reflector or adversarial test generator wired by selecting auto mode alone. `red_team_passed` remains null because this endpoint has no durable red-team outcome evidence. Existing post-write checker results remain the source for observed pass/fail test evidence. A separately supplied loop retains the existing red-team gate behavior, including its current best-effort handling of generator execution errors.

## Remaining scope

This integration evaluates completed turns, sequential items and explicitly loaded plan phases. The [loaded-plan executor](autonomous-plans.md) schedules phase progression and records bounded outcomes/completion callbacks. Automatic checkpoint resume, production red-team configuration and individual revision token/tool streaming remain open. Confidence acceptance does not establish independent task acceptance or exhaustive autonomous-plan guarantees.

## Validation

The agent tests include the real default confidence policy and task classification, without an injected scorer; task titles reach the loop and familiar explanation tasks can revise and then accept. They also exercise actual `Run` and `Stream` callers with deterministic first-pass acceptance, revision and acceptance, deferral, cancellation, total-step exhaustion and persistence failure. PostgreSQL goal tests establish that revisions remain in one admitted turn and one usage ledger. API tests exercise configured auto mode through the production RPC, per-session durable observations, a cold server and in-flight recovery.

Run the package race checks with the mandatory local PostgreSQL test environment:

```sh
PG_TEST_URL='postgres://overkill_test:local-audit-only@127.0.0.1:55432/overkill_test?sslmode=disable' \
OVERKILL_REQUIRE_TEST_DB=1 \
.cache/toolchains/go/bin/go test -race ./internal/agent ./internal/api ./internal/session -count=1
```
