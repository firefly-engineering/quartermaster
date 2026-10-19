---
name: mass-implement
description: >-
  Implement a set of ready-for-agent tickets that form a dependency DAG. Fans out a
  fresh sub-agent per ticket (each applies the `implement` skill + `/code-review` in an
  isolated worktree, taught the shared spec as global context), stacks each change on
  its dependencies, then LINEARIZES the whole DAG into one clean, conflict-resolved,
  mergeable PR stack before pushing. Use when asked to "tackle all the ready-for-agent
  tickets", "implement this stack/epic of tickets", or build a DAG of dependent tickets
  autonomously.
disable-model-invocation: true
---

# Implement a DAG of tickets, then ship a clean stack

You are the **orchestrator**. You do not write feature code yourself: you decompose the
work, drive sub-agents, verify their output, and assemble a mergeable stack. The whole
run must complete **without live human interaction** (see *Human input* below).

## Inputs (ask only if genuinely unknowable)
- **Ticket source**: a repo + label (default `ready-for-agent`) or an explicit ticket list.
- **Global context**: the spec/PRD/issue that all tickets descend from. Its URL is injected
  verbatim into every sub-agent prompt so no agent re-derives shared decisions.
- **Target repo**: where the code changes land (may differ from where you were invoked).

## Phase 0: Map the DAG
1. Pull the tickets and each ticket's **dependencies** (parse the "Blocked by" section /
   linked issues). Confirm every dependency is either in the set or already merged to the
   trunk; a dependency that is neither is a blocker: stop and report it.
2. Build the dependency DAG and a **topological order**. Note which tickets are independent
   **siblings** (parallelizable) vs. **dependents** (must wait). Multi-parent tickets become
   merge points.
3. Restate the plan in your own text: the order, the sibling groups, and the merge points.

## Phase 1: Implement (fan-out)
Process tickets in topological order. **Run independent siblings in parallel; run a dependent
only after all its dependencies are finalized and verified.**

For each ticket, spawn a **fresh** sub-agent (`Agent`, general-purpose) whose prompt:
- Names the ticket (full acceptance criteria) and injects the **global-context URL** as the
  authoritative source for shared decisions; points at the specific design doc(s).
- Points at the dependency code it builds on (already present in its worktree).
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
  assumptions or human-input needs.

### Isolation: use the worktree hooks, not ad-hoc directories
Do **not** hand-create workspaces in arbitrary paths. Isolate each ticket with the harness's
worktree mechanism so worktrees land in the canonical `.claude/worktrees/` location and are
auto-cleaned:
- Prefer spawning each sub-agent with **`Agent(isolation: "worktree")`**, or provision the
  worktree via **`EnterWorktree`** / the configured **WorktreeCreate / WorktreeRemove** hooks
  (these are VCS-agnostic: they make a jj workspace or git worktree as the repo dictates).
- **Base each worktree on the ticket's dependency change(s)**, not on trunk, so a dependent
  inherits its dependencies' code and a multi-parent ticket starts from a merge of its
  parents. Name worktrees/branches after the ticket (e.g. `ticket-<N>`) so the mapping is
  legible.

## Phase 2: Verify each change yourself, and handle human input
- **Never trust a sub-agent's "green" claim.** After each agent finishes, independently run
  the build + full test suite in its worktree, plus any repo invariant checks (e.g. an
  import-boundary test). Ignore editor/LSP "package not found / out-of-workspace module"
  diagnostics: they are noise for out-of-tree worktrees; the compiler and tests are ground
  truth. Watch specifically for leftover unused imports from an agent's final review edits.
- Only after a change is verified do you finalize it (name its branch/bookmark) and unblock
  its dependents.
- **Human input**: if a ticket needs a decision, secret, or outward-facing action you cannot
  supply (e.g. "publish to a real external service"), do NOT ask live and do NOT perform the
  side effect. Have the agent implement everything provable offline, then **file a new
  "needs-info" ticket** capturing exactly what's required and **add it to the original
  ticket's "Blocked by"**. Continue the rest of the DAG.

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
- One fresh agent per ticket; never reuse an agent's context across tickets.
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
