---
name: worklog
description: >-
  Keep the day's worklog note in the Obsidian vault (~/src/github.com/sigma/worklog)
  through the worklog MCP server's tools: one living note per working day, across every
  project, led by progress on the day's objectives and the next actions as ▶ buttons. Use
  to open the day (objectives, triage of yesterday's leftovers), to record a human unblock
  session or its decisions, to queue or record an AFK agent batch (mass-implement results,
  PR stacks, merges), to capture a new bug, HITL need or work item sent to another repo
  (IaC agent over herdr), to refine the day's objectives, and to close the day. Also use
  when the user asks "where are we", "what's next" or "what's left" for today.
---

# Worklog

One **note** per working day holds the day's live state across every project. It is a
**living document**: each update rewrites it into the current state, and the vault's jj
history keeps the timeline. Its job is to **lower the barrier to action**. Someone glancing
at it sees how far the objectives have got and what to do next, and starts the next
action with one click.

This skill holds the judgement: the phases, the routes, the triage, and when each phase is
done. The worklog's MCP tools do every write. They need `worklog serve` registered as an
MCP server ([README](https://github.com/firefly-engineering/quartermaster#register-the-mcp-server)).
Without it, stop and say so: never edit the note by hand instead.

## The tools

| Tool | Use it to |
|---|---|
| `day_open` | create today's note, with the previous note's open items as carry-over rows |
| `day_read` | read a note: status, objectives, *Next*, items by project and bucket, states, parse errors |
| `objective_set` | add, refine, progress or settle an objective |
| `item_upsert` | create or rewrite a work item: title, ref, cost, route, body, prompt and button label, and in *Sent to other repos* its `sent` status |
| `item_transition` | move a work item to a new state with a summary, or to its own state to rewrite only the summary, or tick a manual action; with `part`, file a done or dropped item into that Part |
| `part_write` | create or rewrite one Part whole: number, kind, title, icon and body |
| `action_upsert` | create, reword or move a manual action: a checkbox with an id |
| `process_set` | add, reword or settle a *Process* entry |
| `next_set` | replace *Next*, by work item, manual action or anchor id |
| `status_set` | rewrite the status line |
| `carryover_verdict` | record keep, defer or drop on a carry-over row |
| `refresh` | reconcile the note with GitHub and herdr |
| `prompt_send` | send a work item's prompt to its herdr session; leaves the note alone |
| `task_dispatch` | start work on a ref, in its project's session or in a fresh agent |

`prompt_send` is the tool form of the ▶ button's `worklog send`: see
[Buttons](#buttons). The server lists each tool's input schema. Every tool but `day_open`
takes an optional `date`, today (UTC) by default.

## Every tool commits

Each tool takes the note's lock, re-reads it, writes its one change and commits it as one
jj change. So there is nothing to re-read first and nothing to commit after. A result with
`committed: false` says why in `skipped`; tell the user nothing was committed.

A tool that refuses names the reason: an illegal transition, an unknown id, a unit that
fails to parse. Report it, and fix the cause through the tools. Never edit the note around
a refusal.

Tools never refresh ([ADR 0008](https://github.com/firefly-engineering/quartermaster/blob/main/docs/adr/0008-mutating-tools-do-not-refresh.md)).
Between refreshes the note can lag: a merged PR stays 🔍 until the next `refresh`.

Times come from the clock, in UTC. When the user gives a time for the status line or an
objective's refinement, pass it as `at`; never estimate one.

## The note

The layout comes from [`template.md`](template.md), and the server renders it. Its order
is what makes the note actionable:

1. **Status**: one line, where the day stands.
2. **Objectives**: each with its progress (`2/3 decisions`, `stack in review`) and state.
3. **Next**: the user's next actions, at most five, in the order to take them. Each
   action is one line with a cost, and a ▶ button whenever a session can start it.
4. One `##` section per project worked on today, in the order they first appear. Each
   holds the day's Parts, then *What remains*.
5. **Carry-over** and **Process** come last.

A work item's state is its icon. Pick the state; the server writes the icon:

| Icon | State | Means |
|---|---|---|
| ⬜ | `TODO` | not started |
| 🔄 | `IN_PROGRESS` | a session or batch is on it, or some of it is done |
| 🔍 | `IN_REVIEW` | a PR is up and waits on review |
| ⏳ | `WAITING` | blocked or waiting: on another repo, a person, a date or another item |
| ✅ | `DONE` | done |
| ❌ | `DROPPED` | missed or dropped, with the reason |

The server derives every project, *What remains* and bucket icon on each write
([spec §4.2](https://github.com/firefly-engineering/quartermaster/blob/main/docs/spec.md#42-states)).
A Part's icon is yours to write with `part_write`: 🔄 while it runs, 🔍 while its work is
in review, ✅ when it is done. A finished Part holds plain lines, not work items:
`item_transition` with `part` files a done or dropped item there as one line. A Part's
body may carry anchors, paragraphs ending `^<id>`, for *Next* to link.
`item_transition` refuses a move the state model forbids
([spec §4.3](https://github.com/firefly-engineering/quartermaster/blob/main/docs/spec.md#43-transitions)).
When the evidence changes but the state does not (a PR goes up, CI turns green, a `DONE`
item's evidence needs correcting), move the item to its own state with the new summary. Never route through
another state to change a summary; that writes a false step into the history.

**Next** is rebuilt at every update with `next_set`, from the *What remains* buckets and
the routes. Each action is a step the user takes now: a decision to settle, a batch to
start, a stack to review, an IaC request to send, a manual operation that is due. Each one
names its work item, where its context and prompt live. The server renders the line, its
details link and its button.

Write tight. One line per item. A decision recorded on a ticket gets a line and a link,
not a paragraph. A finished Part shrinks to a line or a short table; the jj history keeps
the long version. Only what lives nowhere else is kept in full, such as a command run by
hand or a resource created by hand.

Refs are always links. Give `item_upsert` a `ref` (`owner/repo#N`) and it renders the
link. In prose, summaries and the status line, write the link yourself:
`[#871](https://github.com/<o>/<r>/issues/871)`, and `[IaC#564](…)` across repos. Link
every PR, run and commit.

## The day's cycle

The day runs **open**, then repeats **unblock → AFK → land** as often as needed, then
**close**. **Capture** happens any time. Work out the phase from what the user says.
When they only ask "where are we", call `refresh`, then `day_read`, and report from it.

Every phase ends the same way: `refresh`, then the tool calls that write the phase's
result, then `status_set` and `next_set`.

### Open

1. Call `day_open`. It creates the note and collects **every open item of the previous
   note** as a carry-over row: unmet objectives, open *What remains* items, unticked
   manual actions, carry-over rows not kept, dropped or done, and *Process* entries. When
   today's note was prepared ahead, `day_open` keeps it: it adds only the rows the note
   does not carry yet (`added` counts them) and rewrites the status line as `open`, leaving
   its objectives, *Next* and buckets as written. A prepared note that does not parse is
   refused with its errors: fix them through the tools, then call `day_open` again. Open
   once a day: a later call rewrites the status line as `open` again.
   Review findings left as judgement calls in prose are not collected: read the previous
   note and capture them.
2. Check each row against its source of truth (`gh issue view`, `gh pr view`, herdr
   replies). An item closed overnight is reported, not triaged: drop it with the link.
3. Ask for the day's **objectives**, and add each with `objective_set`. Each names its
   project and a checkable done-condition, such as "epic #639 closed" or "stack merged".
   Offer candidates from the carry-over and the open epics. **Milestones first when one
   earns it**: read the projects' open GitHub milestones
   (`gh api repos/<o>/<r>/milestones`), and any that is **late** (past its due date) or
   **urgent** (due within seven days) offers "make progress on <milestone>" as the first
   candidate — the day's primary objective unless the user says otherwise. Its
   done-condition names the milestone work that moves it: the coverage register (#265) and
   the milestone's open issues for trunk.
4. Triage the carry-over with the user, one `carryover_verdict` per row:
   - **keep**: in scope today. Give it its work item (id, repo, bucket), and it moves into
     its project's bucket.
   - **defer**: stays on the carry-over, so tomorrow's open finds it again.
   - **drop**: closed, wontfix, or not relevant, with the reason.

   A row with no ticket gets a proposal: file a ticket, or carry it explicitly.

Done when every objective has a done-condition, every carry-over row has a verdict, and
*Next* holds the first actions.

### Unblock

A human session that turns open questions into agent-ready work.

1. Build the **decision list**: every HITL item in scope (`ready-for-human`,
   `needs-triage`, `needs-info`, open questions in tickets). Order it by what each
   decision unblocks. Write each with `item_upsert` in *Decisions for you*: a one-line
   question, a rough cost, and its [route](#routes). One that goes through a button gets
   a prompt. Open the session's *Part N — Human session* with `part_write`, 🔄, at the
   project's next free N.
2. Work through the list with the user, one decision at a time. A quick one is settled
   inline; a larger one goes to its route's skill, through a button. Record each decision
   where it belongs: a ticket comment, an ADR or spec PR, or a relabel to
   `ready-for-agent`. The user's live answer approves recording that decision on that
   ticket. Propose new tickets, epics and other repos' items before creating them. Then
   move the item on. A settled question goes to `DONE` with the link as its summary and
   `part: N`, which files it into the session's Part as one line; from `WAITING` too when
   the link shows what it waited on is done. When the item is the ticket itself and it is
   now `ready-for-agent`, move it into *Agent queue* with `item_upsert` instead.
3. Close the Part with `part_write`, ✅: one line and a link per decision, the filed
   lines included (`day_read` returns its `body`). Rebuild the
   **Agent queue** with `item_upsert`: the `ready-for-agent` tickets, their dependency
   order, and the batch as one work item whose prompt carries the batch's global context.

Done when every decision taken is recorded outside the note, and *Next* leads with the
batch's ▶ button.

### AFK

A batch runs through `/implement` for one ticket, or `/mass-implement` for several.
Both are user-invoked, so the batch starts from the ▶ button in *Next*, which sends the
command to the project's session. For example:
`/mass-implement arbora-partners/trunk #883 #871 — global context: <epic URL>`. When the
user asks you to start it, send it yourself (see [Buttons](#buttons)). For one ticket,
`task_dispatch` also works: mode `session` sends its prompt to the project's session, and
mode `fresh` gives it its own workspace and agent, and moves it to `IN_PROGRESS`.

A batch takes every ticket it can reach, not only the ones unblocked today: each
`ready-for-agent` ticket whose blockers are all merged or in the same batch.
`/mass-implement` runs a dependent after its dependencies, so work the batch unblocks joins
it instead of waiting for the next one. Leave a ticket out only when it also waits on a
human decision, another repo or a date.

Move each ticket in the batch to `IN_PROGRESS`, and record a *Part N — Agent batch* with
`part_write`, 🔄, its title marked **running**. When it reports back, rewrite the Part with
`part_write` as a table with one row per ticket: PR, then outcome (`built`, **part only**,
or back to HITL with the reason), and its icon 🔍 while the PRs wait on review. Then:

- Move each built ticket to `IN_REVIEW`, its PR as the summary.
- Move each ticket that went back to HITL into *Decisions for you* with `item_upsert`,
  and to `WAITING` with the question it blocks on.
- Write each new ticket into its bucket.
- List each review finding left as a judgement call in the Part's body.

### Land

The user reviews and merges. `refresh` moves each merged PR and each issue closed as
completed to `DONE`. Move a ticket closed by hand yourself, with its commit and `part: N`
to file it into the Land Part. Record a *Part N — Land* with `part_write`, ✅ once it is
done:

- the merge (stack and merge commit);
- the tickets closed, by commit or by hand;
- the post-merge operations: releases, deploys, live checks, temporary grants and their
  removal. One only the user can run is a manual action: `action_upsert` puts it in the
  Part (`part: N`) or in *Your actions*.

Then settle each objective whose done-condition now holds: `objective_set` with state
`DONE`.

### Capture

Any time:

- A **new bug** gets a proposed ticket. Once filed, `item_upsert` puts it in its bucket.
- A **new HITL need** goes to *Decisions for you*, with the question it blocks on.
- A **refined objective** is rewritten in place: `objective_set` with its row and the
  reason, which it stamps `(refined HH:MM UTC: <reason>)`.
- A **manual operation** only the user can run is a manual action: `action_upsert` writes
  it, in *Your actions* by default. When the user says it is done, tick it with
  `item_transition` to `DONE`; never untick one.
- **Another repo's work** follows [Other repos](#other-repos).
- A **process change** goes to *Process* with `process_set`. Settle the entry there once
  it is adopted or dropped, so the next open does not carry it.

### Close

Make tomorrow's open trivial.

1. Call `refresh`, then chase what it left and why: replies with no links, and items it
   could not prove.
2. Compact each finished Part with `part_write`, ✅.
3. Rewrite the status line as the day's outcome with `status_set`, label `close`:
   objectives met and missed, and the next step with what gates it. Leave a missed
   objective open, its progress saying why, so tomorrow's `day_open` carries it.
4. Make sure every *What remains* item has a bucket, a link and its next action.
5. Give every found issue a ticket or an explicit carry. Nothing lives only in prose.
6. Make *Next* tomorrow's first actions.

Done when someone could open tomorrow from *Objectives*, *Next* and *What remains*
alone.

## Routes

Every open item carries a **route**: the next skill that moves it. Its ▶ button sends
that skill's command. Write it as the item's `route`.

| Shape | Route |
|---|---|
| Fog: a new idea or requirement too big for one session | `/wayfinder`. Its decision tickets join the decision list, and the objective links the map |
| One plan or design to stress-test | `/grill-with-docs` when the outcome should land as an ADR or glossary term, otherwise `/grill-me` |
| A raw or under-specified ticket | `/triage` |
| A settled discussion with no tickets yet | `/to-spec`, then `/to-tickets` |
| A question only someone outside the session can answer | `/to-questionnaire`, then *Outside input: chase* |
| A quick question the user can answer now | inline, in the unblock session |
| One `ready-for-agent` ticket | `/implement` |
| Several `ready-for-agent` tickets | `/mass-implement` |
| Work another repo owns | [Other repos](#other-repos) |

A route changes as the item moves. A grilled ticket becomes `ready-for-agent`. An
implemented one goes back to `/grill-me` when the batch finds a gap.

## Buttons

A ▶ button runs `worklog send`, which types a work item's prompt into a herdr
session: the user's agent for a project, in the herdr workspace labelled with the
project's name (`trunk`, `IaC`). Nothing is pasted by hand.

Give a work item a prompt with `item_upsert`: `prompt.to` is the workspace label,
`prompt.text` the prompt, and `button` the label. The server renders the item, its
button and its prompt block; [`template.md`](template.md) shows the result, and
[`buttons-setup.md`](buttons-setup.md) why the button carries only a reference.
`next_set` repeats the button on the item's line in *Next*.

Once a prompt has been sent and done its job, drop it: `item_upsert` with
`"prompt": null` removes the prompt, its button and the button on its *Next* line, so
no stale button re-sends it. Give the body again to keep a `#####` item; without one
it becomes a one-line item. Leaving `prompt` out is refused, not a drop.

The judgement stays here:

- The label names what the button does and where: `grill in trunk`, `send to IaC`,
  `run batch in trunk`.
- A prompt is self-contained, because the session receiving it may not have the note's
  context.
- `worklog send` adds a closing line to every prompt: when the work is done, update
  the worklog's `^<item-id>` work item. Leave that line out of the prompt. A session that
  receives it runs the matching phase here, against that work item.

When the note shows prompts but no buttons, the vault lacks the Shell commands setup.
Point the user at [`buttons-setup.md`](buttons-setup.md) once.

To send a prompt yourself, call `prompt_send` with the item's id. It does not touch the
note: record the send yourself. It fails if herdr is not found, if the workspace is missing
or has no single agent, or if that agent is `blocked` on an approval; leave the item as it
was and say why.

## Other repos

Some work belongs to another repo; the current repo's `CLAUDE.md` draws the boundary.
For trunk, `arbora-partners/IaC` owns what Arbora operates. That work goes to the repo's
own session over herdr. It is tracked in the **originating** project's *Sent to other
repos* bucket, because that project is waiting on it.

1. Write the work item with `item_upsert` under *Sent to other repos*, with `sent: to send`
   and a prompt to the other repo's workspace label. The server writes
   `→ <workspace label>, **to send**` after its title. The prompt says what
   is needed and why, links the ticket it unblocks, says how the result is verified, and
   asks for a reply with links (issue, PR, run). Give it no `ref` yet: `refresh` closes
   an item when its ref closes, and the ticket it unblocks is not that event. Once the
   reply names the other repo's issue, make that its `ref`.
2. Put it in *Next*.
3. When the user asks you to send it, send it yourself (see [Buttons](#buttons)). Once
   sent, set its status with `item_upsert`, `sent: sent`. Leave its state: `refresh`
   closes it, `WAITING` or not, once its ref closes.
4. `refresh` tracks the rest. It reads the reply, marks the item **acked** and adds its
   links; once GitHub closes it, it reads **done**.

When the user works directly in another repo that day, that repo gets its own project
section.

## Reporting

End each phase with the note's status line, its *Next* list, and the note's path.
