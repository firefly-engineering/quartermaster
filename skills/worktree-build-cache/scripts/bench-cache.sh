#!/usr/bin/env bash
# bench-cache.sh — compare a warm incremental build against a fresh-worktree
# build of the same repo at the same commit.
#
# Usage (from the repo root):
#   bench-cache.sh [--vcs jj|git] -- <build command...>
#
# Prints incremental and worktree timings and a verdict. Removes the spawned
# worktree. Never touches shared caches.
set -euo pipefail

usage() {
  echo "usage: bench-cache.sh [--vcs jj|git] -- <build command...>" >&2
  exit 2
}

vcs_arg=""
if [ "${1:-}" = "--vcs" ]; then
  vcs_arg="${2:-}"
  case "$vcs_arg" in jj|git) ;; *) usage ;; esac
  shift 2
fi
[ "${1:-}" = "--" ] || usage
shift
[ $# -ge 1 ] || usage

now() {
  if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import time; print(time.time())'
  else
    perl -MTime::HiRes=time -e 'print time'
  fi
}

root=$(pwd)
log_root="${TMPDIR:-/tmp}/wtbench-root.log"
log_wt="${TMPDIR:-/tmp}/wtbench-worktree.log"

if [ -n "$vcs_arg" ]; then
  vcs="$vcs_arg"
elif command -v jj >/dev/null 2>&1 && jj root >/dev/null 2>&1; then
  vcs=jj
else
  vcs=git
fi

echo "vcs: $vcs"

# Warm incremental: build twice in the root, time the second run.
echo "warming: first build in root (not timed)"
if ! (cd "$root" && "$@" ) >"$log_root" 2>&1; then
  echo "FAIL: root build failed; log follows:" >&2
  tail -n 40 "$log_root" >&2
  exit 1
fi

t0=$(now)
if ! (cd "$root" && "$@" ) >>"$log_root" 2>&1; then
  echo "FAIL: root build failed; log follows:" >&2
  tail -n 40 "$log_root" >&2
  exit 1
fi
t1=$(now)
incremental=$(python3 -c "print(f'{$t1 - $t0:.2f}')")

# Fresh worktree at the current commit.
wt_parent="$(mktemp -d "${TMPDIR:-/tmp}/wtbench.XXXXXX")"
wt="$wt_parent/wt"
if [ "$vcs" = jj ]; then
  jj workspace add "$wt" >/dev/null
else
  git -C "$root" worktree add --detach "$wt" HEAD >/dev/null
fi

t2=$(now)
if ! (cd "$wt" && "$@" ) >"$log_wt" 2>&1; then
  echo "FAIL: worktree build failed; log follows:" >&2
  tail -n 40 "$log_wt" >&2
  # fall through to cleanup, then exit
  if [ "$vcs" = jj ]; then
    jj workspace forget "$(basename "$wt")" >/dev/null 2>&1 || true
  else
    git -C "$root" worktree remove --force "$wt" >/dev/null 2>&1 || true
  fi
  rm -rf "$wt_parent"
  exit 1
fi
t3=$(now)
worktree=$(python3 -c "print(f'{$t3 - $t2:.2f}')")

# Cleanup.
if [ "$vcs" = jj ]; then
  jj workspace remove --force "$wt" >/dev/null 2>&1 \
    || { jj workspace forget "$(basename "$wt")" >/dev/null 2>&1 || true; rm -rf "$wt"; }
else
  git -C "$root" worktree remove --force "$wt" >/dev/null 2>&1 || rm -rf "$wt"
fi
rm -rf "$wt_parent"

ratio=$(python3 -c "print(f'{$worktree / $incremental:.2f}')")
# Sub-second incremental builds make the ratio pure noise (spawn/link overhead);
# allow an absolute 1s floor before declaring the shared cache broken.
verdict="healthy (worktree build ~= incremental)"
python3 -c "import sys; sys.exit(0 if $worktree <= max(1.3 * $incremental, $incremental + 1.0) else 1)" \
  || verdict="BROKEN: worktree build is ${ratio}x incremental (${worktree}s vs ${incremental}s) — shared cache not reused"

cat <<EOF
incremental: ${incremental}s
worktree:    ${worktree}s
ratio:       ${ratio}
verdict:     ${verdict}
EOF
[ "$verdict" = "healthy (worktree build ~= incremental)" ]
