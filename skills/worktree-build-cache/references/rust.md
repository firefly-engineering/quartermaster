# Rust / cargo

The default layout is the problem: `target/` lives inside each checkout, so
every fresh worktree recompiles the world — dependencies included. This is
the ecosystem where the cold-worktree failure is most common.

## Recipes, in order of impact

1. **Shared build directory.** Point `build.target-dir` at a per-machine path
   via `.cargo/config.toml` (helps everyone; a normal repo change) or export
   `CARGO_TARGET_DIR` in the shell profile (per-user). One cold rebuild pays
   for the switch. Caveat: concurrent builds from different checkouts
   serialize on the target-dir lock — usually a fine trade, but if agents
   build in parallel consider pairing this with sccache.
2. **sccache.** `export RUSTC_WRAPPER=sccache` (machine-level). Compilations
   are cached content-addressed and shared across checkouts without the
   single-directory lock. Note cargo still links in each worktree; the win is
   on the compile side.
3. **Neither changes dependency *downloads*** — the registry cache
   (`~/.cargo/registry`) is already shared, so downloads were never the slow
   part; recompiling them was.

## Tests

`cargo test` compiles test binaries into the same `target/` — every cold
worktree also relinks every test binary, which for large crates costs more
than the library build. The shared target-dir / sccache recipes above fix
tests with the same switch; there is no separate test cache to configure.
`cargo nextest` adds its own run cache but still depends on compiled binaries
being present.

## Diagnosis

- `cargo build --timings` or watching the first lines: "Compiling <dep>"
  for dozens of crates in the worktree while the root says "Finished" in
  milliseconds → per-checkout target dir, recipe 1.
- Worktree says "Fresh <crate>" for most crates → target dir is shared and
  the invariant should hold; look elsewhere.
- Check `~/.cargo/config.toml` and the repo's `.cargo/config.toml` — one
  already may set target-dir; a wrong or relative value defeats it.
- Slow *test execution* (not compile) in a fresh worktree usually isn't a
  cache problem at all — look for tests depending on checkout-local state
  (fixtures under the old path, absolute paths in config).

## Do not

- Do not symlink `target/` between checkouts — same lock contention as a
  shared dir, plus stale absolute paths in some artifacts; the config option
  is the supported way.
