# Mutating tools do not refresh; refresh runs on demand

A mutating tool could run `refresh` before its own change, so the note is reconciled at
every write. It does not. A mutating tool applies its one change and nothing else. `refresh`
runs only when called: the skill calls it at the end of each phase ([spec §5]), and the user
or an agent may call it at any time.

## Why

`refresh` asks GitHub and herdr about every ref and dispatched item. Running it on every
write would make each call slow and make writes fail offline. It would also fold unrelated
transitions into the write's jj change, which breaks one change per transition
([ADR 0006]). The note only needs to be true when someone reads it, and the phase
boundaries are where it is read ([ADR 0003]).

## Consequences

- Between refreshes the note can lag reality: a merged PR stays 🔍 until the next `refresh`.
- Writes need no network and stay fast.
- Each jj change holds one transition, so the history reads as the day's events.

[spec §5]: ../spec.md#5-phases
[ADR 0003]: 0003-no-daemon-reconcile-on-read.md
[ADR 0006]: 0006-mutations-lock-check-rename-commit.md
