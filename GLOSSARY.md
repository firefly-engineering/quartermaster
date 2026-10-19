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

A GitHub repo, `owner/repo`, worked on that day. Has one section in the note.

## Part

A dated chunk of a project's day: a human session, an agent batch, or a land. An agent
batch Part records the run of a **batch**, or of one ticket's `/implement`.

## Bucket

A fixed-name group under a project's *What remains*: *Decisions for you*, *Your actions*,
*Agent queue*, *Sent to other repos*, *Outside input: chase*, *Waiting*.

## Work item

An addressable entry in a bucket or Part, with an **item id** (`^<id>`) and a **state**.
Agent-tracked ones carry a status icon. A **manual action** carries a task checkbox
instead and belongs to the user.

## Batch

A work item covering several tickets, run by one `/mass-implement` on one **harness**. It
has no ref, and its tier is any one of its tickets' tiers.

## Route

The next skill that moves a work item, such as `/triage`, `/implement` or
`/grill-with-docs`.

## Prompt

The self-contained text a work item sends to a session, kept in its `herdr-prompt` block.

## Button

A ▶ link in the note that makes Obsidian run `worklog send` for one work item's
prompt.

## Session

An available agent of a **harness**'s kind in the project's herdr workspace and checkout,
picked for each prompt rather than fixed. Its workspace may be created on demand, and the
tool never closes it ([ADR 0011](docs/adr/0011-a-work-items-tier-routes-it-to-a-harness.md)).

## Dispatch

Starting work on a ref or a work item, by prompting a session (`session`) or by creating an
isolated workspace and a new agent (`fresh`). Its mode is independent of the **tier**.

## Harness

An agent program and how it is launched, named in config: its herdr agent kind, arguments
and model, such as claude, or omp on a GLM model. One per kind. A prompt reaches a harness
through its **tier**, never by naming it.

## Tier

The class of work a work item belongs to, such as design or well-specified implementation,
from a closed list the worklog skill owns. Config maps each tier to one **harness**. A route
gives the default tier, and the item may override it. _Avoid_: lane.

## Escalation

Raising a work item's **tier** after its worker reports it stuck, so its next dispatch goes
to a stronger harness.

## Ref

`owner/repo#N`: an issue or PR, unambiguous across projects.

## Carry-over

The open items of the previous note, each given a **verdict**: keep, defer or drop.
