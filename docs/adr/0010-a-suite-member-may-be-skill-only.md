# A suite member may be skill-only

[ADR 0009](0009-quartermaster-is-the-suite.md) gives each tool of the suite a binary, a
`<tool>.toml` and a `programs.quartermaster.<tool>` option. Some members need none of them:
`worktree-build-cache` is judgement plus one shell script, `scripts/bench-cache.sh`, a
benchmark that checks a fresh git worktree or jj workspace reuses the machine's shared build
and test caches. It ships as a skill in the plugin bundle, `skills/worktree-build-cache/`, and
nothing else.

A skill-only member:

- adds no binary, no `<tool>.toml` and no home-manager option;
- serves no MCP tool and no MCP prompt, and is not embedded in a binary (`skills/embed.go`
  holds only the worklog skill, for `workflow_rules`);
- is not the "second tool" of ADR 0009: it does not trigger moving `internal/` under
  `worklog/`.

## Why

Every agent that runs the benchmark already has a shell. The benchmark runs for minutes and
its diagnosis reads the build logs, which a tool call would hide behind a blocking request.
The bundle already reaches every agent's skill directory, so no harness needs the text as a
prompt ([ADR 0005](0005-the-skill-is-the-single-source-of-workflow-rules.md)'s reason for
`workflow_rules`).

## Consequences

- Adding a skill-only member touches `skills/` and the `skills` list in
  `.claude-plugin/plugin.json`, which `nix/skills.nix` copies from and the bundle check in
  `nix/checks.nix` holds every directory under `skills/` to.
- A member grows an MCP surface only when the server must act on it itself. For
  `worktree-build-cache` that would be `task_dispatch` checking cache health before it
  spawns a fresh workspace ([ADR 0004](0004-dispatch-isolates-in-a-jj-workspace.md)); then
  the check becomes Go and this decision is revisited.
