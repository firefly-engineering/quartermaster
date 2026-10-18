# quartermaster — glossary

The ubiquitous language of the worklog. Definitions only; behaviour lives in
[`docs/spec.md`](docs/spec.md).

## Quartermaster

The suite: several MCP servers, skills and tools under one repo and one plugin bundle. Each
tool in it has its own name ([ADR 0009](docs/adr/0009-quartermaster-is-the-suite.md)).

## Suite member

Anything the suite ships: a **tool**, with its own binary, or a skill-only member, a skill in
the plugin bundle and nothing else, such as `worktree-build-cache`
([ADR 0010](docs/adr/0010-a-suite-member-may-be-skill-only.md)).

## Worklog

The suite's first tool: it keeps the day's **note** and dispatches its work. Also the name
of its skill.

## Note

One markdown file per working day, `YYYY-MM-DD.md` in the vault, holding the day's live
state across every project. A living document: each update rewrites it into the current
state, and the vault's jj history keeps the timeline. The **previous note** is the latest
dated note before today.

## Objective

A goal for the day, naming its project and a checkable **done-condition**.

## Next

The ordered list of the user's next actions, at most five, each pointing at a **work item**.

## Project

A GitHub repo, `owner/repo`, worked on that day. Has one section in the note and one herdr
**session**.

## Part

A dated chunk of a project's day: a human session, an agent batch, or a land.

## Bucket

A fixed-name group under a project's *What remains*: *Decisions for you*, *Your actions*,
*Agent queue*, *Sent to other repos*, *Outside input: chase*, *Waiting*.

## Work item

An addressable entry in a bucket or Part, with an **item id** (`^<id>`) and a **state**.
Agent-tracked ones carry a status icon. A **manual action** carries a task checkbox
instead and belongs to the user.

## Route

The next skill that moves a work item, such as `/triage`, `/implement` or
`/grill-with-docs`.

## Prompt

The self-contained text a work item sends to a session, kept in its `herdr-prompt` block.

## Button

A ▶ link in the note that makes Obsidian run `worklog send` for one work item's
prompt.

## Session

The agent pane in a project's herdr workspace, which receives that project's prompts. The
workspace is the one with a pane in the project's checkout, else the one labelled with the
repo's name ([spec §9.2](docs/spec.md#92-task_dispatch)).

## Dispatch

Starting work on a ref, by prompting the project's session (`session`) or by creating an
isolated workspace and a new agent (`fresh`).

## Harness

An agent program and how it is launched: its herdr agent kind, arguments and model, such as
claude, or omp on a GLM model. A prompt reaches a harness through its **tier**, never by
naming it.

## Tier

The class of work a prompt belongs to, such as design and decisions or well-specified
implementation. Config maps each tier to one **harness**. A route gives a work item its
tier, and the item may override it. _Avoid_: lane.

## Ref

`owner/repo#N`: an issue or PR, unambiguous across projects.

## Carry-over

The open items of the previous note, each given a **verdict**: keep, defer or drop.
