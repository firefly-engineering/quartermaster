---
name: worktree-build-cache
description: >
  Verify that builds and test runs in fresh git worktrees and jj workspaces
  reuse the machine's shared compiler, dependency and test caches instead of
  cold-rebuilding and cold-rerunning, and fix them when they don't. Use
  whenever spawning a sub-agent or agent session in an isolated checkout,
  creating or working in a worktree/workspace, when a fresh-checkout build or
  test run is suspiciously slow, when someone asks whether a build or tests in
  a new worktree/workspace will be fast, and for setting or checking
  per-project build/test-speed expectations recorded in AGENTS.md or CLAUDE.md.
  Also re-run after changing a build tool, test runner, cache configuration, or
  CI cache setup.
---

# Worktree build cache

A fresh worktree has no build artifacts: no `target/`, no `node_modules/`, no
`.venv/`. Shared, machine-wide caches — `GOCACHE` (which also caches *test
results*), ccache/sccache, the cargo registry, the Nix store, the pnpm store —
*can* make a build there nearly as fast as an incremental one, and a test run
nearly as fast as a no-op re-run. Whether they actually do depends on the tool
and its configuration. Never assume; measure. Tests are often the bigger loss:
test caches are more fragile than build caches (environment-sensitive,
mtime-sensitive, per-directory state), so a worktree that builds fast can
still re-run every test.

## The invariant

A build — and a test run — in a fresh worktree at the same source state
should cost about the same as the warm incremental equivalent in the main
checkout. If it instead costs about the same as a cold build, cache sharing
is broken and every isolated agent pays the cold price.

## Workflow

1. **Read recorded expectations first.** Check the repo's `AGENTS.md` (then
   `CLAUDE.md`) for a "Worktree build expectations" section. It names the
   build command and the last measured numbers. Honor it; re-measure only when
   asked, or after the build config changed. No section means nobody has
   checked — proceed.
2. **Identify the build and test commands.** From `AGENTS.md`, else infer from
   project files (`justfile`, `Makefile`, `package.json`, `Cargo.toml`,
   `go.mod`, `flake.nix`). If they are ambiguous or take more than a couple of
   minutes, confirm with the user before running them repeatedly.
3. **Detect the VCS frontend.** jj if `.jj` exists and `jj root` resolves; git
   otherwise. In a colocated repo a jj workspace is backed by a git worktree —
   what matters is measuring the checkout style the user's agents actually
   use. Default to the repo's own frontend; ask if the user runs both.
4. **Measure.** From the repo root:
   `scripts/bench-cache.sh -- <command...>` (pass `--vcs jj|git` to
   override detection). The script times a warm incremental run, then spawns
   a worktree at the current commit and times the same command there, then
   removes the worktree. Run it once per command worth benchmarking — usually
   the build and the test command. For tests, run the script a second time if
   the first worktree test run looks cold: a *second* worktree run shows
   whether the cache is shared-but-missed-once (healthy) or never populated
   machine-wide (broken). It never deletes caches to simulate cold — that
   destroys real state; only a diagnosis that needs a true cold baseline
   justifies one, with the user's say-so.
5. **Interpret.** worktree ≤ ~1.3× incremental (and neither is near-cold) →
   shared cache healthy. worktree ≈ cold → broken; diagnose. At
   sub-second scales the ratio is noise — trust the build/test logs (does the
   worktree recompile dependencies? re-run tests?) over the ratio, and prefer
   an absolute time floor as the recorded tripwire (see step 8).
6. **Diagnose.** Read `references/<ecosystem>.md` for the build tool in
   question — Go, Rust, TypeScript, Python, Nix. With several ecosystems in
   one repo, chase the slowest layer first: dependency download → codegen →
   compile → link.
7. **Fix only what pays.** If the measured penalty is small (sub-second, or
   a small fraction of the command), the right outcome is *no fix* — say so in
   the expectations block with the numbers that justify it. Caches that cost
   complexity (shared locks, stale state) must earn their keep. When a fix is
   warranted, prefer machine- or user-level configuration (sccache, a shared
   `CARGO_TARGET_DIR` in the shell profile) over repo edits. A repo config
   change is fine when it helps everyone who builds the repo (e.g.
   `.cargo/config.toml` target-dir) — it is a normal contribution, not an
   expectations file. After any fix, re-run the benchmark to verify the
   invariant now holds, and expect one last cold rebuild as the price of the
   switch.
8. **Record expectations.** Propose — do not silently write — this block for
   the repo's `AGENTS.md`, filled with the measured numbers:

   ```markdown
   ## Worktree build expectations
   - Build: `<command>` from the repo root. Tests: `<command>`.
   - Warm incremental: build <Ns>, tests <Ns>. Fresh worktree/workspace:
     build <Ns>, tests <Ns> (measured YYYY-MM-DD).
   - Verdict: shared cache <healthy|broken — one-line cause and fix>.
   - Tripwire: if a fresh-checkout <build/test> exceeds <absolute floor —
     preferred over a ratio; at sub-second scales ratios are noise>, cache
     sharing broke: run the worktree-build-cache benchmark before touching
     build config.
   ```

   This is the only per-project artifact the skill relies on. Numbers decay:
   date them, and treat them as a tripwire, not a contract. Leave commit
   decisions to the user unless the repo's conventions say otherwise — say in
   the report what is committed and what sits in the working tree.

## Notes

- **`-buildvcs=false` for Go test runs.** Go stamps `vcs.revision`, `vcs.time`
  and `vcs.modified` into every `package main` binary it builds — including
  test binaries. The stamps come from the checkout's own `.git`, so they
  differ between two checkouts of identical content (and flip when the
  working copy goes dirty→clean), which makes every main package's test
  binary — and therefore its whole cache entry — differ per checkout. In a
  shared-path or copied-tree setup, pass `-buildvcs=false`; test binaries do
  not ship.
- **Symlinks break naive content hashing.** A repo may contain symlinks, and
  a symlink to a directory (`.claude/skills → .agents/skills` is the classic)
  makes `cat` fail with EISDIR. A hash loop that treats every listed name as
  a regular file then truncates mid-list or dies — and the resulting identity
  depends on failure order, which looks exactly like "the cache randomly
  misses". When hashing a tree by content: resolve each entry (regular file →
  bytes; symlink → its link target; directory symlink → the target string,
  never its contents), and make failures loud instead of silent.
- **`jj -R <root> file list` prints paths relative to the invoking cwd**, not
  the repo root. Run the listing with cwd inside the root, or two identical
  checkouts produce two different identities (`../`-heavy names from the
  other one) and every lookup misses. Same class of bug as the symlink one:
  the identity depends on where the caller stood, not on content.
- Concurrent builds that share one cache dir (cargo target, sccache) contend
  on a lock — that is correctness working, not a bug. If agents build in
  parallel, weigh lock contention against cold rebuilds; sccache, being
  content-addressed, contends least.
- The benchmark moves the *whole* cost question to one command. If the
  project builds in stages (install deps, then compile), benchmark the stage
  that matters or the whole pipeline, consistently in both places.
- Clean up everything the benchmark spawns; the script handles its worktree,
  but remove any scratch files you create yourself.
