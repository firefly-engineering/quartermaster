# Go

Go shares caches machine-wide by default, so a fresh worktree build is
usually close to incremental. If it is not, something is overriding the
defaults.

## What is shared already

- `GOCACHE` (default `~/.cache/go-build`, or `os.UserCacheDir()`): compiled
  packages, content-addressed — hits across checkouts with identical source.
- `GOMODCACHE` (`~/go/pkg/mod`): downloaded modules, shared.
- `ccache`/`sccache` for cgo if configured.

## Common reasons a worktree build is cold

1. **The project overrides `GOCACHE`** — in a `justfile`, `Makefile`,
   `flake.nix` devShell, `.envrc`, or a wrapper script. Grep the build files
   for `GOCACHE=` before blaming the toolchain. Fix: make the override point
   at a shared path, or drop it; if it exists to isolate outputs for CI, gate
   it on CI being set rather than applying it locally.
2. **`GOFLAGS` or `-a` flags** forcing full rebuilds (`-a` rebuilds
   everything regardless of cache).
3. **The build is not `go build`.** A script that runs `go clean -cache`,
   regenerates code with unstable output, or embeds timestamps in generated
   files invalidates the cache every run. Generated-file instability is a
   codegen problem, not a cache problem — pin the generator's output.
4. **cgo with path-dependent flags.** C toolchain invocations that embed
   absolute paths change per checkout. `-trimpath` on the Go side plus
   sccache for the C side usually settles it.

## Tests

Two layers with different behavior (verified empirically):

- **Compiling test binaries** hits the build cache like anything else —
  content-keyed, path-independent, shared across worktrees.
- **Test *results*** are keyed in part by the working directory: a fresh
  worktree re-executes the tests **once**, then caches within that worktree.
  This is per-directory, not jj-specific — a plain directory copy behaves the
  same. A first run in a new worktree showing real `--- PASS` lines is
  expected and correct, not a broken cache; every later run prints `(cached)`.

Do not "fix" the once-per-workspace re-run unless the suite is slow:
`-count=1`, `go clean -testcache`, or a scratch `GOCACHE` all make things
strictly worse. Two levers do exist if the suite justifies it:

- **`go test -trimpath ./...`** strips the checkout path from the cache keys —
  verified: a first run in a fresh worktree then hits results cached by
  another checkout. Applies per-invocation; a user-wide `GOFLAGS=-trimpath`
  would leak into every Go project on the machine, so wire it into the repo's
  test command instead.
- **A faster suite** is often the honest fix — the re-run costs one suite
  execution.

The cache is *also* invalidated by:

1. **Environment differences per checkout.** A direnv `.envrc` that sets
   different values per worktree, or tests reading env the root sets and the
   worktree does not, defeats the cache silently. Keep exported env
   checkout-independent.
2. **Tests reading mtimes or absolute paths** — the worktree's files have
   fresh mtimes and a new path; a test that asserts on either re-runs.
3. **`-count=1` or `GOFLAGS=-count=1`** in a wrapper/justfile disables the
   cache everywhere; check the wrapper before blaming the worktree.

Distinguish the layers when judging: cached compile shows near-instant wall
time even where tests re-run; a cached test run prints `(cached)` per package.
Expectation to record: builds fully cached across worktrees; tests re-run
once per new worktree (cost = one suite run), cached after — or fully cached
if the test command uses `-trimpath`.

## Test-binary identity

The cache key starts with the test binary's build. Two things can make
otherwise identical checkouts build different test binaries:

1. **VCS stamping** (`-buildvcs`, default on): Go embeds `vcs.revision`,
   `vcs.time`, `vcs.modified` into every `package main` binary — including
   test binaries of main packages. The stamps come from the local `.git`, so
   they differ per checkout and flip when the working copy goes dirty→clean.
   Pass `-buildvcs=false` for test runs; test binaries do not ship.
2. **Missing `-trimpath`** in some invocations but not others: untrimmed
   absolute paths land in the binary and in its action ID. Use the same flag
   set everywhere a given test binary is built.
3. **A broken identity hash, not the toolchain.** If the same content hashes
   differently across checkouts (or hits vary run to run for no visible
   reason), suspect the hashing itself: a symlink to a directory makes `cat`
   fail with EISDIR, and a loop that treats every entry as a regular file
   truncates its input mid-list or aborts — silently, under `set -e`. See
   SKILL.md's symlink note; make identity hashing symlink-aware and loud.

## Recipes

- Cross-path reuse improves with `go build -trimpath`; harmless to add.
- Vendor mode (`-mod=vendor`) skips the module cache but compilation still
  hits `GOCACHE`.
- Verify quickly: `go env GOCACHE GOMODCACHE` from both the root checkout and
  the worktree — different values mean an override is in play.
