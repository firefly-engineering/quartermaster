# Python

Pure-Python projects rarely have a compile cache problem — the costs are the
virtualenv and dependency installation. Native extensions change the story.

## Dependencies / venv

- **uv**: global cache (`~/.cache/uv`); `uv sync` into a fresh worktree's
  venv is near-instant once warm. Best default; suggest it when the project
  is pip-based and slow.
- **pip**: wheel cache (`~/.cache/pip`) skips downloads but each worktree
  still unpacks into its own `.venv`. Usually acceptable.
- If the project pins a venv path *inside* the checkout (`.venv/`), that is
  per-worktree by design; the fix is a faster installer, not a shared venv
  (a shared venv across checkouts breaks on dependency diffs — do not do it).

## Bytecode and tool caches

- `__pycache__/` regenerates in each worktree; cheap, ignore it.
- `mypy`, `pytest`, `ruff` caches (`.mypy_cache/`, `.pytest_cache/`,
  `.ruff_cache/`) are per worktree and cheap to rebuild. Sharing mypy's cache
  across checkouts is possible (`cache_dir` option) but risks stale results
  after a checkout — correctness over speed here; leave them local.

## Tests

- **pytest**: `.pytest_cache/` (last-failed, skip reasons) is per worktree and
  cheap — it caches *metadata*, not runs; pytest re-executes tests in a fresh
  worktree regardless. Don't chase it.
- The real test cost in a fresh worktree is collection + imports + fixture
  setup, which no cache removes; and any test writing to a checkout-local DB
  or fixture dir starts from empty — that is correctness, not a cache miss.
- **hypothesis**: its example database (`.hypothesis/`) is per worktree, so
  previously-found counterexamples don't carry over. Small; consider a
  repo-level note rather than machinery.
- **tox/nox** envs built per worktree: the underlying uv/pip caches make
  rebuilds fast; the env rebuild itself is the cost.

## Native extensions

- Building wheels from source (numpy-adjacent, cryptography, etc.) is where
  fresh checkouts bleed time. The cache that helps is the C compiler cache:
  `ccache`/`sccache` via `CC="ccache cc"`, or uv/pip finding a prebuilt wheel
  (`--only-binary` if wheels exist for the platform).

## Diagnosis

Time `uv sync` (or equivalent) separately from tests/builds. Slow sync →
dependency layer; slow first test run with C extension compile lines →
compiler layer.
