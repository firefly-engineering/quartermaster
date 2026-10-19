# quartermaster

quartermaster is a suite of MCP servers, skills and tools
([ADR 0009](docs/adr/0009-quartermaster-is-the-suite.md)). Its first tool, `worklog`, is a
single Go binary that keeps the day's **worklog note** in an Obsidian vault and dispatches
the work it lists to agents running under [herdr](https://github.com/herdrdev/herdr). It
serves the note's operations as MCP tools, so Claude Code, oh-my-pi or any MCP client
mutates the note through one state machine instead of rewriting markdown by hand. [Status](#status) lists what is built.

The design is in [`docs/spec.md`](docs/spec.md), decisions in [`docs/adr/`](docs/adr/),
terms in [`GLOSSARY.md`](GLOSSARY.md).

## Status

Built today:

- `worklog version`.
- `worklog serve`: a stdio MCP server with the `workflow_rules` and `phase` prompts
  ([spec §8](docs/spec.md#8-mcp-prompts)) and the tools `day_open`, `day_read`,
  `objective_set`, `item_upsert`, `item_transition`, `part_write`, `action_upsert`,
  `process_set`, `next_set`, `status_set`, `carryover_verdict`, `refresh`, `prompt_send`
  and `task_dispatch` ([spec §6](docs/spec.md#6-mcp-tools)).
- `worklog note open|read|objective|item|transition|part|action|process|next|status|verdict|refresh|send|dispatch`:
  the same tools from the command line.
- `worklog send <vault> <date> <item-id>`: the ▶ button handler
  ([spec §9.1](docs/spec.md#91-worklog-send--buttons)).
- The configuration ([spec §3](docs/spec.md#3-configuration)): `serve` and `note` read
  `worklog.toml` and take `--config` and `--vault`.
- The `skills` flake package: the `worklog`, `worktree-build-cache` and `mass-implement` skills
  as a Claude plugin directory.
- The home-manager module.

## Usage

```sh
worklog serve [--config <file>] [--vault <repo>]
worklog note [--config <file>] [--vault <repo>] <verb> [<json input>]
```

The config file defaults to `$XDG_CONFIG_HOME/quartermaster/worklog.toml`, else
`~/.config/quartermaster/worklog.toml`; a missing default file is not an error. `--vault`
overrides `vault.repo`, the repo that holds the vault. `[harness.<name>]` names the agents
work runs on and `[tiers]` maps each work item's tier to one; by default `basic-implementation`
and `refactoring` go to omp on GLM, everything else to claude
([spec §3](docs/spec.md#3-configuration)).

Each `note` verb runs the MCP tool of the same operation. It takes the tool's input as one
JSON object argument, `{}` when left out, and prints the tool's output as JSON:

```sh
worklog note open                          # day_open: create today's note
worklog note read '{"date": "2026-10-17"}' # day_read: a note as JSON, plus parse errors
worklog note transition '{"item": "login-stack", "to": "DONE", "summary": "merged in abc123"}'
```

| Verb | Tool | Does |
|---|---|---|
| `open` | `day_open` | creates today's note |
| `read` | `day_read` | returns a note as structured data |
| `objective` | `objective_set` | adds, refines, progresses or settles an objective |
| `item` | `item_upsert` | creates or rewrites a work item in a bucket, with its sent status in *Sent to other repos* |
| `transition` | `item_transition` | moves a work item to a new state, or ticks a manual action; with `part`, files a done item into a Part |
| `part` | `part_write` | creates or rewrites one Part whole |
| `action` | `action_upsert` | creates, rewords or moves a manual action |
| `process` | `process_set` | adds, rewords or settles a *Process* entry |
| `next` | `next_set` | replaces *Next* with up to five actions, by item id |
| `status` | `status_set` | rewrites the status line |
| `verdict` | `carryover_verdict` | records keep, defer or drop on a carry-over row |
| `refresh` | `refresh` | reconciles the note with GitHub and herdr |
| `send` | `prompt_send` | sends a work item's prompt to its herdr session |
| `dispatch` | `task_dispatch` | starts work on a ref, in its project's session or a fresh agent |

The write tools change the note through one lock, check, rename and commit sequence
([ADR 0006](docs/adr/0006-mutations-lock-check-rename-commit.md)), one jj change each, and
never refresh first ([ADR 0008](docs/adr/0008-mutating-tools-do-not-refresh.md)). Each takes
an optional `date`, today (UTC) by default; times are UTC from the clock unless an `at` is
given. `worklog serve` lists every tool's input schema.

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

`prompt_send` takes `{date, item}` and does what `worklog send` does
([spec §9.1](docs/spec.md#91-worklog-send--buttons)): it sends the item's prompt block,
with its closing line, to the one agent in the herdr workspace the block names, and refuses a
`blocked` agent. It leaves the note alone; recording the send is the skill's.

`task_dispatch` takes `{ref, mode, instruction}`, the ref as `owner/repo#N`, and finds the
ref's work item in the note by the link leading its title
([spec §9.2](docs/spec.md#92-task_dispatch)). In place of `ref` it takes `item`, a work item's
id: a batch, which has no ref, runs in its project with its own prompt, and its workspace,
branch and agent are named after the id. Mode `session`, the default, sends the item's
prompt, or `/implement <url>`, to the project's herdr session as `worklog send` does,
and leaves the note alone. Mode `fresh` creates the ref's workspace at
`<dispatch.workspace_root>/<owner>/<repo>/issue-<N>` with `jj workspace add`, or with
`git worktree add -b issue-<N>` when the project has no `.jj`
([ADR 0004](docs/adr/0004-dispatch-isolates-in-a-jj-workspace.md)), and reuses one that exists
for the ref. It then starts the harness `[tiers]` maps the item's tier to (`default` when it
has none) in a new tab of the project's herdr workspace, prompts it, and records
`<!-- qm pane=<id> ws=<path> -->` on the item, which it moves to `IN_PROGRESS`. If herdr fails, the workspace stays and the item does not move; if the note
cannot be written, the error names the agent's pane.

## Install

The flake exports, for `x86_64-linux`, `aarch64-linux` and `aarch64-darwin`
([spec §10](docs/spec.md#10-packaging)):

| Output | Content |
|---|---|
| `packages.<system>.worklog` (default) | the `worklog` binary |
| `packages.<system>.skills` | the skill bundle, a Claude plugin directory |
| `homeManagerModules.default` | `programs.quartermaster.worklog` |
| `overlays.default` | `worklog` |

Add it to your flake's inputs:

```nix
inputs.quartermaster.url = "github:firefly-engineering/quartermaster";
```

## home-manager

The module installs the `worklog` binary and writes `settings` to
`~/.config/quartermaster/worklog.toml`, key for key
([spec §3](docs/spec.md#3-configuration)). It does not install the skill or register the
MCP server ([spec §10.1](docs/spec.md#101-home-manager-module)); the sections below do.

```nix
{ inputs, ... }:
{
  imports = [ inputs.quartermaster.homeManagerModules.default ];

  programs.quartermaster.worklog = {
    enable = true;
    settings = {
      vault.repo = "~/src/github.com/sigma/worklog";
      # The default; every project's checkout is <root>/<owner>/<repo>.
      projects.root = "~/src/github.com";
      # Optional, only for a project that breaks the convention.
      projects."arbora-partners/IaC" = {
        path = "~/work/IaC";
        workspace = "infra";
      };
    };
  };
}
```

Projects need no entry: any `owner/repo` has its checkout at `<projects.root>/<owner>/<repo>`,
and dispatch finds its herdr workspace live, as the one with a pane in that checkout, else
the one labelled with the repo's name ([spec §9.2](docs/spec.md#92-task_dispatch)). An entry
under `projects."owner/repo"` overrides the `path`, the `workspace` label, or both.

`package` defaults to this flake's build.

### The skills

The `skills` package holds `.claude-plugin/plugin.json`, `skills/worklog`, and
`skills/worktree-build-cache` and `skills/mass-implement`, skill-only suite members
([ADR 0010](docs/adr/0010-a-suite-member-may-be-skill-only.md)). Home-manager's
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

Or with `programs.agentSkills.plugins`, which links the skills into every agent's skill
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

A harness without skills gets the worklog skill's text from the `workflow_rules` prompt
([ADR 0005](docs/adr/0005-the-skill-is-the-single-source-of-workflow-rules.md)).

## Register the MCP server

The server is `worklog serve`, over stdio.

### Claude Code

With the CLI, for every project:

```sh
claude mcp add --scope user worklog -- worklog serve
```

For one session, from a file:

```json
{
  "mcpServers": {
    "worklog": {
      "command": "worklog",
      "args": ["serve"]
    }
  }
}
```

```sh
claude --mcp-config worklog.json
```

From home-manager, next to the plugin above:

```nix
{ config, lib, ... }:
{
  programs.claude-code.mcpServers.worklog = {
    command = lib.getExe config.programs.quartermaster.worklog.package;
    args = [ "serve" ];
  };
}
```

### oh-my-pi

oh-my-pi reads the same `mcpServers` JSON from `~/.omp/agent/mcp.json`, or from
`.omp/mcp.json` in a project. Put the file above there.

## Obsidian ▶ buttons

Each ▶ button in the note runs `worklog send` through the community
[Shell commands](https://publish.obsidian.md/shellcommands) plugin
([spec §9.1](docs/spec.md#91-worklog-send--buttons)).

The shell command, set up once per vault:

```sh
PATH="/etc/profiles/per-user/$USER/bin:$PATH" worklog send {{vault_path}} {{_date}} {{_item}}
```

An Obsidian started from the Dock does not have the nix profile on `PATH`; the prefix lets
it find `worklog` and `herdr`.
[`skills/worklog/buttons-setup.md`](skills/worklog/buttons-setup.md) has the steps and why
the URI carries only a reference. If `data.json` changes outside Obsidian, read
[After `data.json` changes outside Obsidian](skills/worklog/buttons-setup.md#after-datajson-changes-outside-obsidian)
before you open the plugin's settings.

## Development

See [`AGENTS.md`](AGENTS.md).

## License

[MIT](LICENSE).
