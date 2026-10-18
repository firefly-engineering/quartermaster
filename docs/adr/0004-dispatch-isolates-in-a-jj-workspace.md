# Fresh dispatch isolates in a jj workspace, with a git worktree fallback

`task_dispatch mode=fresh` creates the agent's checkout with `jj workspace add` when the
project has `.jj`. Otherwise it uses `git worktree add`. Workspaces live under a configured
root, outside the project tree.

## Why

The projects are jj repos, and the jj front-end is the user's choice whatever a repo's docs
say. In a jj repo, a git worktree resolves git to the main repo and desyncs jj's snapshot.
Putting the workspace inside the project tree would let the parent workspace snapshot it.

## Consequences

- Mode `session` stays the default. It reuses the project's existing herdr session, which
  isolates itself the way `/implement` does today.
