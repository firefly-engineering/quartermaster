# Nix

The Nix store is global and content-addressed: identical inputs yield
identical store paths, so a fresh worktree build should be all store hits —
near-instant. If it rebuilds, inputs differ between checkouts, not caches.

## Common reasons a worktree `nix build` rebuilds

1. **Dirty/uncommitted sources.** A flake with uncommitted changes resolves
   differently per checkout (the dirty worktree is the source). Commit, or
   build a clean tree ref.
2. **Source filters including the checkout path** or absolute paths leaking
   into derivations (env vars, `builtins.currentSystem` misuse). Same
   content → same path should hold; if it doesn't, find what injects the
   path.
3. **Different lock/revision per checkout** — the worktree is at a different
   commit by construction; benchmark at the *same* commit, or expect
   legitimately different inputs.
4. **`nix develop` shell rebuilding** — shells are derivations too; a slow
   shell entry in a fresh worktree has the same causes as above.

## Tests

Test execution inside derivations (`nix build` with `doCheck`, `nix flake
check`) is cached only via the whole derivation: identical inputs → store hit,
any input change → tests re-run as part of the rebuild. There is no separate
test cache to configure. If a fresh worktree re-runs checks, the cause is one
of the input-difference reasons above, not the test layer.

## Recipes

- Evaluate the narrowest attribute that answers the question —
  `nix build .#<attr>` on the specific output, never a whole closure.
- Keep logs out of the terminal: `--quiet`, `--no-link`, `-L` when you want
  build logs on failure.
- To confirm hits vs rebuilds: `nix build --no-link --print-out-paths` (or
  `-v`) and look for "this derivation will be built" — an empty build list
  means full store hits.

## Do not

- Do not `nix-collect-garbage` to "reset" caches during a benchmark — that
  destroys real state for every project on the machine.
