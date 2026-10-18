# A mutation locks, checks, renames and commits

Every mutation runs one sequence:

1. Take `flock` on a lock file in the vault's ignored `.quartermaster/` directory.
2. Read and hash the note.
3. Splice and write a temp file.
4. Re-hash the note on disk, and retry if it changed.
5. Rename the temp file over the note.
6. Commit the note alone as one jj change.

## Why

- `flock` serializes quartermaster processes, and the OS frees the lock when a holder dies,
  so no stale lock is ever left.
- Obsidian takes no lock, so the hash check is the only defence against overwriting a
  human edit.
- The rename keeps readers from ever seeing a half-written note.
- One jj change per mutation keeps the timeline the skill relied on. It is now automatic,
  and touches only the note.
- The lock lives outside the note's path so jj and Obsidian never see it.

## Consequences

- A small race with Obsidian remains between the check and the rename. It is accepted and
  documented, and jj history keeps every version.
