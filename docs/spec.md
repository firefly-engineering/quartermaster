# quartermaster — specification

quartermaster is a suite of MCP servers, skills and tools
([ADR 0009](adr/0009-quartermaster-is-the-suite.md)). This specifies its first tool,
`worklog`: a single Go binary that keeps the day's **worklog note** in an Obsidian vault and
dispatches the work it lists to agents running under herdr. It serves the note's operations
as MCP tools, so every harness (Claude Code, oh-my-pi, any MCP client) mutates the note
through the same state machine instead of rewriting markdown by hand.

It grows out of the hand-written `worklog` skill and its `worklog-send` script. Terms in
**bold** are defined in [`GLOSSARY.md`](../GLOSSARY.md); decisions are recorded in
[`docs/adr/`](adr/).

## 1. Goals and non-goals

Goals:

1. **One state machine for the note.** Agents submit transitions (`start`, `review`,
   `complete`, `block`), never free-form edits. The server refuses an illegal one.
2. **Lower the barrier to action.** The note stays what the worklog skill made it: led by
   objectives and *Next*, with a ▶ button on every action a session can start.
3. **Human edits stay first-class.** The user edits the note in Obsidian at any time. A
   manual action is a plain task checkbox the user ticks; the server reads it and never
   undoes it ([ADR 0001]).
4. **Safe under concurrency.** Several agents and Obsidian write one note at once without
   losing an edit ([§7]).
5. **Self-describing.** The workflow rules ship once, as the `worklog` skill, and are also
   served as an MCP prompt ([ADR 0005]).
6. **Nix-native.** A flake exports the package, the skill bundle and a home-manager module
   ([§10]).

Non-goals for v1: a long-lived daemon or heartbeat ([ADR 0003]); editing notes other than
the daily worklog; a GUI; any issue tracker other than GitHub.

## 2. Architecture

```
 Obsidian ──edits──►  vault/worklog/YYYY-MM-DD.md  ◄──flock + atomic rename + jj commit──┐
    │ ▶ button (obsidian://shell-commands)                                              │
    ▼                                                                                   │
 worklog send <vault> <date> <id> ──herdr agent prompt──► project session               │
                                                                                        │
 Claude Code ─┐                                                                         │
 oh-my-pi    ─┼─ stdio MCP ─► worklog serve ── note engine ─────────────────────────────┘
 other        ─┘                 │
                                 ├── gh (issue / PR / milestone state)
                                 ├── jj / git (workspaces for dispatch)
                                 └── herdr (tabs, agent start, prompt, status)
```

One binary, several subcommands:

| Command | Role |
|---|---|
| `worklog serve` | stdio MCP server: tools ([§6]) and prompts ([§8]) |
| `worklog send <vault> <date> <item-id>` | ▶ button handler; replaces the skill's `worklog-send` script ([§9]) |
| `worklog note <subcommand>` | the same operations as the tools, for scripts and tests |
| `worklog version` | prints the version |

Each process is **stateless**: every call reads the vault, does its work under the note's
lock, and exits or waits for the next call. All durable state is the note, GitHub and
herdr ([ADR 0003]).

## 3. Configuration

TOML, loaded in this order, later wins: built-in defaults, then the user file, then
`--config` / `--vault` flags. The user file's path defaults to
`$XDG_CONFIG_HOME/quartermaster/worklog.toml`, resolved once in `main` and passed down: no
package reads the environment or cwd.

```toml
[vault]
repo = "~/src/github.com/sigma/worklog"   # jj repo holding the vault
dir = "worklog"                           # Obsidian vault, relative to repo
name = "worklog"                          # Obsidian vault name, for obsidian:// URIs

[dispatch]
workspace_root = "~/.local/share/quartermaster/worklog/workspaces"  # <root>/<owner>/<repo>/issue-<n>

[harness.claude]                # a named harness: a herdr agent kind and its arguments
kind = "claude"

[harness.glm]
kind = "omp"
args = ["--model", "glm-5"]

[tiers]                         # a work item's tier to a harness name
default = "claude"              # every tier not listed, and every item with no tier
basic-implementation = "glm"
refactoring = "glm"

[projects]
root = "~/src/github.com"       # every project's checkout is <root>/<owner>/<repo>

# Optional: an override, only for a project that breaks the convention.
# [projects."arbora-partners/IaC"]
# path = "~/work/IaC"           # its checkout, instead of <root>/<owner>/<repo>
# workspace = "infra"           # its herdr workspace label, instead of finding it (§9.2)
```

A **harness** is `[harness.<name>]`: a herdr agent `kind` and its `args`. `[tiers]` maps a
work item's tier to a harness name; a tier it does not list, and an item with no tier, go
to `default` ([ADR 0011]). The defaults are the harnesses and tiers above. A user file's
`[harness.*]` replaces the whole built-in harness set, and its `[tiers]` the built-in tiers;
fields never merge inside a table. After merging, each of these is a load error:

- `[tiers]` has no `default`;
- a `[tiers]` key is neither `default` nor a tier of the list cut from the worklog skill's
  `## Tiers` table ([ADR 0005]); the error lists the valid ones;
- a `[tiers]` value names a harness that is not defined;
- a harness has no `kind`, or two harnesses share one (the error names both);
- a harness name does not match `[a-z][a-z0-9_-]*`;
- `[harness.worker]`: "`[harness.worker]` is gone: name your harnesses and map tiers to
  them in `[tiers]`". `worker` is a reserved name.

A harness no tier names is allowed.

A **project** is keyed by `owner/repo`. Every ref the server handles is `owner/repo#N`
([ADR 0007]); a bare `#N` is accepted only where a project is already in scope.

Any `owner/repo` is a project, with no entry: its checkout is `<root>/<owner>/<repo>`, and
its herdr workspace is found in herdr's live state when it is needed (§9.2). In the
`projects` table, `root` is a string and every other key an `owner/repo` table overriding
that project's `path`, `workspace`, or both; `root` is never an `owner/repo`, so the two do
not collide. `root`, like every path here, expands a leading `~`.

## 4. The note

The note format is the worklog template ([`skills/worklog/template.md`](../skills/worklog/template.md)),
canonical per [ADR 0001]. The server parses it with a real markdown parser (goldmark) and
locates each unit by its source byte range; a mutation splices only the bytes of the units
it changes, so everything else, including user prose, round-trips byte for byte
([ADR 0002]).

### 4.1 Addressable units

| Unit | Located by |
|---|---|
| Front matter | the leading `---` YAML block, LF or CRLF line ends |
| Status line | the first paragraph after `# Worklog — <date>` starting `**HH:MM UTC:**`, or `**HH:MM UTC, <label>:**` where the label names the phase |
| Objectives | the table under `## Objectives`, one row each |
| Next | the ordered list under `## Next` |
| Project section | `## <icon> <owner/repo>` |
| Part | `### <icon> Part N — <kind>: <title>`, where the kind is `Human session`, `Agent batch` or `Land`, and nothing else |
| Bucket | `#### <icon> <bucket name>` under `### <icon> What remains` |
| Work item | a `#####` heading under a bucket, never under a Part, whose paragraph ends with `^<item-id>`; or a list item carrying `^<item-id>`. Its ref, cost, route and tier are read as the renderer writes them, below |
| Anchor | a paragraph in a Part or bucket ending with `^<id>`: a link target, with no state |
| Prompt block | a ```` ```herdr-prompt id=<item-id> to=<label> ```` fence, inside a `> [!prompt]-` callout |
| Manual action | a task list item `- [ ]` / `- [x]`, or an ordered one `1. [ ]` in *Next* |
| Carry-over | the table under `## Carry-over` |
| Process | the list under `## Process` |

A work item's ref is the link opening its title, before the title text: `[#N](<url>): <title>`,
read as `owner/repo#N` from its GitHub issue or PR URL. A list item writes its cost, route and
tier on its line, `` <title> · <cost> · Route: `<route>` · Tier: `<tier>` ``, then its sent
status and summary; they are read from the end of its title before those: the tier is the code
span ending it, after ` · Tier: `, the route the code span ending what is left, after
` · Route: `, and the cost what lies between the first ` · ` and those. So a list item's
title holds no ` · `, and the renderer refuses one that does. A `#####` item ends its body,
before its button, with the sentences `` Cost: <cost>. Route: `<route>`. Tier: `<tier>`. ``,
read from the end the same way. A body ending with a bare `<cost>.`, as written before the
`Cost:` marker, reads as prose: the item has no cost until it is rewritten. Each is absent
when the item has none.

The tier is the class of the item's work, one of the closed list in the worklog skill's tier
table ([ADR 0011]). It routes the item's prompts to a harness through config; the note never
names the harness.

Item and anchor ids are lowercase letters, digits and `-`, unique within a note. Every
`[[#^<id>|details]]` link in *Next* names a work item or an anchor. A section the note lacks
parses as absent, and a checkbox in any section is a manual action.

### 4.2 States

A work item's state is its leading icon. `🔍` is new relative to the skill: it carries the
draft's *in review*.

| State | Icon | Means |
|---|---|---|
| `TODO` | ⬜ | not started |
| `IN_PROGRESS` | 🔄 | a session or batch is on it, or part of it is done |
| `IN_REVIEW` | 🔍 | a PR is up and waits on review |
| `WAITING` | ⏳ | blocked or waiting: on another repo, a person, a date, an item |
| `DONE` | ✅ | done |
| `DROPPED` | ❌ | missed or dropped, with the reason |

A project, *What remains* or bucket heading's icon is derived, never written by hand. It
is ✅ when every item under it is `DONE` or `DROPPED`. Otherwise it is ⏳ if anything waits,
else 🔄 if anything is in progress or already finished, else 🔍 if anything is in review,
else ⬜. A heading with nothing under it shows `—`, which is never a work item's state. The
server recomputes every derived icon on each write.

A Part's icon is written with the Part, never derived: 🔄 while it runs, 🔍 while its work is
in review, ✅ when it is done. A finished Part holds plain lines, not work items, so there is
nothing to derive it from. A project's icon counts its Parts' icons along with its items.

### 4.3 Transitions

```
TODO ──start──► IN_PROGRESS ──review──► IN_REVIEW ──complete──► DONE
  │              │  │  ▲                  │                     ▲
  │              │  │  └──────rework──────┘                     │
  │              │  └──complete, no review (evidence required)──┘
  │              └──block──► WAITING ──unblock──► IN_PROGRESS
  │                          └──complete (evidence required)──► DONE
  └──────────complete / drop (direct close)──────────────────► DONE / DROPPED

every state, DONE and DROPPED included ──restate (summary required unless filed)──► itself
```

Any state may go to `DROPPED` (with a reason) or `WAITING` (with what it waits on). A work
item in any state may move to itself with a `summary`, which rewrites only the summary, in one commit:
the evidence changed but the state did not (a PR went up, CI turned green, a `DONE` item's
evidence was wrong). `WAITING` to `WAITING` re-states what the item waits on. A move to
itself without a `summary` is refused, unless it files a `DONE` or `DROPPED` item into a
Part ([§6]). A manual action has no summary, so a ticked one cannot move to itself ([§4.4]).
`IN_PROGRESS` may complete without a review (a manual operation, a push to `main`), but its
summary must name the evidence: the commit, run or result. `WAITING` may complete on the
same terms, when what it waited on is done: an issue closed or a PR merged, named in its
summary. An item sent to another repo closes this way, with no unblock first. `DONE` and `DROPPED` are
terminal but for that restatement; reopening is a new item. A transition carries a `summary`
(PR URL, commit, reason), rendered into the item's line or paragraph. `refresh` closing a
terminal item's `fresh` dispatch tab and dropping its `pane=` ([§9.2]) is not a transition: its
state stays.

### 4.4 Manual actions

A manual action is something only the user can do, kept as a task checkbox so the user can
tick it in Obsidian. On read, a ticked box (`[x]`) is `DONE`, an unticked one is `TODO`.
The server may create, reword or move a manual action, and may tick one when the user asks
it to, but never unticks one ([ADR 0001]). A *Next* line that points at a manual action is
itself a checkbox (`1. [ ] …`), and ticking either one ticks both at the next write.

A write knows a box by its `^<id>`, a *Next* line by the item its details link points at,
and any other box by its text. A write may remove a ticked box with its line, as `next_set`
drops a done *Next* line, but may not leave an open box in its place. A box known by its text
cannot be told from a new one once reworded, so a write that removes a ticked box known by its
text may add no open box known by its text: making both changes takes two writes.

## 5. Phases

The day's cycle (open, unblock, AFK, land, capture, close) is judgement, and stays in the
skill ([`skills/worklog/SKILL.md`](../skills/worklog/SKILL.md)). Each phase ends with a
`refresh` and the tool calls that write its result. The tools below are the only way the
skill writes the note.

## 6. MCP tools

Every mutating tool takes the note's lock ([§7]), re-reads the note, applies one
transition, writes, and commits one jj change. It does not run `refresh` first
([ADR 0008]). Every tool returns the affected units as structured JSON and the note's path.

| Tool | Does | Draft equivalent |
|---|---|---|
| `day_open` | Create today's note from the template, with every open item of the previous note as a carry-over row. Its front matter lists the projects of the rows it carries. A note prepared ahead, one that exists and is not empty, is kept: it gains a carry-over row for each open item of the previous note it does not carry yet, its status line is rewritten as `open`, and its front matter's projects list gains the carried projects; nothing else is written. It already carries an item with an `^<id>` when it holds that id anywhere (a work item, a manual action, or a carry-over row linking `^<id>`), and one without an id when a carry-over row's item is that item's row as rendered. Only the previous note is collected. A prepared note that does not parse is refused, naming its errors. | `session_init_plan` |
| `day_read` | Return the note as structured data: status, objectives, Next, items by project and bucket, states. Read-only. | — |
| `objective_set` | Add, refine (stamping `(refined HH:MM UTC: <reason>)`) or settle an objective. | `session_init_plan` |
| `item_upsert` | Create or update a work item in a bucket: title, ref, cost, route, tier, optional prompt (`to`, text), and for *Sent to other repos* its sent status (`to send`, `sent`, `acked`, `done`). The renderer writes the status, ref, cost, route and tier, and the parser reads them back ([§4.1]); nothing else parses the title. A tier outside the list the binary cuts from the embedded skill's tier table ([ADR 0005], [ADR 0011]) is refused, naming the valid ones. An update keeps a ref, cost, route or tier left out or `""`, as it keeps the repo, bucket and sent status; an explicit `null` clears one, dropping the ref's link from the title or that part of the item. On create, and in `carryover_verdict`'s keep, a `null` is absent. Creating a project section adds its `owner/repo` to the front matter's `projects:` list, once. An update rewrites the item whole, so it refuses to drop a prompt left out; an explicit `"prompt": null` drops the prompt and its ▶ button, in its bucket and on its *Next* line, and the item stays a `#####` item only when the update gives a body. When the item as written is `TODO`, its route starts with `/` and it has no prompt, it has no ▶ button to send that command: the result's `warnings` names the item and its route. The write goes through all the same. | — |
| `item_transition` | Move a work item through [§4.3]: `{item, to, summary, part?}`. With `part: N` and a move to `DONE` or `DROPPED`, the item leaves its bucket and *Next* in the same commit, and becomes a plain line in Part N with its summary, without its id, button or prompt. Without `part` it stays in its bucket. A move to the item's own state, in any state, rewrites only its summary and needs one. With `part: N`, a move to its own `DONE` or `DROPPED` state files the item into Part N as above, and keeps its last summary when none is given. | `task_update_state` |
| `part_write` | Create or rewrite one Part whole: number, kind, title, icon (🔄, 🔍 or ✅, written, never derived) and body. The body may carry anchors. | — |
| `action_upsert` | Create, reword or move a manual action, a checkbox with an id. Ticking it is `item_transition`; nothing unticks it ([§4.4]). | — |
| `process_set` | Add, reword or settle a *Process* entry. | — |
| `next_set` | Replace *Next* with an ordered list of item ids; the server renders each line, button and details link. A work item's line takes the entry's `cost`, else the item's cost, else none; nothing is read back from the line *Next* held. A manual action's or an anchor's line takes its cost from the entry only. The result's `warnings` holds one line for each listed work item that is `TODO`, with a route starting with `/` and no prompt, so no ▶ button; manual actions and anchors never warn. The write goes through all the same. | — |
| `status_set` | Rewrite the status line. | — |
| `carryover_verdict` | Record keep / defer / drop on a carry-over row; keep moves it into its project's bucket. A keep with no title, on a row linking a work item as `[[<prev>#^<id>\|^<id>]]`, takes that item's title from the note the link names, without its ref link, cost, route, tier, sent status and summary, and its ref, cost, route and tier unless given; any other row's text is the title. The row stays as written. | — |
| `refresh` | Reconcile: GitHub state of every ref, herdr state of every dispatched item, ticked checkboxes. Applies only transitions the evidence proves ([§11]). On a `DONE` or `DROPPED` item with a `fresh` dispatch marker, it also closes the dispatch's herdr tab once its worker is finished, and drops `pane=` from the marker ([§9.2]). It reports what it closed or dropped as applied, and a close it skipped or that failed as skipped, with why; a failed close fails no other step. | — |
| `prompt_send` | Send a work item's prompt block to its session; the tool form of `worklog send`. | — |
| `task_dispatch` | Start work on a ref ([§9.2]). | `task_dispatch` |

Times come from the clock (UTC). A tool that records an event the user timed takes an
explicit `at`; nothing estimates a time.

## 7. Concurrency and persistence

A mutation runs this sequence ([ADR 0006]):

1. Open `<vault>/.quartermaster/worklog/locks/<date>.lock` and take `flock(LOCK_EX)`, with a
   timeout (default 10 s). The lock is an OS-held descriptor, so a dead process frees it. The
   suite's `.quartermaster/` directory is listed in the vault's `.gitignore`; Obsidian hides
   dot directories.
2. Read the note and hash it.
3. Parse, apply the transition, render.
4. Write `<note>.tmp.<random>` in the same directory, `fsync` it.
5. Re-hash the note on disk. If it changed since step 2 (Obsidian does not take the lock),
   discard the temp file and go back to step 2, at most 3 times, then fail.
6. `rename` the temp file over the note.
7. `jj -R <repo> commit -m "worklog(<date>): <tool>: <headline>" 'root:"<dir>/<date>.md"'`.
   If the repo is not jj, skip and say so in the result.
8. Release the lock.

Step 5 narrows but cannot close the race with Obsidian, which writes without a lock; the
window is the time between 5 and 6. Section-scoped splicing ([§4]) keeps a lost race
cheap to merge by hand, and the jj history keeps every version.

## 8. MCP prompts

| Prompt | Content |
|---|---|
| `workflow_rules` | The worklog skill: `SKILL.md`, then `template.md`. |
| `phase` (arg `name`) | The one phase's section of the skill, for a harness that wants less context. |

The text is embedded in the binary from `skills/worklog/` at build time, so the prompt and
the installed skill cannot drift ([ADR 0005]).

## 9. Sending and dispatch

### 9.1 `worklog send` (▶ buttons)

The Obsidian Shell commands plugin runs `worklog send <vault> <date> <item-id>` when
a button is clicked. Any web page can open an `obsidian://` URI, so the command takes only
a date and an id and validates both: the date parses as ISO, the id matches §4.1, the note
exists under the vault. It reads the prompt from the note's `herdr-prompt` block (through
callout `>` markers), resolves `to` to exactly one herdr workspace with exactly one agent,
refuses an agent whose status is not `idle`, `done` or `working` (a `blocked` agent shows
an approval prompt that typed text could answer), and runs
`herdr agent prompt <pane> <text>` with no shell in between. It appends the closing line
the skill relies on (`send.Closing`): update work item `^<id>`, or `owner/repo#N` for a
prompt no block holds, in the note at its path, and refresh *Next*. `refresh` reads an
item's reply from its own closing line, up to the next closing line in the pane ([§11]).

Behaviour is a strict port of `worklog-send`; its self-check becomes the Go test suite.

### 9.2 `task_dispatch`

Input: `{ref: "owner/repo#N", mode: "session" | "fresh", instruction?}`.

The **project's herdr workspace** is resolved at use, from herdr's live state, the first of:

1. the workspace labelled `projects."<owner>/<repo>".workspace`, when set;
2. the workspace with a pane whose cwd (`herdr pane list`) is the project's checkout (§3) or
   a directory under it, compared by whole path components once cleaned, so `/a/bc` is not
   under `/a/b`. Panes of two workspaces there is an error naming both and asking for an
   override;
3. the workspace labelled with the repo's name (`herdr workspace list`).

No match is an error asking for an override. A `herdr-prompt` block's `to=` still names a
workspace by its label (§9.1).

- **`session`** (default): build the prompt from the ref's work item (or `/implement <url>`
  when none exists) and send it to the session, as in §9.1: the block's `to=` for an item
  with a prompt, else the project's herdr workspace.
- **`fresh`**: an isolated workspace and a new agent:
  1. `gh issue view` for title and body.
  2. Resolve the project's herdr workspace, which only reads, then create the workspace at
     `<workspace_root>/<owner>/<repo>/issue-<N>` from the project's checkout:
     `jj workspace add` when the checkout has `.jj`, else `git worktree add -b issue-<N>`
     ([ADR 0004]). An existing workspace for the ref is reused, never clobbered.
  3. `herdr tab create` in the project's herdr workspace, cwd at the new workspace, labelled
     with the agent's name (step 4).
  4. `herdr agent start <name> --kind <kind> --pane <pane> -- <args>`, the `kind` and `args`
     of the harness `[tiers]` maps the item's tier to, or of `default` when the item has no
     tier (§3). herdr agent
     names are unique among all live agents and match `[a-z][a-z0-9_-]{0,31}`, so the name is
     the first of `<repo>-<N>`, `<repo>-<N>-2`, `<repo>-<N>-3`, … that no live agent has in
     `herdr agent list` (its `name` field), read with step 2's read-only checks. `<repo>` is
     the repo part of `owner/repo`; the owner stays out. The name is lowercased, every
     character outside `[a-z0-9_-]` becomes `-`, and it is prefixed `a` unless it starts with a
     letter (`1password` → `a1password-5`). Only the repo part is cut to fit 32 characters;
     `-<N>` and `-<k>` never are. An agent taking the name before `agent start` makes herdr
     refuse it, a herdr failure like any other. The workspace's directory, jj workspace name
     and git branch stay `issue-<N>`.
  5. `herdr agent prompt` with the issue and the instruction.
  6. Record the pane id and workspace path on the work item (an HTML comment
     `<!-- qm pane=<id> ws=<path> -->` on the item's line), and transition it to
     `IN_PROGRESS`.
  7. Once the item is `DONE` or `DROPPED`, each `refresh` closes the agent's tab
     (`herdr tab close`, the tab id read from `herdr pane get`) and drops `pane=` from the
     marker, so tabs do not pile up. `ws=` stays. It closes the tab when both hold:
     - the agent is `idle` or `done`, or no agent runs in the pane any more;
     - the agent still bears its dispatch name, `<repo>-<N>` with its counter, as step 4
       gives it for the marker's `ws=` path.

     A `working` or `blocked` agent, or one in any other status, is left for the next
     `refresh`. Renaming the agent in herdr keeps its tab, and `pane=` stays: that is how the
     developer keeps one. A pane already gone just has `pane=` dropped. Only a marker with
     `ws=` records a `fresh` dispatch, so a session's pane is never closed.

herdr 0.9.5 has no single "create a pane running a command" call, so dispatch composes
`tab create`, `agent start` and `agent prompt`.

## 10. Packaging

The flake exports:

| Output | Content |
|---|---|
| `packages.<system>.worklog` (default) | the static `worklog` binary (`CGO_ENABLED=0`) |
| `packages.<system>.skills` | a Claude plugin directory holding `skills/worklog`, `skills/worktree-build-cache` and `skills/mass-implement` ([ADR 0010]), for `programs.agentSkills.plugins` or `programs.claude-code.plugins` |
| `homeManagerModules.default` | `programs.quartermaster.worklog` ([§10.1]) |
| `overlays.default` | `worklog` |
| `checks` | the package build with its tests, the skill bundle against its manifest, and the module evaluated on home-manager |

Systems: `x86_64-linux`, `aarch64-linux`, `aarch64-darwin`, as in toolbox. The dev shell
and the package's Go come from toolbox.

### 10.1 home-manager module

```nix
programs.quartermaster.worklog = {
  enable = true;
  package = …;                 # default: this flake's
  settings = { vault.repo = "…"; projects.root = "…"; };   # → quartermaster/worklog.toml
};
```

The module installs the package and writes `~/.config/quartermaster/worklog.toml`. It does
not install the skill or register MCP servers, because each user's setup does those in its
own way; the README shows the snippets for `programs.agentSkills`,
`programs.claude-code` and oh-my-pi.

## 11. Failure modes

| Failure | Effect | Handling |
|---|---|---|
| Worker crashes | Item stays 🔄 | `refresh` asks herdr for the recorded pane. Gone, or the agent exited, with no transition recorded: move to `WAITING` with "worker exited". Never to `DONE`. |
| Closing a finished `fresh` dispatch's tab fails | Tab stays, `pane=` stays | `refresh` reports the failure as a skipped item and carries on; the next `refresh` retries. A tab already gone counts as closed ([§9.2]). |
| Process dies holding the lock | — | The OS frees an `flock` with the descriptor. |
| Obsidian writes mid-mutation | Lost edit | Hash check and retry ([§7]); jj history for the narrow window left. |
| Workspace for a ref already exists | `add` fails | Reuse it if it is the ref's; otherwise fail with its path. `git worktree prune` before a git add. |
| Unparseable note (hand edit broke a structure) | — | Mutating tools refuse, naming the unit and line; `day_read` returns what it could parse plus the errors. Never rewrite what failed to parse. |
| herdr not running or label ambiguous | Send fails | Fail with the reason; the item stays **to send**. |
| Another prompt sent to the same pane | Its reply's links credited to the earlier item | A reply runs from the line after its item's full closing-line prefix, up to the next closing line for any work item or ref; a mention of `work item ^<id>` in reply prose matches neither. A later prompt's own body, before its closing line, still counts as the earlier reply: a prompt cannot open with a marker, since a slash command must come first ([#90](https://github.com/firefly-engineering/quartermaster/issues/90)). |

## 12. Open questions

- ~~Should `refresh` run automatically at the start of each mutating tool, or only on demand?~~
  Settled: on demand only ([ADR 0008]).
  [#14](https://github.com/firefly-engineering/quartermaster/issues/14)
- ~~Is a `## Log` section worth its noise, given that jj history already keeps the timeline?~~
  Settled: no. The note has no *Log* and there is no `log_event`; the jj history is the
  timeline, and Parts and item summaries carry what the note needs.
  [#15](https://github.com/firefly-engineering/quartermaster/issues/15)

[ADR 0001]: adr/0001-the-worklog-note-is-the-canonical-format.md
[ADR 0002]: adr/0002-structural-patching-by-source-range.md
[ADR 0003]: adr/0003-no-daemon-reconcile-on-read.md
[ADR 0004]: adr/0004-dispatch-isolates-in-a-jj-workspace.md
[ADR 0005]: adr/0005-the-skill-is-the-single-source-of-workflow-rules.md
[ADR 0006]: adr/0006-mutations-lock-check-rename-commit.md
[ADR 0007]: adr/0007-refs-are-owner-repo-number.md
[ADR 0008]: adr/0008-mutating-tools-do-not-refresh.md
[ADR 0010]: adr/0010-a-suite-member-may-be-skill-only.md
[ADR 0011]: adr/0011-a-work-items-tier-routes-it-to-a-harness.md
[§4]: #4-the-note
[§4.1]: #41-addressable-units
[§4.3]: #43-transitions
[§4.4]: #44-manual-actions
[§6]: #6-mcp-tools
[§7]: #7-concurrency-and-persistence
[§8]: #8-mcp-prompts
[§9]: #9-sending-and-dispatch
[§9.2]: #92-task_dispatch
[§10]: #10-packaging
[§10.1]: #101-home-manager-module
[§11]: #11-failure-modes
