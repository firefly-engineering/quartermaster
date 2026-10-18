# quartermaster

quartermaster is a single Go binary that keeps the day's **worklog note** in an Obsidian
vault and dispatches the work it lists to agents running under
[herdr](https://github.com/herdrdev/herdr). It is to serve the note's operations as MCP
tools, so Claude Code, oh-my-pi or any MCP client mutates the note through one state
machine instead of rewriting markdown by hand. Most of that is not built yet: see
[Status](#status).

The design is in [`docs/spec.md`](docs/spec.md), decisions in [`docs/adr/`](docs/adr/),
terms in [`GLOSSARY.md`](GLOSSARY.md).

## Status

Built today:

- `quartermaster version`.
- `quartermaster serve`: a stdio MCP server with the `workflow_rules` and `phase` prompts
  ([spec §8](docs/spec.md#8-mcp-prompts)). It serves no tools yet.
- `quartermaster send <vault> <date> <item-id>`: the ▶ button handler
  ([spec §9.1](docs/spec.md#91-quartermaster-send--buttons)).
- The `skills` flake package: the `worklog` skill as a Claude plugin directory.
- The home-manager module.

Not built yet:

- The MCP tools ([spec §6](docs/spec.md#6-mcp-tools)):
  [#8](https://github.com/firefly-engineering/quartermaster/issues/8),
  [#9](https://github.com/firefly-engineering/quartermaster/issues/9),
  [#10](https://github.com/firefly-engineering/quartermaster/issues/10),
  [#11](https://github.com/firefly-engineering/quartermaster/issues/11).
- `quartermaster note`, the same operations as a CLI: with the tools, in
  [#8](https://github.com/firefly-engineering/quartermaster/issues/8) and
  [#9](https://github.com/firefly-engineering/quartermaster/issues/9).

The config loader exists, but no command calls it yet: the first tool that needs the vault
will.

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
