# AGENTS.md

quartermaster is a Go binary that keeps the day's Obsidian worklog note and dispatches its
work to agents under herdr, served as MCP tools. Read [`docs/spec.md`](docs/spec.md) first;
decisions are in [`docs/adr/`](docs/adr/), terms in [`GLOSSARY.md`](GLOSSARY.md).

## Layout

| Path | Holds |
|---|---|
| `cmd/quartermaster/` | the binary's entry point |
| `internal/config/` | config loading: defaults, then `config.toml`, then flags (spec §3) |
| `internal/dispatch/` | `task_dispatch`: session mode, and fresh mode in a jj workspace (spec §9.2, ADR 0004) |
| `internal/engine/` | the mutation engine: lock, hash check, rename, jj commit; the only writer of a note (ADR 0006) |
| `internal/note/` | the note parser: every unit of spec §4.1 by source range (ADR 0002) |
| `internal/refresh/` | `refresh`: reconcile the note from GitHub, herdr and ticked checkboxes (ADR 0003) |
| `internal/send/` | `quartermaster send`: deliver a work item's prompt to its herdr session (spec §9.1) |
| `internal/server/` | the MCP server (`quartermaster serve`): the prompts, cut from the embedded skill |
| `internal/tools/` | every MCP tool and `note` verb, one `Def` each (spec §6) |
| `skills/worklog/` | the worklog skill: one source for the installed skill and the `workflow_rules` prompt (ADR 0005) |
| `skills/embed.go` | embeds the skill into the binary |
| `.claude-plugin/plugin.json` | the skill bundle's manifest |
| `nix/` | package, skill bundle, home-manager module, checks |

## Development

- `direnv allow` or `nix develop`: the toolchain comes from toolbox (`go-toolchain`, `just`,
  `nix-toolchain`, `vcs-toolchain`). Add tools there, not ad hoc.
- `just check` runs fmt-check, lint and tests; `just flake-check` runs `nix flake check`.
  CI runs both.
- Every x86_64 Linux CI job uses `runs-on: ${{ vars.CI_RUNNER || 'ubuntu-latest' }}`, never a
  hardcoded label. `CI_RUNNER` is a repo variable, flipped org-wide by `just ci-runner` in
  firefly-engineering/.org. macOS and arm jobs stay pinned.
- `goToolchainVersion` in `flake.nix` picks one toolbox `go-toolchain` version for both the
  shell and the package; the package's Go is read from its pin map, never written here.
- With no Go dependencies `vendorHash` is `null`. The first dependency needs a real hash.

## Conventions

- Parse markdown with goldmark and splice source ranges (ADR 0002). No regex parsers.
- No package reads the environment or cwd. `main` resolves paths and passes them down.
- Every note mutation goes through the lock, check, rename and commit sequence (ADR 0006).

## Agent skills

### Issue tracker

Issues live in GitHub Issues for firefly-engineering/quartermaster, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
