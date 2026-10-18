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

[harness.worker]                # default harness for task_dispatch mode=fresh
kind = "omp"                    # a herdr agent kind
args = ["--model", "glm-5"]

[projects]
root = "~/src/github.com"       # every project's checkout is <root>/<owner>/<repo>

# Optional: an override, only for a project that breaks the convention.
# [projects."arbora-partners/IaC"]
# path = "~/work/IaC"           # its checkout, instead of <root>/<owner>/<repo>
# workspace = "infra"           # its herdr workspace label, instead of finding it (§9.2)
```

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
| Work item | a `#####` heading under a bucket, never under a Part, whose paragraph ends with `^<item-id>`; or a list item carrying `^<item-id>` |
| Anchor | a paragraph in a Part or bucket ending with `^<id>`: a link target, with no state |
| Prompt block | a ```` ```herdr-prompt id=<item-id> to=<label> ```` fence, inside a `> [!prompt]-` callout |
| Manual action | a task list item `- [ ]` / `- [x]`, or an ordered one `1. [ ]` in *Next* |
| Carry-over | the table under `## Carry-over` |
| Process | the list under `## Process` |

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
```

Any state may go to `DROPPED` (with a reason) or `WAITING` (with what it waits on);
`WAITING` to `WAITING` re-states what the item waits on, and no other state moves to itself.
`IN_PROGRESS` may complete without a review (a manual operation, a push to `main`), but its
summary must name the evidence: the commit, run or result. `WAITING` may complete on the
same terms, when what it waited on is done: an issue closed or a PR merged, named in its
summary. An item sent to another repo closes this way, with no unblock first. `DONE` and `DROPPED` are
terminal; reopening is a new item. A transition carries a `summary`
(PR URL, commit, reason), rendered into the item's line or paragraph.

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
| `day_open` | Create today's note from the template, with every open item of the previous note as a carry-over row. Its front matter lists the projects of the rows it carries. Refuses if a non-empty note exists. | `session_init_plan` |
| `day_read` | Return the note as structured data: status, objectives, Next, items by project and bucket, states. Read-only. | — |
| `objective_set` | Add, refine (stamping `(refined HH:MM UTC: <reason>)`) or settle an objective. | `session_init_plan` |
| `item_upsert` | Create or update a work item in a bucket: title, ref, cost, route, optional prompt (`to`, text), and for *Sent to other repos* its sent status (`to send`, `sent`, `acked`, `done`). The renderer writes the status and the parser reads it; nothing else parses the title. Creating a project section adds its `owner/repo` to the front matter's `projects:` list, once. | — |
| `item_transition` | Move a work item through [§4.3]: `{item, to, summary, part?}`. With `part: N` and a move to `DONE` or `DROPPED`, the item leaves its bucket and *Next* in the same commit, and becomes a plain line in Part N with its summary, without its id, button or prompt. Without `part` it stays in its bucket. | `task_update_state` |
| `part_write` | Create or rewrite one Part whole: number, kind, title, icon (🔄, 🔍 or ✅, written, never derived) and body. The body may carry anchors. | — |
| `action_upsert` | Create, reword or move a manual action, a checkbox with an id. Ticking it is `item_transition`; nothing unticks it ([§4.4]). | — |
| `process_set` | Add, reword or settle a *Process* entry. | — |
| `next_set` | Replace *Next* with an ordered list of item ids; the server renders each line, button and details link. | — |
| `status_set` | Rewrite the status line. | — |
| `carryover_verdict` | Record keep / defer / drop on a carry-over row; keep moves it into its project's bucket. | — |
| `refresh` | Reconcile: GitHub state of every ref, herdr state of every dispatched item, ticked checkboxes. Applies only transitions the evidence proves ([§11]). | — |
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
the skill relies on: update work item `^<id>` and refresh *Next*.

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
  3. `herdr tab create` in the project's herdr workspace, cwd at the new workspace.
  4. `herdr agent start issue-<N> --kind <harness.kind> --pane <pane> -- <args>`.
  5. `herdr agent prompt` with the issue and the instruction.
  6. Record the pane id and workspace path on the work item (an HTML comment
     `<!-- qm pane=<id> ws=<path> -->` on the item's line), and transition it to
     `IN_PROGRESS`.

herdr 0.9.5 has no single "create a pane running a command" call, so dispatch composes
`tab create`, `agent start` and `agent prompt`.

## 10. Packaging

The flake exports:

| Output | Content |
|---|---|
| `packages.<system>.worklog` (default) | the static `worklog` binary (`CGO_ENABLED=0`) |
| `packages.<system>.skills` | a Claude plugin directory holding `skills/worklog` and `skills/worktree-build-cache` ([ADR 0010]), for `programs.agentSkills.plugins` or `programs.claude-code.plugins` |
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
| Process dies holding the lock | — | The OS frees an `flock` with the descriptor. |
| Obsidian writes mid-mutation | Lost edit | Hash check and retry ([§7]); jj history for the narrow window left. |
| Workspace for a ref already exists | `add` fails | Reuse it if it is the ref's; otherwise fail with its path. `git worktree prune` before a git add. |
| Unparseable note (hand edit broke a structure) | — | Mutating tools refuse, naming the unit and line; `day_read` returns what it could parse plus the errors. Never rewrite what failed to parse. |
| herdr not running or label ambiguous | Send fails | Fail with the reason; the item stays **to send**. |

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
[§4]: #4-the-note
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
