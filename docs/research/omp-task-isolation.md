# Can omp's isolated task sub-agents keep each ticket's change apart in a jj repo?

Research for [#117](https://github.com/firefly-engineering/quartermaster/issues/117), serving
point 7 of [How /mass-implement hands per-ticket workers to omp agents](https://github.com/firefly-engineering/quartermaster/issues/101#issuecomment-6269399665).
Researched 2026-10-19 against omp **18.11.3** (the installed version), read from upstream
[can1357/oh-my-pi at tag `v18.11.3`](https://github.com/can1357/oh-my-pi/tree/96894a6147d8cc64b7e00ad3a7d7178fd93b9531)
and cross-checked against `strings` on the installed binary
(`/opt/homebrew/Cellar/omp/18.11.3/bin/omp`), which carries the same messages.

## Short answer

omp's isolation cannot give `/mass-implement` per-ticket bases, and it refuses jj workspaces
that have no `.git`. It *can* keep a change apart (branch mode with `apply: false` leaves a
`omp/task/<id>` git branch, which jj imports as a bookmark in a colocated repo), but both
switches are global settings, not per task. The robust route on omp is the hand-made one: the
orchestrator runs `jj workspace add <path> -r <dep1> -r <dep2> …` per ticket and points a
non-isolated sub-agent at that path.

## 1. Base on the ticket's dependency changes

**No.** An isolated task always starts from a copy of the parent session's checkout; nothing
in the task schema names a base revision.

- The isolation source is the git repo root of the parent session's cwd:
  `prepareIsolationContext(request.session.cwd)`
  ([structured-subagent.ts#L854](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/structured-subagent.ts#L854)
  → [isolation-runner.ts#L145-L147](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/isolation-runner.ts#L145-L147)).
- `ensureIsolation` clones that whole root into `getWorktreeDir(t<digest>)/m`
  (`~/.omp/wt/…`) with the chosen backend, then detaches the clone's `.git` so it borrows the
  source object DB through alternates
  ([worktree.ts#L619-L669](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/worktree.ts#L619-L669)). On macOS
  `auto` tries APFS `clonefile`, then ZFS, then `rcopy`
  ([crates/pi-iso/src/lib.rs#L137](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/crates/pi-iso/src/lib.rs#L137)); `rcopy` is
  `git worktree add --detach <merged> HEAD`
  ([crates/pi-iso/src/rcopy.rs](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/crates/pi-iso/src/rcopy.rs)).
- The baseline is "a point-in-time copy of the parent's working tree": git `HEAD` plus staged,
  unstaged and untracked content
  ([worktree.ts#L285-L306](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/worktree.ts#L285-L306),
  [#L202-L235](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/worktree.ts#L202-L235)).
- The task item is `{ name?, agent?, task, solutionSpace, effort?, outputSchema?, schemaMode?, tools?, isolated? }`:
  no `cwd`, no base revision
  ([docs/tools/task.md#L35](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/docs/tools/task.md#L35)).

So the only lever is the parent's own checkout: in a colocated repo, `jj new <dep1> <dep2>`
then `jj new` (so `@` is empty and git `HEAD` is the merge) before spawning. That serializes
spawns with different bases, and with `async.enabled` the clone happens inside the background
job ([structured-subagent.ts#L851-L880](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/structured-subagent.ts#L851-L880)),
so the tool call returning does not prove the snapshot was taken; moving `@` for the next
ticket can race it. A batch `tasks[]` call clones every item from the same `@`. Workable only
for siblings sharing one base.

## 2. Keeping each change apart

**Yes, but only globally.** `task.isolation.merge: branch` with `task.isolation.apply: false`
keeps each task's change on its own git branch and never touches the parent checkout.

- Branch mode builds the commits through temporary indexes (no checkout) and writes
  `refs/heads/omp/task/<id>`, parented on the baseline `HEAD`. Agent commits are kept verbatim
  on a clean baseline; on a dirty baseline they are replayed so the parent's WIP is not carried
  ([worktree.ts#L887-L974](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/worktree.ts#L887-L974)).
- With `apply: true` the branch is cherry-picked onto the parent's `HEAD` and then deleted
  ([isolation-runner.ts#L633-L692](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/isolation-runner.ts#L633-L692),
  [worktree.ts#L995-L1031](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/worktree.ts#L995-L1031)).
  With `apply: false` the merge step is skipped and the result names the branch: "changes
  captured on branch `omp/task/<id>` (apply=false). Not merged."
  ([structured-subagent.ts#L929-L930](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/structured-subagent.ts#L929-L930),
  [prompts/tools/isolation-summary.md](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/prompts/tools/isolation-summary.md)).
  Patch mode with `apply: false` instead leaves a `<id>.patch` artifact.
- **Per task vs global.** The `task` tool forwards only `isolated` per item
  ([task/index.ts#L908](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/index.ts#L908)); merge and apply
  come from settings
  ([structured-subagent.ts#L467-L470](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/structured-subagent.ts#L467-L470),
  [task/settings.ts#L148-L175](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/settings.ts#L148-L175)).
  The `eval` tool's `agent(prompt, isolated=, apply=, merge=)` takes `apply` per call, but
  `merge=false` only forces patch mode; branch mode still comes from the setting
  ([eval/agent-bridge.ts#L181-L186](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/eval/agent-bridge.ts#L181-L186),
  [docs/tools/eval.md#L213](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/docs/tools/eval.md#L213)).
- Settings can be scoped narrower than `~/.omp/agent/config.yml`: a project
  `<cwd>/.omp/config.yml`, a per-run `omp --config <overlay.yml>`, or `--profile`
  ([docs/config-usage.md#L173-L184](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/docs/config-usage.md#L173-L184)). So a batch dispatched
  with `--config` carrying `task.isolation: {merge: branch, apply: false}` gets it without
  changing the user's default.
- In a colocated jj repo, `refs/heads/omp/task/<id>` is imported by jj as bookmark
  `omp/task/<id>` on the next jj command (jj's standard git-ref import; not exercised here).

## 3. jj support of `isolation.backend: auto`

**Colocated only.** The backend setting picks a copy mechanism (`apfs`, `btrfs`, `zfs`,
`reflink`, `overlayfs`, `projfs`, `block-clone`, `rcopy`); none is jj-aware, and every
capture, commit and merge step is git
([docs/tools/task.md#L136-L137](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/docs/tools/task.md#L136-L137)).

- `getRepoRoot` throws for a pure jj workspace: "Isolated task execution requires a Git
  checkout, but this workspace is pure Jujutsu (`.jj/` without a colocated `.git/`). Run
  `jj git init --colocate` …" ([worktree.ts#L50-L65](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/packages/coding-agent/src/task/worktree.ts#L50-L65)).
  "Pure" means jj is the nearest VCS root: colocated workspaces pass; a jj workspace nested
  inside a git checkout fails ([crates/pi-vcs/src/lib.rs#L290-L333](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/crates/pi-vcs/src/lib.rs#L290-L333)).
- A secondary `jj workspace add` workspace has no `.git` unless created with `--colocate`
  (jj 0.46; default only when the current workspace is colocated and `git.colocate` is true,
  per `jj workspace add --help`). quartermaster's `fresh` dispatch and Claude's worktree hooks
  make such workspaces (this research ran in one: `gh` reported "not a git repository"), so an
  omp batch dispatched `fresh` would fail on its first `isolated: true` task.
- What lands where: the clone at `~/.omp/wt/t<9-hex digest>/m` (`OMP_WORKTREE_DIR` overrides
  the root), including a full CoW copy of `.jj/` for a main workspace; the branch in the
  parent's git refs; patch artifacts in the session artifacts dir; failed merges retain the
  workspace as `…retained-*`.

## What this means for `skills/mass-implement/SKILL.md`

The harness-neutral rule stands (one checkout per ticket, based on its dependencies, its change
kept apart). The omp note should say: do not use `isolated: true` for tickets; create each
ticket's checkout yourself with `jj workspace add <path> --name ticket-<N> -r <dep>…` (multiple
`-r` gives the merge base), spawn a non-isolated `task` told to work only under that absolute
path, verify there, and `jj workspace forget` + remove it when done. The ticket's change is
then an ordinary jj change in the shared store, ready to stack. `isolated: true` with
`merge: branch, apply: false` is a fallback only for a colocated checkout and siblings that
share one base.

## Not verified

- No live omp run. The research harness blocks jj/git commands outside the agent's own
  worktree, so the planned throwaway colocated repo could not be built. Everything above is
  read from source at the installed version's tag.
- Whether a non-isolated omp sub-agent works cleanly in a path outside the parent's cwd
  (`workspace.additionalDirectories` / `--add-dir` exist,
  [docs/settings.md#L771](https://github.com/can1357/oh-my-pi/blob/96894a6147d8cc64b7e00ad3a7d7178fd93b9531/docs/settings.md#L771)); the hand-made route assumes it does.
- How jj sets git `HEAD` when `@` itself is a merge; hence the extra `jj new` in §1.
