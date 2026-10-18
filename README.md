# quartermaster

quartermaster is a single Go binary that keeps the day's **worklog note** in an Obsidian
vault and dispatches the work it lists to agents running under
[herdr](https://github.com/herdrdev/herdr). It is to serve the note's operations as MCP
tools, so Claude Code, oh-my-pi or any MCP client mutates the note through one state
machine instead of rewriting markdown by hand. Much of that is not built yet: see
[Status](#status).

The design is in [`docs/spec.md`](docs/spec.md), decisions in [`docs/adr/`](docs/adr/),
terms in [`GLOSSARY.md`](GLOSSARY.md).

## Status

Built today:

- `quartermaster version`.
- `quartermaster serve`: a stdio MCP server with the `workflow_rules` and `phase` prompts
  ([spec §8](docs/spec.md#8-mcp-prompts)) and the tools `day_open`, `day_read`,
  `objective_set`, `item_upsert`, `item_transition`, `next_set`, `status_set`,
  `carryover_verdict` and `refresh` ([spec §6](docs/spec.md#6-mcp-tools)).
- `quartermaster note open|read|objective|item|transition|next|status|verdict|refresh`: the
  same tools from the command line.
- `quartermaster send <vault> <date> <item-id>`: the ▶ button handler
  ([spec §9.1](docs/spec.md#91-quartermaster-send--buttons)).
- The configuration ([spec §3](docs/spec.md#3-configuration)): `serve` and `note` read
  `config.toml` and take `--config` and `--vault`.
- The `skills` flake package: the `worklog` skill as a Claude plugin directory.
- The home-manager module.

Not built yet:

- The other MCP tool, and its `note` verb:
  [#11](https://github.com/firefly-engineering/quartermaster/issues/11) (`task_dispatch`).

## Usage

```sh
quartermaster serve [--config <file>] [--vault <repo>]
quartermaster note [--config <file>] [--vault <repo>] <verb> [<json input>]
```

The config file defaults to `$XDG_CONFIG_HOME/quartermaster/config.toml`, else
`~/.config/quartermaster/config.toml`; a missing default file is not an error. `--vault`
overrides `vault.repo`, the repo that holds the vault.

Each `note` verb runs the MCP tool of the same operation. It takes the tool's input as one
JSON object argument, `{}` when left out, and prints the tool's output as JSON:

```sh
quartermaster note open                          # day_open: create today's note
quartermaster note read '{"date": "2026-10-17"}' # day_read: a note as JSON, plus parse errors
quartermaster note transition '{"item": "login-stack", "to": "DONE", "summary": "merged in abc123"}'
```

| Verb | Tool | Does |
|---|---|---|
| `open` | `day_open` | creates today's note |
| `read` | `day_read` | returns a note as structured data |
| `objective` | `objective_set` | adds, refines, progresses or settles an objective |
| `item` | `item_upsert` | creates or rewrites a work item in a bucket |
| `transition` | `item_transition` | moves a work item to a new state, or ticks a manual action |
| `next` | `next_set` | replaces *Next* with up to five actions, by item id |
| `status` | `status_set` | rewrites the status line |
| `verdict` | `carryover_verdict` | records keep, defer or drop on a carry-over row |
| `refresh` | `refresh` | reconciles the note with GitHub and herdr |

The write tools change the note through one lock, check, rename and commit sequence
([ADR 0006](docs/adr/0006-mutations-lock-check-rename-commit.md)), one jj change each, and
never refresh first ([ADR 0008](docs/adr/0008-mutating-tools-do-not-refresh.md)). Each takes
an optional `date`, today (UTC) by default; times are UTC from the clock unless an `at` is
given. `quartermaster serve` lists every tool's input schema.

`day_open` creates today's note (UTC) from the template. Each open item of the previous
note, the latest dated note before today, becomes a carry-over row with no verdict: unmet
objectives, open *What remains* items, unticked manual actions, carry-over rows not kept,
dropped or done, and *Process* to-dos. It refuses when today's note exists and is not empty.

`refresh` reconciles the note on demand ([ADR 0003](docs/adr/0003-no-daemon-reconcile-on-read.md),
[spec §11](docs/spec.md#11-failure-modes)), in one write, and moves an item only on evidence.
It asks `gh` about the issue or PR each open work item's title leads with: a merged PR, or an
issue closed as completed, is `DONE` with the PR or the commit that closed it; an issue closed
as not planned or as a duplicate is `DROPPED`. It asks `herdr` about the pane in each
`IN_PROGRESS` item's dispatch marker: a pane gone, or no agent in it, is `WAITING` ("worker
exited"), never `DONE`. For an item in *Sent to other repos* marked **sent** or **acked**, it
reads the agent the item was sent to (`herdr agent read`); GitHub links in the reply after the
item's prompt mark it **acked** and are added to its line; once GitHub moves it to `DONE`, it
reads **done**. Its write also ticks paired
checkboxes and derives icons, as every write does. It returns what it changed, and every item
it checked and left as it was, with why.

## Install

The flake exports, for `x86_64-linux`, `aarch64-linux` and `aarch64-darwin`
([spec §10](docs/spec.md#10-packaging)):

| Output | Content |
|---|---|
| `packages.<system>.quartermaster` (default) | the binary |
| `packages.<system>.skills` | the skill bundle, a Claude plugin directory |
| `homeManagerModules.default` | `programs.quartermaster` |
| `overlays.default` | `quartermaster` |

Add it to your flake's inputs:

```nix
inputs.quartermaster.url = "github:firefly-engineering/quartermaster";
```

## home-manager

The module installs the binary and writes `settings` to
`~/.config/quartermaster/config.toml`, key for key
([spec §3](docs/spec.md#3-configuration)). It does not install the skill or register the
MCP server ([spec §10.1](docs/spec.md#101-home-manager-module)); the sections below do.

```nix
{ inputs, ... }:
{
  imports = [ inputs.quartermaster.homeManagerModules.default ];

  programs.quartermaster = {
    enable = true;
    settings = {
      vault.repo = "~/src/github.com/sigma/worklog";
      projects."firefly-engineering/quartermaster" = {
        path = "~/src/github.com/firefly-engineering/quartermaster";
        workspace = "quartermaster";
      };
    };
  };
}
```

`package` defaults to this flake's build.

### The skill

The `skills` package holds `.claude-plugin/plugin.json` and `skills/worklog`. Home-manager's
`programs.claude-code.plugins` installs it, passing it to `claude` as `--plugin-dir`:

```nix
{ inputs, pkgs, ... }:
{
  programs.claude-code = {
    enable = true;
    plugins = [ inputs.quartermaster.packages.${pkgs.stdenv.hostPlatform.system}.skills ];
  };
}
```

Or with `programs.agentSkills.plugins`, which links the skill into every agent's skill
directory. `programs.agentSkills` is not a home-manager option: it is the module in
[sigma/systems](https://github.com/sigma/systems/blob/main/home-modules/agent-skills.nix),
which you import yourself.

```nix
{ inputs, pkgs, ... }:
{
  programs.agentSkills.plugins = [
    inputs.quartermaster.packages.${pkgs.stdenv.hostPlatform.system}.skills
  ];
}
```

A harness without skills gets the same text from the `workflow_rules` prompt
([ADR 0005](docs/adr/0005-the-skill-is-the-single-source-of-workflow-rules.md)).

## Register the MCP server

The server is `quartermaster serve`, over stdio.

### Claude Code

With the CLI, for every project:

```sh
claude mcp add --scope user quartermaster -- quartermaster serve
```

For one session, from a file:

```json
{
  "mcpServers": {
    "quartermaster": {
      "command": "quartermaster",
      "args": ["serve"]
    }
  }
}
```

```sh
claude --mcp-config quartermaster.json
```

From home-manager, next to the plugin above:

```nix
{ config, lib, ... }:
{
  programs.claude-code.mcpServers.quartermaster = {
    command = lib.getExe config.programs.quartermaster.package;
    args = [ "serve" ];
  };
}
```

### oh-my-pi

oh-my-pi reads the same `mcpServers` JSON from `~/.omp/agent/mcp.json`, or from
`.omp/mcp.json` in a project. Put the file above there.

## Obsidian ▶ buttons

Each ▶ button in the note runs `quartermaster send` through the community
[Shell commands](https://publish.obsidian.md/shellcommands) plugin
([spec §9.1](docs/spec.md#91-quartermaster-send--buttons)).

The shell command, set up once per vault:

```sh
PATH="/etc/profiles/per-user/$USER/bin:$PATH" quartermaster send {{vault_path}} {{_date}} {{_item}}
```

An Obsidian started from the Dock does not have the nix profile on `PATH`; the prefix lets
it find `quartermaster` and `herdr`.
[`skills/worklog/buttons-setup.md`](skills/worklog/buttons-setup.md) has the steps and why
the URI carries only a reference.

## Development

See [`AGENTS.md`](AGENTS.md).

## License

[MIT](LICENSE).
