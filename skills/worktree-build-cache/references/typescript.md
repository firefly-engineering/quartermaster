# TypeScript / JavaScript

Two layers to share: dependencies (`node_modules/`) and build artifacts
(tsbuildinfo, bundler caches, monorepo task caches).

## Dependencies

- **pnpm**: global content-addressed store with hardlinks — installs in a
  fresh worktree are near-free once the store is warm. Best default.
- **npm/yarn**: the download cache is shared (`~/.npm`, yarn cache), so a
  fresh install skips the network but still unpacks and links everything.
  Works; slower than pnpm on big trees.
- If the repo uses pnpm already, a fresh worktree install being slow usually
  means the store dir was overridden in `.npmrc` — check it.

## Build artifacts

- **`*.tsbuildinfo`** (incremental tsc): lives in the checkout → fresh
  worktree recompiles. Recompilation is usually the *correct* behavior after
  a checkout; this layer is cheap unless the project is huge.
- **turborepo**: caches in `.turbo/` per checkout, `~/.turbo` or a remote
  cache shared. Enabling the shared dir (or remote) is the big monorepo win.
- **nx**: `~/.nx` shared by default; if worktree builds are cold, someone
  pointed the cache at a local dir.
- **webpack/vite** caches under `node_modules/.cache/`: per worktree.
  Normally fine (warm-up is one build); move `CACHE_DIR` only for huge
  rebuilds.

## Tests

- **jest**: transform cache in `node_modules/.cache/jest` — per worktree, so
  a fresh worktree re-transforms everything once. Cheap for small repos; for
  big ones, `cacheDirectory` in the jest config can point at a shared path.
- **vitest**: same shape (cache in `node_modules/.cache/vitest`).
- **turborepo/nx**: test tasks are cached like build tasks — the shared/remote
  cache is where the win is; a fresh worktree with warm task cache skips test
  runs entirely for unchanged inputs.
- **playwright**: browsers are shared machine-wide (`~/Library/Caches/
  ms-playwright`) and install once; per-checkout cost is `node_modules`
  again. Test *artifacts* (traces, screenshots) are per-worktree by design —
  do not share them.

## Diagnosis

`npm ci && npm run build` timings split cleanly: slow install → dependency
layer (pnpm/store); fast install but slow build → artifact layer (task
cache). Fix the slow layer only; layers that are already cheap earn no
machinery.
