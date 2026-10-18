# quartermaster is the suite; each tool carries its own name

quartermaster is the umbrella for several MCP servers, skills and tools. The repo, the Go
module and the plugin bundle carry its name. Each tool in it carries its own: the first is
the **worklog**, so its binary is `worklog` (`worklog serve`, `worklog note`, `worklog send`)
and its MCP server reports `worklog`.

Files are kept under the suite's namespace, split by tool:

| What | Where |
|---|---|
| Config | `$XDG_CONFIG_HOME/quartermaster/worklog.toml` |
| State | `~/.local/share/quartermaster/worklog/` |
| Vault locks | `<vault>/.quartermaster/worklog/locks/` |
| home-manager | `programs.quartermaster.worklog` |
| Flake | `packages.worklog`, overlay attribute `worklog` |

The packages under `internal/` and `docs/spec.md` stay where they are: they all belong to
the worklog. They move under a `worklog/` directory when a second tool arrives.

## Why

Naming the binary after the repo would leave no name for the next tool, and would make
`quartermaster serve` mean "the worklog" for good. Keeping files under the suite's
namespace stops the generic word `worklog` from claiming a top-level config or state
directory that other software may want, and puts every tool of the suite in one place.

## Consequences

- A second tool adds a binary, a `<tool>.toml` and a `programs.quartermaster.<tool>`
  option, and touches none of the worklog's.
- The ▶ button's shell command runs `worklog send`.
- Moving `internal/` under `worklog/` is deferred work, owed when the second tool lands.
