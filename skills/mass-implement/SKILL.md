---
name: mass-implement
description: >-
  Implement a batch of ready-for-agent tickets that form a dependency DAG, on one
  harness (claude or omp). Fans out a fresh sub-agent per ticket (each applies the
  `implement` skill + `/code-review` in its own checkout, taught the shared spec as global
  context), stacks each change on its dependencies, then LINEARIZES the whole DAG into one
  clean, conflict-resolved, mergeable PR stack before pushing. Use when asked to "tackle all
  the ready-for-agent tickets", "implement this stack/epic of tickets", run a batch work
  item, or build a DAG of dependent tickets autonomously.
disable-model-invocation: true
---

# Implement a DAG of tickets, then ship a clean stack

You are the **orchestrator**. You do not write feature code yourself: you decompose the
work, drive sub-agents, verify their output, and assemble a mergeable stack. The whole
run must complete **without live human interaction** (see *Human input* below), except
Phase 3½.

## Inputs (ask only if genuinely unknowable)
- **Ticket source**: a **batch** work item listing its tickets, an explicit ticket list, or a
  repo + label (default `ready-for-agent`).
- **Global context**: the spec/PRD/issue that all tickets descend from. Its URL is injected
  verbatim into every sub-agent prompt so no agent re-derives shared decisions.
- **Target repo**: where the code changes land (may differ from where you were invoked).

## The batch
The unit of a run is a **batch**: one worklog work item listing tickets whose tiers route to
one harness. Its tier is any one of its tickets' tiers (the worklog skill's
[Tiers](../worklog/SKILL.md#tiers)). The harness running you is the batch's harness: run
**every phase** (implement, verify, linearize, publish) on it, with its own sub-agent tool.
Hand no phase to another harness or model.

A batch's stack stands alone on trunk. Start only once every batch it depends on has merged
to trunk; base nothing on another batch's unmerged branches.

## Phase 0: Map the DAG
1. Pull the tickets and each ticket's **dependencies** (parse the "Blocked by" section /
   linked issues). Confirm every dependency is either in the set or already merged to the
   trunk. A dependency that is neither is a blocker, including one in another batch that
   has not merged yet: stop before Phase 1 and report it.
2. Build the dependency DAG and a **topological order**. Note which tickets are independent
   **siblings** (parallelizable) vs. **dependents** (must wait). Multi-parent tickets become
   merge points.
3. Restate the plan in your own text: the order, the sibling groups, and the merge points.

## Phase 1: Implement (fan-out)
Process tickets in topological order. **Run independent siblings in parallel; run a dependent
only after all its dependencies are finalized and verified.**

For each ticket, spawn a **fresh** sub-agent with the harness's sub-agent tool (claude:
`Agent`, general-purpose; omp: `task`) whose prompt:
- Names the ticket (full acceptance criteria) and injects the **global-context URL** as the
  authoritative source for shared decisions; points at the specific design doc(s).
- Names its checkout's absolute path (see *Isolation*) and points at the dependency code it
  builds on, already present there.
- Instructs it to invoke the **`implement`** skill (TDD at seams → typecheck + single-file
  tests often → full test suite once → **`/code-review`** → set the change description with a
  Conventional Commit message). If a repo test harness/fake exists, tell it to test against
  that, offline.
- **Locks down VCS**: the agent describes its change and nothing else; it must NOT create
  branches/bookmarks, push, merge, or touch any other worktree, and fixes forward (never
  `jj undo`/`jj op restore`: the op log is shared with every sibling). It writes its PR title
  (line 1) and body to `/tmp/<repo>-pr-<N>.md`. The orchestrator owns verification, stacking,
  branch naming, and pushing.
- Gives **parallel context**: the siblings in flight (ticket, packages touched, identifiers
  each adds), with the rule "reuse a sibling's name and signature for a shared helper, and
  say so in your report".
- Ends with a **self-contained final report**: what it built, the exact `build`/`test`
  summary line, the `/code-review` outcome, the change description it set, and any
  assumptions. A ticket it cannot finish ends the report with one line in the worklog
  skill's [Escalation](../worklog/SKILL.md#escalation) forms: `question: …` or `stuck: …`.

### Isolation: one checkout per ticket
Each ticket gets **its own checkout**, and its change stays there as its own change, never
patched into your checkout:
- **Base it on the ticket's dependency change(s)**, not on trunk, so a dependent inherits
  its dependencies' code and a multi-parent ticket starts from a merge of its parents.
- Name it, and its workspace or branch, `ticket-<N>`, so the mapping is legible.
- Remove it once its change is verified and stacked.

**On claude**, use the worktree mechanism, so checkouts land in `.claude/worktrees/` and are
auto-cleaned: spawn with **`Agent(isolation: "worktree")`**, or provision via
**`EnterWorktree`** / the configured **WorktreeCreate / WorktreeRemove** hooks (they make a
jj workspace or git worktree as the repo dictates).

**On omp**, make each checkout yourself. Never set `isolated: true` on a ticket's `task`:
omp's isolation copies your own checkout, takes no base revision, and refuses a jj
workspace without `.git`
([research](https://github.com/firefly-engineering/quartermaster/blob/main/docs/research/omp-task-isolation.md)). Per ticket:
1. `jj workspace add <path> --name ticket-<N> -r <dep>…`, one `-r` per dependency (`-r 'trunk()'` when it has
   none). Put `<path>` beside your checkout, never inside it, e.g.
   `<your checkout>-ticket-<N>`.
2. Spawn a `task` with `isolated: false`, told to work only under that absolute path: run
   every command with it as the working directory, and touch no file outside it.
3. Verify there (Phase 2), then `jj workspace forget ticket-<N>` and remove `<path>`.

A non-isolated `task` works under a path outside your cwd with no `--add-dir` or
`workspace.additionalDirectories` (checked live on omp 18.11.3, in a headless run with the
default approval mode). Its change is an ordinary jj change in the shared store, ready to
stack.

## Phase 2: Verify each change yourself, and handle human input
- **Never trust a sub-agent's "green" claim.** After each agent finishes, independently run
  the build + full test suite in its checkout, plus any repo invariant checks (e.g. an
  import-boundary test). Ignore editor/LSP "package not found / out-of-workspace module"
  diagnostics: they are noise for out-of-tree worktrees; the compiler and tests are ground
  truth. Watch specifically for leftover unused imports from an agent's final review edits.
- Only after a change is verified do you finalize it (name its branch/bookmark) and unblock
  its dependents.
- **Human input**: if a ticket needs a decision, secret, or outward-facing action you cannot
  supply (e.g. "publish to a real external service"), do NOT ask live and do NOT perform the
  side effect. Have the agent implement everything provable offline, then **file a new
  "needs-info" ticket** capturing exactly what's required and **add it to the original
  ticket's "Blocked by"**. Continue the rest of the DAG. A ticket report's `question: …` is
  this case: its question becomes the needs-info ticket. A call an agent made itself and
  could ship is not this case: it waits for Phase 3½.
- **A stuck ticket**: a report's `stuck: …` drops that ticket and every ticket depending on
  it from the batch. Record them as their own work item with `item_upsert`, the batch's
  tier, then `item_transition` it to `WAITING` with the agent's `stuck: …` as its summary;
  the worklog skill's [Escalation](../worklog/SKILL.md#escalation) takes it from there. The
  rest of the batch goes on and ships.
- **A batch-level failure**, one that is no single ticket's (a linearized step you cannot
  make build, a push or publish that fails, CI you cannot turn green), ends the run: move
  the batch item to `WAITING` with `stuck: <what you tried, and where it stopped>`.

## Phase 3: Linearize into a clean stack (the wrinkle)
A DAG of stacked/merged changes is painful to merge: squash-merging stacked PRs whose base
isn't yet on trunk silently drops content, and **parallel siblings drift on shared
interfaces** (sibling A changes a signature; sibling B, built in parallel, never saw it). So
before opening any PR, dedicate an **integration/linearization agent** (or do it yourself) to
flatten the DAG into a single **linear** stack on trunk. Publish nothing until this phase is
done, even when a ticket finishes early: a PR opened mid-run goes stale as siblings land, and
its review has to be redone after the rebase.

1. Choose a linear order consistent with the topo sort. Rebase each change onto the previous
   one so the final shape is `trunk → t1 → t2 → … → tn` (collapse merge points into the line).
2. At **every** rebase step, resolve conflicts and then **rebuild + retest that step** before
   moving on. Resolve two conflict classes:
   - **Textual**: overlapping edits (e.g. two siblings adding CLI flags; take the union).
   - **Semantic** (the dangerous one): an interface/signature/contract one sibling changed and
     another didn't implement. A clean text merge that fails to compile is a semantic conflict;
     the rebuild is what catches it. Fix by conforming the lagging code to the new contract.
3. Verify the **tip** of the linear stack builds and passes the full suite + invariants.

## Phase 3½: Settle the judgement calls before publishing
Collect every call the run left open: review findings kept as judgement calls,
assumptions a ticket didn't settle, a report's "decisions for you", and any shared
name or default an agent chose. Put them to the user in **one round**, numbered, each
with your recommendation. Fold each answer into the change it belongs to, then re-run
Phase 3's step checks from the lowest change touched. Publish only after that: every
answer that lands after the push re-runs CI on every PR above it.
This is the run's one live interaction. When no user is present, publish anyway, and
list the calls in the batch's worklog Part and on each PR.

## Phase 4: Publish the stack
1. Push the linear branches.
2. Open one **draft PR per ticket**, each **based on its predecessor branch** (the first on
   trunk). Body links the global-context spec and the ticket; note any "Blocked by".
3. Group them as a GitHub **native stack**: `POST /repos/{o}/{r}/stacks` with the PR numbers
   bottom to top (each base must already equal the previous head). A PR inside a native stack
   refuses base changes; to reshape, `POST …/stacks/{n}/unstack`, re-chain, recreate.
4. Wait for CI on **every** PR in the stack (`gh pr checks <n> --watch`). Once all pass, mark
   every PR ready for review (`gh pr ready <n>`). If one fails, fix it forward on its branch,
   then re-verify and re-push every branch above it. Mark nothing ready until the whole stack
   is green: a partly ready stack invites merging a parent whose child is broken.
5. State the **merge order explicitly** (parent-first, bottom of the stack up). Advise
   **rebase- or merge-commit** over squash for the intermediate PRs, or merging strictly in
   order so each PR auto-retargets to trunk and its diff collapses to its own delta. Squashing
   a stacked PR out of order is what corrupts the stack.
6. Restate the final table (PR, ticket, base, CI status, ready) in your own text.

## Guardrails
- One fresh agent per ticket, on the batch's harness; never reuse an agent's context across
  tickets.
- Orchestrator text is the only channel the user/classifier sees: **restate** every
  sub-agent result and every verification outcome; never paste raw tool/agent output.
- Do not push to trunk, force-push shared branches, or merge PRs unless explicitly asked; the
  human merges. (Force-pushing your own feature branches during linearization is fine.)
- Keep changes on their branches; leave the human a mergeable stack, not a merged trunk.

## Pitfalls learned the hard way
- **LSP diagnostics lie about out-of-tree worktrees** ("cannot find package … in GOROOT",
  "undefined: X" for a symbol in a sibling file). Trust `build`/`test`, not the editor.
- **Agents over-report success.** Re-verify build + tests + invariants yourself, every time.
- **Squash + stacked PRs = silent content loss** when a PR's base hasn't landed on trunk yet.
  Linearize first; merge parent-first; prefer non-squash for the middle of the stack.
- **Parallel siblings cause interface drift.** The linearization rebuild is the only reliable
  place to catch it: a textual auto-merge is not enough.
- If recovery is ever needed, your local VCS stack is the source of truth even when the remote
  branches have been rebased/mangled by merges: reconstruct from it.
